package idempotency_test

import (
	"strings"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestCanonicalJSON(t *testing.T) {
	canonical, err := idempotency.CanonicalJSON(`{"z":1.0,"a":[2,1]}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"a":[2,1],"z":1.0}` {
		t.Fatalf("canonical JSON = %s", canonical)
	}
	if _, err = idempotency.CanonicalJSON(`{"outer":{"same":1,"same":2}}`); err == nil {
		t.Fatal("duplicate nested key was accepted")
	}
}

func TestCanonicalJSONObject(t *testing.T) {
	t.Parallel()

	canonical, err := idempotency.CanonicalJSONObject(`{"z":1.0,"a":[2,1]}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"a":[2,1],"z":1.0}` {
		t.Fatalf("canonical JSON object = %s", canonical)
	}

	for _, payload := range []string{`[]`, `"text"`, `null`, `1`, `true`} {
		if _, err = idempotency.CanonicalJSONObject(payload); err == nil {
			t.Fatalf("non-object payload %s was accepted", payload)
		}
	}
}

func TestCanonicalJSONRejectsDeepNesting(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat("[", 1000) + strings.Repeat("]", 1000)
	if _, err := idempotency.CanonicalJSON(deep); err == nil {
		t.Fatal("deeply nested payload was accepted")
	}

	shallow := strings.Repeat("[", 10) + strings.Repeat("]", 10)
	if _, err := idempotency.CanonicalJSON(shallow); err != nil {
		t.Fatalf("shallow payload was rejected: %v", err)
	}
}
