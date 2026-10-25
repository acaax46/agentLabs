package app

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/training"
	"context"
	"time"
)

func (s *Service) SetTraining(store *training.Store) { s.training = store }
func (s *Service) Training() (*training.Store, error) {
	if s.training == nil {
		return nil, &access.Error{Code: "TRAINING_UNAVAILABLE", Message: "训练题库服务尚未连接。", Status: 503}
	}
	return s.training, nil
}

// Training exposes the fixed training operation whitelist to the desktop shell.
func (d *Desktop) Training(req training.Request) training.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return training.Response{Error: training.PublicError(err)}
	}
	store, err := d.service.Training()
	if err != nil {
		return training.Response{Error: training.PublicError(err)}
	}
	return store.Call(ctx, req)
}
