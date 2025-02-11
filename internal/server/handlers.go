package server

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
)

// handleHealthz handles the health check request.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

//nolint:dupl //handleRegister handles the register request.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	res, err := s.client.Register(r.Context(), &pb.RegisterRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}

	csrfToken, err := generateCSRFToken()
	if err != nil {
		http.Error(w, "Failed to generate CSRF token", http.StatusInternalServerError)
		return
	}

	// Set CSRF token as cookie
	setCookie(w, csrfCookieName, csrfToken, csrfExpiry)

	// Set PAT as cookie
	setCookie(w, patCookieName, res.GetPat(), patExpiry)

	// Return only user ID in response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

//nolint:dupl //handleLogin handles the login request.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	res, err := s.client.Login(r.Context(), &pb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}

	csrfToken, err := generateCSRFToken()
	if err != nil {
		http.Error(w, "Failed to generate CSRF token", http.StatusInternalServerError)
		return
	}

	setCookie(w, csrfCookieName, csrfToken, csrfExpiry)
	setCookie(w, patCookieName, res.GetPat(), patExpiry)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
}

// handleLogout handles the logout request.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Clear the CSRF cookie
	setCookie(w, csrfCookieName, "", -1)

	// Clear the PAT cookie
	setCookie(w, patCookieName, "", -1)

	if _, err := s.client.Logout(r.Context(), &pb.LogoutRequest{}); err != nil {
		handleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleValidate handles the validate request.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.client.Validate(r.Context(), &pb.ValidateRequest{}); err != nil {
		// Clear the CSRF and PAT cookies
		setCookie(w, csrfCookieName, "", -1)
		setCookie(w, patCookieName, "", -1)

		handleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//nolint:gocyclo //handleErrors is a helper function to handle gRPC errors.
func handleError(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.OK:
		return
	case codes.Unauthenticated:
		http.Error(w, "Invalid token", http.StatusUnauthorized)
	case codes.PermissionDenied:
		http.Error(w, "Permission denied", http.StatusForbidden)
	case codes.NotFound:
		http.Error(w, "Resource not found", http.StatusNotFound)
	case codes.AlreadyExists:
		http.Error(w, "Resource already exists", http.StatusConflict)
	case codes.InvalidArgument:
		http.Error(w, "Invalid argument", http.StatusBadRequest)
	case codes.Unimplemented:
		http.Error(w, "Unimplemented", http.StatusNotImplemented)
	case codes.Unavailable:
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
	case codes.FailedPrecondition:
		http.Error(w, "Failed precondition", http.StatusPreconditionFailed)
	case codes.ResourceExhausted:
		http.Error(w, "Resource exhausted", http.StatusTooManyRequests)
	case codes.Canceled:
		http.Error(w, "Request canceled", http.StatusRequestTimeout)
	case codes.DeadlineExceeded:
		http.Error(w, "Deadline exceeded", http.StatusGatewayTimeout)
	case codes.Internal:
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	case codes.DataLoss:
		http.Error(w, "Data loss", http.StatusInternalServerError)
	case codes.Aborted:
		http.Error(w, "Aborted", http.StatusInternalServerError)
	case codes.OutOfRange:
		http.Error(w, "Out of range", http.StatusInternalServerError)
	case codes.Unknown:
		http.Error(w, "Unknown error", http.StatusInternalServerError)
	}
}
