//nolint:testpackage // Exercises PostgreSQL error handling through the real pgx client.
package joblogs

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

func TestFetchActiveWorkflowLogPairsProtocolFailures(t *testing.T) {
	for _, tc := range []struct {
		name, workflowID, want string
		code                   codes.Code
		queryError             bool
	}{
		{
			name: "query rejects invalid UUID", workflowID: "not-a-workflow", queryError: true,
			code: codes.InvalidArgument, want: "invalid workflow or user id in job log batch",
		},
		{
			name: "null workflow ID cannot be scanned", workflowID: "00000000-0000-0000-0000-000000000001",
			code: codes.Internal, want: "failed to scan active workflow for job logs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := workflowPairsProtocolPostgres(t, tc.queryError)
			repo := &Repository{pg: pg}
			got, err := repo.fetchActiveWorkflowLogPairs(t.Context(), []workflowLogPair{{
				workflowID: tc.workflowID, userID: "10000000-0000-0000-0000-000000000001",
			}})
			if got != nil || status.Code(err) != tc.code || !strings.Contains(status.Convert(err).Message(), tc.want) {
				t.Fatalf("workflow pairs = %v, %v; want nil, %v containing %q", got, err, tc.code, tc.want)
			}
		})
	}
}

// PostgreSQL normally returns non-null IDs and defers UUID errors to row iteration.
// These wire responses exercise the remaining guards without a production injection hook.
func workflowPairsProtocolPostgres(t *testing.T, queryError bool) *postgres.Postgres {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveWorkflowPairsProtocol(listener, queryError) }()
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL listener: %v", closeErr)
		}
		if serveErr := <-done; serveErr != nil {
			t.Errorf("PostgreSQL protocol fixture: %v", serveErr)
		}
	})
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("PostgreSQL listener has no TCP address")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pg, err := postgres.New(ctx, &postgres.Config{
		Host: "127.0.0.1", Port: addr.Port,
		User: "test", Database: "test", MaxConns: 1, DialTimeout: time.Second,
		TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	return pg
}

//nolint:gocyclo // One dispatch loop handles the pgx startup, ping, lookup, and shutdown messages.
func serveWorkflowPairsProtocol(listener net.Listener, queryError bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	backend := pgproto3.NewBackend(conn, conn)
	if _, err := backend.ReceiveStartupMessage(); err != nil {
		return err
	}
	backend.Send(&pgproto3.AuthenticationOk{})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err := backend.Flush(); err != nil {
		return err
	}
	for {
		message, err := backend.Receive()
		if err != nil {
			return err
		}
		switch message.(type) {
		case *pgproto3.Query: // Pool health check.
			backend.Send(&pgproto3.EmptyQueryResponse{})
			backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		case *pgproto3.Parse:
			if queryError {
				backend.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "22P02", Message: "invalid input syntax for type uuid"})
			} else {
				backend.Send(&pgproto3.ParseComplete{})
			}
		case *pgproto3.Describe:
			if !queryError {
				backend.Send(&pgproto3.ParameterDescription{ParameterOIDs: []uint32{2950, 2950}})
				backend.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{
					{Name: []byte("id"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1},
					{Name: []byte("user_id"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1},
				}})
			}
		case *pgproto3.Bind:
			backend.Send(&pgproto3.BindComplete{})
		case *pgproto3.Execute:
			backend.Send(&pgproto3.DataRow{Values: [][]byte{nil, []byte("10000000-0000-0000-0000-000000000001")}})
			backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		case *pgproto3.Sync:
			backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		case *pgproto3.Terminate:
			return nil
		default:
			return fmt.Errorf("unexpected PostgreSQL message %T", message)
		}
		if err := backend.Flush(); err != nil {
			return err
		}
	}
}
