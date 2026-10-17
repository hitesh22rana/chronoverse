//nolint:testpackage // Tests unexported handlers directly.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

type fakeNotificationsServiceClient struct {
	notificationspb.NotificationsServiceClient

	listError error
	markError error
}

func (f *fakeNotificationsServiceClient) ListNotifications(
	_ context.Context, _ *notificationspb.ListNotificationsRequest, _ ...grpc.CallOption,
) (*notificationspb.ListNotificationsResponse, error) {
	if f.listError != nil {
		return nil, f.listError
	}

	return &notificationspb.ListNotificationsResponse{}, nil
}

func (f *fakeNotificationsServiceClient) MarkNotificationsRead(
	_ context.Context, _ *notificationspb.MarkNotificationsReadRequest, _ ...grpc.CallOption,
) (*notificationspb.MarkNotificationsReadResponse, error) {
	if f.markError != nil {
		return nil, f.markError
	}

	return &notificationspb.MarkNotificationsReadResponse{}, nil
}

// TestHandleListNotificationsMapsRPCError covers the backend failure mapping.
func TestHandleListNotificationsMapsRPCError(t *testing.T) {
	t.Parallel()

	s := &Server{notificationsClient: &fakeNotificationsServiceClient{
		listError: status.Error(codes.Unavailable, "backend unavailable"),
	}}

	request := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody).
		WithContext(context.WithValue(t.Context(), userIDKey{}, "user-42"))

	response := httptest.NewRecorder()
	s.handleListNotifications(response, request)

	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "failed to list notifications\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

// TestHandleMarkNotificationsReadRejectsInvalidBody pins the decode guard: a
// body without a JSON content type or with malformed JSON never reaches the
// notifications backend.
func TestHandleMarkNotificationsReadRejectsInvalidBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, contentType string }{
		{"missing content type", `{"ids":["notification-1"]}`, ""},
		{"malformed JSON", `{"ids":[`, testJSONContentType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A nil backend makes any call past the decode guard fail the test.
			s := &Server{}
			request := httptest.NewRequest(http.MethodPut, "/notifications", strings.NewReader(tc.body)).WithContext(context.WithValue(t.Context(), userIDKey{}, "user-42"))
			request.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			s.handleMarkNotificationsRead(response, request)
			if response.Code != http.StatusBadRequest || response.Body.String() != "invalid request body\n" {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

// TestHandleMarkNotificationsReadMapsRPCError covers the backend failure mapping.
func TestHandleMarkNotificationsReadMapsRPCError(t *testing.T) {
	t.Parallel()

	s := &Server{notificationsClient: &fakeNotificationsServiceClient{
		markError: status.Error(codes.NotFound, "notification not found"),
	}}

	request := httptest.NewRequest(http.MethodPut, "/notifications", strings.NewReader(`{"ids":["notification-1"]}`)).
		WithContext(context.WithValue(t.Context(), userIDKey{}, "user-42"))
	request.Header.Set("Content-Type", testJSONContentType)

	response := httptest.NewRecorder()
	s.handleMarkNotificationsRead(response, request)

	if response.Code != http.StatusNotFound || response.Body.String() != "failed to mark notifications as read\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
