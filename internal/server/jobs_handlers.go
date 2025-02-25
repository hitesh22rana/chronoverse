package server

import (
	"encoding/json"
	"net/http"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type createJobRequest struct {
	Name     string `json:"name"`
	Payload  string `json:"payload"`
	Kind     string `json:"kind"`
	Interval int32  `json:"interval"`
	MaxRetry int32  `json:"max_retry"`
}

// handleCreateJob handles the create job request.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
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

	// CreateJob creates a new job.
	res, err := s.jobsClient.CreateJob(r.Context(), &jobspb.CreateJobRequest{
		UserId:   userID,
		Name:     req.Name,
		Payload:  req.Payload,
		Kind:     req.Kind,
		Interval: req.Interval,
		MaxRetry: req.MaxRetry,
	})
	if err != nil {
		handleError(w, err, "failed to create job")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

type updateJobRequest struct {
	Name     string `json:"name"`
	Payload  string `json:"payload"`
	Kind     string `json:"kind"`
	Interval int32  `json:"interval"`
}

// handleUpdateJob handles the update job request.
func (s *Server) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	var req updateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path	parameters
	jobID := r.PathValue("id")
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

	// UpdateJob updates the job details.
	_, err := s.jobsClient.UpdateJob(r.Context(), &jobspb.UpdateJobRequest{
		Id:       jobID,
		UserId:   userID,
		Name:     req.Name,
		Payload:  req.Payload,
		Kind:     req.Kind,
		Interval: req.Interval,
	})
	if err != nil {
		handleError(w, err, "failed to update job")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// handleGetJob handles the get job by ID and user ID request.
//
//nolint:dupl // It's okay to have similar code for different handlers.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	jobID := r.PathValue("id")
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

	// GetJob gets the job by ID.
	res, err := s.jobsClient.GetJob(r.Context(), &jobspb.GetJobRequest{
		Id:     jobID,
		UserId: userID,
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

// handleListJobsByUserID handles the list jobs by user ID request.
func (s *Server) handleListJobsByUserID(w http.ResponseWriter, r *http.Request) {
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

	// ListJobsByUserID lists the jobs by user ID.
	res, err := s.jobsClient.ListJobsByUserID(r.Context(), &jobspb.ListJobsByUserIDRequest{
		UserId: userID,
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

// handleListScheduledJobs handles the list scheduled jobs by job ID request.
//
//nolint:dupl // It's okay to have similar code for different handlers.
func (s *Server) handleListScheduledJobs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	jobID := r.PathValue("id")
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

	// ListScheduledJobs lists the scheduled jobs by job ID.
	res, err := s.jobsClient.ListScheduledJobs(r.Context(), &jobspb.ListScheduledJobsRequest{
		JobId:  jobID,
		UserId: userID,
	})
	if err != nil {
		handleError(w, err, "failed to list scheduled jobs")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}
