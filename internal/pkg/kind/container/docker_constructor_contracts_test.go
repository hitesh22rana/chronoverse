//nolint:testpackage // Checks constructor and network failures through the Docker API.
package container

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDockerConstructorRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		options []DockerWorkflowOption
		cause   string
	}{
		{name: "incomplete TLS identity", options: []DockerWorkflowOption{WithDockerProxyTLS(DockerProxyTLSConfig{CAFile: "missing-ca.pem"})}, cause: "cert/key missing"},
		{name: "unreadable TLS identity", options: []DockerWorkflowOption{WithDockerProxyTLS(DockerProxyTLSConfig{
			CAFile: "missing-ca.pem", CertFile: "missing-cert.pem", KeyFile: "missing-key.pem", ServerName: "docker-proxy",
		})}, cause: "load docker proxy TLS config"},
		{name: "invalid daemon endpoint", options: []DockerWorkflowOption{WithDockerHost("invalid-daemon-endpoint")}, cause: "initialize docker client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workflow, err := NewDockerWorkflow(tc.options...)
			if workflow != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.cause) {
				t.Fatalf("workflow=%v error=%v, want nil/Internal/%q", workflow, err, tc.cause)
			}
		})
	}
}

func TestDockerConstructorAuthenticatesProxyWithMutualTLS(t *testing.T) {
	t.Parallel()
	ca := newDockerProxyTestCA(t, 1, "constructor-ca")
	identity := issueDockerProxyTestCertificate(t, ca, 2, "constructor-client", nil, x509.ExtKeyUsageClientAuth)
	serverIdentity := issueDockerProxyTestCertificate(t, ca, 3, "docker-proxy", []string{"docker-proxy"}, x509.ExtKeyUsageServerAuth)
	dir := t.TempDir()
	caFile, certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "client.pem"), filepath.Join(dir, "client-key.pem")
	writeDockerProxyTestFile(t, caFile, ca.pem)
	writeDockerProxyTestPair(t, certFile, keyFile, identity)
	cert, err := tls.X509KeyPair(serverIdentity.certPEM, serverIdentity.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.pem) {
		t.Fatal("invalid test CA")
	}
	var pings atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(dockerProxyTokenHeader) != "constructor-token" {
			t.Error("proxy bearer token was not forwarded")
		}
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("proxy did not authenticate client identity")
		}
		pings.Add(1)
		w.Header().Set("API-Version", "1.51")
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	workflow, err := NewDockerWorkflow(nil, WithDockerHost(server.URL), WithDockerProxyToken("constructor-token"), WithDockerProxyTLS(DockerProxyTLSConfig{
		CAFile: caFile, CertFile: certFile, KeyFile: keyFile, ServerName: "docker-proxy",
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := workflow.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if pings.Load() == 0 {
		t.Fatal("constructor did not check proxy health")
	}
	if workflow.DockerHost() != server.URL {
		t.Fatalf("host=%q, want %q", workflow.DockerHost(), server.URL)
	}
}

func TestEnsureWorkloadNetworkRejectsUnsafeNameBeforeDaemon(t *testing.T) {
	t.Parallel()
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid network name reached Docker")
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	workflow.workloadNetwork = "bridge"
	if err := workflow.ensureWorkloadNetwork(t.Context()); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("error=%v, want FailedPrecondition", err)
	}
}

func TestEnsureWorkloadNetworkPreservesDaemonFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                    string
		failInspect, failCreate bool
		cause                   string
	}{
		{name: "inspect unavailable", failInspect: true, cause: "failed to inspect"},
		{name: "create failed with no winner", failCreate: true, cause: "failed to create"},
		{name: "post-create inspect unavailable", cause: "failed to verify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var inspections, creates atomic.Int32
			workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
					if inspections.Add(1) == 1 && !tc.failInspect {
						w.WriteHeader(http.StatusNotFound)
						writeDockerTestResponse(t, w, `{"message":"network not found"}`)
						return
					}
					w.WriteHeader(http.StatusInternalServerError)
					writeDockerTestResponse(t, w, `{"message":"inspect unavailable"}`)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
					creates.Add(1)
					if tc.failCreate {
						w.WriteHeader(http.StatusInternalServerError)
						writeDockerTestResponse(t, w, `{"message":"create failed"}`)
						return
					}
					w.WriteHeader(http.StatusCreated)
					writeDockerTestResponse(t, w, `{"Id":"new-network"}`)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			err := workflow.ensureWorkloadNetwork(t.Context())
			if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.cause) {
				t.Fatalf("error=%v, want Internal/%q", err, tc.cause)
			}
			wantInspections, wantCreates := int32(2), int32(1)
			if tc.failInspect {
				wantInspections, wantCreates = 1, 0
			}
			if inspections.Load() != wantInspections || creates.Load() != wantCreates {
				t.Fatalf("inspect/create calls=%d/%d, want %d/%d", inspections.Load(), creates.Load(), wantInspections, wantCreates)
			}
		})
	}
}
