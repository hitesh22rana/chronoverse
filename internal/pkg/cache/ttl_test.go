package cache_test

import (
	"testing"
	"time"

	cachepkg "github.com/hitesh22rana/chronoverse/internal/pkg/cache"
)

func TestAddJitter(t *testing.T) {
	ttl := 30 * time.Minute
	maxJitter := 3 * time.Minute

	for range 100 {
		got := cachepkg.AddJitter(ttl, maxJitter)
		if got < ttl || got > ttl+maxJitter {
			t.Fatalf("AddJitter() = %s, want value between %s and %s", got, ttl, ttl+maxJitter)
		}
	}
}

func TestAddJitterDisabled(t *testing.T) {
	tests := []struct {
		name      string
		ttl       time.Duration
		maxJitter time.Duration
	}{
		{name: "no expiration", ttl: 0, maxJitter: time.Minute},
		{name: "negative expiration", ttl: -time.Second, maxJitter: time.Minute},
		{name: "no jitter", ttl: time.Minute, maxJitter: 0},
		{name: "negative jitter", ttl: time.Minute, maxJitter: -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepkg.AddJitter(tt.ttl, tt.maxJitter); got != tt.ttl {
				t.Fatalf("AddJitter() = %s, want %s", got, tt.ttl)
			}
		})
	}
}
