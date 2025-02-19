package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	delimiter = "$"
)

// generateCSRFToken generates a CSRF token from the given session.
func generateCSRFToken(session, secret string) (string, error) {
	timeStamp := strconv.FormatInt(time.Now().Unix(), 10)
	data := session + timeStamp
	sha, err := _generateHMAC(data, secret)
	if err != nil {
		return "", err
	}

	return sha + delimiter + timeStamp, nil
}

// verifyCSRFToken verifies the given CSRF token against the session.
func verifyCSRFToken(csrfToken, session, secret string, maxAge time.Duration) error {
	parts := strings.Split(csrfToken, delimiter)
	if len(parts) != 2 {
		return status.Error(codes.InvalidArgument, "invalid csrf token")
	}

	sha, timeStamp := parts[0], parts[1]

	ts, err := strconv.ParseInt(timeStamp, 10, 64)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to parse timestamp: %v", err)
	}

	data := session + timeStamp
	expectedSHA, err := _generateHMAC(data, secret)
	if err != nil {
		return err
	}

	if sha != expectedSHA {
		return status.Error(codes.InvalidArgument, "invalid csrf token")
	}

	// Check if the token has expired
	if time.Unix(ts, 0).Add(maxAge).Before(time.Now()) {
		return status.Error(codes.InvalidArgument, "csrf token has expired")
	}

	return nil
}

// _generateHMAC is a helper function that generates an HMAC from the given data and secret.
func _generateHMAC(data, secret string) (string, error) {
	h := hmac.New(sha256.New, []byte(secret))
	if _, err := h.Write([]byte(data)); err != nil {
		return "", status.Errorf(codes.Internal, "failed to write data to hmac: %v", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
