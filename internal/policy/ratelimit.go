package policy

import (
	"sync"
	"time"
)

// RateLimiter is a per-(service, resource) token bucket.
//
// In-process for now, so a limit is per replica. The Redis-backed
// implementation that shares limits across replicas is a drop-in behind this
// same Check method. Unlike the Python original this must be explicitly
// synchronised: net/http serves every request in its own goroutine, so the
// bucket map is genuinely concurrent here where asyncio made it effectively
// single-threaded.
type RateLimiter struct {
	mu               sync.Mutex
	buckets          map[bucketKey]*bucket
	defaultPerMinute int
	now              func() time.Time // injectable so tests need no sleeps
}

type bucketKey struct{ service, resource string }

type bucket struct {
	tokens       float64
	updated      time.Time
	capacity     float64
	refillPerSec float64
}

// NewRateLimiter builds a limiter with the given fallback limit.
func NewRateLimiter(defaultPerMinute int) *RateLimiter {
	return &RateLimiter{
		buckets:          map[bucketKey]*bucket{},
		defaultPerMinute: defaultPerMinute,
		now:              time.Now,
	}
}

// Check consumes one token, reporting whether the call is within the limit.
// A perMinute of zero or less means "use the default".
func (r *RateLimiter) Check(service, resource string, perMinute int) bool {
	rpm := perMinute
	if rpm <= 0 {
		rpm = r.defaultPerMinute
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	key := bucketKey{service, resource}
	b, ok := r.buckets[key]
	if !ok {
		// A new bucket starts full, so a service's first request is never the one
		// that gets limited.
		b = &bucket{
			tokens:       float64(rpm),
			updated:      now,
			capacity:     float64(rpm),
			refillPerSec: float64(rpm) / 60.0,
		}
		r.buckets[key] = b
	}

	b.tokens = min(b.capacity, b.tokens+now.Sub(b.updated).Seconds()*b.refillPerSec)
	b.updated = now
	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}
