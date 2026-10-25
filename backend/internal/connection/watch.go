package connection

import (
	"context"
	"time"
)

// Watch closes active requests on rotation, revocation, expiry or user deactivation, also across API processes.
// A sent request may still incur supplier usage; cancellation never implies a refund.
func (s *Store) Watch(parent context.Context, c Credential, user, provider, model string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(ctx, 2*time.Second)
				_, err := s.ForJob(check, c.ID, c.Generation, user, provider, model)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}
