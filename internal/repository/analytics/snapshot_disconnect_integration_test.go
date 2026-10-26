//nolint:testpackage // Exercises snapshot boundaries against PostgreSQL.
package analytics

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// snapshotBoundary selects where the proxy drops the connection: after
// `selects` completed SELECTs, or after `rows` DataRows of the following SELECT.
type snapshotBoundary struct {
	selects int
	rows    int
}

// snapshotProgress reports whether the current frame is the selected cut point.
type snapshotProgress struct {
	completed    int
	rows         int
	cutNextReady bool
}

func (p *snapshotProgress) observe(kind byte, body []byte, boundary snapshotBoundary) bool {
	switch kind {
	case 'C':
		tag := strings.TrimSuffix(string(body), "\x00")
		if tag == "BEGIN" {
			p.completed, p.rows = 0, 0
			p.cutNextReady = boundary.selects == 0 && boundary.rows == 0
		}
		if strings.HasPrefix(tag, "SELECT ") {
			p.completed++
			p.rows = 0
			p.cutNextReady = p.completed == boundary.selects && boundary.rows == 0
		}
	case 'D':
		if p.completed == boundary.selects && boundary.rows > 0 {
			p.rows++
			return p.rows == boundary.rows
		}
	case 'Z':
		return p.cutNextReady && len(body) == 1 && body[0] == 'T'
	}

	return false
}

func TestIntegrationAnalyticsSnapshotDisconnects(t *testing.T) {
	for _, tc := range []struct {
		name, operation string
		boundary        snapshotBoundary
	}{
		{"aggregate", "failed to get user analytics", snapshotBoundary{selects: 0}},
		{"kinds", "failed to get workflow kind analytics", snapshotBoundary{selects: 1}},
		{"kinds partial", "failed to collect workflow kind analytics", snapshotBoundary{selects: 1, rows: 1}},
		{"top workflows", "failed to get top workflow analytics", snapshotBoundary{selects: 2}},
		{"commit", "failed to commit user analytics snapshot", snapshotBoundary{selects: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := testkit.Postgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			userID := testkit.SeedUser(ctx, t, pg, "snapshot-"+uuid.NewString()+"@chronoverse.test")
			workflowID := testkit.SeedWorkflow(ctx, t, pg, userID, "snapshot-"+tc.name)
			if _, err := pg.Exec(ctx, `INSERT INTO analytics(user_id,workflow_id,kind,jobs_count,logs_count) VALUES($1,$2,'HEARTBEAT',1,2)`, userID, workflowID); err != nil {
				t.Fatal(err)
			}
			cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
			if err != nil {
				t.Fatal(err)
			}
			address, cut := snapshotDisconnectProxy(t, net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))), tc.boundary)
			host, portText, err := net.SplitHostPort(address)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := postgres.New(ctx, &postgres.Config{
				Host: host, Port: port, User: cfg.ConnConfig.User, Password: cfg.ConnConfig.Password,
				Database: cfg.ConnConfig.Database, MaxConns: 1, TLSConfig: &postgres.TLSConfig{},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reader.Close)
			got, err := New(nil, reader).GetUserAnalytics(ctx, userID)
			if got != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.operation+":") {
				t.Fatalf("GetUserAnalytics = %+v, %v; want nil, Internal at %s", got, err, tc.operation)
			}
			select {
			case <-cut:
			case <-ctx.Done():
				t.Fatal("connection did not reach the selected snapshot boundary")
			}
		})
	}
}

// Forward results up to the selected boundary before simulating a lost connection.
// Every pool connection is served so a health-check retry cannot stall on the listen backlog.
func snapshotDisconnectProxy(t *testing.T, upstream string, boundary snapshotBoundary) (address string, disconnected <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cut := make(chan struct{})
	var cutOnce sync.Once
	var (
		connsMu sync.Mutex
		conns   []net.Conn
	)
	track := func(conn net.Conn) {
		connsMu.Lock()
		conns = append(conns, conn)
		connsMu.Unlock()
	}
	closeConns := func() {
		connsMu.Lock()
		defer connsMu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		conns = nil
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		closeConns()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("snapshot proxy did not stop")
		}
	})
	go func() {
		defer close(done)
		var handlers sync.WaitGroup
		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				break
			}
			track(client)
			handlers.Add(1)
			go func(client net.Conn) {
				defer handlers.Done()
				defer client.Close()
				server, dialErr := net.DialTimeout("tcp", upstream, 5*time.Second)
				if dialErr != nil {
					return
				}
				track(server)
				defer server.Close()
				if err := client.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return
				}
				if err := server.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return
				}
				copying := make(chan struct{})
				go func() {
					defer close(copying)
					defer server.Close()
					if _, err := io.Copy(server, client); err != nil {
						return
					}
				}()
				if forwardSnapshotResponses(client, server, boundary) {
					cutOnce.Do(func() {
						_ = listener.Close()
						close(cut)
					})
				}
				_ = server.Close()
				_ = client.Close()
				<-copying
			}(client)
		}
		handlers.Wait()
		closeConns()
	}()
	return listener.Addr().String(), cut
}

func forwardSnapshotResponses(client io.Writer, server io.Reader, boundary snapshotBoundary) bool {
	var progress snapshotProgress
	for {
		var header [5]byte
		if _, err := io.ReadFull(server, header[:]); err != nil {
			return false
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length < 4 || length > 16<<20 {
			return false
		}
		body := make([]byte, int(length)-4)
		if _, err := io.ReadFull(server, body); err != nil {
			return false
		}
		cut := progress.observe(header[0], body, boundary)
		if _, err := client.Write(header[:]); err != nil {
			return false
		}
		if _, err := client.Write(body); err != nil {
			return false
		}
		if cut {
			return true
		}
	}
}
