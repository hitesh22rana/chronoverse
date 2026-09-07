package heartbeat_test

import (
	"context"
	"crypto/tls"
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
		heartbeat.WithTargetResolver(resolveAnyTarget),
	)
}

func resolveAnyTarget(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
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

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return states[http.StateClosed] >= 5
	}, 500*time.Millisecond, 10*time.Millisecond, "each Execute should have closed its transport connection")
}

func newProxiedHeartbeat(
	proxyFor func(*url.URL) (*url.URL, error),
	resolve func(context.Context, string) ([]net.IPAddr, error),
) (hb *heartbeat.HeartBeat, resolved func() []string) {
	var mu sync.Mutex
	var hosts []string
	h := heartbeat.New(
		heartbeat.WithDialerFactory(func(time.Duration) *net.Dialer { return &net.Dialer{} }),
		heartbeat.WithProxyResolver(proxyFor),
		heartbeat.WithTargetResolver(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			mu.Lock()
			hosts = append(hosts, host)
			mu.Unlock()
			return resolve(ctx, host)
		}),
	)
	return h, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), hosts...)
	}
}

func newTunnelProxy(
	t *testing.T,
	handle func(connectRequest, request *http.Request) (int, http.Header),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, connectRequest *http.Request) {
		if connectRequest.Method != http.MethodConnect {
			t.Errorf("proxy method = %s, want CONNECT", connectRequest.Method)
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("proxy response writer does not support hijacking")
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		defer conn.Close()
		if _, writeErr := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); writeErr != nil {
			t.Errorf("write CONNECT response: %v", writeErr)
			return
		}
		if flushErr := buffered.Flush(); flushErr != nil {
			t.Errorf("flush CONNECT response: %v", flushErr)
			return
		}

		tunneledRequest, err := http.ReadRequest(buffered.Reader)
		if err != nil {
			t.Errorf("read tunneled request: %v", err)
			return
		}
		defer tunneledRequest.Body.Close()
		statusCode, headers := handle(connectRequest, tunneledRequest)
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		if _, err := fmt.Fprintf(buffered, "HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)); err != nil {
			t.Errorf("write tunneled status: %v", err)
			return
		}
		for key, values := range headers {
			for _, value := range values {
				if _, err := fmt.Fprintf(buffered, "%s: %s\r\n", key, value); err != nil {
					t.Errorf("write tunneled header: %v", err)
					return
				}
			}
		}
		if _, err := buffered.WriteString("Content-Length: 0\r\nConnection: close\r\n\r\n"); err != nil {
			t.Errorf("write tunneled response: %v", err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Errorf("flush tunneled response: %v", err)
		}
	}))
}

func TestHeartBeat_Execute_EnvProxyPinsValidatedDestination(t *testing.T) {
	var proxyMu sync.Mutex
	var connectTargets []string
	var originHosts []string
	var proxyAuthorization []string
	proxy := newTunnelProxy(t, func(connectRequest, request *http.Request) (int, http.Header) {
		proxyMu.Lock()
		connectTargets = append(connectTargets, connectRequest.Host)
		originHosts = append(originHosts, request.Host)
		proxyAuthorization = append(proxyAuthorization, connectRequest.Header.Get("Proxy-Authorization"))
		proxyMu.Unlock()
		return http.StatusOK, nil
	})
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	assert.NoError(t, err)
	proxyURL.User = url.UserPassword("worker", "secret")
	h, resolved := newProxiedHeartbeat(
		func(*url.URL) (*url.URL, error) { return proxyURL, nil },
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
	)

	err = h.Execute(t.Context(), 5*time.Second, "http://heartbeat.example.test/ping", http.StatusOK, nil)
	assert.NoError(t, err)

	proxyMu.Lock()
	targets := append([]string(nil), connectTargets...)
	hosts := append([]string(nil), originHosts...)
	authorization := append([]string(nil), proxyAuthorization...)
	proxyMu.Unlock()
	assert.Equal(t, []string{"93.184.216.34:80"}, targets, "proxy must receive the validated IP, not resolve the user hostname")
	assert.Equal(t, []string{"heartbeat.example.test"}, hosts, "origin Host header must be preserved")
	assert.Equal(t, []string{"Basic d29ya2VyOnNlY3JldA=="}, authorization)
	assert.Equal(t, []string{"heartbeat.example.test"}, resolved())
}

func TestHeartBeat_Execute_EnvProxyPreservesTLSIdentity(t *testing.T) {
	tlsFixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverTLSConfig := tlsFixture.TLS.Clone()
	tlsFixture.Close()

	connectTarget := make(chan string, 1)
	serverName := make(chan string, 1)
	serverTLSConfig.GetConfigForClient = func(info *tls.ClientHelloInfo) (*tls.Config, error) {
		serverName <- info.ServerName
		return nil, nil //nolint:nilnil // nil selects the current TLS config.
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connectTarget <- request.Host
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = buffered.Flush(); err != nil {
			return
		}
		tlsConn := tls.Server(conn, serverTLSConfig)
		if handshakeErr := tlsConn.HandshakeContext(request.Context()); handshakeErr == nil {
			return
		}
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	assert.NoError(t, err)

	h, _ := newProxiedHeartbeat(
		func(*url.URL) (*url.URL, error) { return proxyURL, nil },
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
	)
	err = h.Execute(t.Context(), 5*time.Second, "https://heartbeat.example.test/status", http.StatusOK, nil)
	assert.Error(t, err, "the fixture certificate is intentionally untrusted")
	select {
	case got := <-connectTarget:
		assert.Equal(t, "93.184.216.34:443", got)
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive CONNECT")
	}
	select {
	case got := <-serverName:
		assert.Equal(t, "heartbeat.example.test", got)
	case <-time.After(time.Second):
		t.Fatal("origin TLS handshake did not preserve SNI")
	}
}

func TestHeartBeat_Execute_EnvProxyRedirectReevaluatesNoProxy(t *testing.T) {
	var targetMu sync.Mutex
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetMu.Lock()
		targetHits++
		targetMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	proxyHits := 0
	proxy := newTunnelProxy(t, func(_, _ *http.Request) (int, http.Header) {
		proxyHits++
		return http.StatusFound, http.Header{"Location": {target.URL + "/final"}}
	})
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	assert.NoError(t, err)

	h, resolved := newProxiedHeartbeat(
		func(requestURL *url.URL) (*url.URL, error) {
			if requestURL.Hostname() == "heartbeat.example.test" {
				return proxyURL, nil
			}
			return nil, nil //nolint:nilnil // A nil proxy is the explicit direct-connection result.
		},
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host == "heartbeat.example.test" {
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			}
			return resolveAnyTarget(ctx, host)
		},
	)
	err = h.Execute(t.Context(), 5*time.Second, "http://heartbeat.example.test/start", http.StatusOK, nil)
	assert.NoError(t, err)

	targetMu.Lock()
	hits := targetHits
	targetMu.Unlock()
	assert.Equal(t, 1, proxyHits)
	assert.Equal(t, 1, hits, "NO_PROXY-equivalent redirect must connect directly")
	assert.Equal(t, []string{"heartbeat.example.test", mustURLHostname(t, target.URL)}, resolved())
}

func TestHeartBeat_Execute_EnvProxyRedirectCanSelectProxy(t *testing.T) {
	proxyHits := 0
	proxy := newTunnelProxy(t, func(_, _ *http.Request) (int, http.Header) {
		proxyHits++
		return http.StatusOK, nil
	})
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	assert.NoError(t, err)

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://proxied.example.test/final", http.StatusFound)
	}))
	defer direct.Close()

	h, resolved := newProxiedHeartbeat(
		func(requestURL *url.URL) (*url.URL, error) {
			if requestURL.Hostname() == "proxied.example.test" {
				return proxyURL, nil
			}
			return nil, nil //nolint:nilnil // A nil proxy is the explicit direct-connection result.
		},
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host == "proxied.example.test" {
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			}
			return resolveAnyTarget(ctx, host)
		},
	)
	err = h.Execute(t.Context(), 5*time.Second, direct.URL, http.StatusOK, nil)
	assert.NoError(t, err)
	assert.Equal(t, 1, proxyHits, "redirect must re-evaluate proxy policy")
	assert.Equal(t, []string{mustURLHostname(t, direct.URL), "proxied.example.test"}, resolved())
}

func TestHeartBeat_Execute_EnvProxyRelativeRedirectKeepsOriginalHost(t *testing.T) {
	proxyHits := 0
	proxy := newTunnelProxy(t, func(_, request *http.Request) (int, http.Header) {
		proxyHits++
		if request.URL.Path == "/start" {
			return http.StatusFound, http.Header{"Location": {"/final"}}
		}
		return http.StatusOK, nil
	})
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	assert.NoError(t, err)

	h, resolved := newProxiedHeartbeat(
		func(*url.URL) (*url.URL, error) { return proxyURL, nil },
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
	)
	err = h.Execute(t.Context(), 5*time.Second, "http://heartbeat.example.test/start", http.StatusOK, nil)
	assert.NoError(t, err)
	assert.Equal(t, 2, proxyHits)
	assert.Equal(t, []string{"heartbeat.example.test", "heartbeat.example.test"}, resolved())
}

func TestHeartBeat_Execute_ProxyResolverErrorFailsClosed(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	h, _ := newProxiedHeartbeat(
		func(*url.URL) (*url.URL, error) { return nil, fmt.Errorf("proxy configuration is invalid") },
		resolveAnyTarget,
	)
	err := h.Execute(t.Context(), 5*time.Second, target.URL, http.StatusOK, nil)
	assert.ErrorContains(t, err, "proxy configuration is invalid")
	assert.Zero(t, targetHits, "proxy errors must not fall back to a direct connection")
}

func TestHeartBeat_Execute_TargetResolverRejectionMapsToNotAllowed(t *testing.T) {
	h := heartbeat.New(
		heartbeat.WithDialerFactory(func(time.Duration) *net.Dialer { return &net.Dialer{} }),
		heartbeat.WithTargetResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return nil, fmt.Errorf("%w: 10.0.0.1", heartbeat.ErrDisallowedTarget)
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

func mustURLHostname(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Hostname()
}
