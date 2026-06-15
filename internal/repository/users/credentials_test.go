//nolint:testpackage // Tests the private timing hash and normalized credential error.
package users

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDummyPasswordHash(t *testing.T) {
	if err := bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte("chronoverse-dummy-password")); err != nil {
		t.Fatalf("dummy password hash is invalid: %v", err)
	}
}

func TestInvalidCredentialsError(t *testing.T) {
	err := invalidCredentialsError()
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Fatalf("invalidCredentialsError() code = %s, want %s", got, codes.Unauthenticated)
	}
	if got, want := status.Convert(err).Message(), "invalid email or password"; got != want {
		t.Fatalf("invalidCredentialsError() message = %q, want %q", got, want)
	}
}
