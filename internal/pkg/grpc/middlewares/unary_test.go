package middlewares_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
)

func TestUnaryLoggingInterceptorDoesNotLogAuthorizationToken(t *testing.T) {
	const sensitiveToken = "sensitive-bearer-token"

	core, logs := observer.New(zapcore.InfoLevel)
	interceptor := grpcmiddlewares.UnaryLoggingInterceptor(zap.New(core))
	// Audience and role are now derived from the validated JWT context
	// (set by auth.ValidateToken), never from the metadata header. The
	// metadata "audience"/"role" entries are still populated by the
	// gateway for the unauthenticated RegisterUser/LoginUser hop, but
	// they are intentionally NOT logged — the validated context is the
	// only authoritative source.
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer "+sensitiveToken,
	))
	ctx = auth.WithAudience(ctx, "users-service")
	ctx = auth.WithRole(ctx, string(auth.RoleUser))

	_, err := interceptor(
		ctx,
		struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Test"},
		func(context.Context, any) (any, error) { return struct{}{}, nil },
	)
	if err != nil {
		t.Fatalf("expected interceptor to succeed, got %v", err)
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}

	fields := entries[0].ContextMap()
	if _, ok := fields["auth_token"]; ok {
		t.Fatalf("authorization token field must not be logged: %#v", fields)
	}
	if strings.Contains(fmt.Sprint(fields), sensitiveToken) {
		t.Fatalf("authorization token value must not be logged: %#v", fields)
	}
	if fields["audience"] != "users-service" {
		t.Fatalf("expected audience field to remain available, got %#v", fields)
	}
	if fields["role"] != "user" {
		t.Fatalf("expected role field to remain available, got %#v", fields)
	}
}

func TestLogAuthenticationFailure(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	err := status.Error(codes.Unauthenticated, "invalid token")

	grpcmiddlewares.LogAuthenticationFailure(context.Background(), zap.New(core), err)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["grpc.code"] != codes.Unauthenticated.String() || fields["grpc.error"] != err.Error() {
		t.Fatalf("unexpected authentication failure fields: %#v", fields)
	}
}
