//nolint:testpackage // Shares scoped PostgreSQL fault injection between contract tests.
package jobs

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// scopedJobFaultTrigger rejects only the fixture row. Column names are quoted
// identifiers and values are SQL literals, including inside the function body.
func scopedJobFaultTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, table, event string, deferred bool, fields map[string]string) func() {
	t.Helper()
	if len(fields) == 0 {
		t.Fatal("fault trigger requires a fixture selector")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	predicates := make([]string, 0, len(keys))
	for _, key := range keys {
		predicates = append(predicates, "NEW."+pgx.Identifier{key}.Sanitize()+"::text = "+quote(fields[key]))
	}
	name := "cv_job_fault_" + fixtureTag()
	function := name + "_fn"
	body := "BEGIN IF " + strings.Join(predicates, " AND ") + " THEN RAISE EXCEPTION 'job contract fault'; END IF; RETURN NEW; END;"
	if _, err := pg.Exec(ctx, fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS %s", pgx.Identifier{function}.Sanitize(), quote(body))); err != nil {
		t.Fatal(err)
	}
	removed := false
	remove := func() {
		t.Helper()
		if removed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", pgx.Identifier{name}.Sanitize(), pgx.Identifier{table}.Sanitize())); err != nil {
			t.Errorf("drop scoped fault trigger: %v", err)
			return
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", pgx.Identifier{function}.Sanitize())); err != nil {
			t.Errorf("drop scoped fault function: %v", err)
			return
		}
		removed = true
	}
	t.Cleanup(remove)
	kind, timing := "TRIGGER", ""
	if deferred {
		kind, timing = "CONSTRAINT TRIGGER", "DEFERRABLE INITIALLY DEFERRED"
	}
	statement := fmt.Sprintf("CREATE %s %s AFTER %s ON %s %s FOR EACH ROW EXECUTE FUNCTION %s()",
		kind, pgx.Identifier{name}.Sanitize(), event, pgx.Identifier{table}.Sanitize(), timing, pgx.Identifier{function}.Sanitize())
	if _, err := pg.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	return remove
}
