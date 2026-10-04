package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req createWorkflowRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
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

	// Forward log retention only when set; service applies default otherwise.
	if req.LogRetention != nil {
		protoReq.LogRetention = req.LogRetention
	}

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

func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req updateWorkflowRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	idempotencyKey, ok := idempotencyKeyFromHeader(r)
	if !ok {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
		return
	}

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

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

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

func (s *Server) handleTerminateWorkflow(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

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

func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

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

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	cursor := r.URL.Query().Get("cursor")
	filters, err := parseWorkflowListFilters(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	res, err := s.workflowsClient.ListWorkflows(r.Context(), &workflowspb.ListWorkflowsRequest{
		UserId:  userID,
		Cursor:  cursor,
		Filters: filters,
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

func parseWorkflowListFilters(values url.Values) (*workflowspb.ListWorkflowsFilters, error) {
	query := values.Get("query")

	kind := values.Get("kind")
	if kind != "" {
		if !isValidKind(kind) {
			return nil, errors.New("invalid kind")
		}
	}

	buildStatus := values.Get("build_status")
	if buildStatus != "" {
		if !isValidBuildStatus(buildStatus) {
			return nil, errors.New("invalid build status")
		}
	}

	terminatedStr := values.Get("terminated")
	if terminatedStr == "" {
		terminatedStr = "false"
	}
	terminated, err := strconv.ParseBool(terminatedStr)
	if err != nil {
		return nil, errors.New("invalid terminated")
	}

	// If build status is provided, terminated must be false
	if buildStatus != "" && terminated {
		return nil, errors.New("terminated cannot be true when build status is provided")
	}

	intervalMin, err := parseOptionalNonNegativeInt32(values.Get("interval_min"))
	if err != nil {
		return nil, errors.New("invalid interval_min")
	}

	intervalMax, err := parseOptionalNonNegativeInt32(values.Get("interval_max"))
	if err != nil || (intervalMax != 0 && intervalMax < intervalMin) {
		return nil, errors.New("invalid interval_max")
	}

	return &workflowspb.ListWorkflowsFilters{
		Query:        query,
		Kind:         kind,
		BuildStatus:  buildStatus,
		IsTerminated: terminated,
		IntervalMin:  intervalMin,
		IntervalMax:  intervalMax,
	}, nil
}
