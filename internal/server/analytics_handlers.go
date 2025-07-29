package server

import (
	"encoding/json"
	"net/http"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

// handleGetUserAnalytics handles the get user analytics request.
func (s *Server) handleGetUserAnalytics(w http.ResponseWriter, r *http.Request) {
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

	res, err := s.analyticsClient.GetUserAnalytics(r.Context(), &analyticspb.GetUserAnalyticsRequest{
		UserId: userID,
	})
	if err != nil {
		handleError(w, err, "failed to get user analytics")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleGetWorkflowAnalytics handles the get workflow analytics request.
//
//nolint:dupl // it's okay to have similar code for different handlers
func (s *Server) handleGetWorkflowAnalytics(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

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

	res, err := s.analyticsClient.GetWorkflowAnalytics(r.Context(), &analyticspb.GetWorkflowAnalyticsRequest{
		UserId:     userID,
		WorkflowId: workflowID,
	})
	if err != nil {
		handleError(w, err, "failed to get workflow analytics")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}
