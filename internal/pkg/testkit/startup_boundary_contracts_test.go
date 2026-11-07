//nolint:testpackage // Tests suite ownership at private fixture startup boundaries.
package testkit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaStartupFailuresRetainOwnershipAndCause(t *testing.T) {
	for _, stage := range []string{"port", "container", "partial container", "topics", "success"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("startup fault")
			s := &suite{}
			started, ensured, terminated := 0, 0, 0
			var created *kgo.Client
			fixture := terminationContainer{terminate: func(ctx context.Context) error { require.Equal(t, t.Context(), ctx); terminated++; return nil }}
			startup := kafkaStartup{
				reservePort: func() (string, error) {
					if stage == "port" {
						return "", cause
					}
					return "19092", nil
				},
				startContainer: func(ctx context.Context, request testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
					started++
					require.Equal(t, t.Context(), ctx)
					require.Equal(t, "confluentinc/cp-kafka:8.3.1", request.Image)
					require.True(t, request.Started)
					require.Equal(t, "false", request.Env["KAFKA_AUTO_CREATE_TOPICS_ENABLE"])
					require.Equal(t, "PLAINTEXT://localhost:19092", request.Env["KAFKA_ADVERTISED_LISTENERS"])
					host := &container.HostConfig{}
					request.HostConfigModifier(host)
					require.Equal(t, []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "19092"}}, host.PortBindings[network.MustParsePort("9092/tcp")])
					if stage == "container" {
						return nil, cause
					}
					if stage == "partial container" {
						return fixture, cause
					}
					return fixture, nil
				},
				ensureTopics: func(ctx context.Context, client *kgo.Client, topics ...string) error {
					ensured++
					created = client
					require.Equal(t, t.Context(), ctx)
					require.Equal(t, []string{"workflows", "jobs", "job_logs", "analytics"}, topics)
					require.Equal(t, []string{"localhost:19092"}, client.OptValue(kgo.SeedBrokers))
					if stage == "topics" {
						return cause
					}
					return nil
				},
			}
			seed, client, err := startKafkaWith(t.Context(), s, startup)
			if stage == "success" {
				require.NoError(t, err)
				require.Equal(t, "localhost:19092", seed)
				require.Same(t, created, client)
				defer client.Close()
			} else {
				require.ErrorIs(t, err, cause)
				require.Empty(t, seed)
				require.Nil(t, client)
				require.ErrorContains(t, err, map[string]string{
					"port": "reserve kafka host port", "container": "start kafka container",
					"partial container": "start kafka container", "topics": "create kafka topics",
				}[stage])
			}
			expectedStarted := 1
			if stage == "port" {
				expectedStarted = 0
			}
			require.Equal(t, expectedStarted, started)
			expectedOwned := 0
			if stage == "topics" || stage == "success" || stage == "partial container" {
				expectedOwned = 1
			}
			require.Len(t, s.containers, expectedOwned)
			require.Equal(t, map[string]int{"topics": 1, "success": 1}[stage], ensured)
			if created != nil {
				if stage == "topics" {
					require.ErrorIs(t, created.Context().Err(), context.Canceled)
				} else {
					require.NoError(t, created.Context().Err())
				}
			}
			s.terminate(t.Context())
			require.Equal(t, expectedOwned, terminated)
			s.terminate(t.Context())
			require.Equal(t, expectedOwned, terminated)
		})
	}
}

type addressedFixture struct {
	terminationContainer
	address string
	err     error
}

func (c addressedFixture) PortEndpoint(context.Context, string, string) (string, error) {
	return c.address, c.err
}

func TestMeilisearchStartupFailuresRetainContainerAndReleaseClient(t *testing.T) {
	for _, stage := range []string{"container", "partial container", "address", "setup", "success"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("startup fault")
			server, closed := fixtureHealthServer(t)
			s := &suite{meiliURI: "previous-uri"}
			terminated, setups := 0, 0
			fixture := addressedFixture{terminationContainer: terminationContainer{terminate: func(context.Context) error { terminated++; return nil }}, address: server.URL}
			if stage == "address" {
				fixture.err = cause
			}
			startup := meilisearchStartup{
				startContainer: func(ctx context.Context) (*tcmeili.MeilisearchContainer, error) {
					require.Equal(t, t.Context(), ctx)
					if stage == "container" {
						return nil, cause
					}
					container := &tcmeili.MeilisearchContainer{Container: fixture}
					if stage == "partial container" {
						return container, cause
					}
					return container, nil
				},
				setupIndexes: func(ctx context.Context, client meilisearch.ServiceManager) error {
					setups++
					require.Equal(t, t.Context(), ctx)
					health, err := client.HealthWithContext(ctx)
					require.NoError(t, err)
					require.Equal(t, "available", health.Status)
					if stage == "setup" {
						return cause
					}
					return nil
				},
			}
			client, err := startMeilisearchWith(t.Context(), s, startup)
			if stage == "success" {
				require.NoError(t, err)
				require.NotNil(t, client)
				require.Equal(t, server.URL, s.meiliURI)
				client.Close()
			} else {
				require.ErrorIs(t, err, cause)
				require.Nil(t, client)
				require.Equal(t, "previous-uri", s.meiliURI)
				require.ErrorContains(t, err, map[string]string{
					"container": "start meilisearch container", "partial container": "start meilisearch container",
					"address": "meilisearch address", "setup": "setup meilisearch indexes",
				}[stage])
			}
			expectedOwned := 1
			if stage == "container" {
				expectedOwned = 0
			}
			require.Len(t, s.containers, expectedOwned)
			wantSetups := 0
			if stage == "setup" || stage == "success" {
				wantSetups = 1
			}
			require.Equal(t, wantSetups, setups)
			if wantSetups == 1 {
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("startup client retained its idle connection")
				}
			}
			s.terminate(t.Context())
			require.Equal(t, expectedOwned, terminated)
			s.terminate(t.Context())
			require.Equal(t, expectedOwned, terminated)
		})
	}
}

func fixtureHealthServer(t *testing.T) (server *httptest.Server, closed <-chan struct{}) {
	t.Helper()
	closedSignal := make(chan struct{})
	var once sync.Once
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.Header.Get("Authorization") != "Bearer chronoverse-test-master-key" {
			t.Error("client lost endpoint or key")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"status":"available"}`))
		if err != nil {
			t.Error(err)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			once.Do(func() { close(closedSignal) })
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server, closedSignal
}
