package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
)

const (
	csrfCookieName = "csrf"
	patCookieName  = "pat"
	csrfHeaderKey  = "X-CSRF-Token"
	csrfExpiry     = 12 * 60 * 60
	patExpiry      = 12 * 60 * 60
)

func generateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random CSRF token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		MaxAge:   maxAge,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
}
