package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// Get status from the query parameters
	status := r.URL.Query().Get("status")
	if status != "" {
		// Validate the job status
		if !isValidJobStatus(status) {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
	}

	// ListJobs lists the jobs by job ID.
	res, err := s.jobsClient.ListJobs(r.Context(), &jobspb.ListJobsRequest{
		WorkflowId: workflowID,
		UserId:     userID,
		Cursor:     cursor,
		Filters: &jobspb.ListJobsFilters{
			Status: status,
		},
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

// handleJobEvents handles the job events by job ID request.
func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	// Get the workflow ID from the path parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
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

	// Set SSE headers before writing anything
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Disable compression for SSE
	w.Header().Del("Content-Encoding")
	w.Header().Del("Transfer-Encoding") // Ensure no transfer encoding

	// Create response controller for streaming
	rc, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Create a context that can be canceled when the client disconnects
	ctx := r.Context()

	// Start the gRPC stream
	stream, err := s.jobsClient.StreamJobLogs(ctx, &jobspb.StreamJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		// Send an error event to the client
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		rc.Flush()
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	rc.Flush()

	// Stream the logs
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		default:
			msg, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Stream ended normally
					fmt.Fprintf(w, "event: end\ndata: {\"status\":\"stream_ended\"}\n\n")
					rc.Flush()
					return
				}

				// Send error event and close
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				rc.Flush()
				return
			}

			if msg == nil {
				continue
			}

			// Marshal the log message
			data, err := json.Marshal(msg)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: failed to marshal log message\n\n")
				rc.Flush()
				continue
			}

			// Send the log event
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			rc.Flush()
		}
	}
}
