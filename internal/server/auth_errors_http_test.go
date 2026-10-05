//nolint:testpackage // Tests the unexported auth middleware and handlers directly.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

const (
	testCSRFSecret      = "test-secret-0123456789abcdef"
	testJSONContentType = "application/json"
)

const (
	testSessionKey = "0123456789abcdef0123456789abcdef"
	testAltKey     = "ffffffffffffffffffffffffffffffff"
)

const (
	// testCSRFVerifyFailedMsg is the body withVerifyCSRFMiddleware sends for
	// every rejection verifyCSRFToken returns: handleError replaces the gRPC
	// detail with this generic message, so the rejection class is visible only
	// in the status code.
	testCSRFVerifyFailedMsg = "failed to verify csrf token\n"
)

func newTestCrypto(t *testing.T) *crypto.Crypto {
	t.Helper()

	c, err := crypto.New(testSessionKey)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}
	return c
}

// alwaysUnauthenticated is an auth double whose token never validates.
func alwaysUnauthenticated(t *testing.T) auth.IAuth {
	t.Helper()

	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().
		ValidateToken(gomock.Any(), gomock.Any()).
		Return(nil, nil, status.Error(codes.Unauthenticated, "signature is invalid")).
		AnyTimes()
	return authService
}

// TestVerifySessionMiddlewareRejectsBadSessionCookies pins every rejection the
// session middleware makes before it touches Redis, so a forged or unreadable
// session cookie can never reach a downstream handler.
func TestVerifySessionMiddlewareRejectsBadSessionCookies(t *testing.T) {
	decryptable, err := newTestCrypto(t).Encrypt("expired.jwt.token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tests := []struct {
		name    string
		cookie  string
		wantMsg string
	}{
		{name: "session cookie is not base64", cookie: "not-base64!!", wantMsg: "failed to decrypt session\n"},
		{name: "session cookie is not valid ciphertext", cookie: "aGVsbG8gd29ybGQgYW5kIG1vcmU=", wantMsg: "failed to decrypt session\n"},
		{name: "decryptable cookie with an invalid token", cookie: decryptable, wantMsg: "invalid token\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{auth: alwaysUnauthenticated(t), crypto: newTestCrypto(t)}

			nextCalled := false
			handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
			req.AddCookie(secureTestCookie(sessionCookieName, test.cookie))

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusUnauthorized, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if nextCalled {
				t.Fatal("next handler ran for a rejected session cookie")
			}
		})
	}
}

// TestVerifySessionMiddlewareRejectsMissingCookie isolates the cookie-absent
// rejection, which never reaches the crypto or auth layers.
func TestVerifySessionMiddlewareRejectsMissingCookie(t *testing.T) {
	s := &Server{auth: alwaysUnauthenticated(t), crypto: newTestCrypto(t)}

	nextCalled := false
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	if got := res.Body.String(); got != "session not found\n" {
		t.Fatalf("body = %q, want %q", got, "session not found\n")
	}
	if nextCalled {
		t.Fatal("next handler ran without a session cookie")
	}
}

// TestVerifySessionMiddlewareRejectsForeignKeyCookie proves a session cookie
// minted under a different AES key cannot be unwrapped by this server.
func TestVerifySessionMiddlewareRejectsForeignKeyCookie(t *testing.T) {
	other, err := crypto.New(testAltKey)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}
	forged, err := other.Encrypt("jwt.token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	s := &Server{auth: alwaysUnauthenticated(t), crypto: newTestCrypto(t)}
	nextCalled := false
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, forged))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	if got := res.Body.String(); got != "failed to decrypt session\n" {
		t.Fatalf("body = %q, want %q", got, "failed to decrypt session\n")
	}
	if nextCalled {
		t.Fatal("next handler ran with a foreign session cookie")
	}
}

// TestVerifySessionMiddlewareRejectsUnusableToken proves a non-status error
// from the auth service is still refused rather than treated as a pass.
func TestVerifySessionMiddlewareRejectsUnusableToken(t *testing.T) {
	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().
		ValidateToken(gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("boom")).
		Times(1)

	session, err := newTestCrypto(t).Encrypt("expired.jwt")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	s := &Server{auth: authService, crypto: newTestCrypto(t)}
	nextCalled := false
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, session))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	if got := res.Body.String(); got != "invalid token\n" {
		t.Fatalf("body = %q, want %q", got, "invalid token\n")
	}
	if nextCalled {
		t.Fatal("next handler ran for an unusable token")
	}
}

// TestVerifyCSRFMiddlewareRejectsMalformedTokens pins each distinct CSRF
// rejection with both its status and its exact body. The two missing-cookie
// rejections answer before verification and name the cookie that is absent.
// Everything verifyCSRFToken rejects is reported through handleError with one
// generic message, so the underlying classification only shows up in the
// status: a malformed, HMAC-mismatched or expired token is
// codes.InvalidArgument (400), while an unparsable timestamp is codes.Internal
// (500).
func TestVerifyCSRFMiddlewareRejectsMalformedTokens(t *testing.T) {
	const session = "session-value"

	validToken, err := generateCSRFToken(session, testCSRFSecret)
	if err != nil {
		t.Fatalf("generate csrf token: %v", err)
	}
	foreignToken, err := generateCSRFToken(session, "a-different-secret-0123456789")
	if err != nil {
		t.Fatalf("generate foreign csrf token: %v", err)
	}
	otherSessionToken, err := generateCSRFToken("other-session", testCSRFSecret)
	if err != nil {
		t.Fatalf("generate other-session csrf token: %v", err)
	}
	// Deterministic expiry: a timestamp two hours in the past, outside the
	// one-hour CSRFExpiry used below. No sleeping involved.
	staleStamp := strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)
	staleHMAC, err := generateHMAC(session+staleStamp, testCSRFSecret)
	if err != nil {
		t.Fatalf("generate hmac: %v", err)
	}

	tests := []struct {
		name       string
		csrfCookie string
		hasCSRF    bool
		session    string
		hasSession bool
		wantCode   int
		wantMsg    string
	}{
		{
			name: "missing csrf cookie", hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: "csrf token not found\n",
		},
		{
			name: "missing session cookie", hasCSRF: true, csrfCookie: validToken,
			wantCode: http.StatusBadRequest, wantMsg: "session token not found\n",
		},
		{
			// Not exactly two "$"-separated parts: invalid argument.
			name: "csrf token without a delimiter", hasCSRF: true, csrfCookie: "single-part-token",
			hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: testCSRFVerifyFailedMsg,
		},
		{
			name: "csrf token with too many parts", hasCSRF: true, csrfCookie: validToken + delimiter + "extra",
			hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: testCSRFVerifyFailedMsg,
		},
		{
			// The timestamp is present but not an integer: internal error.
			name:    "csrf token with a non numeric timestamp",
			hasCSRF: true, csrfCookie: "abcdef" + delimiter + "not-a-number",
			hasSession: true, session: session,
			wantCode: http.StatusInternalServerError, wantMsg: testCSRFVerifyFailedMsg,
		},
		{
			name: "csrf token signed with another secret", hasCSRF: true, csrfCookie: foreignToken,
			hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: testCSRFVerifyFailedMsg,
		},
		{
			name: "expired csrf token", hasCSRF: true, csrfCookie: staleHMAC + delimiter + staleStamp,
			hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: testCSRFVerifyFailedMsg,
		},
		{
			name: "csrf token minted for another session", hasCSRF: true, csrfCookie: otherSessionToken,
			hasSession: true, session: session,
			wantCode: http.StatusBadRequest, wantMsg: testCSRFVerifyFailedMsg,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{
				validationCfg: &ValidationConfig{CSRFHMACSecret: testCSRFSecret, CSRFExpiry: time.Hour},
			}
			nextCalled := false
			handler := s.withVerifyCSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/workflows", http.NoBody)
			if test.hasCSRF {
				req.AddCookie(secureTestCookie(csrfCookieName, test.csrfCookie))
			}
			if test.hasSession {
				req.AddCookie(secureTestCookie(sessionCookieName, test.session))
			}

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != test.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, test.wantCode, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if nextCalled {
				t.Fatal("next handler ran for a rejected CSRF token")
			}
		})
	}
}

// TestVerifyCSRFRejectionMessages pins the two cookie-level rejections, which
// answer before the HMAC verification and name the missing cookie instead.
func TestVerifyCSRFRejectionMessages(t *testing.T) {
	s := &Server{
		validationCfg: &ValidationConfig{CSRFHMACSecret: testCSRFSecret, CSRFExpiry: time.Hour},
	}
	nextCalled := false
	handler := s.withVerifyCSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	token, err := generateCSRFToken("session-value", testCSRFSecret)
	if err != nil {
		t.Fatalf("generate csrf token: %v", err)
	}

	tests := []struct {
		name     string
		cookies  []*http.Cookie
		wantCode int
		wantMsg  string
	}{
		{
			name:     "no cookies at all",
			wantCode: http.StatusBadRequest,
			wantMsg:  "csrf token not found\n",
		},
		{
			name:     "session cookie only",
			cookies:  []*http.Cookie{secureTestCookie(sessionCookieName, "session-value")},
			wantCode: http.StatusBadRequest,
			wantMsg:  "csrf token not found\n",
		},
		{
			name:     "csrf cookie only",
			cookies:  []*http.Cookie{secureTestCookie(csrfCookieName, token)},
			wantCode: http.StatusBadRequest,
			wantMsg:  "session token not found\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nextCalled = false

			req := httptest.NewRequest(http.MethodPost, "/workflows", http.NoBody)
			for _, cookie := range test.cookies {
				req.AddCookie(cookie)
			}

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != test.wantCode {
				t.Fatalf("status = %d, want %d", res.Code, test.wantCode)
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if nextCalled {
				t.Fatal("next handler ran with a missing cookie")
			}
		})
	}
}

// TestAttachAuthorizationTokenMiddlewareErrors pins the two failure modes of
// the downstream token minting step.
func TestAttachAuthorizationTokenMiddlewareErrors(t *testing.T) {
	t.Run("missing user in context", func(t *testing.T) {
		s := &Server{}
		nextCalled := false
		handler := s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
			auth.ServiceNameAnalytics,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}),
		)

		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/analytics", http.NoBody))

		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
		}
		if got := res.Body.String(); got != "user ID not found in context\n" {
			t.Fatalf("body = %q, want %q", got, "user ID not found in context\n")
		}
		if nextCalled {
			t.Fatal("next handler ran without a user in context")
		}
	})

	t.Run("token minting fails", func(t *testing.T) {
		authService := authmock.NewMockIAuth(gomock.NewController(t))
		authService.EXPECT().
			IssueToken(gomock.Any(), "user-7", auth.ServiceNameAnalytics).
			Return("", status.Error(codes.Internal, "signing key unavailable")).
			Times(1)

		s := &Server{auth: authService}
		nextCalled := false
		handler := s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
			auth.ServiceNameAnalytics,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}),
		)

		req := httptest.NewRequest(http.MethodGet, "/analytics", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user-7"))

		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
		}
		if got := res.Body.String(); got != "failed to issue token\n" {
			t.Fatalf("body = %q, want %q", got, "failed to issue token\n")
		}
		if nextCalled {
			t.Fatal("next handler ran although token minting failed")
		}
	})
}

// TestAttachAuthorizationTokenForwardsBearerMetadata proves the minted token
// travels downstream as bearer metadata for the requested audience only.
func TestAttachAuthorizationTokenForwardsBearerMetadata(t *testing.T) {
	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().
		IssueToken(gomock.Any(), "user-7", auth.ServiceNameNotifications).
		DoAndReturn(func(_ context.Context, subject string, audiences ...string) (string, error) {
			if subject != "user-7" {
				t.Fatalf("subject = %q, want user-7", subject)
			}
			if len(audiences) != 1 || audiences[0] != auth.ServiceNameNotifications {
				t.Fatalf("audiences = %v, want [%s]", audiences, auth.ServiceNameNotifications)
			}
			return "minted.jwt.token", nil
		}).
		Times(1)

	s := &Server{auth: authService}
	observed := ""
	handler := s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
		auth.ServiceNameNotifications,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The middleware attaches outgoing gRPC metadata; the bearer value
			// is what the downstream gRPC interceptor reads.
			md, ok := metadata.FromOutgoingContext(r.Context())
			if !ok {
				t.Error("expected outgoing metadata on the downstream call")
			}
			observed = strings.Join(md.Get("Authorization"), ",")
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user-7"))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if observed != "Bearer minted.jwt.token" {
		t.Fatalf("forwarded metadata = %q, want %q", observed, "Bearer minted.jwt.token")
	}
}

// countingNotificationsClient, countingAnalyticsClient and countingUserClient
// record that a rejected request never left the HTTP process.
type countingNotificationsClient struct {
	notificationspb.NotificationsServiceClient

	listCalls int
	markCalls int
}

func (c *countingNotificationsClient) ListNotifications(
	_ context.Context,
	_ *notificationspb.ListNotificationsRequest,
	_ ...grpc.CallOption,
) (*notificationspb.ListNotificationsResponse, error) {
	c.listCalls++
	return &notificationspb.ListNotificationsResponse{}, nil
}

func (c *countingNotificationsClient) MarkNotificationsRead(
	_ context.Context,
	_ *notificationspb.MarkNotificationsReadRequest,
	_ ...grpc.CallOption,
) (*notificationspb.MarkNotificationsReadResponse, error) {
	c.markCalls++
	return &notificationspb.MarkNotificationsReadResponse{}, nil
}

type countingAnalyticsClient struct {
	analyticspb.AnalyticsServiceClient

	userCalls     int
	workflowCalls int
}

func (c *countingAnalyticsClient) GetUserAnalytics(
	_ context.Context,
	_ *analyticspb.GetUserAnalyticsRequest,
	_ ...grpc.CallOption,
) (*analyticspb.GetUserAnalyticsResponse, error) {
	c.userCalls++
	return &analyticspb.GetUserAnalyticsResponse{}, nil
}

func (c *countingAnalyticsClient) GetWorkflowAnalytics(
	_ context.Context,
	_ *analyticspb.GetWorkflowAnalyticsRequest,
	_ ...grpc.CallOption,
) (*analyticspb.GetWorkflowAnalyticsResponse, error) {
	c.workflowCalls++
	return &analyticspb.GetWorkflowAnalyticsResponse{}, nil
}

type countingUserClient struct {
	userpb.UsersServiceClient

	getCalls    int
	updateCalls int
}

func (c *countingUserClient) GetUser(
	_ context.Context,
	_ *userpb.GetUserRequest,
	_ ...grpc.CallOption,
) (*userpb.GetUserResponse, error) {
	c.getCalls++
	return &userpb.GetUserResponse{}, nil
}

func (c *countingUserClient) UpdateUser(
	_ context.Context,
	_ *userpb.UpdateUserRequest,
	_ ...grpc.CallOption,
) (*userpb.UpdateUserResponse, error) {
	c.updateCalls++
	return &userpb.UpdateUserResponse{}, nil
}

// TestRequestUserIDGuardsHandlers verifies every session-bound handler family
// answers with a concrete 400 and never calls its backend when the session
// middleware left no user in context. Two handlers behind the same middleware
// are covered by their own suites, which also assert the backend stayed
// untouched: the SSE handler in TestHandleJobEventsRejectsMissingIdentifiers
// and the manual schedule in
// TestHandleManualScheduleJobRejectsUnauthenticatedOrFailed.
func TestRequestUserIDGuardsHandlers(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*Server, http.ResponseWriter, *http.Request)
	}{
		{name: "list workflows", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleListWorkflows(w, r) }},
		{name: "get workflow", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetWorkflow(w, r) }},
		{name: "create workflow", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleCreateWorkflow(w, r) }},
		{name: "update workflow", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleUpdateWorkflow(w, r) }},
		{name: "terminate workflow", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleTerminateWorkflow(w, r) }},
		{name: "delete workflow", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleDeleteWorkflow(w, r) }},
		{name: "list jobs", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleListJobs(w, r) }},
		{name: "get job", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetJob(w, r) }},
		{name: "get job logs", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetJobLogs(w, r) }},
		{name: "search job logs", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleSearchJobLogs(w, r) }},
		{name: "list notifications", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleListNotifications(w, r) }},
		{name: "user analytics", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetUserAnalytics(w, r) }},
		{name: "workflow analytics", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetWorkflowAnalytics(w, r) }},
		{name: "get user", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleGetUser(w, r) }},
		{name: "update user", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleUpdateUser(w, r) }},
		{name: "mark notifications read", invoke: func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleMarkNotificationsRead(w, r) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notificationsClient := &countingNotificationsClient{}
			analyticsClient := &countingAnalyticsClient{}
			usersClient := &countingUserClient{}
			jobsClient := &recordingJobsClient{}
			workflowsClient := &recordingWorkflowsClient{}
			s := &Server{
				jobsClient:          jobsClient,
				workflowsClient:     workflowsClient,
				notificationsClient: notificationsClient,
				analyticsClient:     analyticsClient,
				usersClient:         usersClient,
			}

			req := httptest.NewRequest(
				http.MethodPost,
				"/workflows/wf-1/jobs/job-1/logs",
				strings.NewReader(`{}`),
			)
			// A JSON body keeps the body-decoding guard in the JSON handlers
			// satisfied so the missing-user guard is the one under test.
			req.Header.Set("Content-Type", testJSONContentType)
			req.SetPathValue("workflow_id", "wf-1")
			req.SetPathValue("job_id", "job-1")

			res := httptest.NewRecorder()
			test.invoke(s, res, req)

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			// The exact message matters: these handlers also answer "workflow ID
			// not found" and "job ID not found" at the same status, so a
			// substring check would pass with the guards reordered.
			if got, want := res.Body.String(), "user ID not found\n"; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
			got, reached := s.backendCallCount()
			if got != 0 {
				t.Fatalf("backend was called %d times without an authenticated user (%s)", got, reached)
			}
		})
	}
}

// backendCallCount sums every stubbed backend invocation for this server. The
// second result names the clients that were reached, so a guard that lets a
// call escape reports which service saw the request. Every RPC any handler in
// the guard table can reach is counted here; a double whose method is not
// listed would answer through its nil-embedded interface and report as an
// unrelated panic instead of a failed assertion.
func (s *Server) backendCallCount() (total int, reached []string) {
	add := func(client string, calls int) {
		total += calls
		if calls > 0 {
			reached = append(reached, fmt.Sprintf("%s=%d", client, calls))
		}
	}
	if c, ok := s.jobsClient.(*recordingJobsClient); ok {
		add("jobs", c.listCalls+c.getCalls+c.getLogsCall+c.searchCalls+c.schedCalls)
	}
	if c, ok := s.workflowsClient.(*recordingWorkflowsClient); ok {
		add("workflows", c.listCalls+c.getCalls+c.createCalls+c.updateCalls+c.terminateCalls+c.deleteCalls)
	}
	if c, ok := s.notificationsClient.(*countingNotificationsClient); ok {
		add("notifications", c.listCalls+c.markCalls)
	}
	if c, ok := s.analyticsClient.(*countingAnalyticsClient); ok {
		add("analytics", c.userCalls+c.workflowCalls)
	}
	if c, ok := s.usersClient.(*countingUserClient); ok {
		add("users", c.getCalls+c.updateCalls)
	}
	return total, reached
}

// TestHandleErrorMapping pins the gRPC-to-HTTP status table and proves the
// backend message is replaced by the caller's generic message.
func TestHandleErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		code     codes.Code
		wantCode int
	}{
		{name: "unauthenticated", code: codes.Unauthenticated, wantCode: http.StatusUnauthorized},
		{name: "permission denied", code: codes.PermissionDenied, wantCode: http.StatusForbidden},
		{name: "not found", code: codes.NotFound, wantCode: http.StatusNotFound},
		{name: "already exists", code: codes.AlreadyExists, wantCode: http.StatusConflict},
		{name: "aborted", code: codes.Aborted, wantCode: http.StatusConflict},
		{name: "invalid argument", code: codes.InvalidArgument, wantCode: http.StatusBadRequest},
		{name: "unimplemented", code: codes.Unimplemented, wantCode: http.StatusNotImplemented},
		{name: "unavailable", code: codes.Unavailable, wantCode: http.StatusServiceUnavailable},
		{name: "failed precondition", code: codes.FailedPrecondition, wantCode: http.StatusPreconditionFailed},
		{name: "resource exhausted", code: codes.ResourceExhausted, wantCode: http.StatusTooManyRequests},
		{name: "canceled", code: codes.Canceled, wantCode: http.StatusRequestTimeout},
		{name: "deadline exceeded", code: codes.DeadlineExceeded, wantCode: http.StatusGatewayTimeout},
		{name: "internal", code: codes.Internal, wantCode: http.StatusInternalServerError},
		{name: "data loss", code: codes.DataLoss, wantCode: http.StatusInternalServerError},
		{name: "out of range", code: codes.OutOfRange, wantCode: http.StatusInternalServerError},
		{name: "unknown", code: codes.Unknown, wantCode: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handleError(res, status.Error(test.code, "backend detail that must not leak"), "generic message")

			if res.Code != test.wantCode {
				t.Fatalf("status = %d, want %d", res.Code, test.wantCode)
			}
			if got := res.Body.String(); got != "generic message\n" {
				t.Fatalf("body = %q, want %q", got, "generic message\n")
			}
		})
	}
}

// TestHandleErrorWithoutMessageUsesBackendDetail documents that handleError
// falls back to the raw Go error string, which for a gRPC status carries the
// full "rpc error: code = ... desc = ..." envelope.
func TestHandleErrorWithoutMessageUsesBackendDetail(t *testing.T) {
	res := httptest.NewRecorder()
	handleError(res, status.Error(codes.InvalidArgument, "interval_min must be non-negative"))

	want := "rpc error: code = InvalidArgument desc = interval_min must be non-negative\n"
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	if got := res.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestHandleErrorIgnoresUnmappedCodes verifies a gRPC code outside the mapping
// table leaves the response untouched instead of inventing a status.
func TestHandleErrorIgnoresUnmappedCodes(t *testing.T) {
	res := httptest.NewRecorder()
	handleError(res, status.Error(codes.Code(17), "future code"), "generic message")

	if res.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", res.Body.String())
	}
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want the untouched default 200", res.Code)
	}
}
