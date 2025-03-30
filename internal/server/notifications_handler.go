package server

import (
	"encoding/json"
	"net/http"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

// handleListNotifications handles the list notifications request.
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	// Get the user ID from the context
	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// ListNotifications lists the notifications.
	res, err := s.notificationsClient.ListNotifications(r.Context(), &notificationspb.ListNotificationsRequest{
		UserId: userID,
		Cursor: cursor,
	})
	if err != nil {
		handleError(w, err, "failed to list notifications")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleMarkNotificationsRead handles the mark notifications read request.
func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	// Get the user ID from the context
	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	// Get the notification IDs from the request body
	var req notificationspb.MarkNotificationsReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "failed to decode request body", http.StatusBadRequest)
		return
	}

	req.UserId = userID

	// MarkNotificationsRead marks the notifications as read.
	_, err := s.notificationsClient.MarkNotificationsRead(r.Context(), &req)
	if err != nil {
		handleError(w, err, "failed to mark notifications as read")
		return
	}

	// Write the response
	w.WriteHeader(http.StatusNoContent)
}
