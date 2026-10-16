//nolint:testpackage // Verifies real mux, session store, CSRF and RPC composition.
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
	"google.golang.org/grpc/metadata"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

type routeNotificationsClient struct {
	notificationspb.NotificationsServiceClient
	t              *testing.T
	lists, updates int
}

func (c *routeNotificationsClient) check(ctx context.Context, userID string) {
	c.t.Helper()
	if userID != testSessionUserID {
		c.t.Fatalf("RPC user=%q", userID)
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || strings.Join(md.Get("authorization"), ",") != "Bearer route-token" {
		c.t.Fatalf("RPC metadata=%v", md)
	}
}

func (c *routeNotificationsClient) ListNotifications(ctx context.Context, req *notificationspb.ListNotificationsRequest, _ ...grpc.CallOption) (*notificationspb.ListNotificationsResponse, error) {
	c.check(ctx, req.GetUserId())
	if req.GetCursor() != "next-page" {
		c.t.Fatalf("cursor=%q", req.GetCursor())
	}
	c.lists++
	return &notificationspb.ListNotificationsResponse{}, nil
}

func (c *routeNotificationsClient) MarkNotificationsRead(
	ctx context.Context, req *notificationspb.MarkNotificationsReadRequest, _ ...grpc.CallOption,
) (*notificationspb.MarkNotificationsReadResponse, error) {
	c.check(ctx, req.GetUserId())
	if strings.Join(req.GetIds(), ",") != "notification-1,notification-2" {
		c.t.Fatalf("ids=%v", req.GetIds())
	}
	c.updates++
	return &notificationspb.MarkNotificationsReadResponse{}, nil
}

type routeAnalyticsClient struct {
	analyticspb.AnalyticsServiceClient
	recorder *routeNotificationsClient
	calls    int
}

func (c *routeAnalyticsClient) GetWorkflowAnalytics(ctx context.Context, req *analyticspb.GetWorkflowAnalyticsRequest, _ ...grpc.CallOption) (*analyticspb.GetWorkflowAnalyticsResponse, error) {
	c.recorder.check(ctx, req.GetUserId())
	if req.GetWorkflowId() != "workflow-42" {
		c.recorder.t.Fatalf("workflow ID=%q", req.GetWorkflowId())
	}
	c.calls++
	return &analyticspb.GetWorkflowAnalyticsResponse{WorkflowId: req.GetWorkflowId()}, nil
}

func TestIntegrationNotificationRoutesForwardAuthenticatedRequests(t *testing.T) {
	store := newSessionRedisStore(t.Context(), t)
	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ string) (context.Context, any, error) { return ctx, nil, nil }).Times(3)
	authService.EXPECT().IssueToken(gomock.Any(), testSessionUserID, auth.ServiceNameNotifications).DoAndReturn(func(ctx context.Context, _ string, _ ...string) (string, error) {
		role, err := auth.ExtractRoleFromContext(ctx)
		if err != nil || role != auth.RoleUser.String() {
			t.Fatalf("role=%q err=%v", role, err)
		}
		return "route-token", nil
	}).Times(2)
	authService.EXPECT().IssueToken(gomock.Any(), testSessionUserID, auth.ServiceNameAnalytics).Return("route-token", nil)
	client := &routeNotificationsClient{t: t}
	analyticsClient := &routeAnalyticsClient{recorder: client}
	s := &Server{
		rdb: store, crypto: newTestCrypto(t), auth: authService,
		notificationsClient: client, analyticsClient: analyticsClient,
		validationCfg: &ValidationConfig{RequestBodyLimit: 1024, CSRFHMACSecret: "route-secret", CSRFExpiry: time.Hour},
	}
	session := mintSession(t.Context(), t, store, s)
	csrf := generateCSRFToken(session, s.validationCfg.CSRFHMACSecret)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	for _, test := range []struct {
		name, method, body, csrf string
		status                   int
	}{
		{"read", http.MethodGet, "", "", http.StatusOK},
		{"write missing csrf", http.MethodPut, `{"ids":["notification-1","notification-2"]}`, "", http.StatusBadRequest},
		{"write mismatched csrf", http.MethodPut, `{"ids":["notification-1","notification-2"]}`, "wrong", http.StatusForbidden},
		{"write", http.MethodPut, `{"ids":["notification-1","notification-2"]}`, csrf, http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/notifications?cursor=next-page", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(secureTestCookie(sessionCookieName, session))
			if test.csrf != "" {
				request.AddCookie(secureTestCookie(csrfCookieName, csrf))
				request.Header.Set(csrfHeaderName, test.csrf)
			}
			listsBefore, updatesBefore := client.lists, client.updates
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			wantLists, wantUpdates := 0, 0
			if test.status == http.StatusOK {
				wantLists = 1
			}
			if test.status == http.StatusNoContent {
				wantUpdates = 1
			}
			if client.lists-listsBefore != wantLists || client.updates-updatesBefore != wantUpdates {
				t.Fatalf("RPC deltas lists=%d updates=%d; want lists=%d updates=%d", client.lists-listsBefore, client.updates-updatesBefore, wantLists, wantUpdates)
			}
		})
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/analytics/workflow-42", http.NoBody)
	request.AddCookie(secureTestCookie(sessionCookieName, session))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"workflow_id":"workflow-42"`) || analyticsClient.calls != 1 {
		t.Fatalf("analytics status=%d body=%q calls=%d", response.Code, response.Body.String(), analyticsClient.calls)
	}

	if client.lists != 1 || client.updates != 1 {
		t.Fatalf("RPC calls lists=%d updates=%d", client.lists, client.updates)
	}
}
