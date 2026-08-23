package notifications

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNotificationRequestHashCanonicalizesStoredPayload(t *testing.T) {
	t.Parallel()

	first, err := notificationRequestHash("user-1", "WEB_INFO", `{ "message": "done", "count": 1.0 }`)
	if err != nil {
		t.Fatalf("notificationRequestHash() error = %v", err)
	}
	second, err := notificationRequestHash("user-1", "WEB_INFO", `{"count":1.0,"message":"done"}`)
	if err != nil {
		t.Fatalf("notificationRequestHash() canonical input error = %v", err)
	}
	if first != second {
		t.Fatalf("canonical notification hashes differ: %q != %q", first, second)
	}

	changed, err := notificationRequestHash("user-1", "WEB_INFO", `{"count":2.0,"message":"done"}`)
	if err != nil {
		t.Fatalf("notificationRequestHash() changed input error = %v", err)
	}
	if changed == first {
		t.Fatal("changed notification payload produced the same hash")
	}
	if err = validateStoredNotificationCommand(
		first, "user-1", "WEB_INFO", `{"message":"done","count":1.0}`,
	); err != nil {
		t.Fatalf("validateStoredNotificationCommand() error = %v", err)
	}
	if code := status.Code(validateStoredNotificationCommand(
		first, "user-1", "WEB_INFO", `{"message":"changed","count":1.0}`,
	)); code != codes.AlreadyExists {
		t.Fatalf("changed notification code = %s, want %s", code, codes.AlreadyExists)
	}
}

func TestNotificationRequestHashRejectsNonObjectPayload(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{`[]`, `"text"`, `null`, `1`, `true`} {
		_, err := notificationRequestHash("user-1", "WEB_INFO", payload)
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("notificationRequestHash(%s) code = %s, want %s", payload, code, codes.InvalidArgument)
		}
	}
}

func TestCanonicalNotificationIDsDeduplicatesEquivalentUUIDSpellings(t *testing.T) {
	t.Parallel()

	const canonical = "550e8400-e29b-41d4-a716-446655440000"
	ids, err := canonicalNotificationIDs([]string{
		canonical,
		"550E8400-E29B-41D4-A716-446655440000",
		"{550e8400-e29b-41d4-a716-446655440000}",
	})
	if err != nil {
		t.Fatalf("canonicalNotificationIDs() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != canonical {
		t.Fatalf("canonicalNotificationIDs() = %v, want [%s]", ids, canonical)
	}
}

func TestCanonicalNotificationIDsRejectsInvalidUUID(t *testing.T) {
	t.Parallel()

	_, err := canonicalNotificationIDs([]string{"not-a-uuid"})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("canonicalNotificationIDs() code = %s, want %s", code, codes.InvalidArgument)
	}
}

func TestMapNotificationReadDatabaseErrorPreservesContextStatus(t *testing.T) {
	t.Parallel()

	repo := &Repository{}
	for name, test := range map[string]struct {
		err  error
		code codes.Code
	}{
		"canceled":          {err: context.Canceled, code: codes.Canceled},
		"deadline exceeded": {err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := repo.mapNotificationReadDatabaseError(test.err, "commit notification-read transaction")
			if code := status.Code(err); code != test.code {
				t.Fatalf("status code = %s, want %s", code, test.code)
			}
		})
	}
}
