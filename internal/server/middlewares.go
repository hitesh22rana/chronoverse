package server

import (
	"net/http"

	"google.golang.org/grpc/metadata"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	audienceMetadataKey = "audience"
	patMetadataKey      = "pat"
)

// withMethodMiddleware is a middleware that only allows specific HTTP methods.
func withMethodMiddleware(allowedMethod string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != allowedMethod {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// withRequestBodyLimitMiddleware is a middleware that limits the request body size.
func withRequestBodyLimitMiddleware(maxBytes int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

		next.ServeHTTP(w, r)
	}
}

// withVerifyCSRFMiddleware is a middleware that checks the CSRF token.
func withVerifyCSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the CSRF token from the cookie
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil {
			http.Error(w, "CSRF token not found", http.StatusUnauthorized)
			return
		}

		// Get the CSRF token from the header
		header := r.Header.Get(csrfHeaderKey)
		if header == "" {
			http.Error(w, "CSRF token not found", http.StatusUnauthorized)
			return
		}

		// Compare the CSRF token from the cookie and the header
		if cookie.Value != header {
			http.Error(w, "CSRF token mismatch", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// withAttachPatInContextMiddleware is a middleware that checks the PAT and attach the pat to the context.
func withAttachPatInContextMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the PAT from the cookie
		patCookie, err := r.Cookie(patCookieName)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			http.Error(w, "PAT cookie not found", http.StatusUnauthorized)
			return
		}

		// Get the PAT from the cookie
		pat := patCookie.Value
		if pat == "" {
			w.WriteHeader(http.StatusUnauthorized)
			http.Error(w, "PAT not found in cookie", http.StatusUnauthorized)
			return
		}

		// Attach the PAT to the context
		ctx := metadata.AppendToOutgoingContext(
			r.Context(),
			patMetadataKey, pat,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// withAttachAudienceInContextMiddleware is a middleware that attaches the audience to the context.
func withAttachAudienceInContextMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := metadata.AppendToOutgoingContext(
			r.Context(),
			audienceMetadataKey, svcpkg.Info().GetServiceInfo(),
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
