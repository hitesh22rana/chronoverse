//nolint:testpackage // Injects transaction-boundary failures without production test hooks.
package jobs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func intentStorageFixture() (*jobsmodel.ContainerIntent, *jobsmodel.ContainerIntentCommand) {
	i := &jobsmodel.ContainerIntent{
		ID:                 "intent",
		JobID:              "job",
		WorkflowID:         "workflow",
		UserID:             "user",
		RuntimeInstance:    "original",
		ContainerID:        strings.Repeat("a", 64),
		CreateReserved:     true,
		Generation:         1,
		WorkflowGeneration: 1,
		RequestHash:        "frozen",
	}
	c := &jobsmodel.ContainerIntentCommand{
		IntentID:           i.ID,
		JobID:              i.JobID,
		Attempt:            1,
		WorkflowGeneration: 1,
		RuntimeInstance:    i.RuntimeInstance,
		ContainerID:        i.ContainerID,
		LeaseToken:         "lease",
		Actor:              "operator",
		Reason:             "restored",
		Generation:         1,
		Token:              "token",
	}
	return i, c
}

func intentStorageRow(i *jobsmodel.ContainerIntent) claimRow {
	return claimRow{values: []any{
		i.ID, i.JobID, i.WorkflowID, i.UserID, i.Name, i.RuntimeNodeID, i.RuntimeEndpoint, i.RuntimeInstance, i.RequestHash, i.ContainerID, i.Outcome, i.ErrorCode, i.ErrorMessage,
		i.Token, i.NextInspectionAt, i.LastInspectionAt, i.Attempt, i.Generation, i.CreateReserved, i.StartReserved, i.Closed, i.LogRetention,
		i.ImageReference, i.WorkflowGeneration, i.Stage, i.InspectionErrorCode, i.InspectionErrorMessage, i.Quarantined, i.InspectionErrors, i.HeldCapacity,
	}}
}

// Each fault must stop authority/observation processing and preserve the database
// diagnostic. Real PostgreSQL tests separately own transaction and row-lock effects.
func TestContainerIntentStorageFailuresDoNotBecomePermission(t *testing.T) {
	failure := errors.New("lost storage connection")
	for _, tc := range []struct {
		name   string
		rows   []claimRow
		writes int
		run    func(*Repository, *claimTx, *jobsmodel.ContainerIntent, *jobsmodel.ContainerIntentCommand) error
	}{
		{"lookup", []claimRow{{values: []any{"intent"}}}, 0, func(_ *Repository, tx *claimTx, _ *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			c.IntentID = ""
			return resolveContainerIntentID(t.Context(), tx, "get", c)
		}},
		{
			"authority",
			[]claimRow{{values: []any{"workflow"}}, {values: []any{false, int64(1)}}, {values: []any{true, "process"}}},
			0,
			func(r *Repository, tx *claimTx, _ *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
				_, _, err := r.lockContainerIntentAuthority(t.Context(), tx, "reserve_create", c)
				return err
			},
		},
		{"create intent", []claimRow{{values: []any{"intent"}}}, 1, func(r *Repository, tx *claimTx, _ *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return r.createContainerIntent(t.Context(), tx, c)
		}},
		{"load", []claimRow{{err: failure}}, 0, func(_ *Repository, tx *claimTx, _ *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			_, err := loadContainerIntent(t.Context(), tx, c.IntentID)
			return err
		}},
		{"original authority", []claimRow{{values: []any{true}}}, 0, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return validateOriginalAuthority(t.Context(), tx, "reserve_create", i, c, "process", 1)
		}},
		{"materialization lookup", []claimRow{{values: []any{true}}}, 0, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return observeIntentContainer(t.Context(), tx, i, c)
		}},
		{"create grant", nil, 1, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			granted, err := reserveContainerIntent(t.Context(), tx, "reserve_create", i, c)
			if err != nil && granted {
				t.Fatal("failed grant exposed permission")
			}
			return err
		}},
		{
			"start grant",
			[]claimRow{{values: []any{true}}, {values: []any{false}}, {values: []any{false}}},
			2,
			func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
				granted, err := reserveContainerIntent(t.Context(), tx, "reserve_start", i, c)
				if err != nil && granted {
					t.Fatal("failed Start exposed permission")
				}
				return err
			},
		},
		{"first observation", []claimRow{{values: []any{true}}}, 2, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			i.ContainerID = ""
			return recordContainerObservation(t.Context(), tx, i, c)
		}},
		{"late observation", []claimRow{{values: []any{true}}}, 2, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			i.Closed = true
			return recordContainerObservation(t.Context(), tx, i, c)
		}},
		{"close", nil, 2, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return closeContainerIntent(t.Context(), tx, i, c)
		}},
		{"inspection report", []claimRow{{values: []any{true}}}, 1, func(_ *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return reportContainerInspection(t.Context(), tx, i, c)
		}},
		{"redrive", []claimRow{{values: []any{false}}}, 2, func(r *Repository, tx *claimTx, i *jobsmodel.ContainerIntent, c *jobsmodel.ContainerIntentCommand) error {
			return r.redriveContainerIntent(t.Context(), tx, i, c)
		}},
	} {
		for step := 0; step < len(tc.rows)+tc.writes; step++ {
			name := tc.name + "/" + strconv.Itoa(step)
			t.Run(name, func(t *testing.T) {
				tx := &claimTx{t: t, rows: append([]claimRow(nil), tc.rows...), execTags: make([]string, tc.writes), execErrors: make([]error, tc.writes)}
				for index := range tx.execTags {
					tx.execTags[index] = "UPDATE 1"
				}
				if step < len(tc.rows) {
					tx.rows[step].err = failure
				} else {
					tx.execErrors[step-len(tc.rows)] = failure
				}
				i, c := intentStorageFixture()
				err := tc.run(&Repository{}, tx, i, c)
				if err == nil || !strings.Contains(err.Error(), failure.Error()) {
					t.Fatalf("storage failure was swallowed or replaced: %v", err)
				}
			})
		}
	}
}

func TestContainerStartRejectsUnattachedOrConflictingMaterializations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rows   []claimRow
		change func(*jobsmodel.ContainerIntent, *jobsmodel.ContainerIntentCommand)
		code   codes.Code
	}{
		{"no Create reservation", nil, func(i *jobsmodel.ContainerIntent, _ *jobsmodel.ContainerIntentCommand) { i.CreateReserved = false }, codes.FailedPrecondition},
		{"not attached", []claimRow{{values: []any{false}}}, nil, codes.FailedPrecondition},
		{"removed", []claimRow{{values: []any{true}}, {values: []any{true}}}, nil, codes.Aborted},
		{"unresolved predecessor", []claimRow{{values: []any{true}}, {values: []any{false}}, {values: []any{true}}}, nil, codes.Aborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, c := intentStorageFixture()
			if tc.change != nil {
				tc.change(i, c)
			}
			tx := &claimTx{t: t, rows: tc.rows}
			granted, err := reserveContainerIntent(t.Context(), tx, "reserve_start", i, c)
			if granted || status.Code(err) != tc.code || tx.execs != 0 {
				t.Fatalf("invalid Start mutated authority: grant=%v writes=%d error=%v", granted, tx.execs, err)
			}
		})
	}
}

func TestContainerIntentIdentityRefusalsDoNotNeedDatabaseMutation(t *testing.T) {
	i, c := intentStorageFixture()
	for _, tc := range []struct {
		name   string
		change func(*jobsmodel.ContainerIntentCommand)
		code   codes.Code
	}{
		{"wrong job", func(c *jobsmodel.ContainerIntentCommand) { c.JobID = "other" }, codes.InvalidArgument},
		{"wrong daemon", func(c *jobsmodel.ContainerIntentCommand) { c.RuntimeInstance = "other" }, codes.FailedPrecondition},
		{"changed request", func(c *jobsmodel.ContainerIntentCommand) { c.RequestHash = "changed" }, codes.AlreadyExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := *c
			tc.change(&command)
			if err := validateContainerIntentIdentity(i, &command); status.Code(err) != tc.code {
				t.Fatalf("immutable identity accepted: %v", err)
			}
		})
	}
	if err := validateOriginalAuthority(t.Context(), nil, "reserve_create", i, c, "", 1); status.Code(err) != codes.Aborted {
		t.Fatalf("missing process minted permission: %v", err)
	}
	if _, err := (&Repository{}).UpdateContainerIntent(t.Context(), "ensure", nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil mutation accepted: %v", err)
	}
	if _, err := (&Repository{}).ListContainerIntents(t.Context(), nil, false); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil listing accepted: %v", err)
	}
	for _, identity := range [][]string{nil, {"daemon"}, {"", "intent"}, {"daemon", ""}} {
		if _, err := (&Repository{}).GetContainerCleanupReceipt(context.Background(), "job", "container", identity...); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("incomplete receipt read accepted: %v", err)
		}
		if err := (&Repository{}).RecordContainerCleanupReceipt(context.Background(), "job", "container", identity...); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("incomplete receipt write accepted: %v", err)
		}
	}
}

func TestContainerIntentLookupAndInspectionRefuseMissingProof(t *testing.T) {
	t.Run("missing intent lookup", func(t *testing.T) {
		i, c := intentStorageFixture()
		c.IntentID = ""
		tx := &claimTx{t: t, rows: []claimRow{{err: pgx.ErrNoRows}}}
		if err := resolveContainerIntentID(t.Context(), tx, "get", c); status.Code(err) != codes.NotFound {
			t.Fatalf("missing identity accepted: %v", err)
		}
		_ = i
	})
	t.Run("missing locked intent", func(t *testing.T) {
		tx := &claimTx{t: t, rows: []claimRow{{err: pgx.ErrNoRows}}}
		if _, err := loadContainerIntent(t.Context(), tx, "missing"); status.Code(err) != codes.NotFound {
			t.Fatalf("missing intent accepted: %v", err)
		}
	})
	t.Run("deleted job authority", func(t *testing.T) {
		_, c := intentStorageFixture()
		tx := &claimTx{t: t, rows: []claimRow{{err: pgx.ErrNoRows}}}
		if _, _, err := (&Repository{}).lockContainerIntentAuthority(t.Context(), tx, "reserve_create", c); status.Code(err) != codes.Aborted {
			t.Fatalf("deleted job granted authority: %v", err)
		}
	})
	t.Run("unrecorded materialization", func(t *testing.T) {
		i, c := intentStorageFixture()
		tx := &claimTx{t: t, rows: []claimRow{{values: []any{false}}}}
		if err := observeIntentContainer(t.Context(), tx, i, c); status.Code(err) != codes.NotFound {
			t.Fatalf("unverified container adopted: %v", err)
		}
	})
	t.Run("wrong daemon observation", func(t *testing.T) {
		i, c := intentStorageFixture()
		c.RuntimeInstance = "replacement"
		if err := recordContainerObservation(t.Context(), nil, i, c); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("replacement observation accepted: %v", err)
		}
	})
	t.Run("expired inspection", func(t *testing.T) {
		i, c := intentStorageFixture()
		tx := &claimTx{t: t, rows: []claimRow{{values: []any{false}}}}
		if err := validateInspectionLease(t.Context(), tx, i, c); status.Code(err) != codes.Aborted {
			t.Fatalf("expired inspection accepted: %v", err)
		}
	})
	t.Run("unauditable redrive", func(t *testing.T) {
		i, c := intentStorageFixture()
		c.Actor = " "
		if err := (&Repository{}).redriveContainerIntent(t.Context(), nil, i, c); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("anonymous redrive accepted: %v", err)
		}
	})
	t.Run("bounded unicode diagnostic", func(t *testing.T) {
		input := strings.Repeat("界", 2050) + string([]byte{0xff})
		got := truncateDiagnostic(input)
		if got != strings.Repeat("界", 2048) {
			t.Fatalf("diagnostic was not bounded at rune boundary: runes=%d", len([]rune(got)))
		}
	})
}

//nolint:gocyclo // One handoff matrix checks that no read failure leaks partial send authority.
func TestContainerIntentDispatchDoesNotExposePartiallyLoadedOrWrittenAuthority(t *testing.T) {
	failure := errors.New("intent storage failed during authority handoff")
	i, base := intentStorageFixture()
	i.CreateReserved = false
	i.Attempt = 1
	rows := []claimRow{
		{values: []any{i.WorkflowID}},
		{values: []any{false, int64(1)}},
		{values: []any{true, "process"}},
		intentStorageRow(i),
		{values: []any{true}},
		intentStorageRow(i),
	}
	for index := range rows {
		t.Run("read failure "+strconv.Itoa(index), func(t *testing.T) {
			req := *base
			tx := &claimTx{t: t, rows: append([]claimRow(nil), rows...), execTags: []string{"UPDATE 1"}}
			tx.rows[index].err = failure
			result, err := (&Repository{}).applyContainerIntent(t.Context(), tx, "reserve_create", &req)
			if err == nil || result != nil || !strings.Contains(err.Error(), failure.Error()) {
				t.Fatalf("partial authority escaped after failed read: %+v %v", result, err)
			}
		})
	}
	t.Run("unresolved identity", func(t *testing.T) {
		req := *base
		req.IntentID = ""
		tx := &claimTx{t: t, rows: []claimRow{{err: failure}}}
		if result, err := (&Repository{}).applyContainerIntent(t.Context(), tx, "get", &req); result != nil || !errors.Is(err, failure) {
			t.Fatalf("unresolved identity escaped: %+v %v", result, err)
		}
	})
	t.Run("stale workflow snapshot", func(t *testing.T) {
		req := *base
		req.WorkflowGeneration = 2
		tx := &claimTx{t: t, rows: rows[:3]}
		if result, err := (&Repository{}).applyContainerIntent(t.Context(), tx, "ensure", &req); result != nil || status.Code(err) != codes.Aborted || tx.execs != 0 {
			t.Fatalf("stale snapshot wrote intent: %+v %v", result, err)
		}
	})
	t.Run("failed durable intent insert", func(t *testing.T) {
		req := *base
		tx := &claimTx{t: t, rows: rows[:3], execTags: []string{"INSERT 0 0"}, execErrors: []error{failure}}
		if result, err := (&Repository{}).applyContainerIntent(t.Context(), tx, "ensure", &req); result != nil || status.Code(err) != codes.Internal {
			t.Fatalf("failed durable identity escaped: %+v %v", result, err)
		}
	})
	t.Run("mismatched returned identity", func(t *testing.T) {
		req := *base
		req.JobID = "different-job"
		tx := &claimTx{t: t, rows: []claimRow{intentStorageRow(i)}}
		if result, err := (&Repository{}).applyContainerIntent(t.Context(), tx, "get", &req); result != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("mismatched identity returned: %+v %v", result, err)
		}
	})
	t.Run("bound materialization lookup", func(t *testing.T) {
		req := *base
		tx := &claimTx{t: t, rows: []claimRow{{values: []any{true}}}}
		if err := observeIntentContainer(t.Context(), tx, i, &req); err != nil || i.ContainerID != req.ContainerID {
			t.Fatalf("verified binding lookup failed: %+v %v", i, err)
		}
	})
}

func TestCleanupAttemptRequiresCompletePhysicalIdentity(t *testing.T) {
	for _, identity := range [][]string{nil, {"daemon"}, {"", "intent"}, {"daemon", ""}} {
		// No database is installed: invalid proof must be rejected before storage.
		result, err := (&Repository{}).UpdateContainerCleanup(t.Context(), "begin", "job", "container", 1, "", "", false, identity...)
		if result != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("incomplete cleanup identity accepted: identity=%v result=%+v error=%v", identity, result, err)
		}
	}
}
