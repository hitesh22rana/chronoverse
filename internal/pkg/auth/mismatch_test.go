// Package auth — mismatch regression test for rotation atomicity.
//
//nolint:testpackage // needs newWithPaths access, same as rotation_test.go
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func encodePrivPEM(t *testing.T, priv ed25519.PrivateKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal priv: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b})
}

func encodePubPEM(t *testing.T, pub ed25519.PublicKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: b})
}

func TestNewWithPathsRejectsMismatchedKeypair(t *testing.T) {
	_, priv1, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		t.Fatalf("generate priv1: %v", genErr)
	}
	_, priv2, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		t.Fatalf("generate priv2: %v", genErr)
	}
	pub2, ok := priv2.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("pub2 is not ed25519: %T", priv2.Public())
	}

	dir := t.TempDir()
	privPath := filepath.Join(dir, "auth.ed")
	pubPath := filepath.Join(dir, "auth.ed.pub")

	if writeErr := os.WriteFile(privPath, encodePrivPEM(t, priv1), 0o600); writeErr != nil {
		t.Fatalf("write priv: %v", writeErr)
	}
	if writeErr := os.WriteFile(pubPath, encodePubPEM(t, pub2), 0o600); writeErr != nil {
		t.Fatalf("write pub: %v", writeErr)
	}

	_, newErr := newWithPaths("server", privPath, pubPath)
	if newErr == nil {
		t.Fatal("expected newWithPaths to fail on mismatched pair, got nil")
	}
	if status.Code(newErr) != codes.Internal {
		t.Fatalf("expected Internal, got %v", status.Code(newErr))
	}
	if got := status.Convert(newErr).Message(); got != "private and public key mismatch" && !strings.Contains(got, "mismatch") {
		t.Fatalf("expected mismatch message, got %q", got)
	}
}

func TestNewWithPathsAcceptsMatchingKeypair(t *testing.T) {
	_, priv, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		t.Fatalf("generate: %v", genErr)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("pub is not ed25519: %T", priv.Public())
	}

	// Bundle lookup is relative to CWD (certs/issuers/trusted.json), so lay
	// out a full issuer tree under a temp root and chdir into it.
	root := t.TempDir()
	t.Chdir(root)
	issuersDir := filepath.Join(root, "certs", "issuers", "server")
	if mkdirErr := os.MkdirAll(issuersDir, 0o755); mkdirErr != nil {
		t.Fatalf("mkdir issuers: %v", mkdirErr)
	}
	privPath := filepath.Join(issuersDir, "auth.ed")
	pubPath := filepath.Join(issuersDir, "auth.ed.pub")

	if writeErr := os.WriteFile(privPath, encodePrivPEM(t, priv), 0o600); writeErr != nil {
		t.Fatalf("write priv: %v", writeErr)
	}
	if writeErr := os.WriteFile(pubPath, encodePubPEM(t, pub), 0o600); writeErr != nil {
		t.Fatalf("write pub: %v", writeErr)
	}
	kid := kidForKey("server", pub)
	bundle := `{"` + kid + `": {"iss": "server", "pub": "server/auth.ed.pub"}}`
	if writeErr := os.WriteFile(filepath.Join(root, "certs", "issuers", "trusted.json"), []byte(bundle), 0o600); writeErr != nil {
		t.Fatalf("write bundle: %v", writeErr)
	}

	auth, newErr := newWithPaths("server", privPath, pubPath)
	if newErr != nil {
		t.Fatalf("expected newWithPaths to succeed on matching pair, got %v", newErr)
	}
	if auth == nil || auth.kid == "" {
		t.Fatalf("expected auth with kid, got %+v", auth)
	}
	if auth.kid != kid {
		t.Fatalf("expected bundle kid %q, got %q", kid, auth.kid)
	}
}

func TestNewWithPathsRejectsMissingBundle(t *testing.T) {
	_, priv, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		t.Fatalf("generate: %v", genErr)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("pub is not ed25519: %T", priv.Public())
	}

	// Empty CWD: no bundle at either canonical location — must fail closed
	// instead of silently disabling kid-bound verification.
	t.Chdir(t.TempDir())
	if _, err := os.Stat("/certs/issuers/trusted.json"); err == nil {
		t.Skip("host /certs bundle present; would fail with wrong error message")
	}
	dir := t.TempDir()
	privPath := filepath.Join(dir, "auth.ed")
	pubPath := filepath.Join(dir, "auth.ed.pub")

	if writeErr := os.WriteFile(privPath, encodePrivPEM(t, priv), 0o600); writeErr != nil {
		t.Fatalf("write priv: %v", writeErr)
	}
	if writeErr := os.WriteFile(pubPath, encodePubPEM(t, pub), 0o600); writeErr != nil {
		t.Fatalf("write pub: %v", writeErr)
	}

	_, newErr := newWithPaths("server", privPath, pubPath)
	if newErr == nil {
		t.Fatal("expected newWithPaths to fail without a bundle, got nil")
	}
	if status.Code(newErr) != codes.Internal {
		t.Fatalf("expected Internal, got %v", status.Code(newErr))
	}
	if got := status.Convert(newErr).Message(); !strings.Contains(got, "trusted bundle is required") {
		t.Fatalf("expected required-bundle message, got %q", got)
	}
}
