package notifications

import (
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
