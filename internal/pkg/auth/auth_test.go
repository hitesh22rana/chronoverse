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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testIssuer = "users-service"

func newTestAuth(t *testing.T) (*Auth, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &Auth{
		issuer:     testIssuer,
		publicKey:  publicKey,
		privateKey: privateKey,
		tp:         otel.Tracer("auth-test"),
	}, privateKey, publicKey
}

func signToken(t *testing.T, privateKey ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestValidateTokenAllowsBoundedClockSkew(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    testIssuer,
		jwtAudienceClaim:  []string{"users-service"},
		jwtNotBeforeClaim: now.Add(clockSkewLeeway / 2).Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
		jwtRoleClaim:      string(RoleUser),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	if _, _, err := authService.ValidateToken(ctx, "users-service"); err != nil {
		t.Fatalf("validate token within clock-skew leeway: %v", err)
	}
}

func TestValidateTokenRejectsExcessiveClockSkew(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    testIssuer,
		jwtAudienceClaim:  []string{"users-service"},
		jwtNotBeforeClaim: now.Add(clockSkewLeeway + time.Minute).Unix(),
		jwtExpiryClaim:    now.Add(2 * time.Minute).Unix(),
		jwtRoleClaim:      string(RoleUser),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	_, _, err := authService.ValidateToken(ctx, "users-service")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for excessive clock skew, got %v", err)
	}
}

func TestValidateTokenRejectsWrongAudience(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    testIssuer,
		jwtAudienceClaim:  []string{"workflows-service"},
		jwtNotBeforeClaim: now.Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
		jwtRoleClaim:      string(RoleUser),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	_, _, err := authService.ValidateToken(ctx, "users-service")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for wrong audience, got %v", err)
	}
}

func TestValidateTokenRejectsWrongIssuer(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    "rogue-issuer",
		jwtAudienceClaim:  []string{"users-service"},
		jwtNotBeforeClaim: now.Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
		jwtRoleClaim:      string(RoleUser),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	_, _, err := authService.ValidateToken(ctx, "users-service")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for wrong issuer, got %v", err)
	}
}

func TestValidateTokenAcceptsTrustedCrossServiceIssuer(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)
	authService.issuer = "jobs-service"

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    "workflow-worker",
		jwtAudienceClaim:  []string{"jobs-service"},
		jwtNotBeforeClaim: now.Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
		jwtRoleClaim:      string(RoleAdmin),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	if _, _, err := authService.ValidateToken(ctx, "jobs-service"); err != nil {
		t.Fatalf("validate trusted cross-service token: %v", err)
	}
}

func TestValidateTokenRejectsMissingRoleClaim(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    testIssuer,
		jwtAudienceClaim:  []string{"users-service"},
		jwtNotBeforeClaim: now.Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	_, _, err := authService.ValidateToken(ctx, "users-service")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for missing role claim, got %v", err)
	}
}

func TestValidateTokenClassifiesOnlyValidExpiredTokensAsExpired(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)
	now := time.Now()

	tests := []struct {
		name     string
		mutate   func(jwt.MapClaims)
		wantCode codes.Code
	}{
		{name: "expired only", mutate: func(jwt.MapClaims) {}, wantCode: codes.DeadlineExceeded},
		{name: "wrong audience", mutate: func(claims jwt.MapClaims) {
			claims[jwtAudienceClaim] = []string{"workflows-service"}
		}, wantCode: codes.Unauthenticated},
		{name: "untrusted issuer", mutate: func(claims jwt.MapClaims) {
			claims[jwtIssuerClaim] = "rogue-issuer"
		}, wantCode: codes.Unauthenticated},
		{name: "missing role", mutate: func(claims jwt.MapClaims) {
			delete(claims, jwtRoleClaim)
		}, wantCode: codes.Unauthenticated},
		{name: "not valid yet", mutate: func(claims jwt.MapClaims) {
			claims[jwtNotBeforeClaim] = now.Add(time.Minute).Unix()
		}, wantCode: codes.Unauthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := jwt.MapClaims{
				jwtIssuerClaim:    testIssuer,
				jwtAudienceClaim:  []string{"users-service"},
				jwtNotBeforeClaim: now.Add(-2 * time.Minute).Unix(),
				jwtExpiryClaim:    now.Add(-time.Minute).Unix(),
				jwtRoleClaim:      string(RoleUser),
			}
			tt.mutate(claims)
			token := signToken(t, privateKey, claims)
			ctx := WithAuthorizationToken(context.Background(), token)

			_, _, err := authService.ValidateToken(ctx, "users-service")
			if status.Code(err) != tt.wantCode {
				t.Fatalf("expected %s, got %v", tt.wantCode, err)
			}
		})
	}
}

func TestValidateTokenPropagatesClaimsToContext(t *testing.T) {
	authService, privateKey, _ := newTestAuth(t)

	now := time.Now()
	token := signToken(t, privateKey, jwt.MapClaims{
		jwtIssuerClaim:    testIssuer,
		jwtAudienceClaim:  []string{"server", "users-service"},
		jwtNotBeforeClaim: now.Unix(),
		jwtExpiryClaim:    now.Add(time.Minute).Unix(),
		jwtRoleClaim:      string(RoleAdmin),
		jwtSubjectClaim:   "user-42",
	})

	ctx := WithAuthorizationToken(context.Background(), token)
	newCtx, _, err := authService.ValidateToken(ctx, "users-service")
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}

	role, err := ExtractRoleFromContext(newCtx)
	if err != nil || role != string(RoleAdmin) {
		t.Fatalf("expected admin role in context, got role=%q err=%v", role, err)
	}

	aud, err := ExtractAudienceFromContext(newCtx)
	if err != nil || aud != "users-service" {
		t.Fatalf("expected users-service audience in context, got aud=%q err=%v", aud, err)
	}

	sub, err := subjectFromContext(newCtx)
	if err != nil || sub != "user-42" {
		t.Fatalf("expected user-42 subject in context, got sub=%q err=%v", sub, err)
	}
}

func TestIssueTokenStampsAudienceAndRole(t *testing.T) {
	authService, _, publicKey := newTestAuth(t)

	ctx := WithRole(context.Background(), string(RoleUser))
	tok, err := authService.IssueToken(ctx, "user-42", "server", "users-service")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	parsed, err := jwt.Parse(tok, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid signing method")
		}
		return publicKey, nil
	}, jwt.WithIssuer(testIssuer), jwt.WithAudience("server"))
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("unexpected claims type %T", parsed.Claims)
	}

	if iss, _ := claims[jwtIssuerClaim].(string); iss != testIssuer { //nolint:errcheck // test type assertion
		t.Fatalf("expected issuer %q, got %q", testIssuer, iss)
	}

	if role, _ := claims[jwtRoleClaim].(string); role != string(RoleUser) { //nolint:errcheck // test type assertion
		t.Fatalf("expected role %q, got %q", RoleUser, role)
	}
}

func TestTrustedIssuerKnownService(t *testing.T) {
	for _, iss := range []string{"server", "users-service", "scheduling-worker"} {
		if !TrustedIssuer(iss) {
			t.Fatalf("expected %q to be a trusted issuer", iss)
		}
	}
	if TrustedIssuer("rogue") {
		t.Fatalf("expected rogue issuer to be rejected")
	}
}

func TestGatewayAudiencesIncludesEveryForwardedService(t *testing.T) {
	got := map[string]struct{}{}
	for _, a := range GatewayAudiences() {
		got[a] = struct{}{}
	}
	for _, want := range []string{"server", "users-service", "workflows-service", "jobs-service", "notifications-service", "analytics-service"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("GatewayAudiences missing %q", want)
		}
	}
}

func TestExtractRoleFromMetadataStillWorksForUnauthenticatedPath(t *testing.T) {
	// The unauthenticated RegisterUser/LoginUser path on users-service
	// seeds audience/role from metadata so the repo can mint a session
	// JWT. Server-side authorization MUST NOT use this helper.
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(roleMetadataKey, string(RoleUser)))
	role, err := ExtractRoleFromMetadata(ctx)
	if err != nil || role != string(RoleUser) {
		t.Fatalf("expected role %q, got role=%q err=%v", RoleUser, role, err)
	}
}

func TestExtractAudienceFromMetadataStillWorksForUnauthenticatedPath(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(audienceMetadataKey, "users-service"))
	aud, err := ExtractAudienceFromMetadata(ctx)
	if err != nil || aud != "users-service" {
		t.Fatalf("expected audience users-service, got aud=%q err=%v", aud, err)
	}
}
