package sandbox

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Wire protocol v1 between the host and the in-guest runner (runtime/sandbox/runner.py).
//
//	host -> guest (stdin):   tar{ request.json, input/<path>..., output/<path>... }
//	guest -> host (stdout):  tar{ result.json, output/<path>... }
//
// Only regular files travel. Both directions are parsed defensively because the guest is hostile.
const (
	protocolVersion  = 1
	requestEntry     = "request.json"
	resultEntry      = "result.json"
	inputPrefix      = "input/"
	outputPrefix     = "output/"
	maxMetadataBytes = 2 << 20
)

type wireRequest struct {
	Version        int    `json:"version"`
	Code           string `json:"code"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	MaxOutputFiles int    `json:"maxOutputFiles"`
	MaxOutputBytes int64  `json:"maxOutputBytes"`
	MaxStdioBytes  int    `json:"maxStdioBytes"`
}

type wireResult struct {
	Version         int    `json:"version"`
	ExitCode        int    `json:"exitCode"`
	TimedOut        bool   `json:"timedOut"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdoutTruncated"`
	StderrTruncated bool   `json:"stderrTruncated"`
	DurationMS      int64  `json:"durationMs"`
	Error           string `json:"error,omitempty"`
}

func writeEntry(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// EncodeRequest writes the archive a guest runner consumes.
func EncodeRequest(w io.Writer, request Request) error {
	request, err := request.Normalize()
	if err != nil {
		return err
	}
	meta, err := json.Marshal(wireRequest{Version: protocolVersion, Code: request.Code, TimeoutSeconds: request.TimeoutSeconds, MaxOutputFiles: request.Limits.MaxOutputFiles, MaxOutputBytes: request.Limits.MaxOutputBytes, MaxStdioBytes: request.Limits.MaxStdioBytes})
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	if err = writeEntry(tw, requestEntry, meta); err != nil {
		return err
	}
	for _, file := range request.Inputs {
		if err = writeEntry(tw, inputPrefix+file.Path, file.Data); err != nil {
			return err
		}
	}
	for _, file := range request.Outputs {
		if err = writeEntry(tw, outputPrefix+file.Path, file.Data); err != nil {
			return err
		}
	}
	return tw.Close()
}

// readEntries walks an untrusted archive: regular files only, bounded count and size.
func readEntries(r io.Reader, maxTotal int64, maxEntries int, visit func(name string, data []byte) error) error {
	tr := tar.NewReader(r)
	var total int64
	count := 0
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrProtocol, err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("%w: unsupported archive entry %q", ErrProtocol, header.Name)
		}
		count++
		if count > maxEntries {
			return fmt.Errorf("%w: too many archive entries", ErrOutputLimit)
		}
		if header.Size < 0 || header.Size > MaxFileBytes || total+header.Size > maxTotal {
			return fmt.Errorf("%w: archive is too large", ErrOutputLimit)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrProtocol, err)
		}
		total += int64(len(data))
		if err = visit(header.Name, data); err != nil {
			return err
		}
	}
}

// DecodeResult parses a guest archive and enforces the caller's limits regardless of what the
// guest claims. Output paths are validated; anything unexpected fails the whole result.
func DecodeResult(r io.Reader, limits Limits) (Result, error) {
	if limits.MaxOutputFiles < 1 || limits.MaxOutputBytes < 1 || limits.MaxStdioBytes < 1 {
		return Result{}, fmt.Errorf("%w: limits are required", ErrInvalidRequest)
	}
	var meta *wireResult
	var outputs []File
	var total int64
	seen := map[string]bool{}
	err := readEntries(r, limits.MaxOutputBytes+maxMetadataBytes, limits.MaxOutputFiles+1, func(name string, data []byte) error {
		if name == resultEntry {
			if meta != nil || len(data) > maxMetadataBytes {
				return fmt.Errorf("%w: duplicate or oversized %s", ErrProtocol, resultEntry)
			}
			var decoded wireResult
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
		if len(outputs) >= limits.MaxOutputFiles || total > limits.MaxOutputBytes {
			return fmt.Errorf("%w: too many or too large output files", ErrOutputLimit)
		}
		outputs = append(outputs, File{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if meta == nil {
		return Result{}, fmt.Errorf("%w: missing %s", ErrProtocol, resultEntry)
	}
	if meta.Version != protocolVersion {
		return Result{}, fmt.Errorf("%w: unsupported protocol version %d", ErrProtocol, meta.Version)
	}
	if meta.Error != "" {
		return Result{}, fmt.Errorf("%w: guest runner failed: %s", ErrProtocol, truncateText(meta.Error, 200))
	}
	stdout, stdoutCut := truncateUTF8(meta.Stdout, limits.MaxStdioBytes)
	stderr, stderrCut := truncateUTF8(meta.Stderr, limits.MaxStdioBytes)
	duration := time.Duration(meta.DurationMS) * time.Millisecond
	if duration < 0 || duration > 24*time.Hour {
		duration = 0
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
	return Result{
		ExitCode: meta.ExitCode, TimedOut: meta.TimedOut,
		Stdout: stdout, Stderr: stderr,
		StdoutTruncated: meta.StdoutTruncated || stdoutCut, StderrTruncated: meta.StderrTruncated || stderrCut,
		Duration: duration, Outputs: outputs,
	}, nil
}

// DecodeRequest is the guest side of EncodeRequest. The Python runner implements it in the
// image; this version backs Go guests and the protocol tests.
func DecodeRequest(r io.Reader, maxBytes int64) (Request, error) {
	var meta *wireRequest
	var request Request
	err := readEntries(io.LimitReader(r, maxBytes+1), maxBytes, MaxFiles*2+1, func(name string, data []byte) error {
		switch {
		case name == requestEntry:
			if meta != nil || len(data) > maxMetadataBytes+MaxCodeBytes {
				return fmt.Errorf("%w: duplicate or oversized %s", ErrProtocol, requestEntry)
			}
			var decoded wireRequest
			if err := json.Unmarshal(data, &decoded); err != nil {
				return fmt.Errorf("%w: %s is not valid JSON", ErrProtocol, requestEntry)
			}
			meta = &decoded
		case strings.HasPrefix(name, inputPrefix):
			request.Inputs = append(request.Inputs, File{Path: strings.TrimPrefix(name, inputPrefix), Data: data})
		case strings.HasPrefix(name, outputPrefix):
			request.Outputs = append(request.Outputs, File{Path: strings.TrimPrefix(name, outputPrefix), Data: data})
		default:
			return fmt.Errorf("%w: unexpected entry %q", ErrProtocol, name)
		}
		return nil
	})
	if err != nil {
		return Request{}, err
	}
	if meta == nil || meta.Version != protocolVersion {
		return Request{}, fmt.Errorf("%w: missing or unsupported %s", ErrProtocol, requestEntry)
	}
	request.Code, request.TimeoutSeconds = meta.Code, meta.TimeoutSeconds
	request.Limits = Limits{MaxOutputFiles: meta.MaxOutputFiles, MaxOutputBytes: meta.MaxOutputBytes, MaxStdioBytes: meta.MaxStdioBytes}
	return request.Normalize()
}

// EncodeResult is the guest side of DecodeResult.
func EncodeResult(w io.Writer, result Result) error {
	meta, err := json.Marshal(wireResult{Version: protocolVersion, ExitCode: result.ExitCode, TimedOut: result.TimedOut, Stdout: result.Stdout, Stderr: result.Stderr, StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated, DurationMS: result.Duration.Milliseconds()})
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	if err = writeEntry(tw, resultEntry, meta); err != nil {
		return err
	}
	for _, file := range result.Outputs {
		if err = writeEntry(tw, outputPrefix+file.Path, file.Data); err != nil {
			return err
		}
	}
	return tw.Close()
}

// truncateUTF8 cuts s to at most limit bytes without splitting a character.
func truncateUTF8(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func truncateText(s string, limit int) string {
	cut, _ := truncateUTF8(strings.TrimSpace(s), limit)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, cut)
}
