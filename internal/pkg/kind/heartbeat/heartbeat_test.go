package heartbeat_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

// unguarded returns a HeartBeat whose outbound dialer has no egress guard and
// whose destination validator accepts anything. It lets these tests exercise
// plain HTTP semantics against local httptest servers; the guard itself is
// covered by egress tests and by
// TestHeartBeat_Execute_BlockedByDefaultEgressGuard below.
func unguarded() *heartbeat.HeartBeat {
	return heartbeat.New(
		heartbeat.WithDialerFactory(func(time.Duration) *net.Dialer {
			return &net.Dialer{}
		}),
		heartbeat.WithHostValidator(func(context.Context, string) error { return nil }),
	)
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "success",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_ = heartbeat.New()
		})
	}
}

func TestHeartBeatExecuteEmitsHTTPClientTelemetry(t *testing.T) {
	oldMeterProvider := otel.GetMeterProvider()
	oldTracerProvider := otel.GetTracerProvider()
	oldPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetMeterProvider(oldMeterProvider)
		otel.SetTracerProvider(oldTracerProvider)
		otel.SetTextMapPropagator(oldPropagator)
	})

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tracerProvider := sdktrace.NewTracerProvider()
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	traceparent := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, span := tracerProvider.Tracer("heartbeat-test").Start(context.Background(), "execute")
	err := unguarded().Execute(ctx, time.Second, server.URL, http.StatusOK, nil)
	span.End()
	if err != nil {
		t.Fatalf("heartbeat execution failed: %v", err)
	}
	if got := <-traceparent; got == "" {
		t.Fatal("expected heartbeat request to propagate trace context")
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatalf("failed to collect heartbeat metrics: %v", err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "http.client.request.duration" {
				return
			}
		}
	}
	t.Fatal("http.client.request.duration metric not found")
}

func TestHeartBeat_Execute(t *testing.T) {
	tests := []struct {
		name               string
		timeout            time.Duration
		endpoint           string
		expectedStatusCode int
		headers            map[string][]string
		wantErr            bool
		wantReason         terminalreason.Code
		setup              func() *httptest.Server
	}{
		{
			name:               "success",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            map[string][]string{"Content-Type": {"application/json"}},
			wantErr:            false,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Verify headers are set correctly
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					w.WriteHeader(http.StatusOK)
					//nolint:errcheck // Write response body
					w.Write([]byte("OK"))
				}))
			},
		},
		{
			name:               "success: with multiple header values",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            map[string][]string{"Accept": {"application/json", "text/plain"}},
			wantErr:            false,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Verify multiple header values are set correctly
					acceptHeaders := r.Header["Accept"]
					assert.Contains(t, acceptHeaders, "application/json")
					assert.Contains(t, acceptHeaders, "text/plain")
					w.WriteHeader(http.StatusOK)
					//nolint:errcheck // Write response body
					w.Write([]byte("OK"))
				}))
			},
		},
		{
			name:               "success: without headers",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            false,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					//nolint:errcheck // Write response body
					w.Write([]byte("OK"))
				}))
			},
		},
		{
			name:               "error: server returns 404",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            true,
			wantReason:         terminalreason.UnexpectedStatusCode,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
		},
		{
			name:               "error: server returns 500",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            true,
			wantReason:         terminalreason.UnexpectedStatusCode,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
			},
		},
		{
			name:               "error: invalid endpoint",
			timeout:            30 * time.Second,
			endpoint:           "://invalid-url",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            true,
			setup:              nil,
		},
		{
			name:               "error: timeout",
			timeout:            1 * time.Millisecond,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            true,
			wantReason:         terminalreason.TimeLimitExceeded,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					time.Sleep(10 * time.Millisecond) // Sleep longer than timeout
					w.WriteHeader(http.StatusOK)
				}))
			},
		},
		{
			name:               "error: expected status code does not match",
			timeout:            30 * time.Second,
			endpoint:           "",
			expectedStatusCode: http.StatusOK,
			headers:            nil,
			wantErr:            true,
			wantReason:         terminalreason.UnexpectedStatusCode,
			setup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server
			if tt.setup != nil {
				server = tt.setup()
				defer server.Close()
				if tt.endpoint == "" {
					tt.endpoint = server.URL
				}
			}

			h := unguarded()
			ctx := t.Context()

			gotErr := h.Execute(ctx, tt.timeout, tt.endpoint, tt.expectedStatusCode, tt.headers)

			if tt.wantErr {
				assert.Error(t, gotErr)
			} else {
				assert.NoError(t, gotErr)
			}
			if tt.wantReason != "" {
				reason, ok := terminalreason.FromError(gotErr)
				assert.True(t, ok)
				assert.Equal(t, tt.wantReason, reason)
			}
		})
	}
}

func TestHeartBeat_Execute_BlockedByDefaultEgressGuard(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// The production constructor must refuse loopback targets outright.
	err := heartbeat.New().Execute(t.Context(), 5*time.Second, server.URL, http.StatusOK, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestHeartBeat_Execute_CleansUpTransport(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	conns := make(map[net.Conn]http.ConnState)
	states := make(map[http.ConnState]int)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		prev := conns[conn]
		if prev != http.StateNew {
			states[prev]--
		}
		states[state]++
		conns[conn] = state
		if state == http.StateClosed {
			delete(conns, conn)
		}
	}
	server.Start()
	defer server.Close()

	h := unguarded()
	for i := 0; i < 5; i++ {
		err := h.Execute(t.Context(), 2*time.Second, server.URL, http.StatusNoContent, nil)
		assert.NoError(t, err)

		// After Execute returns, the transport has called CloseIdleConnections.
		// The server should have no idle connections — they must be closed
		// well before the 30s IdleConnTimeout.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return states[http.StateIdle] == 0
		}, 500*time.Millisecond, 10*time.Millisecond, "connection should not remain idle after Execute")
	}

	// After all executions, no connections should remain idle; they should be closed.
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return states[http.StateIdle] == 0
	}, 500*time.Millisecond, 10*time.Millisecond)

	mu.Lock()
	closed := states[http.StateClosed]
	mu.Unlock()
	assert.GreaterOrEqual(t, closed, 5, "each Execute should have closed its transport connection")
}

func newProxiedHeartbeat(proxyURL string) (hb *heartbeat.HeartBeat, validated func() []string) {
	var mu sync.Mutex
	var hosts []string
	h := heartbeat.New(
		heartbeat.WithDialerFactory(func(time.Duration) *net.Dialer { return &net.Dialer{} }),
		heartbeat.WithProxyResolver(func(*url.URL) (*url.URL, error) { return url.Parse(proxyURL) }),
		heartbeat.WithHostValidator(func(_ context.Context, host string) error {
			mu.Lock()
			defer mu.Unlock()
			hosts = append(hosts, host)
			return nil
		}),
	)
	return h, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), hosts...)
	}
}

func TestHeartBeat_Execute_EnvProxyRoutesAndValidatesOriginalDestination(t *testing.T) {
	var proxyMu sync.Mutex
	var proxiedHosts []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyMu.Lock()
		proxiedHosts = append(proxiedHosts, r.Host)
		proxyMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	var targetMu sync.Mutex
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetMu.Lock()
		targetHits++
		targetMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	targetHost := mustURLHostname(t, target.URL)
	h, validated := newProxiedHeartbeat(proxy.URL)

	err := h.Execute(t.Context(), 5*time.Second, target.URL+"/ping", http.StatusOK, nil)
	assert.NoError(t, err)

	// The request was forwarded through the proxy...
	proxyMu.Lock()
	hosts := append([]string(nil), proxiedHosts...)
	proxyMu.Unlock()
	fullHost := mustURLHostPort(t, target.URL)
	assert.Equal(t, []string{fullHost}, hosts)

	// ...the ORIGINAL destination was validated, not just the proxy address...
	assert.Equal(t, []string{targetHost}, validated())

	// ...and the target was never contacted directly.
	targetMu.Lock()
	hits := targetHits
	targetMu.Unlock()
	assert.Zero(t, hits)
}

func TestHeartBeat_Execute_EnvProxyRedirectHopsRevalidated(t *testing.T) {
	// The proxy serves both hops so every round trip flows through it.
	redirected := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !redirected {
			redirected = true
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	h, validated := newProxiedHeartbeat(proxy.URL)
	err := h.Execute(t.Context(), 5*time.Second, target.URL, http.StatusOK, nil)
	assert.NoError(t, err)

	// Every redirect hop is validated individually, even when connecting via
	// the proxy (where dial-time inspection only sees the proxy address).
	assert.Len(t, validated(), 2)
}

func TestHeartBeat_Execute_HostValidatorRejectionMapsToNotAllowed(t *testing.T) {
	h := heartbeat.New(
		heartbeat.WithDialerFactory(func(time.Duration) *net.Dialer { return &net.Dialer{} }),
		heartbeat.WithHostValidator(func(_ context.Context, _ string) error {
			return fmt.Errorf("%w: 10.0.0.1", heartbeat.ErrDisallowedTarget)
		}),
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := h.Execute(t.Context(), 5*time.Second, server.URL, http.StatusOK, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint is not allowed")
}

func mustURLHostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Host
}

func mustURLHostname(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Hostname()
}
