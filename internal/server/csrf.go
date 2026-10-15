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
func generateCSRFToken(session, secret string) string {
	timeStamp := strconv.FormatInt(time.Now().Unix(), 10)
	data := session + timeStamp
	sha := generateHMAC(data, secret)

	return sha + delimiter + timeStamp
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
	expectedSHA := generateHMAC(data, secret)

	if !hmac.Equal([]byte(sha), []byte(expectedSHA)) {
		return status.Error(codes.InvalidArgument, "invalid csrf token")
	}

	if time.Unix(ts, 0).Add(maxAge).Before(time.Now()) {
		return status.Error(codes.InvalidArgument, "csrf token has expired")
	}

	return nil
}

func generateHMAC(data, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(data))

	return hex.EncodeToString(h.Sum(nil))
}
