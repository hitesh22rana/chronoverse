package commandidempotency_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
)

type legacySyncTx struct {
	pgx.Tx
	calls   [][]any
	results []pgconn.CommandTag
	failAt  int
}

func (tx *legacySyncTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tx.calls = append(tx.calls, append([]any{query}, args...))
	if tx.failAt == len(tx.calls) {
		return pgconn.CommandTag{}, errors.New("storage unavailable")
	}
	if len(tx.results) >= len(tx.calls) {
		return tx.results[len(tx.calls)-1], nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestSyncLegacyIdentities(t *testing.T) {
	t.Parallel()
	valid := commandidempotency.LegacyIdentity{Operation: "create_workflow", RequestHash: fmt.Sprintf("%064x", 1)}
	conflicting := valid
	conflicting.RequestHash = fmt.Sprintf("%064x", 2)
	tests := []struct {
		name       string
		key        string
		fresh      bool
		identities []commandidempotency.LegacyIdentity
		failAt     int
		zeroWrite  bool
		wantCode   codes.Code
		wantCalls  int
	}{
		{name: "invalid key", key: "\n", identities: []commandidempotency.LegacyIdentity{valid}, wantCode: codes.InvalidArgument},
		{name: "missing identity", key: "key", wantCode: codes.Internal},
		{name: "empty operation", key: "key", identities: []commandidempotency.LegacyIdentity{{RequestHash: valid.RequestHash}}, wantCode: codes.Internal},
		{name: "invalid hash", key: "key", identities: []commandidempotency.LegacyIdentity{{Operation: valid.Operation, RequestHash: "invalid"}}, wantCode: codes.Internal},
		{name: "fresh reset failure", key: "key", fresh: true, identities: []commandidempotency.LegacyIdentity{valid}, failAt: 1, wantCode: codes.Internal, wantCalls: 1},
		{name: "insert failure", key: "key", identities: []commandidempotency.LegacyIdentity{valid}, failAt: 1, wantCode: codes.Internal, wantCalls: 1},
		{name: "fresh conflicting stored hash", key: "key", fresh: true, identities: []commandidempotency.LegacyIdentity{valid}, zeroWrite: true, wantCode: codes.Internal, wantCalls: 2},
		{name: "replay preserves stored hash", key: "key", identities: []commandidempotency.LegacyIdentity{valid}, zeroWrite: true, wantCalls: 1},
		{name: "duplicate identities", key: "  key  ", identities: []commandidempotency.LegacyIdentity{valid, valid}, wantCalls: 1},
		{name: "conflicting duplicate", key: "key", identities: []commandidempotency.LegacyIdentity{valid, conflicting}, wantCode: codes.Internal, wantCalls: 1},
		{name: "fresh writes", key: "key", fresh: true, identities: []commandidempotency.LegacyIdentity{valid}, wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tx := &legacySyncTx{failAt: tt.failAt}
			if tt.zeroWrite {
				if tt.fresh {
					tx.results = append(tx.results, pgconn.NewCommandTag("DELETE 1"))
				}
				tx.results = append(tx.results, pgconn.NewCommandTag("INSERT 0 0"))
			}
			err := commandidempotency.SyncLegacyIdentities(context.Background(), tx, "scope", "operation", tt.key, tt.fresh, tt.identities...)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("error=%v, want code %v", err, tt.wantCode)
			}
			if len(tx.calls) != tt.wantCalls {
				t.Fatalf("calls=%d, want %d", len(tx.calls), tt.wantCalls)
			}
			for i, call := range tx.calls {
				if !reflect.DeepEqual(call[1:4], []any{"scope", "operation", "key"}) {
					t.Fatalf("identity args=%v", call[1:4])
				}
				if tt.fresh && i == 0 {
					query, ok := call[0].(string)
					if !ok || !strings.Contains(query, "DELETE FROM") {
						t.Fatal("fresh synchronization must reset old identities first")
					}
					continue
				}
				if !reflect.DeepEqual(call[4:], []any{valid.Operation, valid.RequestHash}) {
					t.Fatalf("legacy args=%v", call[4:])
				}
			}
		})
	}
}
