//nolint:testpackage // Exercises the driver's I/O boundaries and filesystem discovery.
package clickhouse

import (
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type boundaryConn struct {
	driver.Conn
	ping       func(context.Context) error
	exec       func(string, ...any) error
	query      func(string) (driver.Rows, error)
	batch      driver.Batch
	prepareErr error
}

func (c *boundaryConn) Ping(ctx context.Context) error { return c.ping(ctx) }
func (c *boundaryConn) Exec(_ context.Context, q string, args ...any) error {
	return c.exec(q, args...)
}

func (c *boundaryConn) Query(_ context.Context, q string, _ ...any) (driver.Rows, error) {
	return c.query(q)
}

func (c *boundaryConn) PrepareBatch(context.Context, string, ...driver.PrepareBatchOption) (driver.Batch, error) {
	return c.batch, c.prepareErr
}

type boundaryRows struct {
	driver.Rows
	next              bool
	dirty             bool
	scanErr, finalErr error
	closed            bool
}

func (r *boundaryRows) Next() bool { next := r.next; r.next = false; return next }
func (r *boundaryRows) Scan(values ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(values) != 2 {
		return errors.New("unexpected scan width")
	}
	version, ok := values[0].(*uint32)
	if !ok {
		return errors.New("unexpected version destination")
	}
	dirty, ok := values[1].(*uint8)
	if !ok {
		return errors.New("unexpected dirty destination")
	}
	*version = 7
	*dirty = 0
	if r.dirty {
		*dirty = 1
	}
	return nil
}
func (r *boundaryRows) Err() error   { return r.finalErr }
func (r *boundaryRows) Close() error { r.closed = true; return nil }

type boundaryBatch struct {
	driver.Batch
	sendErr error
	sends   int
}

func (b *boundaryBatch) Send() error { b.sends++; return b.sendErr }

func TestHealthRetryAndCancellationBoundaries(t *testing.T) {
	for _, successAt := range []int{1, 2, 0} {
		t.Run(strconv.Itoa(successAt), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				cause := errors.New("connection unavailable")
				start := time.Now()
				conn := &boundaryConn{ping: func(context.Context) error {
					calls++
					if calls == successAt {
						return nil
					}
					return cause
				}}
				err := healthCheck(t.Context(), conn)
				if successAt == 0 {
					require.ErrorIs(t, err, cause)
					require.Equal(t, 3, calls)
					require.Equal(t, 300*time.Millisecond, time.Since(start))
				} else {
					require.NoError(t, err)
					require.Equal(t, successAt, calls)
				}
			})
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	err := healthCheck(ctx, &boundaryConn{ping: func(context.Context) error { calls++; return ctx.Err() }})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

func TestBatchFailureStagesPreserveDriverCause(t *testing.T) {
	for _, stage := range []string{"prepare", "data", "send", "success"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("batch fault")
			batch := &boundaryBatch{}
			conn := &boundaryConn{batch: batch}
			if stage == "prepare" {
				conn.prepareErr = cause
			}
			if stage == "send" {
				batch.sendErr = cause
			}
			callback := false
			err := (&Client{conn: conn}).BatchInsert(t.Context(), "INSERT INTO events", func(got driver.Batch) error {
				callback = true
				require.Same(t, batch, got)
				if stage == "data" {
					return cause
				}
				return nil
			})
			require.Equal(t, stage != "prepare", callback)
			wantSends := 0
			if stage == "send" || stage == "success" {
				wantSends = 1
			}
			require.Equal(t, wantSends, batch.sends)
			if stage == "success" {
				require.NoError(t, err)
			} else {
				require.Equal(t, codes.Internal, status.Code(err))
				require.Contains(t, err.Error(), cause.Error())
			}
		})
	}
}

func TestAppliedMigrationReadFailuresCloseRows(t *testing.T) {
	cause := errors.New("read fault")
	for _, stage := range []string{"query", "scan", "iteration", "dirty", "success"} {
		t.Run(stage, func(t *testing.T) {
			rows := &boundaryRows{next: true, dirty: stage == "dirty"}
			if stage == "scan" {
				rows.scanErr = cause
			}
			if stage == "iteration" {
				rows.finalErr = cause
			}
			c := &Client{conn: &boundaryConn{query: func(q string) (driver.Rows, error) {
				require.Equal(t, "SELECT version, dirty FROM schema_migrations", q)
				if stage == "query" {
					return nil, cause
				}
				return rows, nil
			}}}
			got, err := getAppliedMigrations(t.Context(), c)
			switch stage {
			case "success":
				require.NoError(t, err)
				require.Equal(t, map[int]bool{7: true}, got)
			case "iteration":
				require.ErrorIs(t, err, cause)
				require.Equal(t, map[int]bool{7: true}, got)
			case "dirty":
				require.ErrorContains(t, err, "marked dirty")
				require.Nil(t, got)
			default:
				require.ErrorIs(t, err, cause)
				require.Nil(t, got)
			}
			require.Equal(t, stage != "query", rows.closed)
		})
	}
}

type unreadableMigration struct{ fs.FS }

func (f unreadableMigration) ReadFile(name string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrPermission}
}

func TestMigrationDiscoveryValidatesSourcesAndOrdersVersions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  fs.FS
		message string
	}{
		{"missing directory", fstest.MapFS{}, "migrations"},
		{"missing separator", fstest.MapFS{"migrations/1.up.sql": {Data: []byte("SELECT 1")}}, "invalid migration filename"},
		{"invalid version", fstest.MapFS{"migrations/x_bad.up.sql": {Data: []byte("SELECT 1")}}, "invalid version"},
		{"unreadable file", unreadableMigration{fstest.MapFS{"migrations/1_ok.up.sql": {Data: []byte("SELECT 1")}}}, "read migration file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getPendingMigrations(tc.source, nil)
			require.ErrorContains(t, err, tc.message)
			require.Nil(t, got)
		})
	}
	source := fstest.MapFS{
		"migrations/10_later.up.sql":  {Data: []byte("SELECT 10")},
		"migrations/2_first.up.sql":   {Data: []byte("SELECT 2")},
		"migrations/1_done.up.sql":    {Data: []byte("SELECT 1")},
		"migrations/2_first.down.sql": {Data: []byte("ignored")},
	}
	got, err := getPendingMigrations(source, map[int]bool{1: true})
	require.NoError(t, err)
	require.Equal(t, []migration{{Version: 2, Name: "2_first.up.sql", Content: "SELECT 2"}, {Version: 10, Name: "10_later.up.sql", Content: "SELECT 10"}}, got)
}

func TestMigrationFailureStopsBeforeLaterStatements(t *testing.T) {
	source := fstest.MapFS{"migrations/1_first.up.sql": {Data: []byte("SELECT 1; SELECT 2")}, "migrations/2_next.up.sql": {Data: []byte("SELECT 3")}}
	for _, stage := range []string{"create", "query", "discover", "dirty", "execute", "clean", "success"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("database fault")
			var executed []string
			rows := &boundaryRows{}
			conn := &boundaryConn{}
			conn.query = func(string) (driver.Rows, error) {
				if stage == "query" {
					return nil, cause
				}
				return rows, nil
			}
			conn.exec = func(q string, _ ...any) error {
				executed = append(executed, strings.TrimSpace(q))
				needle := map[string]string{"create": "CREATE TABLE", "dirty": "INSERT INTO schema_migrations", "execute": "SELECT 1", "clean": "ALTER TABLE"}[stage]
				if needle != "" && strings.HasPrefix(strings.TrimSpace(q), needle) {
					return cause
				}
				return nil
			}
			var input fs.FS = source
			if stage == "discover" {
				input = fstest.MapFS{}
			}
			err := migrate(t.Context(), &Client{conn: conn}, input)
			if stage == "success" {
				require.NoError(t, err)
				require.Len(t, executed, 8)
				return
			}
			require.Error(t, err)
			if stage != "discover" {
				require.ErrorIs(t, err, cause)
			}
			require.NotContains(t, executed, "SELECT 3")
			if stage == "execute" {
				require.NotContains(t, executed, "SELECT 2")
			}
			if stage == "create" {
				require.False(t, rows.closed)
			} else if stage != "query" {
				require.True(t, rows.closed)
			}
		})
	}
}
