package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

type scopeLocks struct {
	keys []string
	ttls []time.Duration
	err  error
}

func (s *scopeLocks) AcquireDistributedLockWithToken(_ context.Context, key string, ttl time.Duration) (token string, acquired bool, err error) {
	s.keys = append(s.keys, key)
	s.ttls = append(s.ttls, ttl)
	return "", false, s.err
}

func (*scopeLocks) ExtendDistributedLockWithToken(context.Context, string, string, time.Duration) (bool, error) {
	panic("unexpected renewal")
}

func (*scopeLocks) ReleaseDistributedLockWithToken(context.Context, string, string) error {
	panic("unexpected release")
}

func TestContainerProviderScopesLocksAndReusesEndpoint(t *testing.T) {
	var pings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			pings.Add(1)
			w.Header().Set("Api-Version", "1.51")
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "/images/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte(`{"message":"No such image"}`))
			if err != nil {
				t.Error(err)
			}
			return
		}
		t.Errorf("unexpected Docker request %s", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	cfg := &config.ExecutionWorker{}
	cfg.ImagePullLockTTL = time.Minute
	cfg.ImagePullLockWaitTimeout = time.Second
	cfg.ImagePullLockRetryInterval = time.Millisecond
	locks := &scopeLocks{err: errors.New("lock unavailable")}
	provider, closeClients := newContainerProvider(cfg, container.ResourceLimits{}, locks)
	t.Cleanup(func() { require.NoError(t, closeClients()) })
	endpoint := "tcp://" + strings.TrimPrefix(server.URL, "http://")
	for _, runtimeID := range []string{"runtime-a", "runtime-b"} {
		before := pings.Load()
		client, err := provider(runtimeID, endpoint)
		require.NoError(t, err)
		if runtimeID == "runtime-b" {
			require.Equal(t, before, pings.Load())
		}
		require.Equal(t, endpoint, client.DockerHost())
		require.ErrorContains(t, client.Build(t.Context(), "example.invalid/workload:tag"), "lock unavailable")
	}
	require.Equal(t, []string{
		"container:image-pull:c23240e6876e4aac1f07507e4016c9cd06c3548dc832db99f4519d54d00a8598:d7f071eafd3765abbc799b0eeb21f95c4c8ad5ac9bf2586cfed78b6ecc164e75",
		"container:image-pull:147999919a3df05f7c6bf7faf52e888f6ad64a30361074012dd59eee9d616c37:d7f071eafd3765abbc799b0eeb21f95c4c8ad5ac9bf2586cfed78b6ecc164e75",
	}, locks.keys)
	require.Equal(t, []time.Duration{time.Minute, time.Minute}, locks.ttls)
	require.Positive(t, pings.Load())
	require.NoError(t, closeClients())
	client, err := provider("runtime-a", endpoint)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestContainerProviderSurfacesTLSConstructionFailure(t *testing.T) {
	cfg := &config.ExecutionWorker{}
	cfg.DockerProxy.TLS.CAFile = "missing-ca.pem"
	provider, closeClients := newContainerProvider(cfg, container.ResourceLimits{}, &scopeLocks{})
	defer func() { require.NoError(t, closeClients()) }()
	client, err := provider("runtime-a", "tcp://localhost:2376")
	require.Nil(t, client)
	require.ErrorContains(t, err, "TLS misconfigured")
}
