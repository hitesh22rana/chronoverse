//nolint:testpackage // Tests private trusted-bundle loading without widening the key API.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func writeContractFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedBundleRejectsMissingMalformedAndUnusableEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, contents, want string }{
		{"invalid JSON", `{`, "parse trusted bundle"},
		{"empty", `{}`, "contains no valid entries"},
		{"null", `null`, "contains no valid entries"},
		{"missing kid", `{"":{"iss":"server","pub":"server/auth.ed.pub"}}`, "missing kid/iss/pub"},
		{"missing issuer", `{"key":{"pub":"server/auth.ed.pub"}}`, "missing kid/iss/pub"},
		{"missing public key", `{"key":{"iss":"server"}}`, "missing kid/iss/pub"},
		{"unreadable public key", `{"key":{"iss":"server","pub":"missing.pem"}}`, "failed to load public key"},
		{"invalid inline public key", `{"key":{"iss":"server","pub":"-----BEGIN PUBLIC KEY-----invalid"}}`, "invalid inline public key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "trusted.json")
			writeContractFile(t, path, []byte(tc.contents))
			bundle, err := loadBundle(path)
			if bundle != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("bundle=%v err=%v, want %q", bundle, err, tc.want)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	if bundle, err := loadBundle(missing); bundle != nil || !os.IsNotExist(err) {
		t.Fatalf("missing bundle=%v err=%v", bundle, err)
	}
}

func TestTrustedBundleLoadsAbsoluteRelativeAndInlinePublicKeys(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pem := encodePubPEM(t, pub)
	absolute := filepath.Join(root, "absolute.pem")
	writeContractFile(t, absolute, pem)
	writeContractFile(t, filepath.Join(root, "server", "auth.ed.pub"), pem)
	entries := map[string]bundleFileEntry{
		"absolute": {Iss: "server", Pub: absolute},
		"relative": {Iss: "server", Pub: "server/auth.ed.pub"},
		"inline":   {Iss: "server", Pub: string(pem)},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "trusted.json")
	writeContractFile(t, path, data)
	bundle, err := loadBundle(path)
	if err != nil || len(bundle) != len(entries) {
		t.Fatalf("bundle=%v err=%v", bundle, err)
	}
	for kid, expected := range entries {
		entry := bundle[kid]
		got, ok := entry.PublicKey.(ed25519.PublicKey)
		if !ok || !got.Equal(pub) || entry.Iss != expected.Iss {
			t.Fatalf("wrong binding for %q: %v", kid, entry)
		}
		wantPath := expected.Pub
		if kid == "inline" {
			wantPath = path + ":" + kid
		}
		if entry.PubPath != wantPath {
			t.Errorf("%s path=%q want%q", kid, entry.PubPath, wantPath)
		}
	}
	if kid, found := findBundleKid(bundle, "unrelated", pub); found || kid != "" {
		t.Fatalf("cross-issuer binding=%q found=%v", kid, found)
	}
}

func TestKeyLoadingRejectsUnreadableAndMalformedPublicKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "key.pem")
	if pub, data, err := loadPublicKeyFromPEM(path); pub != nil || data != nil || !os.IsNotExist(err) {
		t.Fatalf("missing key: %v %v", pub, err)
	}
	writeContractFile(t, path, []byte("not a public key"))
	if pub, data, err := loadPublicKeyFromPEM(path); pub != nil || data != nil || err == nil {
		t.Fatalf("malformed key: %v %v", pub, err)
	}
}

func TestConstructorRejectsKeyFileFailuresBeforeBundleLookup(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, private, public, want string }{
		{"missing private", "missing", "valid", "failed to read private key"},
		{"malformed private", "invalid", "valid", "failed to parse private key"},
		{"missing public", "valid", "missing", "failed to read public key"},
		{"malformed public", "valid", "invalid", "failed to parse public key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			private := filepath.Join(root, "private.pem")
			public := filepath.Join(root, "public.pem")
			switch tc.private {
			case "valid":
				writeContractFile(t, private, encodePrivPEM(t, priv))
			case "invalid":
				writeContractFile(t, private, []byte("invalid"))
			}
			switch tc.public {
			case "valid":
				writeContractFile(t, public, encodePubPEM(t, pub))
			case "invalid":
				writeContractFile(t, public, []byte("invalid"))
			}
			instance, err := newWithPaths("server", private, public)
			if instance != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.want) {
				t.Fatalf("instance=%v err=%v want%q", instance, err, tc.want)
			}
		})
	}
}

func TestConstructorFailsClosedOnCorruptOrWrongIssuerBundle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, bundle, want string }{
		{"corrupt", "{", "failed to load trusted bundle"},
		{"wrong issuer", `{"other":{"iss":"users-service","pub":"server/auth.ed.pub"}}`, "has no entry for issuer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			private := filepath.Join(root, "certs", "issuers", "server", "auth.ed")
			public := private + ".pub"
			writeContractFile(t, private, encodePrivPEM(t, priv))
			writeContractFile(t, public, encodePubPEM(t, pub))
			writeContractFile(t, filepath.Join(root, "certs", "issuers", "trusted.json"), []byte(tc.bundle))
			instance, err := newWithPaths("server", private, public)
			if instance != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.want) {
				t.Fatalf("instance=%v err=%v want%q", instance, err, tc.want)
			}
		})
	}
}
