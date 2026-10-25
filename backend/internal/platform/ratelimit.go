package platform

import (
	"math"
	"sync"
	"time"
)

// rateLimiter is a per-process token bucket per API key. With N API replicas a key may reach
// roughly N times its configured rate; concurrency and money limits, which are exact because they
// live in the database, are the hard guarantees. Replace this with a shared limiter at the edge
// (gateway or Redis) when strict global rates are needed.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{buckets: map[string]*bucket{}} }

// allow takes one token, or says how long until one is available.
func (l *rateLimiter) allow(key string, perMinute int, now time.Time) (bool, time.Duration) {
	if perMinute <= 0 {
		return true, 0
	}
	capacity := math.Max(5, float64(perMinute)/10)
	rate := float64(perMinute) / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	b, known := l.buckets[key]
	if !known {
		if len(l.buckets) > 20000 {
			for name, old := range l.buckets {
				if now.Sub(old.last) > 10*time.Minute {
					delete(l.buckets, name)
				}
			}
		}
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(capacity, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1-b.tokens)/rate*1000)) * time.Millisecond
	return false, wait
}
