//nolint:testpackage // Tests unexported filter helpers directly.
package jobs

import (
	"testing"
)

func TestValidateJobLogsFilterIDs(t *testing.T) {
	t.Parallel()

	valid := "550e8400-e29b-41d4-a716-446655440000"
	if err := validateJobLogsFilterIDs(valid, valid, valid); err != nil {
		t.Fatalf("validateJobLogsFilterIDs() error = %v", err)
	}

	for _, ids := range [][3]string{
		{`" OR "1"="1`, valid, valid},
		{valid, `workflow\" OR`, valid},
		{valid, valid, `job\`},
	} {
		if err := validateJobLogsFilterIDs(ids[0], ids[1], ids[2]); err == nil {
			t.Fatalf("validateJobLogsFilterIDs(%q) expected error", ids)
		}
	}
}

func TestMeiliFilterValueKeepsSingleTenantScope(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		`" OR "1"="1`:     `\" OR \"1\"=\"1`,
		`a\b`:             `a\\b`,
		`"`:               `\"`,
		`\`:               `\\`,
		`plain-uuid-part`: `plain-uuid-part`,
	}
	for probe, want := range cases {
		if got := meiliFilterValue(probe); got != want {
			t.Fatalf("meiliFilterValue(%q) = %q, want %q", probe, got, want)
		}
	}

	// Every quote or backslash in the output must be escaped, so the value
	// cannot break out of its quoted filter string.
	for _, probe := range []string{`" OR "1"="1`, `a\b"x`, `\"`} {
		escaped := meiliFilterValue(probe)
		for i := 0; i < len(escaped); i++ {
			switch escaped[i] {
			case '\\':
				i++
				if i >= len(escaped) || (escaped[i] != '\\' && escaped[i] != '"') {
					t.Fatalf("meiliFilterValue(%q) = %q, bad escape at %d", probe, escaped, i)
				}
			case '"':
				t.Fatalf("meiliFilterValue(%q) = %q, bare quote at %d", probe, escaped, i)
			}
		}
	}
}
