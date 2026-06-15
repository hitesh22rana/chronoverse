package cache

import (
	"crypto/rand"
	"math/big"
	"time"
)

// AddJitter spreads cache expirations across the interval [ttl, ttl+maxJitter].
func AddJitter(ttl, maxJitter time.Duration) time.Duration {
	if ttl <= 0 || maxJitter <= 0 {
		return ttl
	}

	jitter, err := rand.Int(rand.Reader, big.NewInt(int64(maxJitter)+1))
	if err != nil {
		return ttl
	}

	return ttl + time.Duration(jitter.Int64())
}
