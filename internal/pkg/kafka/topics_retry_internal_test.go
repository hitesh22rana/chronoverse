package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type topicRequestFunc func(context.Context, kmsg.Request) (kmsg.Response, error)

func (f topicRequestFunc) Request(ctx context.Context, r kmsg.Request) (kmsg.Response, error) {
	return f(ctx, r)
}

func TestEnsureTopicsRetryContracts(t *testing.T) {
	for _, succeedAt := range []int{1, 3, 0} {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			unavailable := errors.New("admin unavailable")
			start := time.Now()
			requester := topicRequestFunc(func(_ context.Context, r kmsg.Request) (kmsg.Response, error) {
				calls++
				req, ok := r.(*kmsg.CreateTopicsRequest)
				if !ok || req.TimeoutMillis != 10000 || len(req.Topics) != 2 {
					t.Fatalf("admin request = %#v", r)
				}
				for i, name := range []string{"jobs", "logs"} {
					if req.Topics[i].Topic != name || req.Topics[i].NumPartitions != 1 || req.Topics[i].ReplicationFactor != 1 {
						t.Fatalf("topic request = %+v", req.Topics[i])
					}
				}
				if calls == succeedAt {
					return &kmsg.CreateTopicsResponse{}, nil
				}
				return nil, unavailable
			})
			err := ensureTopics(t.Context(), requester, "jobs", "logs")
			wantCalls := succeedAt
			if succeedAt == 0 {
				wantCalls = 30
				if !errors.Is(err, unavailable) {
					t.Fatalf("exhausted retry = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls != wantCalls || time.Since(start) != time.Duration(wantCalls-1)*2*time.Second {
				t.Fatalf("calls=%d elapsed=%s", calls, time.Since(start))
			}
		})
	}
}

func TestEnsureTopicsCancellationInterruptsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		requester := topicRequestFunc(func(context.Context, kmsg.Request) (kmsg.Response, error) {
			calls++
			cancel()
			return nil, errors.New("admin unavailable")
		})
		if err := ensureTopics(ctx, requester, "jobs"); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled retry = %v calls=%d", err, calls)
		}
	})
}

func TestCreateTopicsResponseContracts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response kmsg.Response
		want     string
	}{
		{"wrong response", &kmsg.MetadataResponse{}, "unexpected response type"},
		{"already exists", &kmsg.CreateTopicsResponse{Topics: []kmsg.CreateTopicsResponseTopic{{Topic: "jobs", ErrorCode: kerr.TopicAlreadyExists.Code}}}, ""},
		{"permission denied", &kmsg.CreateTopicsResponse{Topics: []kmsg.CreateTopicsResponseTopic{{Topic: "jobs", ErrorCode: kerr.TopicAuthorizationFailed.Code}}}, "create topic jobs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := createTopicsOnce(t.Context(), topicRequestFunc(func(context.Context, kmsg.Request) (kmsg.Response, error) { return tc.response, nil }), "jobs")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("topic response = %v", err)
			}
		})
	}
}
