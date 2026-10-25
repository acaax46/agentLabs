package app

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/learning"
	"context"
	"time"
)

func (s *Service) SetLearning(store *learning.Store) {
	s.learning = store
	if store != nil && s.connections != nil {
		store.SetConnections(s.connections)
	}
}
func (s *Service) Learning() (*learning.Store, error) {
	if s.learning == nil {
		return nil, &access.Error{Code: "LEARNING_UNAVAILABLE", Message: "教学工作台尚未连接。", Status: 503}
	}
	return s.learning, nil
}

// Learning exposes only the fixed operation whitelist in learning.Call, with the same identity boundary.
func (d *Desktop) Learning(req learning.Request) learning.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return learning.Response{Error: learning.PublicError(err)}
	}
	store, err := d.service.Learning()
	if err != nil {
		return learning.Response{Error: learning.PublicError(err)}
	}
	return store.Call(ctx, req)
}
