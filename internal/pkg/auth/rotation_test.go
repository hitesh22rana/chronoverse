//nolint:testpackage // rotation test needs access to unexported bundle helpers
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
)

//nolint:gocyclo // rotation test exercises old, new, both-bundle, new-only, and no-kid paths in one flow.
func TestKidRotationBundle(t *testing.T) {
	issuer := ServiceNameServer
	audience := ServiceNameUsers

	// Generate two distinct keys for the same issuer (old and new).
	_, oldPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate old key: %v", err)
	}
	oldPub, ok := oldPriv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("old public key is not ed25519: %T", oldPriv.Public())
	}
	oldKid := kidForKey(issuer, oldPub)

	_, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate new key: %v", err)
	}
	newPub, ok := newPriv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("new public key is not ed25519: %T", newPriv.Public())
	}
	newKid := kidForKey(issuer, newPub)
	if oldKid == newKid {
		t.Fatalf("kids must differ for distinct keys")
	}

	// Bundle containing both keys (grace period).
	bundleBoth := map[string]*bundleEntry{
		oldKid: {Iss: issuer, PublicKey: oldPub, PubPath: "old"},
		newKid: {Iss: issuer, PublicKey: newPub, PubPath: "new"},
	}
	// Old issuer instance (still active) and new issuer instance.
	oldAuth := &Auth{issuer: issuer, privateKey: oldPriv, publicKey: oldPub, kid: oldKid, bundle: bundleBoth, tp: otel.Tracer("test")}
	newAuth := &Auth{issuer: issuer, privateKey: newPriv, publicKey: newPub, kid: newKid, bundle: bundleBoth, tp: otel.Tracer("test")}
	// Verifier (e.g., users-service) trusts the same bundle.
	verifierBoth := &Auth{issuer: audience, publicKey: oldPub, kid: oldKid, bundle: bundleBoth, tp: otel.Tracer("test")}

	// Issue with old key, verify with bundle containing both -> ok.
	ctx := WithRole(context.Background(), RoleAdmin.String())
	oldToken, err := oldAuth.IssueToken(ctx, "rot-test", audience)
	if err != nil {
		t.Fatalf("issue old token: %v", err)
	}
	// Ensure kid header is set to oldKid
	if unverified, _, parseErr := new(jwt.Parser).ParseUnverified(oldToken, jwt.MapClaims{}); parseErr == nil {
		if unverified.Header["kid"] != oldKid {
			t.Fatalf("old token kid = %v, want %v", unverified.Header["kid"], oldKid)
		}
	}
	if _, _, verifyErr := verifierBoth.ValidateToken(WithAuthorizationToken(context.Background(), oldToken), audience); verifyErr != nil {
		t.Fatalf("verify old token with both-bundle verifier: %v", err)
	}

	// Issue with new key, verify with both-bundle verifier -> ok.
	newToken, err := newAuth.IssueToken(ctx, "rot-test", audience)
	if err != nil {
		t.Fatalf("issue new token: %v", err)
	}
	if unverified, _, parseErr2 := new(jwt.Parser).ParseUnverified(newToken, jwt.MapClaims{}); parseErr2 == nil {
		if unverified.Header["kid"] != newKid {
			t.Fatalf("new token kid = %v, want %v", unverified.Header["kid"], newKid)
		}
	}
	if _, _, verifyErr2 := verifierBoth.ValidateToken(WithAuthorizationToken(context.Background(), newToken), audience); verifyErr2 != nil {
		t.Fatalf("verify new token with both-bundle verifier: %v", err)
	}

	// After rotation completes, bundle contains only new key.
	bundleNewOnly := map[string]*bundleEntry{
		newKid: {Iss: issuer, PublicKey: newPub, PubPath: "new"},
	}
	verifierNewOnly := &Auth{issuer: audience, publicKey: newPub, kid: newKid, bundle: bundleNewOnly, tp: otel.Tracer("test")}

	// Old token must now be rejected (untrusted kid).
	if _, _, verifyErr3 := verifierNewOnly.ValidateToken(WithAuthorizationToken(context.Background(), oldToken), audience); verifyErr3 == nil {
		t.Fatalf("old token should be rejected after bundle prunes old kid")
	}
	// New token still valid.
	if _, _, verifyErr4 := verifierNewOnly.ValidateToken(WithAuthorizationToken(context.Background(), newToken), audience); verifyErr4 != nil {
		t.Fatalf("new token should still verify after prune: %v", err)
	}

	// Token without kid must be rejected when bundle is present.
	noKidToken := func(priv ed25519.PrivateKey) string {
		claims := jwt.MapClaims{
			jwtIssuerClaim:    issuer,
			jwtAudienceClaim:  []string{audience},
			jwtNotBeforeClaim: time.Now().Unix(),
			"iat":             time.Now().Unix(),
			jwtExpiryClaim:    time.Now().Add(time.Minute).Unix(),
			jwtSubjectClaim:   "rot-test",
			jwtRoleClaim:      RoleAdmin.String(),
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		s, err := tok.SignedString(priv)
		if err != nil {
			t.Fatalf("sign no-kid token: %v", err)
		}
		return s
	}(oldPriv)
	if _, _, verifyErr5 := verifierBoth.ValidateToken(WithAuthorizationToken(context.Background(), noKidToken), audience); verifyErr5 == nil {
		t.Fatalf("token without kid should be rejected when bundle is present")
	}
}
