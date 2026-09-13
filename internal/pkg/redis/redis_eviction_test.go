package redis_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

// With a full instance the idle 2h session (idlest key throughout, so any
// LRU policy evicts it) survives while 30m cache keys are sacrificed.
// evicted_keys > 0 proves the run applied real pressure.
func TestVolatileTTLKeepsSessionsUnderPressure(t *testing.T) {
	ctx := t.Context()

	ctr, err := tcredis.Run(ctx, "redis:8.2.1-alpine")
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	t.Cleanup(func() {
		//nolint:errcheck // Best-effort container shutdown during test teardown.
		_ = ctr.Terminate(ctx)
	})

	host, hostErr := ctr.Host(ctx)
	if hostErr != nil {
		t.Fatalf("container host: %v", hostErr)
	}
	mapped, portErr := ctr.MappedPort(ctx, "6379/tcp")
	if portErr != nil {
		t.Fatalf("container port: %v", portErr)
	}
	portNum, atoiErr := strconv.Atoi(mapped.Port())
	if atoiErr != nil {
		t.Fatalf("container port %q: %v", mapped.Port(), atoiErr)
	}

	store, storeErr := redispkg.New(ctx, &redispkg.Config{
		Host:                     host,
		Port:                     portNum,
		PoolSize:                 10,
		MinIdleConns:             2,
		ReadTimeout:              3 * time.Second,
		WriteTimeout:             3 * time.Second,
		MaxMemory:                "5mb",
		EvictionPolicy:           "volatile-ttl",
		EvictionPolicySampleSize: 5,
		TLSConfig:                &redispkg.TLSConfig{Enabled: false},
	})
	if storeErr != nil {
		t.Fatalf("connect redis: %v", storeErr)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if setErr := store.Set(ctx, "session:victim", "user-1", 2*time.Hour); setErr != nil {
		t.Fatalf("seed session: %v", setErr)
	}

	filler := strings.Repeat("v", 2048)
	for i := range 4000 {
		if floodErr := store.Set(ctx, fmt.Sprintf("cache:%d", i), filler, 30*time.Minute); floodErr != nil {
			t.Fatalf("flood cache key %d: %v", i, floodErr)
		}
	}

	evicted, evictedErr := store.EvictedKeys(ctx)
	if evictedErr != nil {
		t.Fatalf("EvictedKeys() error = %v", evictedErr)
	}
	if evicted == 0 {
		t.Fatal("EvictedKeys() = 0, want > 0: run applied no pressure, test is vacuous")
	}

	var got string
	if _, getErr := store.Get(ctx, "session:victim", &got); getErr != nil {
		t.Fatalf("session evicted under pressure: %v", getErr)
	}
	if got != "user-1" {
		t.Fatalf("session value = %q, want %q", got, "user-1")
	}
}
