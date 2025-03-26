package server

import (
	"encoding/json"
	"net/http"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// handleListJobs handles the list jobs by job ID request.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
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

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// ListJobs lists the jobs by job ID.
	res, err := s.jobsClient.ListJobs(r.Context(), &jobspb.ListJobsRequest{
		WorkflowId: workflowID,
		UserId:     userID,
		Cursor:     cursor,
	})
	if err != nil {
		handleError(w, err, "failed to list jobs")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleGetJob handles the get job by job ID request.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
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

	// GetJob gets the job by job ID.
	res, err := s.jobsClient.GetJob(r.Context(), &jobspb.GetJobRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		handleError(w, err, "failed to get job")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleGetJobLogs handles the get job logs by job ID request.
func (s *Server) handleGetJobLogs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
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

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// GetJobLogs gets the job logs by job ID.
	res, err := s.jobsClient.GetJobLogs(r.Context(), &jobspb.GetJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
		Cursor:     cursor,
	})
	if err != nil {
		handleError(w, err, "failed to get job logs")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}
