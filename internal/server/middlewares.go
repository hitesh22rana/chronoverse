package server

import (
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"strings"
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
		// Skip tracing for streaming endpoints
		if strings.Contains(r.URL.Path, "/workflows/test") || strings.Contains(r.URL.Path, "/logs/raw") || strings.Contains(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}

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
		wrw := &customResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

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

		msg := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		//nolint:gocritic // This if-else chain is better than a switch statement
		if wrw.status >= 500 {
			log.Error(msg, logFields...)
			span.SetAttributes(attribute.String("http.error", http.StatusText(wrw.status)))
			span.RecordError(fmt.Errorf("server error: %s", http.StatusText(wrw.status)))
		} else if wrw.status >= 400 {
			log.Warn(msg, logFields...)
			span.SetAttributes(attribute.String("http.error", http.StatusText(wrw.status)))
			span.RecordError(fmt.Errorf("client error: %s", http.StatusText(wrw.status)))
		} else {
			log.Info(msg, logFields...)
			span.SetAttributes(attribute.String("http.success", http.StatusText(wrw.status)))
		}
	})
}

// withCORSMiddleware adds CORS headers to all responses.
func (s *Server) withCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.frontendConfig.URL)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Credentials", "true") // Critical for cookies
		w.Header().Set("Access-Control-Max-Age", "86400")          // 24 hours

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// withCompressionMiddleware adds HTTP gzip compression for JSON responses.
func (s *Server) withCompressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip compression for streaming endpoints
		if strings.Contains(r.URL.Path, "/workflows/test") || strings.Contains(r.URL.Path, "/logs/raw") || strings.Contains(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}

		// Check if client accepts gzip compression
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		// Create gzip writer with best speed for better performance
		gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			s.logger.Error("failed to create gzip writer", zap.Error(err))
			next.ServeHTTP(w, r)
			return
		}
		defer gz.Close()

		// Set Vary header to indicate response varies based on Accept-Encoding
		w.Header().Set("Vary", "Accept-Encoding")

		// Create proper gzip response writer
		gzipWriter := &gzipResponseWriter{
			ResponseWriter: w,
			gzipWriter:     gz,
			status:         http.StatusOK,
		}

		// Serve the request with compressed response
		next.ServeHTTP(gzipWriter, r)
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
		// Get the CSRF token from cookie
		csrfCookie, err := r.Cookie(csrfCookieName)
		if err != nil {
			http.Error(w, "csrf token not found", http.StatusBadRequest)
			return
		}
		csrfToken := csrfCookie.Value

		// Get the session from cookie
		sessionCookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Error(w, "session token not found", http.StatusBadRequest)
			return
		}
		sessionToken := sessionCookie.Value

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
		// Get the session from cookie instead of header
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Error(w, "session not found", http.StatusUnauthorized)
			return
		}
		session := cookie.Value

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
