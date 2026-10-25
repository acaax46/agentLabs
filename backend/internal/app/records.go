package app

import (
	"context"
	"time"

	"agentlab.local/backend/internal/records"
)

func (d *Desktop) ListRuntimeRecords(kind string, q records.Query) records.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	store, err := d.service.Records()
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	value, err := store.List(ctx, kind, q)
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	return records.Response{Page: &value}
}
func (d *Desktop) GetRuntimeRecord(kind, id string) records.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	store, err := d.service.Records()
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	value, err := store.Get(ctx, kind, id)
	if err != nil {
		return records.Response{Error: records.PublicError(err)}
	}
	return records.Response{Record: &value}
}
