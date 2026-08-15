package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// EnsureTopics creates the given topics with a single partition, retrying until
// the broker is ready to serve admin requests. It is safe to call repeatedly;
// topics that already exist are left untouched.
func EnsureTopics(ctx context.Context, client *kgo.Client, topics ...string) error {
	var lastErr error
	for i := 0; i < 30; i++ {
		lastErr = createTopicsOnce(ctx, client, topics...)
		if lastErr == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return lastErr
}

// createTopicsOnce issues a single CreateTopics request.
func createTopicsOnce(ctx context.Context, client *kgo.Client, topics ...string) error {
	req := &kmsg.CreateTopicsRequest{TimeoutMillis: 10000}
	for _, topic := range topics {
		req.Topics = append(req.Topics, kmsg.CreateTopicsRequestTopic{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		})
	}

	resp, err := client.Request(ctx, req)
	if err != nil {
		return err
	}

	createResp, ok := resp.(*kmsg.CreateTopicsResponse)
	if !ok {
		return fmt.Errorf("unexpected response type %T", resp)
	}

	for _, topicResult := range createResp.Topics {
		topicErr := kerr.ErrorForCode(topicResult.ErrorCode)
		if topicErr == nil || errors.Is(topicErr, kerr.TopicAlreadyExists) {
			continue
		}
		return fmt.Errorf("create topic %s: %w", topicResult.Topic, topicErr)
	}

	return nil
}
