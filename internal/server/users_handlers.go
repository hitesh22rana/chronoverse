package server

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, ok := idempotencyKeyFromHeader(r)
	if !ok {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
		return
	}

	var req registerRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	res, err := s.usersClient.RegisterUser(r.Context(), &userspb.RegisterUserRequest{
		Email:          req.Email,
		Password:       req.Password,
		IdempotencyKey: idempotencyKey,
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

	setCookie(w, csrfCookieName, csrfToken, s.hostConfig.Host, s.hostConfig.Secure, false, s.validationCfg.CSRFExpiry, s.hostConfig.SameSite)
	setCookie(w, sessionCookieName, session, s.hostConfig.Host, s.hostConfig.Secure, true, s.validationCfg.SessionExpiry, s.hostConfig.SameSite)

	w.WriteHeader(http.StatusCreated)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLoginUser(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var header metadata.MD
	res, err := s.usersClient.LoginUser(r.Context(), &userspb.LoginUserRequest{
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

	setCookie(w, csrfCookieName, csrfToken, s.hostConfig.Host, s.hostConfig.Secure, false, s.validationCfg.CSRFExpiry, s.hostConfig.SameSite)
	setCookie(w, sessionCookieName, session, s.hostConfig.Host, s.hostConfig.Secure, true, s.validationCfg.SessionExpiry, s.hostConfig.SameSite)

	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Delete the csrf and session cookies
	setCookie(w, csrfCookieName, "", s.hostConfig.Host, s.hostConfig.Secure, false, -1, s.hostConfig.SameSite)
	setCookie(w, sessionCookieName, "", s.hostConfig.Host, s.hostConfig.Secure, true, -1, s.hostConfig.SameSite)

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

func (s *Server) handleValidate(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
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

	res, err := s.usersClient.GetUser(r.Context(), &userspb.GetUserRequest{
		Id: userID,
	})
	if err != nil {
		handleError(w, err, "failed to get user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

type updateUserRequest struct {
	Password               string `json:"password"`
	NotificationPreference string `json:"notification_preference"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req updateUserRequest
	if err := decodeJSONRequest(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

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

	if _, err := s.usersClient.UpdateUser(r.Context(), &userspb.UpdateUserRequest{
		Id:                     userID,
		NotificationPreference: req.NotificationPreference,
	}); err != nil {
		handleError(w, err, "failed to update user")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
