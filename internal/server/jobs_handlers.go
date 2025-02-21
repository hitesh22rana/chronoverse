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

	// Create the job
	res, err := s.jobsClient.Create(r.Context(), &jobspb.CreateRequest{
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
