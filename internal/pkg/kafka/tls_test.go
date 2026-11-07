package kafka_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestNewSurfacesTLSBuildError(t *testing.T) {
	t.Parallel()

	var cfg config.Kafka
	cfg.Brokers = []string{"127.0.0.1:1"}
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = "/nonexistent/client.crt"
	cfg.TLS.KeyFile = "/nonexistent/client.key"
	cfg.TLS.CAFile = "/nonexistent/ca.crt"

	_, err := kafka.New(context.Background(), cfg.ClientConfig())
	if err == nil {
		t.Fatal("expected TLS build error, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Internal)
	}
}

func TestNewWithoutTLSUnchanged(t *testing.T) {
	t.Parallel()

	_, err := kafka.New(context.Background(), &kafka.Config{})
	if err == nil {
		t.Fatal("expected missing brokers error, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}
