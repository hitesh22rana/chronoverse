package server

import (
	"encoding/json"
	"net/http"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

func (s *Server) handleGetUserAnalytics(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestUserID(w, r)
	if !ok {
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
	json.NewEncoder(w).Encode(newUserAnalyticsHTTPResponse(res))
}

func (s *Server) handleGetWorkflowAnalytics(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
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
	json.NewEncoder(w).Encode(newWorkflowAnalyticsHTTPResponse(res))
}
