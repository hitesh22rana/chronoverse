//nolint:testpackage // Verifies HTTP handlers and authenticated route composition.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	boundaryUserID     = "00000000-0000-4000-8000-000000000031"
	boundaryWorkflowID = "00000000-0000-4000-8000-000000000032"
	boundaryCreateJSON = `{"name":"health check","payload":"{\"url\":\"https://example.com\"}","kind":"HEARTBEAT","interval":60,"max_consecutive_job_failures_allowed":3}`
	boundaryUpdateJSON = `{"name":"updated check","payload":"{\"url\":\"https://example.org\"}","interval":120,"max_consecutive_job_failures_allowed":5}`
)

type boundaryWorkflowClient struct {
	workflowspb.WorkflowsServiceClient
	call func(context.Context, proto.Message) error
}
type boundaryUserClient struct {
	userspb.UsersServiceClient
	call func(context.Context, proto.Message) error
}

func (c *boundaryWorkflowClient) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.CreateWorkflowResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.CreateWorkflowResponse{Id: boundaryWorkflowID}, nil
}

func (c *boundaryWorkflowClient) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.UpdateWorkflowResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.UpdateWorkflowResponse{}, nil
}

func (c *boundaryWorkflowClient) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.GetWorkflowResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.GetWorkflowResponse{Id: boundaryWorkflowID, Name: "health check", Kind: "HEARTBEAT", Interval: 60}, nil
}

func (c *boundaryWorkflowClient) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.TerminateWorkflowResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.TerminateWorkflowResponse{}, nil
}

func (c *boundaryWorkflowClient) DeleteWorkflow(ctx context.Context, req *workflowspb.DeleteWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.DeleteWorkflowResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.DeleteWorkflowResponse{}, nil
}

func (c *boundaryUserClient) GetUser(ctx context.Context, req *userspb.GetUserRequest, _ ...grpc.CallOption) (*userspb.GetUserResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &userspb.GetUserResponse{Email: "boundary@example.com", NotificationPreference: "ALL"}, nil
}

func (c *boundaryUserClient) UpdateUser(ctx context.Context, req *userspb.UpdateUserRequest, _ ...grpc.CallOption) (*userspb.UpdateUserResponse, error) {
	if err := c.call(ctx, req); err != nil {
		return nil, err
	}
	return &userspb.UpdateUserResponse{}, nil
}

type boundaryEndpoint struct {
	name, method, path, body, json string
	status                         int
	request                        proto.Message
	handler                        func(*Server, http.ResponseWriter, *http.Request)
}

func boundaryEndpoints(userID string) []boundaryEndpoint {
	return []boundaryEndpoint{
		{
			"create", http.MethodPost, "/workflows", boundaryCreateJSON, `{"id":"` + boundaryWorkflowID + `"}`, http.StatusCreated,
			&workflowspb.CreateWorkflowRequest{
				UserId: userID, Name: "health check", Payload: `{"url":"https://example.com"}`, Kind: "HEARTBEAT",
				Interval: 60, MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "boundary-command",
			}, (*Server).handleCreateWorkflow,
		},
		{
			"update", http.MethodPut, "/workflows/" + boundaryWorkflowID, boundaryUpdateJSON, "", http.StatusNoContent,
			&workflowspb.UpdateWorkflowRequest{
				Id: boundaryWorkflowID, UserId: userID, Name: "updated check", Payload: `{"url":"https://example.org"}`,
				Interval: 120, MaxConsecutiveJobFailuresAllowed: 5, IdempotencyKey: "boundary-command",
			}, (*Server).handleUpdateWorkflow,
		},
		{
			"get", http.MethodGet, "/workflows/" + boundaryWorkflowID, "", `{"id":"` + boundaryWorkflowID + `","name":"health check","kind":"HEARTBEAT","interval":60}`, http.StatusOK,
			&workflowspb.GetWorkflowRequest{Id: boundaryWorkflowID, UserId: userID}, (*Server).handleGetWorkflow,
		},
		{
			"terminate", http.MethodPatch, "/workflows/" + boundaryWorkflowID, "", "", http.StatusNoContent,
			&workflowspb.TerminateWorkflowRequest{Id: boundaryWorkflowID, UserId: userID}, (*Server).handleTerminateWorkflow,
		},
		{
			"delete", http.MethodDelete, "/workflows/" + boundaryWorkflowID, "", "", http.StatusNoContent,
			&workflowspb.DeleteWorkflowRequest{Id: boundaryWorkflowID, UserId: userID}, (*Server).handleDeleteWorkflow,
		},
		{
			"user get", http.MethodGet, "/users", "", `{"email":"boundary@example.com","notification_preference":"ALL"}`, http.StatusOK,
			&userspb.GetUserRequest{Id: userID}, (*Server).handleGetUser,
		},
		{
			"user update", http.MethodPut, "/users", `{"notification_preference":"ALERTS"}`, "", http.StatusNoContent,
			&userspb.UpdateUserRequest{Id: userID, NotificationPreference: "ALERTS"}, (*Server).handleUpdateUser,
		},
	}
}

func boundaryServer(call func(context.Context, proto.Message) error) *Server {
	return &Server{workflowsClient: &boundaryWorkflowClient{call: call}, usersClient: &boundaryUserClient{call: call}}
}

func boundaryRequest(t *testing.T, endpoint boundaryEndpoint) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set(idempotencyKeyHeader, "boundary-command")
	req.SetPathValue("workflow_id", boundaryWorkflowID)
	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, boundaryUserID))
}

func assertBoundaryResponse(t *testing.T, response *httptest.ResponseRecorder, endpoint boundaryEndpoint) {
	t.Helper()
	if response.Code != endpoint.status {
		t.Fatalf("status=%d body=%q; want %d", response.Code, response.Body.String(), endpoint.status)
	}
	if strings.TrimSpace(response.Body.String()) != endpoint.json {
		t.Fatalf("body=%q; want %q", response.Body.String(), endpoint.json)
	}
	if endpoint.json != "" && response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type=%q", response.Header().Get("Content-Type"))
	}
}

func TestBoundaryHandlersForwardRequestsAndResponses(t *testing.T) {
	for _, endpoint := range boundaryEndpoints(boundaryUserID) {
		t.Run(endpoint.name, func(t *testing.T) {
			calls := 0
			req := boundaryRequest(t, endpoint)
			s := boundaryServer(func(ctx context.Context, got proto.Message) error {
				calls++
				if !proto.Equal(got, endpoint.request) {
					t.Errorf("RPC request=%v; want %v", got, endpoint.request)
				}
				if ctx != req.Context() {
					t.Error("handler replaced caller context")
				}
				return nil
			})
			response := httptest.NewRecorder()
			endpoint.handler(s, response, req)
			assertBoundaryResponse(t, response, endpoint)
			if calls != 1 {
				t.Fatalf("RPC calls=%d; want 1", calls)
			}
		})
	}
}

func TestBoundaryCreatePreservesExplicitRetention(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[retain], func(t *testing.T) {
			endpoint := boundaryEndpoints(boundaryUserID)[0]
			suffix := `,"log_retention":false}`
			if retain {
				suffix = `,"log_retention":true}`
			}
			endpoint.body = strings.TrimSuffix(endpoint.body, "}") + suffix
			want := proto.Clone(endpoint.request).(*workflowspb.CreateWorkflowRequest)
			want.LogRetention = proto.Bool(retain)
			calls := 0
			s := boundaryServer(func(_ context.Context, got proto.Message) error {
				calls++
				if !proto.Equal(got, want) {
					t.Errorf("RPC request=%v; want %v", got, want)
				}
				return nil
			})
			response := httptest.NewRecorder()
			endpoint.handler(s, response, boundaryRequest(t, endpoint))
			assertBoundaryResponse(t, response, endpoint)
			if calls != 1 {
				t.Fatalf("RPC calls=%d", calls)
			}
		})
	}
}

func TestBoundaryHandlersMapServiceFailures(t *testing.T) {
	for _, endpoint := range boundaryEndpoints(boundaryUserID) {
		for _, failure := range []struct {
			name   string
			code   codes.Code
			status int
		}{
			{"invalid", codes.InvalidArgument, http.StatusBadRequest},
			{"missing", codes.NotFound, http.StatusNotFound},
			{"forbidden", codes.PermissionDenied, http.StatusForbidden},
			{"conflict", codes.Aborted, http.StatusConflict},
			{"unavailable", codes.Unavailable, http.StatusServiceUnavailable},
			{"deadline", codes.DeadlineExceeded, http.StatusGatewayTimeout},
			{"internal", codes.Internal, http.StatusInternalServerError},
		} {
			t.Run(endpoint.name+"/"+failure.name, func(t *testing.T) {
				calls := 0
				s := boundaryServer(func(_ context.Context, got proto.Message) error {
					calls++
					if !proto.Equal(got, endpoint.request) {
						t.Errorf("RPC request=%v; want %v", got, endpoint.request)
					}
					return status.Error(failure.code, "private backend detail")
				})
				response := httptest.NewRecorder()
				endpoint.handler(s, response, boundaryRequest(t, endpoint))
				if response.Code != failure.status {
					t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
				}
				if strings.Contains(response.Body.String(), "private backend detail") {
					t.Fatal("backend detail leaked")
				}
				if calls != 1 {
					t.Fatalf("RPC calls=%d", calls)
				}
			})
		}
	}
}

func TestBoundaryHandlersRejectMissingUserBeforeRPC(t *testing.T) {
	for _, endpoint := range boundaryEndpoints(boundaryUserID) {
		t.Run(endpoint.name, func(t *testing.T) {
			s := boundaryServer(func(context.Context, proto.Message) error { t.Fatal("unauthenticated request reached RPC"); return nil })
			req := boundaryRequest(t, endpoint).WithContext(t.Context())
			response := httptest.NewRecorder()
			endpoint.handler(s, response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestBoundaryWorkflowHandlersRequirePath(t *testing.T) {
	for _, endpoint := range boundaryEndpoints(boundaryUserID)[1:5] {
		t.Run(endpoint.name, func(t *testing.T) {
			s := boundaryServer(func(context.Context, proto.Message) error { t.Fatal("missing workflow path reached RPC"); return nil })
			req := boundaryRequest(t, endpoint)
			req.SetPathValue("workflow_id", "")
			response := httptest.NewRecorder()
			endpoint.handler(s, response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestBoundaryMutationHandlersRejectMalformedBodies(t *testing.T) {
	endpoints := boundaryEndpoints(boundaryUserID)
	for _, endpoint := range []boundaryEndpoint{endpoints[0], endpoints[1], endpoints[6]} {
		for _, body := range []string{"", `{`, endpoint.body + `{}`, `{"name":"first","name":"second"}`, `[]`} {
			t.Run(endpoint.name+"/"+body, func(t *testing.T) {
				invalid := endpoint
				invalid.body = body
				s := boundaryServer(func(context.Context, proto.Message) error { t.Fatal("invalid JSON reached RPC"); return nil })
				response := httptest.NewRecorder()
				invalid.handler(s, response, boundaryRequest(t, invalid))
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestBoundaryMutationHandlersRequireJSONContentType(t *testing.T) {
	endpoints := boundaryEndpoints(boundaryUserID)
	for _, endpoint := range []boundaryEndpoint{endpoints[0], endpoints[1], endpoints[6]} {
		t.Run(endpoint.name, func(t *testing.T) {
			s := boundaryServer(func(context.Context, proto.Message) error { t.Fatal("non-JSON request reached RPC"); return nil })
			req := boundaryRequest(t, endpoint)
			req.Header.Set("Content-Type", "text/plain")
			response := httptest.NewRecorder()
			endpoint.handler(s, response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestBoundaryWorkflowWritesRequireCommandIdentity(t *testing.T) {
	for _, endpoint := range boundaryEndpoints(boundaryUserID)[:2] {
		t.Run(endpoint.name, func(t *testing.T) {
			s := boundaryServer(func(context.Context, proto.Message) error {
				t.Fatal("missing command identity reached RPC")
				return nil
			})
			req := boundaryRequest(t, endpoint)
			req.Header.Del(idempotencyKeyHeader)
			response := httptest.NewRecorder()
			endpoint.handler(s, response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestIntegrationBoundaryRoutesAuthenticateAndForward(t *testing.T) {
	store := newSessionRedisStore(t.Context(), t)
	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ string) (context.Context, any, error) { return ctx, nil, nil }).Times(8)
	authService.EXPECT().IssueToken(gomock.Any(), testSessionUserID, auth.ServiceNameWorkflows).Return("boundary-token", nil).Times(5)
	authService.EXPECT().IssueToken(gomock.Any(), testSessionUserID, auth.ServiceNameUsers).Return("boundary-token", nil).Times(2)
	var want proto.Message
	calls := 0
	s := boundaryServer(func(ctx context.Context, got proto.Message) error {
		calls++
		if !proto.Equal(got, want) {
			t.Errorf("RPC request=%v; want %v", got, want)
		}
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || strings.Join(md.Get("authorization"), ",") != "Bearer boundary-token" {
			t.Errorf("RPC metadata=%v", md)
		}
		role, err := auth.ExtractRoleFromContext(ctx)
		if err != nil || role != auth.RoleUser.String() {
			t.Errorf("RPC role=%q err=%v", role, err)
		}
		return nil
	})
	s.rdb, s.crypto, s.auth = store, newTestCrypto(t), authService
	s.validationCfg = &ValidationConfig{RequestBodyLimit: 1024, CSRFHMACSecret: "boundary-secret", CSRFExpiry: time.Hour}
	session := mintSession(t.Context(), t, store, s)
	csrf := generateCSRFToken(session, s.validationCfg.CSRFHMACSecret)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	unauthenticated := httptest.NewRecorder()
	mux.ServeHTTP(unauthenticated, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/users", http.NoBody))
	if unauthenticated.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("missing session status=%d calls=%d", unauthenticated.Code, calls)
	}
	for _, endpoint := range boundaryEndpoints(testSessionUserID) {
		t.Run(endpoint.name, func(t *testing.T) {
			want = proto.Clone(endpoint.request)
			before := calls
			if endpoint.method != http.MethodGet {
				assertBoundaryRouteRequiresCSRF(t, mux, endpoint, session)
				if calls != before {
					t.Fatal("missing CSRF token reached RPC")
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(idempotencyKeyHeader, "boundary-command")
			req.AddCookie(secureTestCookie(sessionCookieName, session))
			addCSRFValues(req, csrf, csrf)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			assertBoundaryResponse(t, response, endpoint)
			if calls-before != 1 {
				t.Fatalf("RPC calls=%d; want 1", calls-before)
			}
		})
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/validate", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, session))
	addCSRFValues(req, csrf, csrf)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("validate status=%d body=%q", response.Code, response.Body.String())
	}
	if calls != 7 {
		t.Fatalf("validation made an unexpected RPC: total calls=%d", calls)
	}
}

func assertBoundaryRouteRequiresCSRF(t *testing.T, mux *http.ServeMux, endpoint boundaryEndpoint, session string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(idempotencyKeyHeader, "boundary-command")
	req.AddCookie(secureTestCookie(sessionCookieName, session))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing CSRF status=%d body=%q", response.Code, response.Body.String())
	}
}
