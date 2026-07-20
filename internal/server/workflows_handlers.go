package server

import (
	"encoding/json"
	"errors"
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
	LogRetention                     *bool  `json:"log_retention"`
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

	idempotencyKey, ok := idempotencyKeyFromHeader(r)
	if !ok {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
		return
	}

	protoReq := &workflowspb.CreateWorkflowRequest{
		UserId:                           userID,
		Name:                             req.Name,
		Payload:                          req.Payload,
		Kind:                             req.Kind,
		Interval:                         req.Interval,
		MaxConsecutiveJobFailuresAllowed: req.MaxConsecutiveJobFailuresAllowed,
		IdempotencyKey:                   idempotencyKey,
	}

	// If log retention is provided, set it in the proto request, otherwise it will be set to the default value in the service layer.
	if req.LogRetention != nil {
		protoReq.LogRetention = req.LogRetention
	}

	// CreateWorkflow creates a new workflow.
	res, err := s.workflowsClient.CreateWorkflow(r.Context(), protoReq)
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

	idempotencyKey, ok := idempotencyKeyFromHeader(r)
	if !ok {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
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
		IdempotencyKey:                   idempotencyKey,
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
	intervalMin, err := parseOptionalNonNegativeInt32(r.URL.Query().Get("interval_min"))
	if err != nil {
		http.Error(w, "invalid interval_min", http.StatusBadRequest)
		return
	}

	// 6. interval_max
	intervalMax, err := parseOptionalNonNegativeInt32(r.URL.Query().Get("interval_max"))
	if err != nil || (intervalMax != 0 && intervalMax < intervalMin) {
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
			IntervalMin:  intervalMin,
			IntervalMax:  intervalMax,
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

func parseOptionalNonNegativeInt32(value string) (int32, error) {
	if value == "" {
		return 0, nil
	}

	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 {
		return 0, errors.New("value must be a non-negative 32-bit integer")
	}

	return int32(parsed), nil
}
