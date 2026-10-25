package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"agentlab.local/backend/internal/access"
)

func decode(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return invalid("连接请求字段或类型不正确。")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return invalid("仅接受一个 JSON 对象。")
	}
	return nil
}
func (s *Store) Call(ctx context.Context, r Request) (any, error) {
	switch r.Action {
	case "list":
		return s.List(ctx)
	case "create":
		var in Input
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.Create(ctx, in, r.Key)
	case "verify", "rotate", "revoke":
		var in Mutation
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		if r.Action == "verify" {
			return s.Verify(ctx, r.ID, in.Version)
		}
		return s.Change(ctx, r.ID, r.Action, in)
	}
	return nil, failure("NOT_FOUND", "账户连接操作不存在。", 404)
}
func AllowedOrigins() []string {
	values := strings.Split(os.Getenv("AGENTLAB_CONNECTION_ALLOWED_ORIGINS"), ",")
	if base := os.Getenv("BASE_URL"); base != "" {
		values = append(values, base)
	}
	return values
}

// ImportEnvironment migrates the existing host gateway once. Restart never restores a revoked key.
// Import only saves a pending connection; the provider must actually verify it before use.
func (s *Store) ImportEnvironment(ctx context.Context) error {
	base, key := os.Getenv("BASE_URL"), os.Getenv("API_KEY")
	if base == "" || key == "" {
		return nil
	}
	ctx = access.System(ctx)
	items, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, c := range items {
		if c.OwnerID == "system" && c.Name == "平台模型网关" {
			return nil
		}
	}
	provider := os.Getenv("AGENTLAB_MODEL_PROVIDER")
	if provider == "" {
		provider = "openai-compatible"
	}
	_, err = s.Create(ctx, Input{Name: "平台模型网关", Provider: provider, BaseURL: base, APIKey: key, Scope: "tenant", Models: []string{"minimax/minimax-m3.1-flash", "deepseek/deepseek-v4.1-flash-1017", "z-ai/glm-5.3-flash"}}, "host-gateway-import-v1")
	return err
}
