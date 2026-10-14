//nolint:testpackage // Covers private header validation and transport guards.
package heartbeat

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHeartbeatDetailsValidation(t *testing.T) {
	for _, tc := range []struct {
		name, payload, message string
		code                   codes.Code
	}{
		{"malformed", `{`, "invalid payload format", codes.InvalidArgument},
		{"invalid duration", `{"endpoint":"https://example.com","timeout":"soon"}`, "timeout is invalid", codes.InvalidArgument},
		{"zero timeout", `{"endpoint":"https://example.com","timeout":"0s"}`, "timeout is invalid", codes.InvalidArgument},
		{"negative timeout", `{"endpoint":"https://example.com","timeout":"-1s"}`, "timeout is invalid", codes.InvalidArgument},
		{"excessive timeout", `{"endpoint":"https://example.com","timeout":"301s"}`, "timeout exceeds maximum", codes.FailedPrecondition},
		{"missing endpoint", `{}`, "missing endpoint", codes.InvalidArgument},
		{"nonstring endpoint", `{"endpoint":123}`, "invalid endpoint", codes.InvalidArgument},
		{"nonnumeric status", `{"endpoint":"https://example.com","expected_status_code":"200"}`, "invalid expected status code", codes.InvalidArgument},
		{"low status", `{"endpoint":"https://example.com","expected_status_code":99}`, "between 100 and 599", codes.InvalidArgument},
		{"high status", `{"endpoint":"https://example.com","expected_status_code":600}`, "between 100 and 599", codes.InvalidArgument},
		{"invalid headers", `{"endpoint":"https://example.com","headers":{"Connection":"close"}}`, "not allowed", codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractAndValidateHeartbeatDetails(tc.payload)
			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, tc := range []struct {
		payload string
		timeout time.Duration
		code    int
	}{
		{`{"endpoint":"https://example.com"}`, 10 * time.Second, 200},
		{`{"endpoint":"https://example.com","timeout":"5m","expected_status_code":599,"headers":{"X-Test":["one","two"]}}`, 5 * time.Minute, 599},
		{`{"endpoint":"https://example.com","expected_status_code":100}`, 10 * time.Second, 100},
	} {
		details, err := ExtractAndValidateHeartbeatDetails(tc.payload)
		if err != nil || details.Endpoint != "https://example.com" || details.TimeOut != tc.timeout || details.ExpectedStatusCode != tc.code {
			t.Fatalf("details=%+v error=%v", details, err)
		}
		if strings.Contains(tc.payload, "X-Test") && strings.Join(details.Headers["X-Test"], ",") != "one,two" {
			t.Fatalf("headers=%v", details.Headers)
		}
	}
}

func TestHeartbeatHeadersRejectUnsupportedValues(t *testing.T) {
	for _, raw := range []any{[]string{"invalid"}, map[string]any{"X-Test": []any{123}}, map[string]any{"X-Test": true}} {
		if _, err := parseHeartbeatHeaders(raw); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("headers=%v error=%v", raw, err)
		}
	}
	got, err := parseHeartbeatHeaders(map[string]any{"X-Empty": []any{}})
	if err != nil || len(got["X-Empty"]) != 0 {
		t.Fatalf("headers=%v error=%v", got, err)
	}
}

func TestHeartbeatTransportRejectsInvalidResolutionAndProxy(t *testing.T) {
	proxyErr := errors.New("proxy configuration failed")
	for _, tc := range []struct {
		name      string
		addresses []net.IPAddr
		proxy     *url.URL
		proxyErr  error
		want      string
	}{
		{name: "no addresses", want: "disallowed"},
		{name: "proxy resolver error", addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, proxyErr: proxyErr, want: proxyErr.Error()},
		{name: "invalid resolved address", addresses: []net.IPAddr{{}}, proxy: &url.URL{Scheme: "http", Host: "proxy.example"}, want: "disallowed"},
		{name: "invalid proxy", addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, proxy: &url.URL{Scheme: "ftp", Host: "proxy.example:21"}, want: "unsupported proxy scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(WithTargetResolver(func(context.Context, string) ([]net.IPAddr, error) { return tc.addresses, nil }), WithProxyResolver(func(*url.URL) (*url.URL, error) { return tc.proxy, tc.proxyErr }))
			transport := newHeartbeatTransport(h, time.Second)
			defer transport.CloseIdleConnections()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(req)
			if response != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("response=%v error=%v", response, err)
			}
			if tc.proxyErr != nil && !errors.Is(err, tc.proxyErr) {
				t.Fatalf("lost proxy error: %v", err)
			}
		})
	}
}

func TestHeartbeatTransportStopsAddressFallbackAfterCancellation(t *testing.T) {
	h := New(WithTargetResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
	}), WithProxyResolver(func(*url.URL) (*url.URL, error) { return &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, nil }))
	transport := newHeartbeatTransport(h, time.Second)
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(req)
	if response != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("response=%v error=%v", response, err)
	}
	if len(transport.proxyTransports) != 1 {
		t.Fatalf("attempted %d destinations after cancellation", len(transport.proxyTransports))
	}
}

func TestHeartbeatTransportFallsBackToNextValidatedAddress(t *testing.T) {
	targets := make(chan string, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		targets <- req.Host
		if req.Host == "8.8.8.8:80" {
			http.Error(w, "destination unavailable", http.StatusBadGateway)
			return
		}
		if req.Host != "1.1.1.1:80" {
			t.Errorf("unexpected CONNECT target %s", req.Host)
			http.Error(w, "unexpected destination", http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("proxy response writer does not support hijacking")
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if deadlineErr := conn.SetDeadline(time.Now().Add(2 * fixtureTimeout)); deadlineErr != nil {
			t.Error(deadlineErr)
			return
		}
		if _, writeErr := io.WriteString(conn, connectEstablished); writeErr != nil {
			t.Error(writeErr)
			return
		}
		request, err := http.ReadRequest(buffered.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		defer request.Body.Close()
		if request.Host != "heartbeat.example.test" {
			t.Errorf("tunneled Host = %s", request.Host)
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"); err != nil {
			t.Error(err)
		}
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := New(WithTargetResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
	}), WithProxyResolver(func(*url.URL) (*url.URL, error) { return proxyURL, nil }))
	if err := h.Execute(t.Context(), fixtureTimeout, "http://heartbeat.example.test", http.StatusNoContent, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"8.8.8.8:80", "1.1.1.1:80"} {
		select {
		case got := <-targets:
			if got != want {
				t.Fatalf("CONNECT target = %s, want %s", got, want)
			}
		default:
			t.Fatalf("missing CONNECT to %s", want)
		}
	}
}

func TestConfigureNAT64PrefixesRejectsIPv4Network(t *testing.T) {
	if err := ConfigureNAT64Prefixes([]string{"10.0.0.0/24"}); err == nil || !strings.Contains(err.Error(), "must be an IPv6 CIDR") {
		t.Fatalf("error=%v", err)
	}
}
