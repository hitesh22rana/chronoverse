//nolint:testpackage // Shares PostgreSQL fixtures and lock probes with repository tests.
package workflows

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// listedWorkflowFixture selects independent list-filter dimensions.
type listedWorkflowFixture struct {
	name, kind, buildStatus string
	interval                int32
	terminated              bool
}

// seedListedWorkflow creates a workflow and sets its list-visible build state.
func seedListedWorkflow(t *testing.T, repo *Repository, userID string, spec listedWorkflowFixture) string {
	t.Helper()
	payload := fixturePayload
	if spec.kind == "HEARTBEAT" {
		payload = `{"endpoint":"https://example.com","headers":{}}`
	}
	created, err := repo.CreateWorkflow(t.Context(), userID, spec.name, payload, spec.kind, spec.interval, 3, true, "list-"+fixtureTag())
	if err != nil {
		t.Fatalf("create list fixture: %v", err)
	}
	var terminatedAt *time.Time
	if spec.terminated {
		now := time.Now().UTC()
		terminatedAt = &now
	}
	completedContainer := spec.kind == "CONTAINER" && spec.buildStatus == testCompletedBuildStatus
	imageRef := sql.NullString{String: fixtureImage, Valid: completedContainer}
	imageDigest := sql.NullString{String: fixtureImageDigest, Valid: completedContainer}
	_, err = testkit.Postgres(t).Exec(t.Context(), "UPDATE "+postgres.TableWorkflows+
		" SET build_status = $1, terminated_at = $2, resolved_image_ref = $3, resolved_image_digest = $4 WHERE id = $5",
		spec.buildStatus, terminatedAt, imageRef, imageDigest, created.ID)
	if err != nil {
		t.Fatalf("set list fixture state: %v", err)
	}
	return created.ID
}

// newListOwner isolates list fixtures and registers their cleanup.
func newListOwner(t *testing.T) string {
	t.Helper()
	pg := testkit.Postgres(t)
	userID := testkit.SeedUser(t.Context(), t, pg, "list-"+fixtureTag()+"@chronoverse.test")
	registerFixtureCleanup(t.Context(), t, pg, userID)
	return userID
}

// listedIDs extracts page identities without changing row order.
func listedIDs(response *workflowsmodel.ListWorkflowsResponse) []string {
	ids := make([]string, 0, len(response.Workflows))
	for _, workflow := range response.Workflows {
		ids = append(ids, workflow.ID)
	}
	return ids
}

func TestIntegrationListWorkflowsLiteralSearch(t *testing.T) {
	repo := newTestRepository(t)
	cases := []struct{ name, query, match, decoy string }{
		{"percent", "%", "sale%discount", "saleXdiscount"},
		{"underscore", "_", "daily_backup", "dailyXbackup"},
		{"backslash", `\`, `folder\backup`, "folderbackup"},
		{"combined literals", `%_\`, `prefix%_\suffix`, "prefixXYZsuffix"},
		{"case insensitive", "ALPHA", "alpha-backup", "beta-backup"},
		{"SQL syntax", "' OR TRUE --", "literal ' OR TRUE -- text", "unrelated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := newListOwner(t)
			spec := listedWorkflowFixture{kind: "CONTAINER", buildStatus: "QUEUED", interval: 60, name: tc.match}
			wantID := seedListedWorkflow(t, repo, userID, spec)
			spec.name = tc.decoy
			seedListedWorkflow(t, repo, userID, spec)
			response, err := repo.ListWorkflows(t.Context(), userID, "", &workflowsmodel.ListWorkflowsFilters{Query: tc.query})
			if err != nil {
				t.Fatalf("literal search: %v", err)
			}
			if got := listedIDs(response); !reflect.DeepEqual(got, []string{wantID}) || response.Cursor != "" {
				t.Fatalf("literal search %q = ids %v cursor %q, want [%s] and no cursor", tc.query, got, response.Cursor, wantID)
			}
		})
	}
}

func TestIntegrationListWorkflowsFilters(t *testing.T) {
	repo := newTestRepository(t)
	userID := newListOwner(t)
	fixtures := []listedWorkflowFixture{
		{name: "alpha-fast", kind: "CONTAINER", buildStatus: "QUEUED", interval: 30},
		{name: "alpha-boundary", kind: "CONTAINER", buildStatus: testCompletedBuildStatus, interval: 60},
		{name: "alpha-upper", kind: "CONTAINER", buildStatus: testCompletedBuildStatus, interval: 120},
		{name: "alpha-terminated", kind: "CONTAINER", buildStatus: testCompletedBuildStatus, interval: 60, terminated: true},
		{name: "beta-heartbeat", kind: "HEARTBEAT", buildStatus: testCompletedBuildStatus, interval: 60},
	}
	ids := make([]string, len(fixtures))
	for index, spec := range fixtures {
		ids[index] = seedListedWorkflow(t, repo, userID, spec)
	}
	seedListedWorkflow(t, repo, newListOwner(t), fixtures[1])
	cases := []struct {
		name    string
		filters *workflowsmodel.ListWorkflowsFilters
		indices []int
	}{
		{"nil filters", nil, []int{0, 1, 2, 3, 4}},
		{"empty filters", &workflowsmodel.ListWorkflowsFilters{}, []int{0, 1, 2, 3, 4}},
		{"kind", &workflowsmodel.ListWorkflowsFilters{Kind: "HEARTBEAT"}, []int{4}},
		{"build status excludes terminated", &workflowsmodel.ListWorkflowsFilters{BuildStatus: testCompletedBuildStatus}, []int{1, 2, 4}},
		{"terminated only", &workflowsmodel.ListWorkflowsFilters{IsTerminated: true}, []int{3}},
		{"build status takes precedence", &workflowsmodel.ListWorkflowsFilters{BuildStatus: testCompletedBuildStatus, IsTerminated: true}, []int{1, 2, 4}},
		{"inclusive minimum", &workflowsmodel.ListWorkflowsFilters{IntervalMin: 60}, []int{1, 2, 3, 4}},
		{"inclusive maximum", &workflowsmodel.ListWorkflowsFilters{IntervalMax: 60}, []int{0, 1, 3, 4}},
		{"combined", &workflowsmodel.ListWorkflowsFilters{Query: "ALPHA", Kind: "CONTAINER", BuildStatus: testCompletedBuildStatus, IntervalMin: 60, IntervalMax: 120}, []int{1, 2}},
		{"exact interval", &workflowsmodel.ListWorkflowsFilters{IntervalMin: 60, IntervalMax: 60}, []int{1, 3, 4}},
		{"negative bounds ignored", &workflowsmodel.ListWorkflowsFilters{IntervalMin: -1, IntervalMax: -1}, []int{0, 1, 2, 3, 4}},
		{"inverted bounds", &workflowsmodel.ListWorkflowsFilters{IntervalMin: 120, IntervalMax: 30}, nil},
		{"no name match", &workflowsmodel.ListWorkflowsFilters{Query: "missing"}, nil},
		{"unknown kind", &workflowsmodel.ListWorkflowsFilters{Kind: "UNKNOWN"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := repo.ListWorkflows(t.Context(), userID, "", tc.filters)
			if err != nil {
				t.Fatalf("ListWorkflows: %v", err)
			}
			want := make([]string, 0, len(tc.indices))
			for _, index := range tc.indices {
				want = append(want, ids[index])
			}
			got := listedIDs(response)
			slices.Sort(got)
			slices.Sort(want)
			if !reflect.DeepEqual(got, want) || response.Cursor != "" {
				t.Fatalf("ids %v cursor %q, want ids %v without cursor", got, response.Cursor, want)
			}
		})
	}
}

// assertListCursor checks the first unreturned row and returns its decoded cursor.
func assertListCursor(t *testing.T, encoded, wantID string, wantTime time.Time) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	cursor := string(raw)
	id, createdAt, err := extractDataFromCursor(cursor)
	if err != nil || id != wantID || !createdAt.Equal(wantTime) {
		t.Fatalf("cursor = (%q, %v, %v), want first unreturned %q at %v", id, createdAt, err, wantID, wantTime)
	}
	return cursor
}

//nolint:gocyclo // Verifies timestamp ordering, ties and all page boundaries in one fixture.
func TestIntegrationListWorkflowsPagination(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID := newListOwner(t)
	ids := make([]string, 5)
	for index := range ids {
		ids[index] = seedListedWorkflow(t, repo, userID, listedWorkflowFixture{name: fmt.Sprintf("page-%d", index), kind: "CONTAINER", buildStatus: "QUEUED", interval: 60})
	}
	timestamp := time.Date(2026, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	if _, err := pg.Exec(t.Context(), "UPDATE "+postgres.TableWorkflows+" SET created_at = $1 WHERE user_id = $2", timestamp, userID); err != nil {
		t.Fatal(err)
	}
	older := timestamp.Add(-time.Hour)
	for _, id := range ids[3:] {
		if _, err := pg.Exec(t.Context(), "UPDATE "+postgres.TableWorkflows+" SET created_at = $1 WHERE id = $2", older, id); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(ids[:3])
	slices.Reverse(ids[:3])
	slices.Sort(ids[3:])
	slices.Reverse(ids[3:])
	for _, limit := range []int{1, 2, 5, 6} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			pageRepo := New(&Config{FetchLimit: limit}, pg)
			got := make([]string, 0, len(ids))
			cursor := ""
			for offset := 0; offset < len(ids); offset += limit {
				response, err := pageRepo.ListWorkflows(t.Context(), userID, cursor, nil)
				if err != nil {
					t.Fatalf("page %d: %v", offset, err)
				}
				end := min(offset+limit, len(ids))
				if pageIDs := listedIDs(response); !reflect.DeepEqual(pageIDs, ids[offset:end]) {
					t.Fatalf("page ids %v, want %v", pageIDs, ids[offset:end])
				}
				got = append(got, listedIDs(response)...)
				if end == len(ids) {
					if response.Cursor != "" {
						t.Fatalf("final cursor = %q, want empty", response.Cursor)
					}
					continue
				}
				wantTime := timestamp
				if end >= 3 {
					wantTime = older
				}
				cursor = assertListCursor(t, response.Cursor, ids[end], wantTime)
			}
			if !reflect.DeepEqual(got, ids) {
				t.Fatalf("all pages = %v, want %v", got, ids)
			}
		})
	}
	empty, err := repo.ListWorkflows(t.Context(), newListOwner(t), "", nil)
	if err != nil || empty == nil || len(empty.Workflows) != 0 || empty.Cursor != "" {
		t.Fatalf("empty owner = (%+v,%v)", empty, err)
	}
}

func TestIntegrationListWorkflowsErrors(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID := newListOwner(t)
	t.Run("invalid user", func(t *testing.T) {
		response, err := repo.ListWorkflows(t.Context(), "not-a-uuid", "", nil)
		if response != nil || status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "invalid user ID") {
			t.Fatalf("invalid user = (%+v,%v)", response, err)
		}
	})
	for _, filters := range []*workflowsmodel.ListWorkflowsFilters{{BuildStatus: "UNKNOWN"}} {
		t.Run(fmt.Sprintf("invalid enum %v", filters), func(t *testing.T) {
			response, err := repo.ListWorkflows(t.Context(), userID, "", filters)
			if response != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("invalid enum = (%+v, %v), want nil and InvalidArgument", response, err)
			}
		})
	}
	lock := lockWholeTable(postgres.TableWorkflows, "%SELECT id, name, payload, kind, build_status%")
	cases := []struct {
		name, stop string
		code       codes.Code
	}{
		{"caller cancellation", "cancel", codes.Canceled},
		{"caller deadline", "deadline", codes.DeadlineExceeded},
		{"database cancellation", "server", codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, step := callerStopPlan(t.Context(), t, pg, tc.stop, lock)
			err := runWhileBlocked(ctx, t, pg, lock, step, func(commandCtx context.Context) error {
				response, listErr := repo.ListWorkflows(commandCtx, userID, "", nil)
				if response != nil {
					return fmt.Errorf("failure returned a partial response: %+v", response)
				}
				return listErr
			})
			if status.Code(err) != tc.code {
				t.Fatalf("ListWorkflows code %v, want %v: %v", status.Code(err), tc.code, err)
			}
			if tc.code == codes.Internal && !strings.Contains(status.Convert(err).Message(), "failed to list all workflows") {
				t.Fatalf("database failure missing operation context: %v", err)
			}
			if _, retryErr := repo.ListWorkflows(t.Context(), userID, "", nil); retryErr != nil {
				t.Fatalf("retry after interruption: %v", retryErr)
			}
		})
	}
}
