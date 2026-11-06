//nolint:testpackage // Exercises malformed RESP acknowledgements through go-redis.
package jobs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

// readRESPCommand accepts the array-of-bulk-strings format emitted by go-redis.
func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	command := make([]string, count)
	for i := range command {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		length, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
		if parseErr != nil {
			return nil, parseErr
		}
		payload := make([]byte, length+2)
		if _, err = io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		command[i] = string(payload[:length])
	}
	return command, nil
}

// The loopback peer deliberately violates Redis's subscription contract while
// completing client setup normally. A valid Redis server would not send these.
func malformedSubscriptionPeer(t *testing.T, channel, reply string) (host string, port int, closed <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	acceptDone := make(chan struct{})
	var connections sync.WaitGroup
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close RESP listener: %v", err)
		}
		<-acceptDone
		connections.Wait()
	})
	go func() {
		defer close(acceptDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				serveMalformedSubscription(t, conn, channel, reply, done)
			}()
		}
	}()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("RESP listener must use TCP")
	}
	return "127.0.0.1", address.Port, done
}

func TestJobLogSubscriptionRejectsMalformedWireAcknowledgements(t *testing.T) {
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(noop.NewMeterProvider())
	defer otel.SetMeterProvider(previous)
	const jobID = "550e8400-e29b-41d4-a716-446655440000"
	channel := redispkg.GetJobLogsChannel(jobID)
	ack := func(kind, target string) string {
		return fmt.Sprintf("*3\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n:1\r\n", len(kind), kind, len(target), target)
	}
	for _, tc := range []struct{ name, reply string }{
		{name: "pong instead of subscription", reply: "+PONG\r\n"},
		{name: "wrong subscription kind", reply: ack("psubscribe", channel)},
		{name: "wrong channel", reply: ack("subscribe", channel+"-other")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, port, closed := malformedSubscriptionPeer(t, channel, tc.reply)
			rdb, err := redispkg.New(t.Context(), &redispkg.Config{
				Host: host, Port: port, PoolSize: 1, ReadTimeout: time.Second, WriteTimeout: time.Second,
				MaxMemory: "100mb", EvictionPolicy: "volatile-ttl", EvictionPolicySampleSize: 5, TLSConfig: &redispkg.TLSConfig{},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := rdb.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			repo := &Repository{rdb: rdb}
			sub, err := repo.subscribeToJobLogsChannel(t.Context(), jobID)
			if sub != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "unexpected subscription acknowledgement") {
				t.Fatalf("subscription=%v error=%v", sub, err)
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatalf("peer did not observe rejected subscription close: %v", err)
			}
		})
	}
}

func serveMalformedSubscription(t *testing.T, conn net.Conn, channel, reply string, done chan<- struct{}) {
	t.Helper()

	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close RESP connection: %v", err)
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Error(err)
		return
	}
	reader := bufio.NewReader(conn)
	for {
		command, readErr := readRESPCommand(reader)
		if readErr != nil {
			return
		}
		response := "+OK\r\n"
		switch strings.ToLower(command[0]) {
		case "hello":
			response = "-ERR unknown command 'hello'\r\n"
		case "ping":
			response = "+PONG\r\n"
		case "subscribe":
			if len(command) != 2 || command[1] != channel {
				t.Errorf("SUBSCRIBE arguments=%v want channel %q", command, channel)
			}
			response = reply
		}
		if _, err := io.WriteString(conn, response); err != nil {
			return
		}
		if strings.EqualFold(command[0], "subscribe") {
			// Repository rejection must close its subscription, releasing the peer.
			_, readErr = reader.ReadByte()
			if errors.Is(readErr, io.EOF) {
				close(done)
			}
			return
		}
	}
}
