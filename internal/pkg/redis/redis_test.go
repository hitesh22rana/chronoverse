//nolint:testpackage // Tests unexported token generation and Lua script constants.
package redis

import (
	"testing"
	"time"

	redismock "github.com/go-redis/redismock/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAcquireDistributedLockWithTokenAcquired(t *testing.T) {
	restore := stubDistributedLockToken("token-1")
	defer restore()

	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectSetNX("lock:key", "token-1", time.Minute).SetVal(true)

	token, acquired, err := store.AcquireDistributedLockWithToken(t.Context(), "lock:key", time.Minute)
	if err != nil {
		t.Fatalf("AcquireDistributedLockWithToken() error = %v", err)
	}
	if !acquired {
		t.Fatalf("AcquireDistributedLockWithToken() acquired = false, want true")
	}
	if token != "token-1" {
		t.Fatalf("AcquireDistributedLockWithToken() token = %q, want token-1", token)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireDistributedLockWithTokenContention(t *testing.T) {
	restore := stubDistributedLockToken("token-1")
	defer restore()

	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectSetNX("lock:key", "token-1", time.Minute).SetVal(false)

	token, acquired, err := store.AcquireDistributedLockWithToken(t.Context(), "lock:key", time.Minute)
	if err != nil {
		t.Fatalf("AcquireDistributedLockWithToken() error = %v", err)
	}
	if acquired {
		t.Fatalf("AcquireDistributedLockWithToken() acquired = true, want false")
	}
	if token != "" {
		t.Fatalf("AcquireDistributedLockWithToken() token = %q, want empty", token)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExtendDistributedLockWithToken(t *testing.T) {
	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectEval(distributedLockExtendWithTokenScript, []string{"lock:key"}, "token-1", int64(60000)).SetVal(int64(1))

	renewed, err := store.ExtendDistributedLockWithToken(t.Context(), "lock:key", "token-1", time.Minute)
	if err != nil {
		t.Fatalf("ExtendDistributedLockWithToken() error = %v", err)
	}
	if !renewed {
		t.Fatalf("ExtendDistributedLockWithToken() renewed = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExtendDistributedLockWithTokenWrongToken(t *testing.T) {
	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectEval(distributedLockExtendWithTokenScript, []string{"lock:key"}, "wrong-token", int64(60000)).SetVal(int64(0))

	renewed, err := store.ExtendDistributedLockWithToken(t.Context(), "lock:key", "wrong-token", time.Minute)
	if err != nil {
		t.Fatalf("ExtendDistributedLockWithToken() error = %v", err)
	}
	if renewed {
		t.Fatalf("ExtendDistributedLockWithToken() renewed = true, want false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseDistributedLockWithToken(t *testing.T) {
	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectEval(distributedLockReleaseWithTokenScript, []string{"lock:key"}, "token-1").SetVal(int64(1))

	if err := store.ReleaseDistributedLockWithToken(t.Context(), "lock:key", "token-1"); err != nil {
		t.Fatalf("ReleaseDistributedLockWithToken() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseDistributedLockWithTokenWrongToken(t *testing.T) {
	client, mock := redismock.NewClientMock()
	store := &Store{client: client}
	mock.ExpectEval(distributedLockReleaseWithTokenScript, []string{"lock:key"}, "wrong-token").SetVal(int64(0))

	err := store.ReleaseDistributedLockWithToken(t.Context(), "lock:key", "wrong-token")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReleaseDistributedLockWithToken() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func stubDistributedLockToken(token string) func() {
	previous := newDistributedLockToken
	newDistributedLockToken = func() string {
		return token
	}
	return func() {
		newDistributedLockToken = previous
	}
}
