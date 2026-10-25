// Package guestproto defines the private host/guest serial protocol. No expected answers or credentials cross it.
package guestproto

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type Request struct {
	Nonce     string `json:"nonce"`
	Files     []File `json:"files"`
	Input     string `json:"input"`
	TimeoutMS int    `json:"timeoutMs"`
	MemoryMB  int    `json:"memoryMb"`
}
type Result struct {
	Nonce       string `json:"nonce"`
	Output      string `json:"output"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exitCode"`
	TimedOut    bool   `json:"timedOut"`
	OutputLimit bool   `json:"outputLimit"`
	DurationMS  int64  `json:"durationMs"`
	Error       string `json:"error,omitempty"`
}
type Ready struct {
	Protocol int    `json:"protocol"`
	Kernel   string `json:"kernel"`
	Runtime  string `json:"runtime"`
	UID      int    `json:"uid"`
}

const Prefix = "AGENTLAB_GUEST_RPC "
