package idempotency_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestDecodeUniqueJSONRejectsMalformedAndAmbiguousPayloads(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", `{"a":`, `{"a":1`, `[1,`, `[1`, "[1 ", `[1}`, `{"a" 1}`, `{"a":1,"a":2}`, `{"a":[{"x":1,"x":2}]}`, `{} {}`, `{} !`, `[{"x":1]`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			destination := map[string]any{"preserved": true}
			err := idempotency.DecodeUniqueJSON(strings.NewReader(raw), &destination)
			if err == nil {
				t.Fatalf("accepted payload %q", raw)
			}
			if len(destination) != 1 || destination["preserved"] != true {
				t.Fatalf("parse failure mutated destination=%v", destination)
			}
		})
	}
	var object map[string]any
	if err := idempotency.DecodeUniqueJSON(strings.NewReader(`{"nested":{"items":[true,null,"ok"]}}`), &object); err != nil {
		t.Fatal(err)
	}
	nested, ok := object["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested object=%v", object)
	}
	items, ok := nested["items"].([]any)
	if !ok {
		t.Fatalf("nested items=%v", nested)
	}
	if len(items) != 3 || items[0] != true || items[1] != nil || items[2] != "ok" {
		t.Fatalf("decoded items=%v", items)
	}
	var integer int
	if err := idempotency.DecodeUniqueJSON(strings.NewReader(`"text"`), &integer); err == nil {
		t.Fatal("destination type mismatch accepted")
	}
	if _, err := idempotency.CanonicalJSONObject(`{"unfinished":`); err == nil {
		t.Fatal("malformed object accepted")
	}
	failure := errors.New("body interrupted")
	if err := idempotency.DecodeUniqueJSON(failingReader{failure}, &object); !errors.Is(err, failure) {
		t.Fatalf("reader failure=%v", err)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestCanonicalHashBindsImageButIgnoresExecutionDetails(t *testing.T) {
	t.Parallel()
	const payload = `{"image":"alpine:3.24.2","cmd":["echo","first"]}`
	first, err := idempotency.WorkflowBuildHash("CONTAINER", payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"image":"alpine:3.24.2"}`))
	if first != hex.EncodeToString(sum[:]) {
		t.Fatalf("image hash=%q", first)
	}
	second, err := idempotency.ContainerBuildHash(`{"timeout":"5s","cmd":["echo","changed"],"image":"alpine:3.24.2"}`)
	if err != nil || second != first {
		t.Fatalf("execution-only change invalidated build hash: %q %v", second, err)
	}
	changed, err := idempotency.ContainerBuildHash(`{"image":"alpine:3.24.1"}`)
	if err != nil || changed == first {
		t.Fatalf("image change did not invalidate build hash: %q %v", changed, err)
	}
	if got, err := idempotency.WorkflowBuildHash("HEARTBEAT", `{}`); err != nil || got != "" {
		t.Fatalf("heartbeat hash=%q err=%v", got, err)
	}
	if got, err := idempotency.WorkflowBuildHash("UNKNOWN", `{}`); got != "" || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid kind hash=%q err=%v", got, err)
	}
	if got, err := idempotency.ContainerBuildHash(`{}`); got != "" || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid image hash=%q err=%v", got, err)
	}
	if got, err := idempotency.HashCanonical(make(chan int)); got != "" || err == nil {
		t.Fatalf("unsupported canonical value hash=%q err=%v", got, err)
	}
}

func TestEventKeysPreserveLegacyAndCanonicalIdentityContracts(t *testing.T) {
	t.Parallel()
	const upper = "A0B1C2D3-E4F5-4678-9ABC-DEF012345678"
	const canonical = "a0b1c2d3-e4f5-4678-9abc-def012345678"
	for _, tt := range []struct{ got, want string }{
		{idempotency.WorkflowEventKey(upper, "BUILD", 0), "workflow:" + canonical + ":BUILD"},
		{idempotency.AutomaticScheduleEventKey(""), "workflow:unknown-event:automatic-job"},
		{idempotency.AutomaticScheduleEventKey("workflow:1:BUILD:2"), "workflow:1:BUILD:2:automatic-job"},
		{idempotency.JobCompletedAnalyticsEventKey(upper), "analytics:job:" + canonical + ":completed"},
		{idempotency.WorkflowAnalyticsEventKey(upper), "analytics:workflow:" + canonical},
	} {
		if tt.got != tt.want {
			t.Errorf("event key=%q want%q", tt.got, tt.want)
		}
	}
}
