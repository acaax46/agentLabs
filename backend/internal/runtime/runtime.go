// Package runtime defines the execution boundary. No host execution fallback exists.
package runtime

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("isolated runtime is not configured")

type Status struct {
	Available bool   `json:"available"`
	Provider  string `json:"provider"`
	Reason    string `json:"reason"`
}

// Provider will be implemented by the VM + OpenSandbox adapter after isolation validation.
type Provider interface {
	Status(context.Context) Status
}

type Unconfigured struct{}

func (Unconfigured) Status(context.Context) Status {
	return Status{Available: false, Provider: "unconfigured", Reason: "尚未连接经过验证的隔离运行环境。"}
}
