package redis_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	testkit "github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// newTestStore starts a throwaway Redis with the given memory/policy.
func newTestStore(ctx context.Context, t *testing.T, maxMemory, evictionPolicy string) *redispkg.Store {
	t.Helper()
	//nolint:contextcheck // RequireDocker mints its own ping context; no caller ctx to propagate.
	testkit.RequireDocker(t)

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
		MaxMemory:                maxMemory,
		EvictionPolicy:           evictionPolicy,
		EvictionPolicySampleSize: 5,
		TLSConfig:                &redispkg.TLSConfig{Enabled: false},
	})
	if storeErr != nil {
		t.Fatalf("connect redis: %v", storeErr)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	return store
}

// TestIntegrationExpireCannotRecreateDeletedSession replays the logout race:
// refresh after concurrent delete must report absence, not resurrect.
func TestIntegrationExpireCannotRecreateDeletedSession(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(ctx, t, "100mb", "volatile-ttl")

	if setErr := store.Set(ctx, "session:victim", "user-1", 2*time.Hour); setErr != nil {
		t.Fatalf("seed session: %v", setErr)
	}

	// Live session: refresh succeeds and the value is untouched.
	refreshed, expireErr := store.Expire(ctx, "session:victim", 2*time.Hour)
	if expireErr != nil {
		t.Fatalf("Expire() error = %v", expireErr)
	}
	if !refreshed {
		t.Fatal("Expire() refreshed = false, want true")
	}

	// Logout deletes the session while the request is still in flight.
	if delErr := store.Delete(ctx, "session:victim"); delErr != nil {
		t.Fatalf("Delete() error = %v", delErr)
	}

	refreshed, expireErr = store.Expire(ctx, "session:victim", 2*time.Hour)
	if expireErr != nil {
		t.Fatalf("Expire() error = %v", expireErr)
	}
	if refreshed {
		t.Fatal("Expire() refreshed = true after delete, want false: refresh resurrected the session")
	}

	var got string
	if _, getErr := store.Get(ctx, "session:victim", &got); status.Code(getErr) != codes.NotFound {
		t.Fatalf("Get() code = %s, want %s: %v", status.Code(getErr), codes.NotFound, getErr)
	}
}

// Under pressure the sliding-refreshed 2h session (idlest key throughout,
// so any LRU policy evicts it) survives while 30m caches are sacrificed.
// evicted_keys > 0 proves real pressure.
func TestIntegrationVolatileTTLKeepsSessionsUnderPressure(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(ctx, t, "5mb", "volatile-ttl")

	if setErr := store.Set(ctx, "session:victim", "user-1", 2*time.Hour); setErr != nil {
		t.Fatalf("seed session: %v", setErr)
	}

	filler := strings.Repeat("v", 2048)
	for i := range 4000 {
		if floodErr := store.Set(ctx, fmt.Sprintf("cache:%d", i), filler, 30*time.Minute); floodErr != nil {
			t.Fatalf("flood cache key %d: %v", i, floodErr)
		}
		// Live request activity: slide the session back to full expiry.
		if i%500 == 499 {
			refreshed, expireErr := store.Expire(ctx, "session:victim", 2*time.Hour)
			if expireErr != nil {
				t.Fatalf("Expire() error at key %d = %v", i, expireErr)
			}
			if !refreshed {
				t.Fatalf("session evicted mid-flood at key %d despite sliding refresh", i)
			}
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
