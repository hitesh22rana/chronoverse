//nolint:testpackage // Checks publication retry contracts at the broker boundary.
package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type retryLogProducer struct {
	t           *testing.T
	failures    int
	attempts    [][]*kgo.Record
	accepted    []*kgo.Record
	wantRecords []*kgo.Record
}

func (p *retryLogProducer) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.t.Helper()
	if p.wantRecords != nil && !reflect.DeepEqual(records, p.wantRecords) {
		p.t.Fatalf("broker attempt changed record contents: got %+v, want %+v", records, p.wantRecords)
	}
	if ctx.Err() != nil {
		p.t.Errorf("broker attempt started canceled: %v", ctx.Err())
	}
	if _, ok := ctx.Deadline(); !ok {
		p.t.Error("broker attempt has no deadline")
	}
	p.attempts = append(p.attempts, append([]*kgo.Record(nil), records...))
	var err error
	if len(p.attempts) <= p.failures {
		err = errors.New("broker temporarily unavailable")
	} else {
		p.accepted = append(p.accepted, records...)
	}
	results := make(kgo.ProduceResults, len(records))
	for i, record := range records {
		results[i] = kgo.ProduceResult{Record: record, Err: err}
	}
	return results
}

func TestJobLogBatchRetriesPreserveRecordsAndBoundAttempts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                 string
		failures, wantAttempts, wantAccepted int
	}{{"recovery", 1, 2, 2}, {"exhausted", 3, 3, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			producer := &retryLogProducer{t: t, failures: tc.failures, wantRecords: []*kgo.Record{
				{Topic: "logs", Key: []byte("job"), Value: []byte("first")},
				{Topic: "logs", Key: []byte("job"), Value: []byte("second")},
			}}
			cfg := defaultConfig()
			cfg.JobLogPublishRetries = 3
			cfg.JobLogPublishBackoff = 0
			repo := &Repository{cfg: cfg, kfk: producer}
			records := []*kgo.Record{{Topic: "logs", Key: []byte("job"), Value: []byte("first")}, {Topic: "logs", Key: []byte("job"), Value: []byte("second")}}
			parent, cancel := context.WithCancel(t.Context())
			cancel()
			repo.publishJobLogBatch(parent, records)
			if len(producer.attempts) != tc.wantAttempts || len(producer.accepted) != tc.wantAccepted {
				t.Fatalf("attempts/accepted=%d/%d, want %d/%d", len(producer.attempts), len(producer.accepted), tc.wantAttempts, tc.wantAccepted)
			}
			for _, attempt := range producer.attempts {
				if len(attempt) != len(records) {
					t.Fatalf("retry batch length=%d", len(attempt))
				}
				for i, record := range attempt {
					if record != records[i] {
						t.Fatalf("retry replaced record %d", i)
					}
				}
			}
		})
	}
}

func TestExecutorConstructorKeepsMissingProducerNil(t *testing.T) {
	t.Parallel()
	repo, err := New(nil, fakeAuth{}, nil, nil, nil, &Services{})
	if err != nil {
		t.Fatal(err)
	}
	if repo.kfk != nil {
		t.Fatal("missing Kafka client became typed nil producer")
	}
	repo.publishJobLogBatch(t.Context(), []*kgo.Record{{Topic: "logs", Key: []byte("job"), Value: []byte("line")}})
	client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	repo, err = New(nil, fakeAuth{}, client, nil, nil, &Services{})
	if err != nil {
		t.Fatal(err)
	}
	if repo.kfk != client {
		t.Fatal("constructor replaced configured Kafka producer")
	}
}

func TestJobLogBatchDeadlineStopsRetryBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		producer := &retryLogProducer{t: t, failures: 10}
		cfg := defaultConfig()
		cfg.JobLogPublishTimeout = time.Millisecond
		cfg.JobLogPublishBackoff = time.Hour
		repo := &Repository{cfg: cfg, kfk: producer}
		repo.publishJobLogBatch(t.Context(), []*kgo.Record{{Topic: "logs", Value: []byte("line")}})
		if len(producer.attempts) != 1 || len(producer.accepted) != 0 {
			t.Fatalf("deadline attempts/accepted=%d/%d", len(producer.attempts), len(producer.accepted))
		}
	})
}
