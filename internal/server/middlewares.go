package server

import (
	"context"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// withAllowedMethodMiddleware is a middleware that only allows specific HTTP methods.
func (s *Server) withAllowedMethodMiddleware(allowedMethod string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != allowedMethod {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// If the method is [POST, PUT, PATCH], limit the request body size
		if r.Method == http.MethodPost ||
			r.Method == http.MethodPut ||
			r.Method == http.MethodPatch {
			r.Body = http.MaxBytesReader(w, r.Body, s.validationCfg.RequestBodyLimit)
		}

		next.ServeHTTP(w, r)
	}
}

// withVerifyCSRFMiddleware is a middleware that checks the CSRF token.
func (s *Server) withVerifyCSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the CSRF token from the header
		csrfToken := r.Header.Get(csrfHeaderKey)
		if csrfToken == "" {
			http.Error(w, "csrf token not found", http.StatusBadRequest)
			return
		}

		// Get the session from the header
		sessionToken := r.Header.Get(sessionHeaderKey)
		if sessionToken == "" {
			http.Error(w, "session token not found", http.StatusBadRequest)
			return
		}

		// Verify the CSRF token
		if err := verifyCSRFToken(csrfToken, sessionToken, s.validationCfg.CSRFHMACSecret, s.validationCfg.CSRFExpiry); err != nil {
			handleError(w, err, "failed to verify csrf token")
			return
		}

		next.ServeHTTP(w, r)
	}
}

// withVerifySessionMiddleware is a middleware that verifies the attached token.
func (s *Server) withVerifySessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the session from the header
		session := r.Header.Get(sessionHeaderKey)
		if session == "" {
			http.Error(w, "session token not found", http.StatusBadRequest)
			return
		}

		// Decrypt and verify the session
		authToken, err := s.crypto.Decrypt(session)
		if err != nil {
			http.Error(w, "failed to decrypt session", http.StatusUnauthorized)
			return
		}

		// Attach the token to the context
		ctx := auth.WithAuthorizationToken(r.Context(), authToken)

		// validate the token
		// if the error code is DeadlineExceeded, it means the token is expired but it is still valid
		if _, err = s.auth.ValidateToken(ctx); err != nil && status.Code(err) != codes.DeadlineExceeded {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		// Get the corresponding auth token
		var _authToken string
		if err = s.rdb.Get(r.Context(), session, &_authToken); err != nil {
			http.Error(w, "invalid auth token", http.StatusUnauthorized)
			return
		}

		if _authToken != authToken {
			http.Error(w, "auth token mismatch", http.StatusUnauthorized)
			return
		}

		// Either the token is valid or it is expired but still valid
		// Call the next handler
		// Attach the session to the context
		ctx = context.WithValue(r.Context(), sessionKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// withAttachAudienceInMetadataHeaderMiddleware is a middleware that attaches the audience to the context.
func withAttachAudienceInMetadataHeaderMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithAudienceInMetadata(r.Context(), svcpkg.Info().GetName())
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// withAttachRoleInMetadataHeaderMiddleware is a middleware that attaches the role to the context.
func withAttachRoleInMetadataHeaderMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithRoleInMetadata(r.Context(), auth.RoleUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
