//nolint:testpackage // Unit tests exercise the uncached environment-proxy resolver directly.
package heartbeat

import (
	"net/url"
	"testing"
)

func TestResolveProxyFromEnvironment(t *testing.T) {
	for _, key := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"NO_PROXY", "no_proxy",
		"REQUEST_METHOD",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_PROXY", "http://proxy.example:8080")
	t.Setenv("HTTPS_PROXY", "https://secure-proxy.example:8443")
	t.Setenv("NO_PROXY", "direct.example")

	tests := []struct {
		name      string
		target    string
		wantProxy string
	}{
		{name: "http", target: "http://proxied.example/status", wantProxy: "http://proxy.example:8080"},
		{name: "https", target: "https://proxied.example/status", wantProxy: "https://secure-proxy.example:8443"},
		{name: "no proxy", target: "https://direct.example/status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := url.Parse(tt.target)
			if err != nil {
				t.Fatalf("parse target: %v", err)
			}
			proxyURL, err := resolveProxyFromEnvironment(target)
			if err != nil {
				t.Fatalf("resolve proxy: %v", err)
			}
			if tt.wantProxy == "" {
				if proxyURL != nil {
					t.Fatalf("proxy = %s, want direct", proxyURL)
				}
				return
			}
			if proxyURL == nil || proxyURL.String() != tt.wantProxy {
				t.Fatalf("proxy = %v, want %s", proxyURL, tt.wantProxy)
			}
		})
	}
}

func TestResolveProxyFromEnvironmentReturnsCGIProxyErrors(t *testing.T) {
	for _, key := range []string{"http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_PROXY", "http://proxy.example:8080")
	t.Setenv("REQUEST_METHOD", "GET")
	target, err := url.Parse("http://proxied.example/status")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if _, err := resolveProxyFromEnvironment(target); err == nil {
		t.Fatal("expected CGI HTTP_PROXY protection to return an error")
	}
}
