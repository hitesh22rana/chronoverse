package commandidempotency

import "testing"

func TestEmptyRequestHashAliasesUseNonNilArray(t *testing.T) {
	t.Parallel()

	aliases := normalizeRequestHashAliases(nil)
	if aliases == nil || len(aliases) != 0 {
		t.Fatalf("empty aliases = %#v, want non-nil empty slice", aliases)
	}
}

func TestRequestHashMatchesCompatibleLegacyHash(t *testing.T) {
	t.Parallel()

	if !requestHashMatches("legacy", nil, "canonical", []string{"legacy"}) {
		t.Fatal("expected compatible legacy hash to match")
	}
	if !requestHashMatches("canonical", []string{"legacy"}, "legacy", nil) {
		t.Fatal("expected stored legacy alias to match after rollback-compatible reservation")
	}
	if requestHashMatches("different", []string{"another"}, "canonical", []string{"legacy"}) {
		t.Fatal("unexpected match for unrelated request hash")
	}
}
