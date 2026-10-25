package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentlab.local/backend/internal/platform/ids"
)

// Session protocol v1 runs one code-agent CLI (pi, OpenCode, Codex CLI, Claude Code) for a whole
// run inside one container, driven by runtime/sandbox/harness.py. The container keeps every
// restriction of a code execution, including --network none: the CLI reaches a model only through
// a relay on the container's own loopback that turns each request into a frame on stdout. The host
// answers on stdin after it has checked, priced and forwarded the request, so credentials, provider
// addresses and the budget never enter the guest.
//
// A frame is one type byte, a 4-byte big-endian length and the payload:
//
//	R host->guest  request archive: request.json, input/<path>..., output/<path>...
//	A host->guest  {"id","status","body"}, the answer to one model call
//	M guest->host  {"id","protocol","body"}, one model call
//	E guest->host  {"kind","name","summary"}, what the CLI says it is doing (untrusted)
//	Z guest->host  result archive: result.json, output/<path>...; the last frame
const (
	sessionVersion     = 1
	MaxSessionSeconds  = 1800
	MaxSessionCalls    = 400
	MaxModelCallBytes  = 8 << 20
	maxEventBytes      = 16 << 10
	maxSessionEvents   = 500
	maxInstructionSize = 64 << 10
	maxPromptSize      = 32 << 10
)

// Harnesses a session can run, with the model protocol each one speaks.
var sessionHarnesses = map[string]string{"pi": "chat", "opencode": "chat", "codex": "responses", "claude-code": "messages"}

// SessionRequest starts one code-agent session. Instruction is the first user message and
// SystemPrompt the platform rules appended to the CLI's own prompt; neither carries a secret.
type SessionRequest struct {
	RunID           string
	Harness         string
	Instruction     string
	SystemPrompt    string
	TimeoutSeconds  int
	MaxOutputTokens int
	ContextWindow   int
	Inputs          []File
	Outputs         []File
	Limits          Limits
	Resources       Resources
	ImageID         string
}

// ModelCall is a model request the guest made. Body is untrusted JSON for Protocol's API.
type ModelCall struct {
	ID       int64           `json:"id"`
	Protocol string          `json:"protocol"`
	Body     json.RawMessage `json:"body"`
}

// ModelAnswer is what the guest's CLI receives: an HTTP status and a JSON body.
type ModelAnswer struct {
	Status int
	Body   json.RawMessage
}

// SessionEvent is a step the CLI reported. It is untrusted narration, never evidence of anything.
type SessionEvent struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// SessionHandler is the trusted side of a session. Model is called for one call at a time.
type SessionHandler interface {
	Model(ctx context.Context, call ModelCall) ModelAnswer
	Event(ctx context.Context, event SessionEvent)
}

// SessionResult is what the host learned. Duration is host-measured from start to removal and is
// set whenever Started is true, even with an error, because the allocation was held that long.
type SessionResult struct {
	Started       bool
	ExitCode      int
	TimedOut      bool
	FinalMessage  string
	Stderr        string
	Skipped       []string
	ModelCalls    int
	EventsDropped int
	Duration      time.Duration
	Outputs       []File
}

// SessionRunner is implemented by backends that can host code-agent sessions.
type SessionRunner interface {
	Session(ctx context.Context, request SessionRequest, handler SessionHandler) (SessionResult, error)
	// AgentImageID pins the code-agent image before a run is queued; empty when sessions are off.
	AgentImageID(ctx context.Context) (string, error)
}

// HarnessProtocol is the model API a harness speaks, or "" for an unknown harness.
func HarnessProtocol(harness string) string { return sessionHarnesses[harness] }

// Normalize validates a session request and fills defaults, returning a copy.
func (r SessionRequest) Normalize() (SessionRequest, error) {
	if HarnessProtocol(r.Harness) == "" {
		return r, fmt.Errorf("%w: unknown harness %q", ErrInvalidRequest, r.Harness)
	}
	if strings.TrimSpace(r.Instruction) == "" || len(r.Instruction) > maxInstructionSize || len(r.SystemPrompt) > maxPromptSize || !utf8.ValidString(r.Instruction+r.SystemPrompt) {
		return r, fmt.Errorf("%w: instruction or system prompt is empty, too long or not UTF-8", ErrInvalidRequest)
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > MaxSessionSeconds {
		return r, fmt.Errorf("%w: session time limit must be 1-%d seconds", ErrInvalidRequest, MaxSessionSeconds)
	}
	if r.MaxOutputTokens == 0 {
		r.MaxOutputTokens = 16384
	}
	if r.ContextWindow == 0 {
		r.ContextWindow = 128000
	}
	if r.MaxOutputTokens < 1 || r.MaxOutputTokens > 65536 || r.ContextWindow < 4096 || r.ContextWindow > 2_000_000 {
		return r, fmt.Errorf("%w: token limits are out of range", ErrInvalidRequest)
	}
	// Reuse the file and limit rules of a code execution.
	shell, err := Request{Code: "session", TimeoutSeconds: 1, Inputs: r.Inputs, Outputs: r.Outputs, Limits: r.Limits}.Normalize()
	if err != nil {
		return r, err
	}
	r.Limits = shell.Limits
	return r, nil
}

type wireSessionRequest struct {
	Version         int    `json:"version"`
	Harness         string `json:"harness"`
	Instruction     string `json:"instruction"`
	SystemPrompt    string `json:"systemPrompt"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	ContextWindow   int    `json:"contextWindow"`
	Limits          struct {
		MaxOutputFiles int   `json:"maxOutputFiles"`
		MaxOutputBytes int64 `json:"maxOutputBytes"`
	} `json:"limits"`
}

type wireSessionResult struct {
	Version       int      `json:"version"`
	ExitCode      int      `json:"exitCode"`
	TimedOut      bool     `json:"timedOut"`
	FinalMessage  string   `json:"finalMessage"`
	Stderr        string   `json:"stderr"`
	Skipped       []string `json:"skipped"`
	EventsDropped int      `json:"eventsDropped"`
}

// EncodeSessionRequest writes the R frame's archive.
func EncodeSessionRequest(w io.Writer, request SessionRequest) error {
	request, err := request.Normalize()
	if err != nil {
		return err
	}
	wire := wireSessionRequest{Version: sessionVersion, Harness: request.Harness, Instruction: request.Instruction, SystemPrompt: request.SystemPrompt,
		TimeoutSeconds: request.TimeoutSeconds, MaxOutputTokens: request.MaxOutputTokens, ContextWindow: request.ContextWindow}
	wire.Limits.MaxOutputFiles, wire.Limits.MaxOutputBytes = request.Limits.MaxOutputFiles, request.Limits.MaxOutputBytes
	meta, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	if err = writeEntry(tw, requestEntry, meta); err != nil {
		return err
	}
	for _, group := range []struct {
		prefix string
		files  []File
	}{{inputPrefix, request.Inputs}, {outputPrefix, request.Outputs}} {
		for _, file := range group.files {
			if err = writeEntry(tw, group.prefix+file.Path, file.Data); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

// DecodeSessionResult parses the Z frame's archive with the caller's limits, whatever the guest claims.
func DecodeSessionResult(r io.Reader, limits Limits) (SessionResult, error) {
	var meta *wireSessionResult
	var result SessionResult
	var total int64
	seen := map[string]bool{}
	err := readEntries(r, limits.MaxOutputBytes+maxMetadataBytes, limits.MaxOutputFiles+1, func(name string, data []byte) error {
		if name == resultEntry {
			if meta != nil || len(data) > maxMetadataBytes {
				return fmt.Errorf("%w: duplicate or oversized %s", ErrProtocol, resultEntry)
			}
			var decoded wireSessionResult
			if err := json.Unmarshal(data, &decoded); err != nil {
				return fmt.Errorf("%w: %s is not valid JSON", ErrProtocol, resultEntry)
			}
			meta = &decoded
			return nil
		}
		rel, ok := strings.CutPrefix(name, outputPrefix)
		if !ok {
			return fmt.Errorf("%w: unexpected entry %q", ErrProtocol, name)
		}
		if err := ValidatePath(rel); err != nil {
			return fmt.Errorf("%w: %v", ErrProtocol, err)
		}
		if seen[rel] {
			return fmt.Errorf("%w: duplicate output %q", ErrProtocol, rel)
		}
		seen[rel] = true
		total += int64(len(data))
		if len(result.Outputs) >= limits.MaxOutputFiles || total > limits.MaxOutputBytes {
			return fmt.Errorf("%w: too many or too large output files", ErrOutputLimit)
		}
		result.Outputs = append(result.Outputs, File{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return SessionResult{}, err
	}
	if meta == nil || meta.Version != sessionVersion {
		return SessionResult{}, fmt.Errorf("%w: missing or unsupported %s", ErrProtocol, resultEntry)
	}
	result.ExitCode, result.TimedOut, result.EventsDropped = meta.ExitCode, meta.TimedOut, max(0, meta.EventsDropped)
	result.FinalMessage, _ = truncateUTF8(meta.FinalMessage, 20000)
	result.Stderr, _ = truncateUTF8(meta.Stderr, 8192)
	for _, name := range meta.Skipped {
		if len(result.Skipped) == 100 {
			break
		}
		result.Skipped = append(result.Skipped, truncateText(name, 200))
	}
	slices.SortFunc(result.Outputs, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return result, nil
}

// ---- frames ----

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	header := make([]byte, 5)
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader, limits map[byte]int) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	limit, known := limits[header[0]]
	size := binary.BigEndian.Uint32(header[1:])
	if !known {
		return 0, nil, fmt.Errorf("%w: unknown frame type %q", ErrProtocol, header[0])
	}
	if int64(size) > int64(limit) {
		return 0, nil, fmt.Errorf("%w: frame %q of %d bytes is over its limit", ErrProtocol, header[0], size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("%w: truncated frame", ErrProtocol)
	}
	return header[0], payload, nil
}

// ---- Docker ----

// AgentImageID resolves the configured code-agent image; sessions are off without one.
func (d *Docker) AgentImageID(ctx context.Context) (string, error) {
	if d.cfg.AgentImage == "" {
		return "", fmt.Errorf("%w: this node has no code-agent image", ErrUnavailable)
	}
	return d.imageID(ctx, d.cfg.AgentImage)
}

// sessionArguments is the command line of a session container: the same restrictions as a code
// execution, plus an init process to reap what the CLI leaves behind, room for its processes, and
// an executable /work so that projects the agent builds can run their own scripts.
func (d *Docker) sessionArguments(name string, request SessionRequest) []string {
	args := d.lockedDown(name, request.RunID, request.Resources.CPUs, request.Resources.MemoryMB, 256, "rw,nosuid,nodev", 256)
	args = append(args, "--init", "--label", "org.agentlab.session=1")
	if d.cfg.Runtime != "" {
		args = append(args, "--runtime", d.cfg.Runtime)
	}
	return append(args, request.ImageID)
}

// Session runs one code-agent session to its end. Model calls are answered one at a time by
// handler; the session ends when the guest delivers its result, when the time limit passes, or when
// ctx is cancelled, which kills the container.
func (d *Docker) Session(ctx context.Context, request SessionRequest, handler SessionHandler) (SessionResult, error) {
	resources := request.Resources
	if resources != (Resources{}) && (math.IsNaN(resources.CPUs) || math.IsInf(resources.CPUs, 0) || resources.CPUs < 0.1 || resources.CPUs > d.cfg.CPUs || resources.MemoryMB < 128 || resources.MemoryMB > d.cfg.MemoryMB || resources.GPU != 0) {
		return SessionResult{}, fmt.Errorf("%w: unsupported resource allocation for a session", ErrInvalidRequest)
	}
	if resources == (Resources{}) {
		request.Resources = Resources{CPUs: d.cfg.CPUs, MemoryMB: d.cfg.MemoryMB}
	}
	request, err := request.Normalize()
	if err != nil {
		return SessionResult{}, err
	}
	if request.ImageID != "" && !imageIDPattern.MatchString(request.ImageID) {
		return SessionResult{}, fmt.Errorf("%w: invalid pinned image ID", ErrInvalidRequest)
	}
	var archive bytes.Buffer
	if err = EncodeSessionRequest(&archive, request); err != nil {
		return SessionResult{}, err
	}
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-ctx.Done():
		return SessionResult{}, ctx.Err()
	}
	if request.ImageID == "" {
		if request.ImageID, err = d.AgentImageID(ctx); err != nil {
			return SessionResult{}, err
		}
	}
	name := "agentlab-ses-" + ids.Token(8)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second+60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, d.cfg.Binary, d.sessionArguments(name, request)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return SessionResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return SessionResult{}, err
	}
	stderr := &limitedBuffer{limit: 8 << 10}
	cmd.Stderr = stderr
	started := time.Now()
	if err = cmd.Start(); err != nil {
		var missing *exec.Error
		if errors.As(err, &missing) {
			return SessionResult{}, fmt.Errorf("%w: container CLI %q was not found", ErrUnavailable, d.cfg.Binary)
		}
		return SessionResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	result, sessionErr := converse(runCtx, request, archive.Bytes(), handler, stdin, bufio.NewReaderSize(stdout, 64<<10))
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if sessionErr != nil || waitErr != nil || runCtx.Err() != nil {
		d.reap(name)
	}
	// The allocation is held from start to removal, so that is what the host measures and bills.
	result.Started, result.Duration = true, time.Since(started)
	switch {
	case ctx.Err() != nil:
		return result, ctx.Err()
	case sessionErr == nil:
		return result, nil
	case runCtx.Err() != nil:
		// The guest ignored its own limit and was killed at the host's hard limit.
		result.TimedOut, result.ExitCode = true, -1
		return result, nil
	case errors.Is(sessionErr, io.ErrUnexpectedEOF) || errors.Is(sessionErr, io.EOF):
		return result, fmt.Errorf("%w: the session ended without a result: %s", ErrProtocol, firstLine(stderr.buf.String(), sessionErr))
	}
	return result, sessionErr
}

// converse runs the frame exchange. Events keep flowing while a model call is being answered;
// model calls are answered strictly one after another.
func converse(ctx context.Context, request SessionRequest, archive []byte, handler SessionHandler, stdin io.Writer, stdout io.Reader) (SessionResult, error) {
	var result SessionResult
	var writeMu sync.Mutex
	send := func(kind byte, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(stdin, kind, payload)
	}
	if err := send('R', archive); err != nil {
		return result, fmt.Errorf("%w: the session did not accept its request: %v", ErrUnavailable, err)
	}
	type frame struct {
		kind    byte
		payload []byte
		err     error
	}
	frames := make(chan frame)
	limits := map[byte]int{'M': MaxModelCallBytes + 1024, 'E': maxEventBytes, 'Z': int(request.Limits.MaxOutputBytes) + 4<<20}
	go func() {
		defer close(frames)
		for {
			kind, payload, err := readFrame(stdout, limits)
			select {
			case frames <- frame{kind, payload, err}:
			case <-ctx.Done():
				return
			}
			if err != nil || kind == 'Z' {
				return
			}
		}
	}()
	calls := make(chan ModelCall, MaxSessionCalls)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		for call := range calls {
			answer := handler.Model(ctx, call)
			if answer.Status == 0 {
				answer.Status = 502
			}
			body := answer.Body
			if len(body) == 0 || !json.Valid(body) {
				body = json.RawMessage(`{"error":{"message":"no answer"}}`)
			}
			payload, _ := json.Marshal(struct {
				ID     int64           `json:"id"`
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			}{call.ID, answer.Status, body})
			if send('A', payload) != nil {
				return
			}
		}
	}()
	defer func() {
		close(calls)
		<-answered
	}()
	events := 0
	for {
		var next frame
		var open bool
		select {
		case next, open = <-frames:
		case <-ctx.Done():
			return result, ctx.Err()
		}
		if !open {
			return result, io.ErrUnexpectedEOF
		}
		if next.err != nil {
			return result, next.err
		}
		switch next.kind {
		case 'M':
			var call ModelCall
			if err := json.Unmarshal(next.payload, &call); err != nil || call.ID < 1 || len(call.Body) == 0 {
				return result, fmt.Errorf("%w: malformed model call", ErrProtocol)
			}
			if call.Protocol != HarnessProtocol(request.Harness) {
				return result, fmt.Errorf("%w: the %s harness called the %s protocol", ErrProtocol, request.Harness, call.Protocol)
			}
			result.ModelCalls++
			if result.ModelCalls > MaxSessionCalls {
				return result, fmt.Errorf("%w: more than %d model calls", ErrProtocol, MaxSessionCalls)
			}
			calls <- call
		case 'E':
			events++
			var event SessionEvent
			if events > maxSessionEvents || json.Unmarshal(next.payload, &event) != nil {
				result.EventsDropped++
				continue
			}
			event.Kind, event.Name, event.Summary = truncateText(event.Kind, 20), truncateText(event.Name, 60), truncateText(event.Summary, 300)
			handler.Event(ctx, event)
		case 'Z':
			decoded, err := DecodeSessionResult(bytes.NewReader(next.payload), request.Limits)
			if err != nil {
				return result, err
			}
			decoded.ModelCalls, decoded.EventsDropped = result.ModelCalls, result.EventsDropped+decoded.EventsDropped
			return decoded, nil
		default:
			return result, fmt.Errorf("%w: unexpected frame %q", ErrProtocol, next.kind)
		}
	}
}
