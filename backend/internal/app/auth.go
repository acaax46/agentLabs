package app

import (
	"context"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
)

func (d *Desktop) identity(ctx context.Context) (context.Context, error) {
	store, err := d.service.Authentication()
	if err != nil {
		return ctx, err
	}
	d.mu.RLock()
	token := d.token
	d.mu.RUnlock()
	user, err := store.Resolve(ctx, token)
	if err != nil {
		return ctx, err
	}
	return access.WithPrincipal(ctx, user.Principal), nil
}
func (d *Desktop) GetAuthState() auth.Response {
	store, err := d.service.Authentication()
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	d.mu.RLock()
	token := d.token
	d.mu.RUnlock()
	if token == "" {
		state := auth.StateFor(nil)
		return auth.Response{State: &state}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	user, err := store.Resolve(ctx, token)
	if err != nil {
		if auth.PublicError(err).Status == 401 {
			state := auth.StateFor(nil)
			return auth.Response{State: &state}
		}
		return auth.Response{Error: auth.PublicError(err)}
	}
	state := auth.StateFor(&user)
	return auth.Response{State: &state}
}
func (d *Desktop) Login(username, password string) auth.Response {
	return d.signIn(func(ctx context.Context, s *auth.Store) (auth.User, string, error) {
		return s.Login(ctx, username, password)
	})
}
func (d *Desktop) Register(input auth.RegisterInput) auth.Response {
	return d.signIn(func(ctx context.Context, s *auth.Store) (auth.User, string, error) { return s.Register(ctx, input) })
}
func (d *Desktop) signIn(fn func(context.Context, *auth.Store) (auth.User, string, error)) auth.Response {
	store, err := d.service.Authentication()
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	user, token, err := fn(ctx, store)
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	d.mu.Lock()
	old := d.token
	d.token = token
	d.mu.Unlock()
	if old != "" {
		_ = store.Logout(ctx, old)
	}
	state := auth.StateFor(&user)
	return auth.Response{State: &state}
}
func (d *Desktop) Logout() auth.Response {
	d.mu.Lock()
	token := d.token
	d.token = ""
	d.mu.Unlock()
	store, err := d.service.Authentication()
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := store.Logout(ctx, token); err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	state := auth.StateFor(nil)
	return auth.Response{State: &state}
}
func (d *Desktop) account(fn func(context.Context, *auth.Store) auth.Response) auth.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	store, err := d.service.Authentication()
	if err != nil {
		return auth.Response{Error: auth.PublicError(err)}
	}
	return fn(ctx, store)
}
func (d *Desktop) ListUsers(q string, page int) auth.Response {
	return d.account(func(ctx context.Context, s *auth.Store) auth.Response {
		value, err := s.ListUsers(ctx, q, page)
		if err != nil {
			return auth.Response{Error: auth.PublicError(err)}
		}
		return auth.Response{Page: &value}
	})
}
func (d *Desktop) CreateUser(input auth.CreateInput) auth.Response {
	return d.account(func(ctx context.Context, s *auth.Store) auth.Response {
		value, err := s.CreateUser(ctx, input)
		if err != nil {
			return auth.Response{Error: auth.PublicError(err)}
		}
		return auth.Response{User: &value}
	})
}
func (d *Desktop) UpdateUser(id string, input auth.UpdateInput) auth.Response {
	return d.account(func(ctx context.Context, s *auth.Store) auth.Response {
		value, err := s.UpdateUser(ctx, id, input)
		if err != nil {
			return auth.Response{Error: auth.PublicError(err)}
		}
		return auth.Response{User: &value}
	})
}
func (d *Desktop) ResetUserPassword(id, password string) auth.Response {
	return d.account(func(ctx context.Context, s *auth.Store) auth.Response {
		if err := s.ResetPassword(ctx, id, password); err != nil {
			return auth.Response{Error: auth.PublicError(err)}
		}
		return auth.Response{}
	})
}
func (d *Desktop) ChangePassword(current, password string) auth.Response {
	return d.account(func(ctx context.Context, s *auth.Store) auth.Response {
		if err := s.ChangePassword(ctx, current, password); err != nil {
			return auth.Response{Error: auth.PublicError(err)}
		}
		d.mu.Lock()
		d.token = ""
		d.mu.Unlock()
		return auth.Response{}
	})
}
