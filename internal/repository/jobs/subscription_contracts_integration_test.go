//nolint:testpackage // Verifies Redis subscription acknowledgement and cleanup.
package jobs

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationJobLogSubscriptionAcknowledgesBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rdb := testkit.Redis(t)
	repo := New(&Config{}, nil, nil, rdb, nil, nil, nil)
	jobID := "subscription-" + fixtureTag()
	sub, err := repo.subscribeToJobLogsChannel(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	// Publishing immediately after return must deliver the first event: Redis
	// has acknowledged the exact job channel before the repository exposes it.
	if err = rdb.Publish(ctx, redispkg.GetJobLogsChannel(jobID), "first event"); err != nil {
		t.Fatal(err)
	}
	message, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if message.Channel != redispkg.GetJobLogsChannel(jobID) || message.Payload != "first event" {
		t.Fatalf("message=%+v", message)
	}
}

func TestIntegrationJobLogSubscriptionMapsClosedClientFailure(t *testing.T) {
	host, portText, err := net.SplitHostPort(testkit.RedisAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := redispkg.New(t.Context(), &redispkg.Config{
		Host: host, Port: port, PoolSize: 1, ReadTimeout: time.Second, WriteTimeout: time.Second,
		MaxMemory: "100mb", EvictionPolicy: "volatile-ttl", EvictionPolicySampleSize: 5,
		TLSConfig: &redispkg.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = rdb.Close(); err != nil {
		t.Fatal(err)
	}
	repo := New(&Config{}, nil, nil, rdb, nil, nil, nil)
	sub, err := repo.subscribeToJobLogsChannel(t.Context(), "closed-client")
	if sub != nil || status.Code(err) != codes.Internal {
		t.Fatalf("subscription=%v error=%v", sub, err)
	}
}

func TestIntegrationJobLogSubscriptionPreservesContextStatus(t *testing.T) {
	rdb := testkit.Redis(t)
	repo := New(&Config{}, nil, nil, rdb, nil, nil, nil)
	for _, tc := range []struct {
		name     string
		deadline bool
		code     codes.Code
	}{
		{name: "canceled", code: codes.Canceled},
		{name: "deadline", deadline: true, code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if tc.deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			} else {
				cancel()
			}
			defer cancel()
			sub, err := repo.subscribeToJobLogsChannel(ctx, "context-"+fixtureTag())
			if sub != nil || status.Code(err) != tc.code {
				t.Fatalf("subscription=%v error=%v, want %v", sub, err, tc.code)
			}
		})
	}
}
