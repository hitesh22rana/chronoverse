package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
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

// handleRegister handles the register request.
//
//nolint:dupl // it's okay to have similar code for different handlers
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	_, err := s.usersClient.Register(r.Context(), &pb.RegisterRequest{
		Email:    req.Email,
		Password: req.Password,
	}, grpc.Header(&header))
	if err != nil {
		handleError(w, err, "failed to register user")
		return
	}

	authToken, err := auth.AuthorizationTokenFromHeaders(header)
	if err != nil {
		handleError(w, err, "failed to get authorization token from headers")
		return
	}

	session, err := s.crypto.Encrypt(authToken)
	if err != nil {
		handleError(w, err, "failed to encrypt session")
		return
	}

	if err = s.rdb.Set(r.Context(), session, authToken, s.validationCfg.SessionExpiry); err != nil {
		handleError(w, err, "failed to set session")
		return
	}

	csrfToken, err := generateCSRFToken(session, s.validationCfg.CSRFHMACSecret)
	if err != nil {
		handleError(w, err, "failed to generate CSRF token")
		return
	}

	setCookie(w, csrfCookieName, csrfToken, s.validationCfg.CSRFExpiry)
	setCookie(w, sessionCookieName, session, s.validationCfg.SessionExpiry)

	// Return only user ID in response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleLogin handles the login request.
//
//nolint:dupl // it's okay to have similar code for different handlers
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	_, err := s.usersClient.Login(r.Context(), &pb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	}, grpc.Header(&header))
	if err != nil {
		handleError(w, err, "failed to login user")
		return
	}

	authToken, err := auth.AuthorizationTokenFromHeaders(header)
	if err != nil {
		handleError(w, err, "failed to get authorization token from headers")
		return
	}

	session, err := s.crypto.Encrypt(authToken)
	if err != nil {
		handleError(w, err, "failed to encrypt session")
		return
	}

	if err = s.rdb.Set(r.Context(), session, authToken, s.validationCfg.SessionExpiry); err != nil {
		handleError(w, err, "failed to set session")
		return
	}

	csrfToken, err := generateCSRFToken(session, s.validationCfg.CSRFHMACSecret)
	if err != nil {
		handleError(w, err, "failed to generate CSRF token")
		return
	}

	setCookie(w, csrfCookieName, csrfToken, s.validationCfg.CSRFExpiry)
	setCookie(w, sessionCookieName, session, s.validationCfg.SessionExpiry)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
}

// handleLogout handles the logout request.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Delete the csrf and session cookies
	setCookie(w, csrfCookieName, "", -1)
	setCookie(w, sessionCookieName, "", -1)

	// Get the session from the context
	session, err := sessionFromContext(r.Context())
	if err != nil {
		http.Error(w, "session not found in context", http.StatusUnauthorized)
		return
	}

	// Delete the session associated with the user
	if err = s.rdb.Delete(r.Context(), session); err != nil {
		http.Error(w, "failed to delete session", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleValidate handles the validate request.
func (s *Server) handleValidate(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

//nolint:gocyclo // handleErrors is a helper function to handle gRPC errors.
func handleError(w http.ResponseWriter, err error, message ...string) {
	msg := err.Error()
	if len(message) > 0 {
		msg = strings.Join(message, " ")
	}

	switch status.Code(err) {
	case codes.OK:
		return
	case codes.Unauthenticated:
		http.Error(w, msg, http.StatusUnauthorized)
	case codes.PermissionDenied:
		http.Error(w, msg, http.StatusForbidden)
	case codes.NotFound:
		http.Error(w, msg, http.StatusNotFound)
	case codes.AlreadyExists:
		http.Error(w, msg, http.StatusConflict)
	case codes.InvalidArgument:
		http.Error(w, msg, http.StatusBadRequest)
	case codes.Unimplemented:
		http.Error(w, msg, http.StatusNotImplemented)
	case codes.Unavailable:
		http.Error(w, msg, http.StatusServiceUnavailable)
	case codes.FailedPrecondition:
		http.Error(w, msg, http.StatusPreconditionFailed)
	case codes.ResourceExhausted:
		http.Error(w, msg, http.StatusTooManyRequests)
	case codes.Canceled:
		http.Error(w, msg, http.StatusRequestTimeout)
	case codes.DeadlineExceeded:
		http.Error(w, msg, http.StatusGatewayTimeout)
	case codes.Internal:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.DataLoss:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.Aborted:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.OutOfRange:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.Unknown:
		http.Error(w, msg, http.StatusInternalServerError)
	}
}
