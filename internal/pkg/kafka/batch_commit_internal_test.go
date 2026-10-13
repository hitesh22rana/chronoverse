package kafka

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type batchCommitClient struct {
	commit          func(context.Context, ...*kgo.Record) error
	pauses, resumes int
}

func (*batchCommitClient) PollFetches(context.Context) kgo.Fetches { return nil }

func (c *batchCommitClient) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	return c.commit(ctx, records...)
}

func (c *batchCommitClient) PauseFetchPartitions(parts map[string][]int32) map[string][]int32 {
	c.pauses++
	return parts
}

func (c *batchCommitClient) ResumeFetchPartitions(map[string][]int32) { c.resumes++ }

//nolint:gocyclo // The table checks processing, acknowledgement and cancellation boundaries.
func TestBatchProcessingCommitBoundaries(t *testing.T) {
	cases := []struct {
		name                                                            string
		processErrors                                                   []error
		commitFailure, cancelBefore, cancelProcess, cancelCommit, stale bool
		wantAttempts, wantCommits, wantPauses                           int
		wantCode                                                        codes.Code
	}{
		{
			name:          "retry full batch before acknowledging",
			processErrors: []error{status.Error(codes.Unavailable, "retry"), status.Error(codes.Aborted, "retry"), nil},
			wantAttempts:  3, wantCommits: 1, wantPauses: 1,
		},
		{name: "nonretryable batch rejected once", processErrors: []error{status.Error(codes.InvalidArgument, "invalid")}, wantAttempts: 1, wantCommits: 1},
		{name: "commit retry never reprocesses", commitFailure: true, wantAttempts: 1, wantCommits: 2, wantPauses: 1},
		{name: "canceled before processing", cancelBefore: true, wantCode: codes.Canceled},
		{name: "canceled retry leaves entire batch uncommitted", cancelProcess: true, wantAttempts: 1, wantPauses: 1, wantCode: codes.Canceled},
		{name: "canceled failed commit", cancelCommit: true, wantAttempts: 1, wantCommits: 1, wantPauses: 1, wantCode: codes.Canceled},
		{name: "revoked lane cannot acknowledge", stale: true, wantAttempts: 1, wantCode: codes.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			records := []*kgo.Record{{Topic: "jobs", Partition: 2, Offset: 10}, {Topic: "jobs", Partition: 2, Offset: 11}}
			attempts, commits := 0, 0
			client := &batchCommitClient{}
			client.commit = func(_ context.Context, got ...*kgo.Record) error {
				commits++
				if !reflect.DeepEqual(got, records) {
					t.Fatalf("committed %v, want complete original batch %v", got, records)
				}
				if tc.cancelCommit {
					cancel()
					return errors.New("commit unavailable")
				}
				if tc.commitFailure && commits == 1 {
					return errors.New("commit unavailable")
				}
				return nil
			}
			runner := NewPartitionBatchRunner(client, func(_ context.Context, got []*kgo.Record) error {
				attempts++
				if !reflect.DeepEqual(got, records) {
					t.Fatalf("processed %v, want same ordered batch %v", got, records)
				}
				if commits != 0 {
					t.Fatal("reprocessed after commit attempt")
				}
				if tc.cancelProcess {
					cancel()
					return status.Error(codes.Unavailable, "retry")
				}
				if attempts <= len(tc.processErrors) {
					return tc.processErrors[attempts-1]
				}
				return nil
			}, &PartitionRunnerConfig{RetryBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}, nil)
			lane := &partitionLane{key: partitionKey{topic: "jobs", partition: 2}}
			if !tc.stale {
				runner.lanes[lane.key] = lane
			}
			if tc.cancelBefore {
				cancel()
			}
			err := runner.processAndCommitBatch(ctx, lane, records)
			gotCode := status.Code(err)
			if errors.Is(err, context.Canceled) {
				gotCode = codes.Canceled
			}
			if gotCode != tc.wantCode {
				t.Fatalf("error=%v, want %v", err, tc.wantCode)
			}
			if attempts != tc.wantAttempts || commits != tc.wantCommits || client.pauses != tc.wantPauses || client.resumes != tc.wantPauses || lane.retryPaused {
				t.Fatalf("attempts=%d commits=%d pauses=%d resumes=%d retryPaused=%v", attempts, commits, client.pauses, client.resumes, lane.retryPaused)
			}
		})
	}
}

func TestEmptyBatchHasNoEffects(t *testing.T) {
	client := &batchCommitClient{commit: func(context.Context, ...*kgo.Record) error { t.Fatal("empty batch committed"); return nil }}
	runner := NewPartitionBatchRunner(client, func(context.Context, []*kgo.Record) error { t.Fatal("empty batch processed"); return nil }, nil, nil)
	if err := runner.processAndCommitBatch(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
}
