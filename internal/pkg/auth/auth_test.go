//nolint:testpackage // Tests bounded validation timing without widening Auth's key API.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateTokenAllowsBoundedClockSkew(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		jwtNotBeforeClaim: now.Add(clockSkewLeeway / 2).Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
	})
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	authService := &Auth{
		publicKey: publicKey,
		tp:        otel.Tracer("auth-test"),
	}
	ctx := WithAuthorizationToken(context.Background(), signedToken)
	if _, err := authService.ValidateToken(ctx); err != nil {
		t.Fatalf("validate token within clock-skew leeway: %v", err)
	}
}

func TestValidateTokenRejectsExcessiveClockSkew(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		jwtNotBeforeClaim: now.Add(clockSkewLeeway + time.Minute).Unix(),
		jwtExpiryClaim:    now.Add(2 * time.Minute).Unix(),
	})
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	authService := &Auth{
		publicKey: publicKey,
		tp:        otel.Tracer("auth-test"),
	}
	ctx := WithAuthorizationToken(context.Background(), signedToken)
	_, err = authService.ValidateToken(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for excessive clock skew, got %v", err)
	}
}
