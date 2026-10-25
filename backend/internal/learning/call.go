package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"bytes"
	"context"
	"encoding/json"
	"io"
)

type Request struct {
	Action  string          `json:"action"`
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}
type Response struct {
	Data  any           `json:"data,omitempty"`
	Error *access.Error `json:"error,omitempty"`
}

func PublicError(err error) *access.Error {
	e := catalog.PublicError(err)
	return &access.Error{Code: e.Code, Message: e.Message, Status: e.Status, Fields: e.Fields}
}
func decode(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return invalid("请求字段或类型不正确。")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return invalid("仅接受一个 JSON 对象。")
	}
	return nil
}
func (s *Store) Call(ctx context.Context, r Request) Response {
	v, e := s.call(ctx, r)
	if e != nil {
		return Response{Error: PublicError(e)}
	}
	return Response{Data: v}
}
func (s *Store) call(ctx context.Context, r Request) (any, error) {
	if _, err := s.principal(ctx); err != nil {
		return nil, err
	}
	switch r.Action {
	case "capabilities":
		return s.Capabilities(ctx), nil
	case "releases":
		return s.Releases(ctx, r.ID)
	case "release":
		return s.GetRelease(ctx, r.ID)
	case "publish":
		var in PublishInput
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.Publish(ctx, r.ID, r.Key, in)
	case "revoke":
		return nil, s.Revoke(ctx, r.ID)
	case "open":
		return s.OpenWorkspace(ctx, r.ID)
	case "workspaces":
		return s.Workspaces(ctx)
	case "workspace":
		return s.GetWorkspace(ctx, r.ID)
	case "save", "reset":
		var in SaveInput
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		if r.Action == "save" {
			return s.Save(ctx, r.ID, in)
		}
		return s.Reset(ctx, r.ID, in)
	case "jobs":
		return s.Jobs(ctx, r.ID)
	case "job":
		return s.GetJob(ctx, r.ID)
	case "enqueue":
		var in JobInput
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.Enqueue(ctx, r.ID, r.Key, in)
	case "cancel":
		return nil, s.Cancel(ctx, r.ID)
	case "events":
		return s.Events(ctx, r.ID)
	case "budgets":
		return s.Accounts(ctx)
	case "budget":
		var in BudgetInput
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.ConfigureBudget(ctx, in)
	case "prices":
		return s.Prices(ctx)
	case "price":
		var in Price
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.AddPrice(ctx, in)
	case "ledger":
		return s.Ledger(ctx)
	case "bundles":
		return s.JudgeBundles(ctx, r.ID)
	case "bundle":
		var in JudgeBundleInput
		if e := decode(r.Payload, &in); e != nil {
			return nil, e
		}
		return s.AddJudgeBundle(ctx, in)
	case "rejudge":
		return s.Rejudge(ctx, r.ID, r.Key)
	}
	return nil, notFound()
}
