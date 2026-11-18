//nolint:testpackage // Exercises image-pull cleanup through the production Redis constructor.
package redis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
)

type imagePullReleaseLocks struct{ *Store }

// Acquisition is a prerequisite here; only release socket behavior is under test.
func (imagePullReleaseLocks) AcquireDistributedLockWithToken(
	context.Context,
	string,
	time.Duration,
) (
	token string,
	acquired bool,
	err error,
) {
	return "owner", true, nil
}

type imagePullReleaseClient struct {
	cancel context.CancelFunc
}

func (imagePullReleaseClient) ImageExists(context.Context, string) (bool, error) { return false, nil }

func (imagePullReleaseClient) DockerHost() string { return "tcp://runtime:2375" }

func (c imagePullReleaseClient) Build(context.Context, string) error {
	c.cancel()
	return context.Canceled
}

func TestIntegrationImagePullReleaseDeadlineBoundsStalledRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises five-second socket cleanup timeout")
	}
	entered := make(chan struct{}, 1)
	address := redisProtocolPeer(t, nil, func(args []string) string {
		if strings.EqualFold(args[0], "eval") {
			select {
			case entered <- struct{}{}:
			default:
			}
			return "" // A stalled transport, not a simulated Lua result.
		}
		return redisHandshakeReply(args)
	})
	cfg := redisProtocolConfig(t, address)
	cfg.ReadTimeout, cfg.WriteTimeout = 20*time.Second, 20*time.Second
	store, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- imagepull.Ensure(ctx, imagePullReleaseClient{cancel: cancel}, imagePullReleaseLocks{Store: store}, "image", imagepull.Config{})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("release did not reach Redis after build cancellation")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cleanup replaced canceled build result: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("image-pull release exceeded cleanup budget with Redis stalled")
	}
}
