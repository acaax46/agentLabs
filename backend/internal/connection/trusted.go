package connection

import (
	"context"
	"slices"
	"time"
)

// Lookup returns the metadata of one connection for trusted platform code that has already
// authorized the caller, for example through a project grant. It never returns the secret and,
// like List, reports an elapsed expiry as "expired".
func (s *Store) Lookup(ctx context.Context, id string) (Connection, error) {
	c, err := s.get(ctx, id)
	if err != nil {
		return c, err
	}
	if c.Status != "revoked" && c.ExpiresAt != nil && !c.ExpiresAt.After(time.Now()) {
		c.Status = "expired"
		c.Message = "连接已到期，请重新连接。"
	}
	return c, nil
}

// Credential returns the secret of exactly the given generation. It is for trusted workers that
// execute a run which was authorized when it was accepted: status, expiry, provider, model and
// generation are re-checked here, ownership is not. A rotated or revoked connection therefore
// stops serving runs that were queued against its older generation.
func (s *Store) Credential(ctx context.Context, id string, generation int, provider, model string) (Credential, error) {
	c, err := s.get(ctx, id)
	if err != nil {
		return Credential{}, err
	}
	if !usable(c, time.Now()) || c.Generation != generation || c.Provider != provider || !slices.Contains(c.Models, model) {
		return Credential{}, missing()
	}
	return s.credential(ctx, c)
}
