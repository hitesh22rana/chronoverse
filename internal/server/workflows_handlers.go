package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type createWorkflowRequest struct {
	Name                             string `json:"name"`
	Payload                          string `json:"payload"`
	Kind                             string `json:"kind"`
	Interval                         int32  `json:"interval"`
	MaxConsecutiveJobFailuresAllowed int32  `json:"max_consecutive_job_failures_allowed"`
}

// handleCreateWorkflow handles the create workflow request.
func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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

	// CreateWorkflow creates a new workflow.
	res, err := s.workflowsClient.CreateWorkflow(r.Context(), &workflowspb.CreateWorkflowRequest{
		UserId:                           userID,
		Name:                             req.Name,
		Payload:                          req.Payload,
		Kind:                             req.Kind,
		Interval:                         req.Interval,
		MaxConsecutiveJobFailuresAllowed: req.MaxConsecutiveJobFailuresAllowed,
	})
	if err != nil {
		handleError(w, err, "failed to create workflow")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

type updateWorkflowRequest struct {
	Name                             string `json:"name"`
	Payload                          string `json:"payload"`
	Interval                         int32  `json:"interval"`
	MaxConsecutiveJobFailuresAllowed int32  `json:"max_consecutive_job_failures_allowed"`
}

// handleUpdateWorkflow handles the update workflow request.
func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req updateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Get the workflow ID from the path parameters
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

	// UpdateWorkflow updates the workflow details.
	_, err := s.workflowsClient.UpdateWorkflow(r.Context(), &workflowspb.UpdateWorkflowRequest{
		Id:                               workflowID,
		UserId:                           userID,
		Name:                             req.Name,
		Payload:                          req.Payload,
		Interval:                         req.Interval,
		MaxConsecutiveJobFailuresAllowed: req.MaxConsecutiveJobFailuresAllowed,
	})
	if err != nil {
		handleError(w, err, "failed to update workflow")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetWorkflow handles the get workflow by ID and user ID request.
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get the workflow ID from the path	parameters
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

	// GetWorkflow gets the workflow by ID.
	res, err := s.workflowsClient.GetWorkflow(r.Context(), &workflowspb.GetWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if err != nil {
		handleError(w, err, "failed to get workflow")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleTerminateWorkflow handles the terminate workflow by ID and user ID request.
func (s *Server) handleTerminateWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get the workflow ID from the path	parameters
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

	// TerminateWorkflow terminates the workflow by ID.
	_, err := s.workflowsClient.TerminateWorkflow(r.Context(), &workflowspb.TerminateWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if err != nil {
		handleError(w, err, "failed to terminate workflow")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteWorkflow handles the delete workflow by ID and user ID request.
func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get the workflow ID from the path parameters
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

	// DeleteWorkflow deletes the workflow by ID.
	_, err := s.workflowsClient.DeleteWorkflow(r.Context(), &workflowspb.DeleteWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if err != nil {
		handleError(w, err, "failed to delete workflow")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListWorkflows handles the list workflows by user ID request.
//
//nolint:gocyclo // This function is complex and can be simplified further.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
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

	// 1. query
	query := r.URL.Query().Get("query")

	// 2. kind
	kind := r.URL.Query().Get("kind")
	if kind != "" {
		// Validate kind
		if !isValidKind(kind) {
			http.Error(w, "invalid kind", http.StatusBadRequest)
			return
		}
	}

	// 3. build_status
	buildStatus := r.URL.Query().Get("build_status")
	if buildStatus != "" {
		// Validate build status
		if !isValidBuildStatus(buildStatus) {
			http.Error(w, "invalid build status", http.StatusBadRequest)
			return
		}
	}

	// 4. terminated
	terminatedStr := r.URL.Query().Get("terminated")
	if terminatedStr == "" {
		terminatedStr = "false"
	}
	terminated, err := strconv.ParseBool(terminatedStr)
	if err != nil {
		http.Error(w, "invalid terminated", http.StatusBadRequest)
		return
	}

	// If build status is provided, terminated must be false
	if buildStatus != "" && terminated {
		http.Error(w, "terminated cannot be true when build status is provided", http.StatusBadRequest)
		return
	}

	// 5. interval_min
	//nolint:errcheck // As we are using the default value of interval_min, we can ignore the error
	intervalMin, _ := strconv.Atoi(r.URL.Query().Get("interval_min"))
	if intervalMin < 0 || intervalMin > math.MaxInt32 {
		http.Error(w, "invalid interval_min", http.StatusBadRequest)
		return
	}

	// 6. interval_max
	//nolint:errcheck // As we are using the default value of interval_max, we can ignore the error
	intervalMax, _ := strconv.Atoi(r.URL.Query().Get("interval_max"))
	if intervalMax < 0 || intervalMax > math.MaxInt32 || (intervalMax != 0 && intervalMax < intervalMin) {
		http.Error(w, "invalid interval_max", http.StatusBadRequest)
		return
	}

	// ListWorkflows lists the workflows by user ID.
	res, err := s.workflowsClient.ListWorkflows(r.Context(), &workflowspb.ListWorkflowsRequest{
		UserId: userID,
		Cursor: cursor,
		Filters: &workflowspb.ListWorkflowsFilters{
			Query:        query,
			Kind:         kind,
			BuildStatus:  buildStatus,
			IsTerminated: terminated,
			//nolint:gosec // G109 // We are not using the value of the variable
			IntervalMin: int32(intervalMin),
			//nolint:gosec // G109 // We are not using the value of the variable
			IntervalMax: int32(intervalMax),
		},
	})
	if err != nil {
		handleError(w, err, "failed to list workflows")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}
