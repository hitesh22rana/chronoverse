//nolint:testpackage // Tests unexported CSRF helpers directly.
package server

import (
	"strings"
	"testing"
	"time"
)

func TestVerifyCSRFToken(t *testing.T) {
	const (
		session = "session-token"
		hmacKey = "test-hmac-key"
	)

	token, err := generateCSRFToken(session, hmacKey)
	if err != nil {
		t.Fatalf("generateCSRFToken() error = %v", err)
	}

	if err := verifyCSRFToken(token, session, hmacKey, time.Hour); err != nil {
		t.Fatalf("verifyCSRFToken() error = %v", err)
	}

	parts := strings.Split(token, delimiter)
	tamperedToken := strings.Repeat("0", len(parts[0])) + delimiter + parts[1]
	if err := verifyCSRFToken(tamperedToken, session, hmacKey, time.Hour); err == nil {
		t.Fatal("verifyCSRFToken() accepted a tampered token")
	}
}
