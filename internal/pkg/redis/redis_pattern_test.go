//nolint:testpackage // Accesses the client to configure Redis ACL failure cases.
package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	goredis "github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIntegrationDeleteByPattern(t *testing.T) {
	requirePatternDocker(t)
	ctx := t.Context()
	container, err := tcredis.Run(ctx, "redis:8.2.1-alpine")
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	t.Cleanup(func() {
		if terminateErr := container.Terminate(context.Background()); terminateErr != nil {
			t.Errorf("terminate Redis: %v", terminateErr)
		}
	})
	address, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	admin := goredis.NewClient(&goredis.Options{Addr: address})
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	store := &Store{client: admin}

	t.Run("multiple scan pages preserve other keys", func(t *testing.T) {
		checkPatternPages(ctx, t, admin, store)
	})

	t.Run("preserved keys retain store contracts", func(t *testing.T) {
		checkPreservedKeyContracts(ctx, t, admin, store)
	})
	t.Run("conditional writes preserve first value and TTL", func(t *testing.T) {
		checkConditionalWriteContracts(ctx, t, admin, store)
	})
	t.Run("pipeline execution and rejection", func(t *testing.T) {
		checkPipelineContracts(ctx, t, admin, store)
	})
	t.Run("log channel preserves messages", func(t *testing.T) {
		checkPatternLogChannel(ctx, t, store)
	})

	t.Run("canceled context", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		assertPatternFailure(canceled, t, store, "context canceled", "failed to scan keys")
	})
	t.Run("expired deadline", func(t *testing.T) {
		expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		assertPatternFailure(expired, t, store, "context deadline exceeded", "failed to scan keys")
	})

	for _, denied := range []struct{ name, permission, operation string }{
		{"scan denied", "-scan", "failed to scan keys"},
		{"pipeline delete denied", "-del", "failed to delete keys"},
	} {
		t.Run(denied.name, func(t *testing.T) {
			username := strings.ReplaceAll(denied.name, " ", "-")
			if err := admin.Do(ctx, "ACL", "SETUSER", username, "on", ">test-password", "~*", "+@all", denied.permission).Err(); err != nil {
				t.Fatal(err)
			}
			client := goredis.NewClient(&goredis.Options{Addr: address, Username: username, Password: "test-password", MaxRetries: -1})
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			key := "denied:" + username
			if err := admin.Set(ctx, key, "preserved", time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			assertPatternFailure(ctx, t, &Store{client: client}, "NOPERM", denied.operation)
			if value, err := admin.Get(ctx, key).Result(); err != nil || value != "preserved" {
				t.Fatalf("failed operation changed key: value=%q error=%v", value, err)
			}
		})
	}
}

func assertPatternFailure(ctx context.Context, t *testing.T, store *Store, cause, operation string) {
	t.Helper()
	deleted, err := store.DeleteByPattern(ctx, "denied:*")
	if deleted != 0 || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), cause) || !strings.Contains(err.Error(), operation) {
		t.Fatalf("DeleteByPattern = %d, %v; want zero/Internal with %q and %q", deleted, err, cause, operation)
	}
}

func requirePatternDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	strictValue, found := os.LookupEnv("TESTKIT_STRICT")
	if !found || strictValue == "" {
		strictValue = os.Getenv("CI")
	}
	strict, parseErr := strconv.ParseBool(strictValue)
	if parseErr != nil {
		strict = false
	}
	client, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err == nil {
		defer client.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		_, err = client.Ping(ctx)
	}
	if err != nil {
		if strict {
			t.Fatalf("Docker unavailable: %v", err)
		}
		t.Skipf("Docker unavailable: %v", err)
	}
}

func checkPatternPages(ctx context.Context, t *testing.T, admin *goredis.Client, store *Store) {
	t.Helper()
	pipe := admin.Pipeline()
	const matched = 1400
	for i := range matched {
		pipe.Set(ctx, fmt.Sprintf("cache:tenant-a:%04d", i), "cached", time.Hour)
	}
	for i := range 300 {
		pipe.Set(ctx, fmt.Sprintf("cache:tenant-b:%04d", i), "retained", time.Hour)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, cursor, err := admin.Scan(ctx, 0, "cache:tenant-a:*", 100).Result()
	if err != nil || cursor == 0 {
		t.Fatalf("fixture must require multiple SCAN pages: cursor=%d error=%v", cursor, err)
	}
	deleted, err := store.DeleteByPattern(ctx, "cache:tenant-a:*")
	if err != nil || deleted != matched {
		t.Fatalf("DeleteByPattern = %d, %v; want %d, nil", deleted, err, matched)
	}
	keys, err := admin.Keys(ctx, "cache:tenant-a:*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("matching keys remain: %v, %v", keys, err)
	}
	keys, err = admin.Keys(ctx, "cache:tenant-b:*").Result()
	if err != nil || len(keys) != 300 {
		t.Fatalf("retained keys = %d, %v; want 300", len(keys), err)
	}
	checkPatternValues(ctx, t, admin, keys)
	deleted, err = store.DeleteByPattern(ctx, "cache:tenant-a:*")
	if err != nil || deleted != 0 {
		t.Fatalf("repeat deletion = %d, %v; want 0, nil", deleted, err)
	}
}

func checkPreservedKeyContracts(ctx context.Context, t *testing.T, admin *goredis.Client, store *Store) {
	t.Helper()
	const key = "cache:tenant-b:contract"
	if err := store.Set(ctx, key, "preserved", time.Hour); err != nil {
		t.Fatal(err)
	}
	var value string
	if _, err := store.Get(ctx, key, &value); err != nil || value != "preserved" {
		t.Fatalf("Get = %q, %v", value, err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, key, &value); status.Code(err) != codes.NotFound {
		t.Fatalf("missing Get = %v", err)
	}
	if err := store.Delete(ctx, key); status.Code(err) != codes.NotFound {
		t.Fatalf("missing Delete = %v", err)
	}
	if err := admin.Set(ctx, key, "invalid JSON", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, key, &value); status.Code(err) != codes.Internal {
		t.Fatalf("invalid JSON Get = %v", err)
	}
}

func checkPatternLogChannel(ctx context.Context, t *testing.T, store *Store) {
	t.Helper()
	channel := GetJobLogsChannel("pattern-invalidation")
	if channel != "job_logs:pattern-invalidation" {
		t.Fatalf("channel = %q", channel)
	}
	pubsub := store.Subscribe(ctx, channel)
	t.Cleanup(func() {
		if err := pubsub.Close(); err != nil {
			t.Error(err)
		}
	})
	receiveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := pubsub.Receive(receiveCtx); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, channel, "preserved"); err != nil {
		t.Fatal(err)
	}
	message, err := pubsub.ReceiveMessage(receiveCtx)
	if err != nil {
		t.Fatal(err)
	}
	if message.Channel != channel || message.Payload != "preserved" {
		t.Fatalf("message = %+v", message)
	}
}

func checkPatternValues(ctx context.Context, t *testing.T, admin *goredis.Client, keys []string) {
	t.Helper()
	for _, key := range keys {
		value, getErr := admin.Get(ctx, key).Result()
		if getErr != nil || value != "retained" {
			t.Fatalf("preserved %s = %q, %v", key, value, getErr)
		}
	}
}

func TestStoreOperationsAfterClose(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	store := &Store{client: client}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("get", func(t *testing.T) {
		var value string
		result, err := store.Get(t.Context(), "closed", &value)
		if result != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
			t.Fatalf("Get = %v, %v; want nil/Internal with closed client", result, err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		err := store.Delete(t.Context(), "closed")
		if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
			t.Fatalf("Delete = %v; want Internal with closed client", err)
		}
	})
	t.Run("exists", func(t *testing.T) {
		exists, err := store.Exists(t.Context(), "closed")
		if exists || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
			t.Fatalf("Exists = %v, %v", exists, err)
		}
	})
	t.Run("conditional write", func(t *testing.T) {
		created, err := store.SetNX(t.Context(), "closed", "value", time.Hour)
		if created || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
			t.Fatalf("SetNX = %v, %v", created, err)
		}
	})
	t.Run("publish", func(t *testing.T) {
		err := store.Publish(t.Context(), "closed", "message")
		if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
			t.Fatalf("Publish = %v; want Internal with closed client", err)
		}
	})
}

func checkConditionalWriteContracts(ctx context.Context, t *testing.T, admin *goredis.Client, store *Store) {
	t.Helper()
	const key = "conditional:first-writer"
	assertStoreExists(ctx, t, store, key, false)
	if created, err := store.SetNX(ctx, key, "first", time.Hour); err != nil || !created {
		t.Fatalf("first SetNX = %v, %v", created, err)
	}
	assertStoreExists(ctx, t, store, key, true)
	if created, err := store.SetNX(ctx, key, "replacement", time.Minute); err != nil || created {
		t.Fatalf("contended SetNX = %v, %v", created, err)
	}
	var value string
	if _, err := store.Get(ctx, key, &value); err != nil || value != "first" {
		t.Fatalf("first writer value = %q, %v", value, err)
	}
	if ttl, err := admin.PTTL(ctx, key).Result(); err != nil || ttl <= 59*time.Minute || ttl > time.Hour {
		t.Fatalf("first writer TTL = %v, %v", ttl, err)
	}
	if deleted, err := store.DeleteByPattern(ctx, "conditional:*"); err != nil || deleted != 1 {
		t.Fatalf("conditional cleanup = %d, %v", deleted, err)
	}
	assertStoreExists(ctx, t, store, key, false)
}

func checkPipelineContracts(ctx context.Context, t *testing.T, admin *goredis.Client, store *Store) {
	t.Helper()
	const key = "pipeline:contract"
	var read *goredis.StringCmd
	if err := store.Pipeline(ctx, func(pipe goredis.Pipeliner) error {
		pipe.Set(ctx, key, "first", time.Hour)
		read = pipe.Get(ctx, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if value, err := read.Result(); err != nil || value != "first" {
		t.Fatalf("ordered pipeline read = %q, %v", value, err)
	}
	rejected := errors.New("reject queued write")
	err := store.Pipeline(ctx, func(pipe goredis.Pipeliner) error {
		pipe.Set(ctx, key, "rejected", time.Hour)
		return rejected
	})
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), rejected.Error()) {
		t.Fatalf("callback rejection = %v", err)
	}
	if value, getErr := admin.Get(ctx, key).Result(); getErr != nil || value != "first" {
		t.Fatalf("rejected callback changed key: %q, %v", value, getErr)
	}
	err = store.Pipeline(ctx, func(pipe goredis.Pipeliner) error {
		pipe.LPush(ctx, key, "wrong type")
		return nil
	})
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("pipeline command failure = %v", err)
	}
	if value, getErr := admin.Get(ctx, key).Result(); getErr != nil || value != "first" {
		t.Fatalf("failed command changed key: %q, %v", value, getErr)
	}
}

func TestSetNXRejectsUnserializableValue(t *testing.T) {
	store := &Store{}
	created, err := store.SetNX(t.Context(), "unserializable", make(chan int), time.Hour)
	if created || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to marshal value") {
		t.Fatalf("SetNX unsupported JSON = %v, %v", created, err)
	}
}

func assertStoreExists(ctx context.Context, t *testing.T, store *Store, key string, want bool) {
	t.Helper()
	if exists, err := store.Exists(ctx, key); err != nil || exists != want {
		t.Fatalf("Exists(%q) = %v, %v; want %v", key, exists, err, want)
	}
}
