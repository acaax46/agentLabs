package app

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/learning"
	"context"
	"time"
)

func (s *Service) SetConnections(store *connection.Store) {
	s.connections = store
	if s.learning != nil {
		s.learning.SetConnections(store)
	}
	if s.execution != nil {
		s.execution.SetConnections(store)
	}
}
func (s *Service) Connections() (*connection.Store, error) {
	if s.connections == nil {
		return nil, &access.Error{Code: "CONNECTIONS_UNAVAILABLE", Message: "账户连接保管服务尚未连接。", Status: 503}
	}
	return s.connections, nil
}
func (d *Desktop) Connections(req connection.Request) learning.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return learning.Response{Error: learning.PublicError(err)}
	}
	store, err := d.service.Connections()
	if err != nil {
		return learning.Response{Error: learning.PublicError(err)}
	}
	value, err := store.Call(ctx, req)
	if err != nil {
		return learning.Response{Error: learning.PublicError(err)}
	}
	return learning.Response{Data: value}
}
