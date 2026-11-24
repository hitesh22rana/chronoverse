//nolint:testpackage // Accesses the client to configure Redis ACL failure cases.
package redis

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
	goredis "github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIntegrationStoreOperations(t *testing.T) {
	requireStoreDocker(t)
	ctx := t.Context()
	container, err := tcredis.Run(ctx, "redis:8.10.2-alpine3.23")
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

	t.Run("value storage and deletion", func(t *testing.T) {
		checkStoreValueContracts(ctx, t, admin, store)
	})
	t.Run("conditional writes preserve first value and TTL", func(t *testing.T) {
		checkConditionalWriteContracts(ctx, t, admin, store)
	})
	t.Run("pipeline execution and rejection", func(t *testing.T) {
		checkPipelineContracts(ctx, t, admin, store)
	})
	t.Run("log channel preserves messages", func(t *testing.T) {
		checkStoreLogChannel(ctx, t, store)
	})
}

// requireStoreDocker mirrors testkit.RequireDocker: testkit imports this
// package, so an in-package test file cannot import it without a cycle.
func requireStoreDocker(t *testing.T) {
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
	client, err := dockerclient.New(dockerclient.FromEnv)
	if err == nil {
		defer client.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		_, err = client.Ping(ctx, dockerclient.PingOptions{})
	}
	if err != nil {
		if strict {
			t.Fatalf("Docker unavailable: %v", err)
		}
		t.Skipf("Docker unavailable: %v", err)
	}
}

func checkStoreValueContracts(ctx context.Context, t *testing.T, admin *goredis.Client, store *Store) {
	t.Helper()
	const key = "preserved:contract"
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

func checkStoreLogChannel(ctx context.Context, t *testing.T, store *Store) {
	t.Helper()
	channel := GetJobLogsChannel("store-contract")
	if channel != "job_logs:store-contract" {
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
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("conditional cleanup: %v", err)
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
