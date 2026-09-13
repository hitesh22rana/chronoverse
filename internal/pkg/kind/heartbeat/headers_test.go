package heartbeat_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
)

func heartbeatPayload(t *testing.T, headers map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"endpoint": "https://example.com/health",
		"headers":  headers,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(payload)
}

func TestExtractAndValidateHeartbeatDetailsHeaderGuard(t *testing.T) {
	t.Parallel()

	t.Run("allowed headers pass through", func(t *testing.T) {
		t.Parallel()

		details, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{
			"Content-Type": "application/json",
			"Accept":       []any{"application/json", "text/plain"},
			"X-Custom":     "value",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := details.Headers["Accept"]; len(got) != 2 || got[0] != "application/json" || got[1] != "text/plain" {
			t.Fatalf("Accept headers = %q, want both values preserved", got)
		}
	})

	for _, name := range []string{
		"host", "HOST", "Content-Length", "content-length",
		"Transfer-Encoding", "connection", "Upgrade", "keep-alive",
		"trailer", "TE", "te", "Proxy-Authorization", "proxy-connection",
	} {
		t.Run("denied header "+name, func(t *testing.T) {
			t.Parallel()

			if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{name: "smuggled"})); err == nil {
				t.Fatalf("header %q accepted, want rejection", name)
			}
		})
	}

	t.Run("empty header name rejected", func(t *testing.T) {
		t.Parallel()

		if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{"   ": "v"})); err == nil {
			t.Fatal("blank header name accepted, want rejection")
		}
	})

	t.Run("more than 20 headers rejected", func(t *testing.T) {
		t.Parallel()

		headers := make(map[string]any, 21)
		for i := 0; i < 21; i++ {
			headers[fmt.Sprintf("X-Header-%02d", i)] = "v"
		}
		if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, headers)); err == nil {
			t.Fatal("21 headers accepted, want rejection")
		}
	})

	t.Run("20 headers accepted", func(t *testing.T) {
		t.Parallel()

		headers := make(map[string]any, 20)
		for i := 0; i < 20; i++ {
			headers[fmt.Sprintf("X-Header-%02d", i)] = "v"
		}
		if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, headers)); err != nil {
			t.Fatalf("20 headers rejected: %v", err)
		}
	})

	t.Run("oversize headers rejected", func(t *testing.T) {
		t.Parallel()

		if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{
			"X-Big": strings.Repeat("a", 9*1024),
		})); err == nil {
			t.Fatal("9KiB header accepted, want rejection")
		}
	})
}

func TestExtractAndValidateHeartbeatDetailsEmptyArrays(t *testing.T) {
	t.Parallel()

	// 20 empty arrays stay within the count cap but exceed 8KiB in names alone.
	headers := make(map[string]any, 20)
	for i := 0; i < 20; i++ {
		headers[fmt.Sprintf("X-Pad-%02d-%s", i, strings.Repeat("a", 480))] = []any{}
	}
	if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, headers)); err == nil {
		t.Fatal("empty-array headers exceeding size cap accepted, want rejection")
	}

	details, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{
		"X-Empty": []any{},
	}))
	if err != nil {
		t.Fatalf("small empty-array header rejected: %v", err)
	}
	if got := details.Headers["X-Empty"]; len(got) != 0 {
		t.Fatalf("X-Empty headers = %q, want empty", got)
	}
}

func TestExtractAndValidateHeartbeatDetailsHeaderNames(t *testing.T) {
	t.Parallel()

	t.Run("padded header name stored trimmed", func(t *testing.T) {
		t.Parallel()

		details, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{
			"  X-Custom  ": "value",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := details.Headers["X-Custom"]; len(got) != 1 || got[0] != "value" {
			t.Fatalf("X-Custom headers = %q, want trimmed name with value preserved", got)
		}
	})

	for _, name := range []string{"X Bad", "Bad\tName", "X\nBad", "X-Custom:"} {
		t.Run(fmt.Sprintf("invalid header name %q", name), func(t *testing.T) {
			t.Parallel()

			if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(heartbeatPayload(t, map[string]any{name: "v"})); err == nil {
				t.Fatalf("header name %q accepted, want rejection", name)
			}
		})
	}
}
