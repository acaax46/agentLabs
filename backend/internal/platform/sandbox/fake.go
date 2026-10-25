package sandbox

import (
	"context"
	"strings"
	"sync"
)

// Fake is an in-memory Runner for tests of everything above the sandbox layer. It executes
// nothing; the handler decides the Result. Production wiring never constructs it.
type Fake struct {
	mu       sync.Mutex
	handler  func(ctx context.Context, request Request) (Result, error)
	calls    []Request
	gpus     []GPU
	session  func(ctx context.Context, request SessionRequest, handler SessionHandler) (SessionResult, error)
	sessions []SessionRequest
}

func NewFake(handler func(ctx context.Context, request Request) (Result, error)) *Fake {
	return &Fake{handler: handler}
}

// SetHandler replaces the behavior, for tests that need different answers over time.
func (f *Fake) SetHandler(handler func(ctx context.Context, request Request) (Result, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}

// SetGPUs makes the fake report devices as if they had passed the CUDA check.
func (f *Fake) SetGPUs(gpus ...GPU) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gpus = append([]GPU(nil), gpus...)
}

func (f *Fake) Probe(context.Context) (Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info := Info{Backend: "fake", Isolation: "none", Slots: 1, CPUs: 4, MemoryMB: 8192, GPUs: append([]GPU(nil), f.gpus...)}
	if len(info.GPUs) == 0 {
		info.GPUReason = "此执行节点没有开放 GPU。"
	}
	return info, nil
}

func (f *Fake) Execute(ctx context.Context, request Request) (Result, error) {
	request, err := request.Normalize()
	if err != nil {
		return Result{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, request)
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return Result{}, ErrUnavailable
	}
	return handler(ctx, request)
}

// SetSession scripts code-agent sessions; without it a session is unavailable.
func (f *Fake) SetSession(session func(ctx context.Context, request SessionRequest, handler SessionHandler) (SessionResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session = session
}

func (f *Fake) Session(ctx context.Context, request SessionRequest, handler SessionHandler) (SessionResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return SessionResult{}, err
	}
	f.mu.Lock()
	session := f.session
	f.sessions = append(f.sessions, request)
	f.mu.Unlock()
	if session == nil {
		return SessionResult{}, ErrUnavailable
	}
	return session(ctx, request, handler)
}

// AgentImageID reports a fixed image, so runs that use sessions can be admitted in tests.
func (f *Fake) AgentImageID(context.Context) (string, error) {
	return "sha256:" + strings.Repeat("f", 64), nil
}

// Sessions returns the normalized session requests received so far.
func (f *Fake) Sessions() []SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SessionRequest(nil), f.sessions...)
}

// Calls returns the normalized requests received so far.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}
