package commandidempotency

import "testing"

func TestRequestHashMatchesCompatibleLegacyHash(t *testing.T) {
	t.Parallel()

	if !requestHashMatches("legacy", "canonical", []string{"legacy"}) {
		t.Fatal("expected compatible legacy hash to match")
	}
	if requestHashMatches("different", "canonical", []string{"legacy"}) {
		t.Fatal("unexpected match for unrelated request hash")
	}
}
