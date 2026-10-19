//nolint:testpackage // Exercises heartbeat recovery through a real PostgreSQL connection.
package runtime

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationHeartbeatMissingNodeRollbackConnectionFailure(t *testing.T) {
	repo := runtimeRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	upstream, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dropped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- dropRuntimeRollback(ctx, listener, net.JoinHostPort(upstream.Host, strconv.Itoa(int(upstream.Port))), dropped)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case proxyErr := <-done:
			if proxyErr != nil {
				t.Errorf("rollback connection proxy: %v", proxyErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("rollback connection proxy did not stop")
		}
	})
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("proxy address is %T, want TCP", listener.Addr())
	}
	pg, err := postgres.New(ctx, &postgres.Config{
		Host:      address.IP.String(),
		Port:      address.Port,
		User:      upstream.User,
		Password:  upstream.Password,
		Database:  upstream.Database,
		MaxConns:  1,
		TLSConfig: &postgres.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	faulted := New(repo.cfg, pg)
	err = faulted.Heartbeat(ctx)
	select {
	case <-dropped:
	default:
		t.Fatalf("heartbeat returned before the rollback connection was dropped: %v", err)
	}
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to rollback missing runtime heartbeat transaction") {
		t.Fatalf("heartbeat with disconnected rollback = %v, want Internal rollback error", err)
	}
	var count int
	if err := repo.pg.QueryRow(ctx, `SELECT count(*) FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed rollback registered node: count=%d err=%v", count, err)
	}
	if err := repo.Heartbeat(ctx); err != nil {
		t.Fatalf("heartbeat after restoring connectivity: %v", err)
	}
	if state := readNodeState(t, repo); state.status != "READY" {
		t.Fatalf("recovered node state = %+v", state)
	}
}

// dropRuntimeRollback forwards real database traffic until ROLLBACK, then
// disconnects both ends before PostgreSQL can acknowledge transaction cleanup.
func dropRuntimeRollback(ctx context.Context, listener net.Listener, upstream string, dropped chan<- struct{}) error {
	client, err := listener.Accept()
	if err != nil {
		return err
	}
	// Refuse replacement connections to this one-connection fault proxy.
	_ = listener.Close()
	defer client.Close()
	server, err := (&net.Dialer{}).DialContext(ctx, "tcp", upstream)
	if err != nil {
		return err
	}
	defer server.Close()
	deadline, _ := ctx.Deadline()
	for _, conn := range []net.Conn{client, server} {
		if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
			return deadlineErr
		}
	}
	forwarded := make(chan struct{})
	go func() {
		//nolint:errcheck // Closing either endpoint is the injected transport failure.
		_, _ = io.Copy(client, server)
		close(forwarded)
	}()
	defer func() {
		_ = server.Close()
		_ = client.Close()
		<-forwarded
	}()
	reader := pgproto3.NewBackend(client, client)
	writer := pgproto3.NewFrontend(server, server)
	message, err := reader.ReceiveStartupMessage()
	if err != nil {
		return err
	}
	writer.Send(message)
	if flushErr := writer.Flush(); flushErr != nil {
		return flushErr
	}
	// Forward frames unchanged so PostgreSQL handles authentication directly.
	for {
		var header [5]byte
		if _, err = io.ReadFull(client, header[:]); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint32(header[1:])) - 4
		if length < 0 || length > 1<<20 {
			return fmt.Errorf("invalid frontend message length: %d", length)
		}
		payload := make([]byte, length)
		if _, err = io.ReadFull(client, payload); err != nil {
			return err
		}
		if header[0] == 'Q' && string(payload) == "rollback\x00" {
			close(dropped)
			return nil
		}
		if _, err = server.Write(append(header[:], payload...)); err != nil {
			return err
		}
	}
}
