package idempotency_test

import (
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
