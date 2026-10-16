//nolint:testpackage // Exercises the real route and middleware composition.
package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteMethodAndAuthenticationContracts(t *testing.T) {
	routes := []struct {
		path, methods string
		public        bool
	}{
		{"/auth/register", "POST", true},
		{"/auth/login", "POST", true},
		{"/auth/logout", "POST", false},
		{"/auth/validate", "POST", false},
		{"/auth/csrf", "GET, HEAD", false},
		{"/users", "GET, HEAD, PUT", false},
		{"/workflows", "GET, HEAD, POST", false},
		{"/workflows/workflow-1", "GET, HEAD, PUT, PATCH, DELETE", false},
		{"/workflows/workflow-1/jobs", "GET, HEAD", false},
		{"/workflows/workflow-1/jobs/schedule", "POST", false},
		{"/workflows/workflow-1/jobs/job-1", "GET, HEAD", false},
		{"/workflows/workflow-1/jobs/job-1/logs", "GET, HEAD", false},
		{"/workflows/workflow-1/jobs/job-1/logs/search", "GET, HEAD", false},
		{"/workflows/workflow-1/jobs/job-1/logs/raw", "GET, HEAD", false},
		{"/workflows/workflow-1/jobs/job-1/events", "GET, HEAD", false},
		{"/notifications", "GET, HEAD, PUT", false},
		{"/analytics", "GET, HEAD", false},
		{"/analytics/workflow-1", "GET, HEAD", false},
	}
	// Nil dependencies ensure every rejection happens before authentication services or RPCs.
	s := &Server{validationCfg: &ValidationConfig{RequestBodyLimit: 16}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"} {
				t.Run(method, func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(method, route.path, http.NoBody)
					request.Header.Set(idempotencyKeyHeader, "route-contract")
					mux.ServeHTTP(response, request)
					allowed := false
					for _, candidate := range strings.Split(route.methods, ", ") {
						if candidate == method {
							allowed = true
						}
					}
					wantStatus, wantBody, wantAllow := http.StatusMethodNotAllowed, "method not allowed\n", route.methods
					if allowed {
						wantAllow = ""
						switch {
						case route.public:
							wantStatus, wantBody = http.StatusBadRequest, "invalid request body\n"
						case method == http.MethodGet || method == http.MethodHead:
							wantStatus, wantBody = http.StatusUnauthorized, "session not found\n"
						default:
							wantStatus, wantBody = http.StatusBadRequest, "csrf token not found\n"
						}
					}
					if response.Code != wantStatus || response.Body.String() != wantBody || response.Header().Get("Allow") != wantAllow {
						t.Fatalf("response = %d %q Allow=%q; want %d %q Allow=%q", response.Code, response.Body.String(), response.Header().Get("Allow"), wantStatus, wantBody, wantAllow)
					}
				})
			}
		})
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", http.NoBody))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route = %d", response.Code)
	}
}

func TestAllowedMethodBodyLimit(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, body := range []string{"1234", "12345"} {
			t.Run(method+body, func(t *testing.T) {
				s := &Server{validationCfg: &ValidationConfig{RequestBodyLimit: 4}}
				calls := 0
				handler := s.withAllowedMethodMiddleware(method, func(w http.ResponseWriter, r *http.Request) {
					calls++
					got, err := io.ReadAll(r.Body)
					limited := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
					if limited && len(body) > 4 {
						var limit *http.MaxBytesError
						if !errors.As(err, &limit) || limit.Limit != 4 || string(got) != "1234" {
							t.Fatalf("body=%q err=%v", got, err)
						}
					} else if err != nil || string(got) != body {
						t.Fatalf("body=%q err=%v", got, err)
					}
					w.WriteHeader(http.StatusAccepted)
				})
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(method, "/", strings.NewReader(body)))
				if calls != 1 || response.Code != http.StatusAccepted {
					t.Fatalf("calls=%d status=%d", calls, response.Code)
				}
			})
		}
	}
}

// TestAllowedMethodServesHeadAsGet pins the HEAD contract: GET routes answer
// HEAD and advertise it in Allow, other routes reject it with their method set.
func TestAllowedMethodServesHeadAsGet(t *testing.T) {
	s := &Server{validationCfg: &ValidationConfig{RequestBodyLimit: 4}}
	calls := 0
	getRoute := s.withAllowedMethodMiddleware(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	})

	response := httptest.NewRecorder()
	getRoute.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/", http.NoBody))
	if calls != 1 || response.Code != http.StatusAccepted {
		t.Fatalf("HEAD on GET route: calls=%d status=%d", calls, response.Code)
	}

	response = httptest.NewRecorder()
	getRoute.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", http.NoBody))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST on GET route: status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}

	postRoute := s.withAllowedMethodMiddleware(http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	response = httptest.NewRecorder()
	postRoute.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/", http.NoBody))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("HEAD on POST route: status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}
