package server

import (
	"encoding/json"
	"net/http"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

type markNotificationsReadRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var req markNotificationsReadRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	// MarkNotificationsRead marks the notifications as read.
	_, err := s.notificationsClient.MarkNotificationsRead(r.Context(), &notificationspb.MarkNotificationsReadRequest{
		UserId: userID,
		Ids:    req.IDs,
	})
	if err != nil {
		handleError(w, err, "failed to mark notifications as read")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
