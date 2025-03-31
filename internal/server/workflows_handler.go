package server

import (
	"encoding/json"
	"net/http"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type createWorkflowRequest struct {
	Name                             string `json:"name"`
	Payload                          string `json:"payload"`
	Kind                             string `json:"kind"`
	Interval                         int32  `json:"interval"`
	MaxConsecutiveJobFailuresAllowed int32  `json:"max_consecutive_job_failures_allowed,omitempty"`
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
	MaxConsecutiveJobFailuresAllowed int32  `json:"max_consecutive_job_failures_allowed,omitempty"`
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

// handleListWorkflows handles the list workflows by user ID request.
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

	// ListWorkflows lists the workflows by user ID.
	res, err := s.workflowsClient.ListWorkflows(r.Context(), &workflowspb.ListWorkflowsRequest{
		UserId: userID,
		Cursor: cursor,
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
