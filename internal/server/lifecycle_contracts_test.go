//nolint:testpackage // Exercises private lifecycle helpers with real listeners.
package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartReturnsListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	s := &Server{logger: zap.NewNop(), httpServer: &http.Server{Addr: listener.Addr().String(), ReadHeaderTimeout: time.Second}}
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	select {
	case err := <-done:
		var operation *net.OpError
		require.ErrorAs(t, err, &operation)
		require.Equal(t, "listen", operation.Op)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return its listen error")
	}
}

func TestStartTreatsClosedServerAsNormalStop(t *testing.T) {
	s := &Server{logger: zap.NewNop(), httpServer: &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}}
	require.NoError(t, s.httpServer.Close())
	require.NoError(t, s.start(context.Background(), time.Second))
}

func TestServeReturnsClosedListenerError(t *testing.T) {
	listener := newLifecycleListener(t)
	require.NoError(t, listener.Close())
	s := &Server{logger: zap.NewNop(), httpServer: &http.Server{ReadHeaderTimeout: time.Second}}
	require.ErrorIs(t, s.serve(context.Background(), listener, time.Second), net.ErrClosed)
}

func TestServeShutdown(t *testing.T) {
	for _, deadlineExpires := range []bool{false, true} {
		name := "drains active request"
		if deadlineExpires {
			name = "reports shutdown deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requestStarted := make(chan struct{})
			finishRequest := make(chan struct{})
			shutdownStarted := make(chan struct{})
			var finishOnce sync.Once
			finish := func() { finishOnce.Do(func() { close(finishRequest) }) }
			t.Cleanup(finish)
			listener := newLifecycleListener(t)
			writeError := make(chan error, 1)
			s := &Server{logger: zap.NewNop(), httpServer: &http.Server{
				ReadHeaderTimeout: time.Second,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					close(requestStarted)
					<-finishRequest
					_, err := io.WriteString(w, "drained")
					writeError <- err
				}),
			}}
			t.Cleanup(func() { _ = s.httpServer.Close() })
			s.httpServer.RegisterOnShutdown(func() { close(shutdownStarted) })
			timeout := 5 * time.Second
			if deadlineExpires {
				timeout = 0
			}
			done := make(chan error, 1)
			go func() { done <- s.serve(ctx, listener, timeout) }()
			response := make(chan error, 1)
			client := &http.Client{Timeout: 5 * time.Second}
			t.Cleanup(client.CloseIdleConnections)
			go func() {
				result, err := client.Get("http://" + listener.Addr().String())
				if err != nil {
					response <- err
					return
				}
				defer result.Body.Close()
				body, err := io.ReadAll(result.Body)
				if err == nil && (result.StatusCode != http.StatusOK || string(body) != "drained") {
					err = errors.New("response was not drained successfully")
				}
				response <- err
			}()
			awaitLifecycle(t, requestStarted)
			cancel()
			awaitLifecycle(t, shutdownStarted)
			if deadlineExpires {
				require.ErrorIs(t, awaitLifecycleError(t, done), context.DeadlineExceeded)
				finish()
			} else {
				select {
				case err := <-done:
					t.Fatalf("shutdown returned before active request completed: %v", err)
				default:
				}
				finish()
				require.NoError(t, awaitLifecycleError(t, done))
			}
			require.NoError(t, awaitLifecycleError(t, response))
			require.NoError(t, awaitLifecycleError(t, writeError))
		})
	}
}

func TestNormalizeServeError(t *testing.T) {
	require.NoError(t, normalizeServeError(http.ErrServerClosed))
	require.ErrorIs(t, normalizeServeError(net.ErrClosed), net.ErrClosed)
}

func newLifecycleListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func awaitLifecycle(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle event")
	}
}

func awaitLifecycleError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle result")
		return nil
	}
}
