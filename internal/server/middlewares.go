package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// withOtelMiddleware adds OpenTelemetry tracing to the HTTP handler.
func (s *Server) withOtelMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		log := s.logger.With(
			zap.Any("ctx", r.Context()),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("host", r.Host),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("user_agent", r.UserAgent()),
		)

		// Start a new span for the request
		ctx, span := s.tp.Start(
			r.Context(),
			fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.String("http.host", r.Host),
				attribute.String("http.remote_addr", r.RemoteAddr),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		// Wrapped response writer to capture the status code
		wrw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}

		// Continue the request with the new context
		next.ServeHTTP(wrw, r.WithContext(ctx))

		duration := time.Since(startTime)

		// Set the status code in the span
		span.SetAttributes(attribute.Int("http.status_code", wrw.status))

		logFields := []zap.Field{
			zap.Int("status", wrw.status),
			zap.Duration("duration_ms", duration),
			zap.String("duration", duration.String()),
		}

		//nolint:gocritic // This if-else chain is better than a switch statement
		if wrw.status >= 500 {
			log.Error("request completed with server error", logFields...)
			span.SetAttributes(attribute.String("http.error", http.StatusText(wrw.status)))
			span.RecordError(fmt.Errorf("server error: %s", http.StatusText(wrw.status)))
		} else if wrw.status >= 400 {
			log.Warn("request completed with client error", logFields...)
			span.SetAttributes(attribute.String("http.error", http.StatusText(wrw.status)))
			span.RecordError(fmt.Errorf("client error: %s", http.StatusText(wrw.status)))
		} else {
			log.Info("request completed successfully", logFields...)
			span.SetAttributes(attribute.String("http.success", http.StatusText(wrw.status)))
		}
	})
}

// withAllowedMethodMiddleware is a middleware that only allows specific HTTP methods.
// It also limits the request body size for [POST, PUT, PATCH] methods.
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

		// Get the corresponding user ID from the session
		var userID string
		if _, err = s.rdb.Get(r.Context(), session, &userID); err != nil {
			http.Error(w, "invalid auth token", http.StatusUnauthorized)
			return
		}

		// Attach the required information to the context
		ctx = context.WithValue(ctx, sessionKey{}, session)
		ctx = context.WithValue(ctx, userIDKey{}, userID)

		// Call the next handler
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// withAttachBasicMetadataHeaderMiddleware is a middleware that attaches the basic metadata to the context.
// This middleware should only be called after the withVerifySessionMiddleware middleware.
// Basic metadata includes the following:
// - Audience.
// - Role.
func withAttachBasicMetadataHeaderMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Attach the audience and role to the context
		ctx := auth.WithAudience(r.Context(), svcpkg.Info().GetName())
		ctx = auth.WithRole(ctx, string(auth.RoleUser))

		// Attach the audience and role to the metadata for outgoing requests and call the next handler
		ctx = auth.WithAudienceInMetadata(ctx, svcpkg.Info().GetName())
		ctx = auth.WithRoleInMetadata(ctx, auth.RoleUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// withAttachAuthorizationTokenInMetadataHeaderMiddleware is a middleware that attaches the authorization token to the context.
// This middleware should only be called after the withVerifySessionMiddleware and withAttachBasicMetadataHeaderMiddleware middlewares.
func (s *Server) withAttachAuthorizationTokenInMetadataHeaderMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// There might be chances that the auth token is expired but the session is still valid, since the auth token is short-lived and the session is long-lived.
		// So, we need to re-issue the auth token.
		userID, ok := r.Context().Value(userIDKey{}).(string)
		if !ok {
			http.Error(w, "user ID not found in context", http.StatusUnauthorized)
			return
		}

		// Issue a new token
		authToken, err := s.auth.IssueToken(r.Context(), userID)
		if err != nil {
			http.Error(w, "failed to issue token", http.StatusInternalServerError)
			return
		}

		// Attach the token to the metadata for outgoing requests and call the next handler
		ctx := auth.WithAuthorizationTokenInMetadata(r.Context(), authToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
