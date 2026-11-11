package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProcessingErrorsDoNotDiscloseContentsThroughRunner(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, code := range []codes.Code{codes.InvalidArgument, codes.Internal, codes.Unavailable} {
			t.Run(fmt.Sprintf("batch=%t/%s", batch, code), func(t *testing.T) {
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
				core, entries := observer.New(zap.InfoLevel)
				attempts, commits := 0, 0
				client := &batchCommitClient{commit: func(context.Context, ...*kgo.Record) error { commits++; return nil }}
				process := func() error {
					attempts++
					if attempts == 1 {
						return status.Error(code, "downstream private-payload")
					}
					return nil
				}
				config := &PartitionRunnerConfig{Name: "diagnostic-contract", Tracer: provider.Tracer("diagnostic-contract"), RetryBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
				runner := NewPartitionRunner(client, func(context.Context, *kgo.Record) error { return process() }, config, nil)
				if batch {
					runner = NewPartitionBatchRunner(client, func(context.Context, []*kgo.Record) error { return process() }, config, nil)
				}
				runner.SetLogger(zap.New(core))
				lane := &partitionLane{key: partitionKey{topic: "workflow", partition: 2}}
				runner.lanes[lane.key] = lane
				record := &kgo.Record{Topic: "workflow", Partition: 2, Offset: 17, Value: []byte(`{"Action":"private-payload"}`)}
				var err error
				if batch {
					err = runner.processAndCommitBatch(t.Context(), lane, []*kgo.Record{record})
				} else {
					err = runner.processAndCommit(t.Context(), lane, record)
				}
				require.NoError(t, err)
				require.Equal(t, 1, commits)
				wantAttempts, wantPauses := 2, 1
				if code == codes.InvalidArgument {
					wantAttempts, wantPauses = 1, 0
				}
				require.Equal(t, wantAttempts, attempts)
				require.Equal(t, wantPauses, client.pauses)
				require.Equal(t, wantPauses, client.resumes)
				spans := recorder.Ended()
				require.Len(t, spans, 1)
				require.Len(t, spans[0].Events(), 1)
				diagnostic := fmt.Sprint(spans[0].Attributes(), spans[0].Events(), spans[0].Status())
				require.NotContains(t, diagnostic, "private-payload")
				require.Contains(t, diagnostic, code.String())
				require.Len(t, entries.All(), wantPauses)
				for _, entry := range entries.All() {
					fields := entry.ContextMap()
					require.NotContains(t, fmt.Sprint(fields), "private-payload")
					require.Equal(t, code.String(), fields["error_code"])
					require.Equal(t, "workflow", fields["topic"])
					require.EqualValues(t, 2, fields["partition"])
				}
			})
		}
	}
}
