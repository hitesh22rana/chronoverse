//nolint:testpackage // Exercises the unexported rotating TLS loader directly.
package container

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type dockerProxyTestCA struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
	pem  []byte
}

func TestDockerProxyTLSConfigReloadsClientCertificateAndCABundle(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")

	caOne := newDockerProxyTestCA(t, 1, "docker-proxy-ca-one")
	caTwo := newDockerProxyTestCA(t, 2, "docker-proxy-ca-two")
	clientOne := issueDockerProxyTestCertificate(t, caOne, 11, "docker-proxy-client-workflow-worker", nil, x509.ExtKeyUsageClientAuth)
	clientTwo := issueDockerProxyTestCertificate(t, caTwo, 12, "docker-proxy-client-workflow-worker", nil, x509.ExtKeyUsageClientAuth)
	serverOne := issueDockerProxyTestCertificate(t, caOne, 21, "docker-proxy", []string{"docker-proxy"}, x509.ExtKeyUsageServerAuth)
	serverTwo := issueDockerProxyTestCertificate(t, caTwo, 22, "docker-proxy", []string{"docker-proxy"}, x509.ExtKeyUsageServerAuth)

	writeDockerProxyTestFile(t, caFile, caOne.pem)
	writeDockerProxyTestPair(t, certFile, keyFile, clientOne)
	cfg, err := newDockerProxyTLSConfig(caFile, certFile, keyFile, "docker-proxy")
	if err != nil {
		t.Fatalf("newDockerProxyTLSConfig() error = %v", err)
	}

	loadedOne, err := cfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate() first error = %v", err)
	}
	if got := parseDockerProxyTestLeaf(t, loadedOne).SerialNumber.Int64(); got != 11 {
		t.Fatalf("first client serial = %d, want 11", got)
	}
	if verifyErr := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{serverOne.cert}}); verifyErr != nil {
		t.Fatalf("VerifyConnection() with first CA error = %v", verifyErr)
	}

	writeDockerProxyTestFile(t, caFile, caTwo.pem)
	writeDockerProxyTestPair(t, certFile, keyFile, clientTwo)
	loadedTwo, err := cfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate() after rotation error = %v", err)
	}
	if got := parseDockerProxyTestLeaf(t, loadedTwo).SerialNumber.Int64(); got != 12 {
		t.Fatalf("rotated client serial = %d, want 12", got)
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{serverOne.cert}}); err == nil {
		t.Fatal("VerifyConnection() accepted server signed only by removed CA")
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{serverTwo.cert}}); err != nil {
		t.Fatalf("VerifyConnection() with rotated CA error = %v", err)
	}
}

func TestDockerProxyTLSConfigRequiresServerName(t *testing.T) {
	dir := t.TempDir()
	ca := newDockerProxyTestCA(t, 1, "docker-proxy-ca")
	client := issueDockerProxyTestCertificate(t, ca, 2, "docker-proxy-client", nil, x509.ExtKeyUsageClientAuth)
	caFile := filepath.Join(dir, "ca.crt")
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	writeDockerProxyTestFile(t, caFile, ca.pem)
	writeDockerProxyTestPair(t, certFile, keyFile, client)

	if _, err := newDockerProxyTLSConfig(caFile, certFile, keyFile, ""); err == nil {
		t.Fatal("newDockerProxyTLSConfig() error = nil, want missing server name error")
	}
}

type dockerProxyTestCertificate struct {
	cert    *x509.Certificate
	certPEM []byte
	keyPEM  []byte
}

func newDockerProxyTestCA(t *testing.T, serial int64, commonName string) *dockerProxyTestCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(CA) error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate(CA) error = %v", err)
	}
	return &dockerProxyTestCA{
		cert: cert,
		key:  key,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

func issueDockerProxyTestCertificate(
	t *testing.T,
	ca *dockerProxyTestCA,
	serial int64,
	commonName string,
	dnsNames []string,
	usage x509.ExtKeyUsage,
) dockerProxyTestCertificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(leaf) error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate(leaf) error = %v", err)
	}
	return dockerProxyTestCertificate{
		cert:    cert,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
}

func writeDockerProxyTestPair(t *testing.T, certFile, keyFile string, cert dockerProxyTestCertificate) {
	t.Helper()
	writeDockerProxyTestFile(t, certFile, cert.certPEM)
	writeDockerProxyTestFile(t, keyFile, cert.keyPEM)
}

func writeDockerProxyTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func parseDockerProxyTestLeaf(t *testing.T, cert *tls.Certificate) *x509.Certificate {
	t.Helper()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("x509.ParseCertificate(client) error = %v", err)
	}
	return leaf
}
