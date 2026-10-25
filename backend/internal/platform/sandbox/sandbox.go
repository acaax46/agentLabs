// Package sandbox runs untrusted, model-written code without ever touching the host.
//
// The contract between the platform and an execution backend is a byte stream, not a shared
// directory: the host writes one tar archive (code, inputs, earlier outputs) and reads one tar
// archive back (result.json, new outputs). Any runtime that can start a network-less container
// or microVM and wire its stdin/stdout can implement Runner, so capacity grows by adding nodes
// and backends can be swapped (runc, gVisor, Kata, Firecracker, a Kubernetes Job) without
// touching the agent loop or the data model.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
)

// File is one workspace file. Path is relative and slash separated, for example "orders.csv".
type File struct {
	Path string
	Data []byte
}

// Request is one stateless code execution. Nothing survives the call except Result.Outputs.
type Request struct {
	// RunID labels the sandbox so operators can find and reap it; it carries no authority.
	RunID string
	// Code is Python source. It runs with the working directory /work.
	Code           string
	TimeoutSeconds int
	// Inputs are user files exposed read-only at /work/input.
	Inputs []File
	// Outputs are results of earlier steps restored at /work/output, where new files are also collected.
	Outputs []File
	Limits  Limits
	// Resources are host-validated allocations, never supplied to the guest as authority.
	Resources Resources
	// ImageID pins a host-resolved image, never a model-controlled image reference.
	ImageID string
}

// ImageResolver freezes a node image before a run enters the queue.
type ImageResolver interface {
	ImageID(context.Context) (string, error)
}

// AllocationImageResolver freezes the image for one allocation: one with a GPU runs a CUDA image.
type AllocationImageResolver interface {
	ImageIDFor(context.Context, Resources) (string, error)
}

type Resources struct {
	CPUs     float64
	MemoryMB int
	GPU      int
	// GPUModel is the card model a GPU allocation must match, for example "RTX 3070", compared
	// case-insensitively with the device names the node found. Empty accepts any card.
	GPUModel string
}

// GPU is one device a node may lease, as found by a real CUDA execution on that device.
type GPU struct {
	Index    int
	Name     string
	MemoryMB int
}

// Limits bound what a single execution may return to the platform. Zero means the ceiling.
type Limits struct {
	MaxOutputFiles int
	MaxOutputBytes int64
	MaxStdioBytes  int
}

// Result is everything the platform learns about an execution. The guest is untrusted, so the
// host re-validates every field before use.
type Result struct {
	ExitCode        int
	TimedOut        bool
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
	Outputs         []File
}

// Info describes a ready backend. Isolation is reported honestly so operators and customers can
// tell a container sandbox from a microVM.
type Info struct {
	Backend   string
	Isolation string
	Image     string
	Runtime   string
	// Slots is how many executions this node runs at once.
	Slots    int
	CPUs     float64
	MemoryMB int
	// GPUs are the devices that passed a real CUDA check; GPUReason says why there are none.
	GPUs      []GPU
	GPUReason string
	GPUImage  string
}

// MatchingGPUs counts the devices whose name contains the words of model in order, ignoring case
// and punctuation, so "L4" matches "NVIDIA L4" but not "NVIDIA L40S". GPUs are priced per card
// type, so a near miss must not count. An empty model matches every device.
func (i Info) MatchingGPUs(model string) int {
	count := 0
	for _, gpu := range i.GPUs {
		if GPUModelMatches(gpu.Name, model) {
			count++
		}
	}
	return count
}

func GPUModelMatches(name, model string) bool {
	words := func(text string) []string {
		return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	have, want := words(name), words(model)
	if len(want) == 0 {
		return true
	}
	for start := 0; start+len(want) <= len(have); start++ {
		if slices.Equal(have[start:start+len(want)], want) {
			return true
		}
	}
	return false
}

// Runner executes requests. Implementations must be safe for concurrent use and must fail
// closed: if isolation cannot be established they return an error and never fall back to
// running the code anywhere else.
type Runner interface {
	// Probe performs a real execution and returns what it found; it backs readiness checks.
	Probe(ctx context.Context) (Info, error)
	Execute(ctx context.Context, request Request) (Result, error)
}

var (
	ErrUnavailable    = errors.New("sandbox backend unavailable")
	ErrInvalidRequest = errors.New("invalid sandbox request")
	ErrProtocol       = errors.New("sandbox returned an invalid result")
	ErrOutputLimit    = errors.New("sandbox output exceeded its limit")
	// ErrNoCapacity means nothing was started: every matching device stayed busy. Nothing ran, so
	// nothing is billed, unlike a failure after a container may have started.
	ErrNoCapacity = errors.New("sandbox capacity unavailable")
)

// Ceilings. Callers may lower them per request, never raise them.
const (
	DefaultTimeoutSeconds = 60
	MaxTimeoutSeconds     = 300
	MaxCodeBytes          = 256 << 10
	MaxFileBytes          = 64 << 20
	MaxFiles              = 64
	MaxOutputFiles        = 32
	MaxOutputBytes        = 64 << 20
	MaxStdioBytes         = 64 << 10
)

// Normalize validates a request and fills defaults. It returns a copy; the caller's value is untouched.
func (r Request) Normalize() (Request, error) {
	if strings.TrimSpace(r.Code) == "" || len(r.Code) > MaxCodeBytes {
		return r, fmt.Errorf("%w: code is empty or larger than %d bytes", ErrInvalidRequest, MaxCodeBytes)
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > MaxTimeoutSeconds {
		return r, fmt.Errorf("%w: timeout must be 1-%d seconds", ErrInvalidRequest, MaxTimeoutSeconds)
	}
	if r.Limits.MaxOutputFiles == 0 {
		r.Limits.MaxOutputFiles = MaxOutputFiles
	}
	if r.Limits.MaxOutputBytes == 0 {
		r.Limits.MaxOutputBytes = MaxOutputBytes
	}
	if r.Limits.MaxStdioBytes == 0 {
		r.Limits.MaxStdioBytes = MaxStdioBytes
	}
	if r.Limits.MaxOutputFiles < 1 || r.Limits.MaxOutputFiles > MaxOutputFiles || r.Limits.MaxOutputBytes < 1 || r.Limits.MaxOutputBytes > MaxOutputBytes || r.Limits.MaxStdioBytes < 1 || r.Limits.MaxStdioBytes > MaxStdioBytes {
		return r, fmt.Errorf("%w: output limits are out of range", ErrInvalidRequest)
	}
	if len(r.Inputs)+len(r.Outputs) > MaxFiles {
		return r, fmt.Errorf("%w: more than %d workspace files", ErrInvalidRequest, MaxFiles)
	}
	seen := map[string]bool{}
	for _, group := range [][]File{r.Inputs, r.Outputs} {
		for _, file := range group {
			if err := ValidatePath(file.Path); err != nil {
				return r, err
			}
			if int64(len(file.Data)) > MaxFileBytes {
				return r, fmt.Errorf("%w: file %q is larger than %d bytes", ErrInvalidRequest, file.Path, MaxFileBytes)
			}
			if seen[file.Path] {
				return r, fmt.Errorf("%w: duplicate file %q", ErrInvalidRequest, file.Path)
			}
			seen[file.Path] = true
		}
	}
	return r, nil
}

// ValidatePath accepts a plain relative path. Anything that could address a location outside the
// workspace is rejected rather than cleaned: path.Clean must be a no-op.
func ValidatePath(name string) error {
	if name == "" || len(name) > 200 || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:\x00\r\n") || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("%w: unsafe file path %q", ErrInvalidRequest, name)
	}
	return nil
}
