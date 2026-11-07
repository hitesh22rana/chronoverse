package meilisearch_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
)

func TestNewSurfacesTLSBuildError(t *testing.T) {
	t.Parallel()

	var cfg config.MeiliSearch
	cfg.URI = "https://search.example.test"
	cfg.MasterKey = "test-key"
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = "/nonexistent/client.crt"
	cfg.TLS.KeyFile = "/nonexistent/client.key"
	cfg.TLS.CAFile = "/nonexistent/ca.crt"

	_, err := meilisearch.New(context.Background(), cfg.ClientConfig())
	if err == nil {
		t.Fatal("expected TLS build error, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Internal)
	}
}
