package cache

import (
	"math/rand/v2"
	"time"
)

// AddJitter spreads cache expirations across the interval [ttl, ttl+maxJitter].
func AddJitter(ttl, maxJitter time.Duration) time.Duration {
	if ttl <= 0 || maxJitter <= 0 {
		return ttl
	}

	return ttl + time.Duration(rand.Int64N(int64(maxJitter)+1)) //nolint:gosec // Cache-expiry jitter is non-security use; crypto/rand costs 48B and 3 allocs per call.
}
