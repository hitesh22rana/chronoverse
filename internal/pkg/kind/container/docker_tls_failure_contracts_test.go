//nolint:testpackage // Covers rotating credential failure boundaries.
package container

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestDockerProxyTLSCredentialFailures(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "key.pem")
	ca := newDockerProxyTestCA(t, 51, "contract-ca")
	identity := issueDockerProxyTestCertificate(t, ca, 52, "client", nil, x509.ExtKeyUsageClientAuth)
	writeDockerProxyTestFile(t, caPath, ca.pem)
	writeDockerProxyTestPair(t, certPath, keyPath, identity)
	if cfg, err := newDockerProxyTLSConfig(caPath, certPath+".missing", keyPath, "proxy"); cfg != nil || err == nil {
		t.Fatal("accepted missing initial certificate")
	}
	if cfg, err := newDockerProxyTLSConfig(caPath+".missing", certPath, keyPath, "proxy"); cfg != nil || err == nil {
		t.Fatal("accepted missing initial CA")
	}
	invalid := filepath.Join(dir, "invalid.pem")
	writeDockerProxyTestFile(t, invalid, []byte("invalid PEM"))
	if pool, err := loadDockerProxyCAPool(invalid); pool != nil || err == nil {
		t.Fatal("accepted malformed CA")
	}
	cfg, err := newDockerProxyTLSConfig(caPath, certPath, keyPath, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if cert, err := cfg.GetClientCertificate(nil); cert != nil || err == nil {
		t.Fatal("accepted deleted rotating client key")
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("accepted missing peer certificate")
	}
	server := issueDockerProxyTestCertificate(t, ca, 53, "proxy", []string{"proxy"}, x509.ExtKeyUsageServerAuth)
	if err := os.Remove(caPath); err != nil {
		t.Fatal(err)
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{server.cert}}); err == nil {
		t.Fatal("accepted deleted rotating CA")
	}
}

func TestDockerProxyTLSVerifiesIntermediateChain(t *testing.T) {
	root := newDockerProxyTestCA(t, 61, "root")
	intermediate := newDockerProxyTestCA(t, 62, "intermediate")
	der, err := x509.CreateCertificate(rand.Reader, intermediate.cert, root.cert, &intermediate.key.PublicKey, root.key)
	if err != nil {
		t.Fatal(err)
	}
	intermediate.cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leaf := issueDockerProxyTestCertificate(t, intermediate, 63, "proxy", []string{"proxy"}, x509.ExtKeyUsageServerAuth)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	writeDockerProxyTestFile(t, caPath, root.pem)
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.cert, intermediate.cert}}
	if err := verifyDockerProxyConnection(&state, caPath, "proxy"); err != nil {
		t.Fatalf("valid intermediate chain rejected: %v", err)
	}
	state.PeerCertificates = state.PeerCertificates[:1]
	if err := verifyDockerProxyConnection(&state, caPath, "proxy"); err == nil {
		t.Fatal("accepted leaf without required intermediate")
	}
}
