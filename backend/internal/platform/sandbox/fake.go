package sandbox

import (
	"context"
	"sync"
)

// Fake is an in-memory Runner for tests of everything above the sandbox layer. It executes
// nothing; the handler decides the Result. Production wiring never constructs it.
type Fake struct {
	mu      sync.Mutex
	handler func(ctx context.Context, request Request) (Result, error)
	calls   []Request
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

func (f *Fake) Probe(context.Context) (Info, error) {
	return Info{Backend: "fake", Isolation: "none", Slots: 1, CPUs: 4, MemoryMB: 8192}, nil
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

// Calls returns the normalized requests received so far.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}
