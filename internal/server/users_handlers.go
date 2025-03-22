package server

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleRegisterUser handles the register request.
//
//nolint:dupl // it's okay to have similar code for different handlers
func (s *Server) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	res, err := s.usersClient.RegisterUser(r.Context(), &userpb.RegisterUserRequest{
		Email:    req.Email,
		Password: req.Password,
	}, grpc.Header(&header))
	if err != nil {
		handleError(w, err, "failed to register user")
		return
	}

	authToken, err := auth.ExtractAuthorizationTokenFromHeaders(header)
	if err != nil {
		handleError(w, err, "failed to get authorization token from headers")
		return
	}

	session, err := s.crypto.Encrypt(authToken)
	if err != nil {
		handleError(w, err, "failed to encrypt session")
		return
	}

	if err = s.rdb.Set(r.Context(), session, res.GetUserId(), s.validationCfg.SessionExpiry); err != nil {
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

// handleLoginUser handles the login request.
//
//nolint:dupl // it's okay to have similar code for different handlers
func (s *Server) handleLoginUser(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	res, err := s.usersClient.LoginUser(r.Context(), &userpb.LoginUserRequest{
		Email:    req.Email,
		Password: req.Password,
	}, grpc.Header(&header))
	if err != nil {
		handleError(w, err, "failed to login user")
		return
	}

	authToken, err := auth.ExtractAuthorizationTokenFromHeaders(header)
	if err != nil {
		handleError(w, err, "failed to get authorization token from headers")
		return
	}

	session, err := s.crypto.Encrypt(authToken)
	if err != nil {
		handleError(w, err, "failed to encrypt session")
		return
	}

	if err = s.rdb.Set(r.Context(), session, res.GetUserId(), s.validationCfg.SessionExpiry); err != nil {
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
