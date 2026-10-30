//nolint:testpackage // Tests private claim and token extraction contracts.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthorizationTransportRejectsMalformedBearerValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "Bearer", "Bearer ", "Bearer  token", "Bearer token extra", "Basic token", "Bearer\ttoken"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			headers := metadata.Pairs(authorizationMetadataKey, value)
			for _, extract := range []func() (string, error){
				func() (string, error) { return ExtractAuthorizationTokenFromHeaders(headers) },
				func() (string, error) {
					return ExtractAuthorizationTokenFromMetadata(metadata.NewIncomingContext(t.Context(), headers))
				},
			} {
				token, err := extract()
				if token != "" || status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("malformed header %q: token=%q err=%v", value, token, err)
				}
			}
		})
	}
}

func TestAuthorizationTransportRejectsDuplicatesAndRetainsValidCredentials(t *testing.T) {
	t.Parallel()
	duplicate := metadata.Pairs(authorizationMetadataKey, "Bearer first", authorizationMetadataKey, "Bearer second")
	if token, err := ExtractAuthorizationTokenFromHeaders(duplicate); token != "" || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("duplicate authorization values: token=%q err=%v", token, err)
	}
	if token, err := ExtractAuthorizationTokenFromMetadata(metadata.NewIncomingContext(t.Context(), duplicate)); token != "" || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("duplicate metadata values: token=%q err=%v", token, err)
	}
	if token, err := ExtractAuthorizationTokenFromHeaders(metadata.MD{}); token != "" || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing header: token=%q err=%v", token, err)
	}
	if token, err := ExtractAuthorizationTokenFromMetadata(t.Context()); token != "" || status.Code(err) != codes.NotFound {
		t.Fatalf("missing metadata: token=%q err=%v", token, err)
	}
	for _, extract := range []func() (string, error){
		func() (string, error) {
			return ExtractAuthorizationTokenFromHeaders(WithSetAuthorizationTokenInHeaders("signed.token"))
		},
		func() (string, error) {
			return ExtractAuthorizationTokenFromMetadata(metadata.NewIncomingContext(t.Context(), WithSetAuthorizationTokenInHeaders("signed.token")))
		},
	} {
		if token, err := extract(); err != nil || token != "signed.token" {
			t.Fatalf("valid token=%q err=%v", token, err)
		}
	}
}

func TestOutgoingAuthorizationReplacesOnlyCopiedBearerMetadata(t *testing.T) {
	t.Parallel()
	original := metadata.Pairs(authorizationMetadataKey, "Bearer old", "request-id", "request-42")
	ctx := metadata.NewOutgoingContext(t.Context(), original)
	updated := WithAuthorizationTokenInMetadata(ctx, "new")
	got, ok := metadata.FromOutgoingContext(updated)
	if !ok || len(got.Get(authorizationMetadataKey)) != 1 || got.Get(authorizationMetadataKey)[0] != "Bearer new" || got.Get("request-id")[0] != "request-42" {
		t.Fatalf("updated metadata=%v", got)
	}
	if original.Get(authorizationMetadataKey)[0] != "Bearer old" {
		t.Fatal("input metadata mutated")
	}
	unchanged, _ := metadata.FromOutgoingContext(ctx)
	if unchanged.Get(authorizationMetadataKey)[0] != "Bearer old" {
		t.Fatal("input context mutated")
	}
	plain, _ := metadata.FromOutgoingContext(WithAuthorizationTokenInMetadata(t.Context(), "first"))
	if got := plain.Get(authorizationMetadataKey); len(got) != 1 || got[0] != "Bearer first" {
		t.Fatalf("fresh metadata=%v", plain)
	}
}

func TestInternalAuthorizationPropagatesValidatedAdminClaims(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAuth(t)
	ctx, err := WithInternalServiceAuthorization(t.Context(), a, "worker-42", ServiceNameJobs, ServiceNameWorkflows)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	token, err := ExtractAuthorizationTokenFromHeaders(md)
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{ServiceNameJobs, ServiceNameWorkflows} {
		validated, parsed, validationErr := a.ValidateToken(WithAuthorizationToken(t.Context(), token), audience)
		if validationErr != nil || parsed == nil || !IsInternalService(validated) {
			t.Fatalf("audience=%s token=%v err=%v", audience, parsed, validationErr)
		}
		if subject, subjectErr := subjectFromContext(validated); subjectErr != nil || subject != "worker-42" {
			t.Fatalf("subject=%q err=%v", subject, subjectErr)
		}
	}
	if _, _, audienceErr := a.ValidateToken(WithAuthorizationToken(t.Context(), token), ServiceNameUsers); status.Code(audienceErr) != codes.Unauthenticated {
		t.Fatalf("unlisted audience accepted: %v", audienceErr)
	}
	failure := errors.New("issuer unavailable")
	ctx, err = WithInternalServiceAuthorization(t.Context(), failingIssuer{failure: failure}, "worker-42", ServiceNameJobs)
	if ctx != nil || !errors.Is(err, failure) {
		t.Fatalf("signing failure: ctx=%v err=%v", ctx, err)
	}
}

func TestContextClaimsRejectMissingAndEmptyValues(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		set     func(context.Context, string) context.Context
		extract func(context.Context) (string, error)
	}{
		{"audience", WithAudience, audienceFromContext}, {"role", WithRole, roleFromContext}, {"subject", WithSubject, subjectFromContext}, {"token", WithAuthorizationToken, tokenFromContext},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, ctx := range []context.Context{t.Context(), tt.set(t.Context(), "")} {
				got, err := tt.extract(ctx)
				if got != "" || status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("missing/empty %s=%q err=%v", tt.name, got, err)
				}
			}
			if got, err := tt.extract(tt.set(t.Context(), "value")); err != nil || got != "value" {
				t.Fatalf("%s=%q err=%v", tt.name, got, err)
			}
		})
	}
	a, _, _ := newTestAuth(t)
	if _, _, err := a.ValidateToken(t.Context(), ""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty audience=%v", err)
	}
}

type failingIssuer struct {
	IAuth
	failure error
}

func (f failingIssuer) IssueToken(context.Context, string, ...string) (string, error) {
	return "", f.failure
}

func TestTokenValidationRejectsUnsignedClaimsAndMismatchedIssuerBinding(t *testing.T) {
	t.Parallel()
	a, private, pub := newTestAuth(t)
	if token, err := a.IssueToken(t.Context(), "subject", ServiceNameJobs); token != "" || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing role: token=%q err=%v", token, err)
	}
	if _, parsed, err := a.ValidateToken(t.Context(), ServiceNameJobs); parsed != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing token: parsed=%v err=%v", parsed, err)
	}
	claims := jwt.MapClaims{
		jwtIssuerClaim:   testIssuer,
		jwtAudienceClaim: []string{ServiceNameJobs},
		jwtExpiryClaim:   time.Now().Add(time.Minute).Unix(),
		jwtRoleClaim:     string(RoleUser),
		jwtSubjectClaim:  "user-42",
	}
	attacker := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := attacker.SignedString([]byte("attacker-controlled-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, parsed, algorithmErr := a.ValidateToken(WithAuthorizationToken(t.Context(), signed), ServiceNameJobs); parsed != nil || status.Code(algorithmErr) != codes.Unauthenticated {
		t.Fatalf("wrong signing algorithm: parsed=%v err=%v", parsed, algorithmErr)
	}
	a.bundle = map[string]*bundleEntry{"trusted-key": {Iss: ServiceNameServer, PublicKey: pub}}
	validSignature := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	validSignature.Header["kid"] = "trusted-key"
	signed, err = validSignature.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	_, parsed, err := a.ValidateToken(WithAuthorizationToken(t.Context(), signed), ServiceNameJobs)
	if parsed != nil || status.Code(err) != codes.Unauthenticated || !strings.Contains(status.Convert(err).Message(), "kid issuer mismatch") {
		t.Fatalf("kid issuer binding: parsed=%v err=%v", parsed, err)
	}
}
