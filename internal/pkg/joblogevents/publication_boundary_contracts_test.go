//nolint:testpackage // Observes live publication and queue shutdown contracts.
package joblogevents

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

func logPublishPeer(t *testing.T, reply func([]string) string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				if deadlineErr := conn.SetDeadline(time.Now().Add(10 * time.Second)); deadlineErr != nil {
					t.Errorf("set fixture deadline: %v", deadlineErr)
					return
				}
				reader := bufio.NewReader(conn)
				for {
					args, readErr := readRedisCommand(reader)
					if readErr != nil {
						return
					}
					response := reply(args)
					if _, writeErr := io.WriteString(conn, response); writeErr != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done; workers.Wait() })
	return listener.Addr().String()
}

func readRedisCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[0] != '*' {
		return nil, fmt.Errorf("expected RESP array, got %q", line)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	args := make([]string, count)
	for i := range args {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) < 3 || line[0] != '$' {
			return nil, fmt.Errorf("expected bulk argument, got %q", line)
		}
		size, sizeErr := strconv.Atoi(strings.TrimSpace(line[1:]))
		if sizeErr != nil {
			return nil, sizeErr
		}
		data := make([]byte, size+2)
		if _, err = io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

func logPublicationStore(t *testing.T, commands chan<- []string) *redispkg.Store {
	t.Helper()
	address := logPublishPeer(t, func(args []string) string {
		switch strings.ToLower(args[0]) {
		case "hello":
			return "-ERR unknown command 'hello'\r\n"
		case "ping":
			return "+PONG\r\n"
		case "publish":
			commands <- args
			return ":1\r\n"
		default:
			return "+OK\r\n"
		}
	})
	host, portString, err := net.SplitHostPort(address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)
	store,
		err := redispkg.New(t.Context(),
		&redispkg.Config{
			Host:                     host,
			Port:                     port,
			PoolSize:                 1,
			ReadTimeout:              time.Second,
			WriteTimeout:             time.Second,
			MaxMemory:                "100mb",
			EvictionPolicy:           "volatile-ttl",
			EvictionPolicySampleSize: 5,
			TLSConfig:                &redispkg.TLSConfig{},
		})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestLivePublicationPreservesPayloadAndReportsEncodingOrRedisFailure(t *testing.T) {
	commands := make(chan []string, 4)
	store := logPublicationStore(t, commands)
	event := &jobsmodel.JobLogEvent{
		JobID:      "job",
		WorkflowID: "workflow",
		EventKey:   "event",
		Retention:  true,
		Message:    "hello",
		TimeStamp: time.Date(2026,
			time.November,
			6,
			1,
			2,
			3,
			0,
			time.UTC),
		Stream:      "stderr",
		SequenceNum: 7,
	}
	published, err := PublishLive(t.Context(), store, event)
	require.NoError(t, err)
	require.True(t, published)
	command := <-commands
	require.Equal(t, []string{"publish", redispkg.GetJobLogsChannel("job")}, command[:2])
	var decoded jobsmodel.JobLogEvent
	require.NoError(t, json.Unmarshal([]byte(command[2]), &decoded))
	require.Equal(t, *event, decoded)
	invalid := *event
	invalid.TimeStamp = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	published, err = PublishLive(t.Context(), store, &invalid)
	require.False(t, published)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "marshal job log event")
	require.Empty(t, commands)
	payload, err := Marshal(&invalid)
	require.Nil(t, payload)
	require.Equal(t, codes.Internal, status.Code(err))
	_, err = Marshal(nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	published, err = PublishLive(t.Context(), store, nil)
	require.False(t, published)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	published, err = PublishLive(ctx, store, event)
	require.False(t, published)
	require.ErrorContains(t, err, "context canceled")
}

func TestPublisherDrainsQueuedEventAndStopsWhenCanceled(t *testing.T) {
	commands := make(chan []string, 4)
	store := logPublicationStore(t, commands)
	publisher := NewLivePublisher(store, LivePublisherConfig{BufferSize: 1, PublishTimeout: time.Second})
	require.True(t, publisher.TryPublish(&jobsmodel.JobLogEvent{JobID: "job", WorkflowID: "workflow", EventKey: "event", Retention: true, Message: "queued"}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); publisher.Run(ctx, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("publisher did not stop")
		}
	})
	select {
	case command := <-commands:
		require.Equal(t, redispkg.GetJobLogsChannel("job"), command[1])
		var event jobsmodel.JobLogEvent
		require.NoError(t, json.Unmarshal([]byte(command[2]), &event))
		require.True(t, event.LivePublished)
		require.Equal(t, "event", event.EventKey)
		require.Equal(t, "queued", event.Message)
	case <-time.After(2 * time.Second):
		t.Fatal("queued event was not published")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not stop")
	}
	var disabled *LivePublisher
	disabled.Run(t.Context(), nil)
}

func TestPublisherWarnsOnFailureWithoutBlockingDurableCaller(t *testing.T) {
	core, entries := observer.New(zap.WarnLevel)
	publisher := &LivePublisher{publishTimeout: time.Second, rdb: &redispkg.Store{}}
	publisher.publish(t.Context(), zap.New(core), nil)
	require.Zero(t, entries.Len())
	event := &jobsmodel.JobLogEvent{
		JobID:      "job",
		WorkflowID: "workflow",
		EventKey:   "event",
		Retention:  true,
		TimeStamp: time.Date(10000,
			time.January,
			1,
			0,
			0,
			0,
			0,
			time.UTC),
		Stream:      "stderr",
		SequenceNum: 3,
	}
	publisher.publish(t.Context(), zap.New(core), event)
	require.Len(t, entries.All(), 1)
	fields := entries.All()[0].ContextMap()
	require.Equal(t, "job", fields["job_id"])
	require.Equal(t, "workflow", fields["workflow_id"])
	require.Equal(t, "event", fields["event_key"])
	require.Equal(t, "stderr", fields["stream"])
	require.EqualValues(t, 3, fields["sequence_num"])
	require.Contains(t, fields["error"], "marshal job log event")
}

func TestMarshalDerivesStableCanonicalEventIdentity(t *testing.T) {
	t.Parallel()
	event := &jobsmodel.JobLogEvent{JobID: "550E8400-E29B-41D4-A716-446655440000", Stream: "stderr", SequenceNum: 5}
	payload, err := Marshal(event)
	require.NoError(t, err)
	require.Equal(t, "log:550e8400-e29b-41d4-a716-446655440000:stderr:5", event.EventKey)
	var decoded jobsmodel.JobLogEvent
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, *event, decoded)
	repeated, err := Marshal(event)
	require.NoError(t, err)
	require.Equal(t, payload, repeated)
}
