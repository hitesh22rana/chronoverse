//nolint:testpackage // Verifies fixture ownership with the real adapters and services.
package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestIntegrationSuiteOwnsClientsAndFindsMatchingRecords(t *testing.T) {
	RequireDocker(t)
	s := newContractSuite()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.terminate(ctx)
	})
	for _, option := range []Option{WithPostgres(), WithClickHouse(), WithRedis(), WithMeilisearch(), WithKafka()} {
		option(s)
	}
	for _, svc := range []service{servicePostgres, serviceClickHouse, serviceRedis, serviceMeilisearch, serviceKafka} {
		s.start(t, svc)
		require.True(t, s.started[svc])
		require.NoError(t, s.startErrs[svc])
	}
	user := SeedUser(t.Context(), t, s.pg, "suite-owner@chronoverse.test")
	workflow := SeedWorkflow(t.Context(), t, s.pg, user, "Fixture ownership")
	require.NotEmpty(t, user)
	require.NotEmpty(t, workflow)
	var owner string
	require.NoError(t, s.pg.QueryRow(t.Context(), "SELECT user_id FROM workflows WHERE id=$1", workflow).Scan(&owner))
	require.Equal(t, user, owner)
	require.NoError(t, s.rdb.Set(t.Context(), "fixture", "ready", time.Minute))
	require.True(t, s.ms.IsHealthy())

	consumer, err := kgo.NewClient(kgo.SeedBrokers(s.kafkaSeed), kgo.ConsumeTopics(kafkapkg.TopicJobs), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	t.Cleanup(consumer.Close)
	records := []*kgo.Record{
		{Topic: kafkapkg.TopicJobs, Key: []byte("fixture"), Value: []byte("ignore")},
		{Topic: kafkapkg.TopicJobs, Key: []byte("fixture"), Value: []byte("match")},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	require.NoError(t, s.kfk.ProduceSync(ctx, records...).FirstErr())
	seen := []string{}
	found := WaitForRecord(t, consumer, 30*time.Second, func(record *kgo.Record) bool {
		seen = append(seen, string(record.Value))
		return string(record.Value) == "match"
	})
	require.Equal(t, []byte("match"), found.Value)
	require.Equal(t, []string{"ignore", "match"}, seen)
	pg, redis, kafka := s.pg, s.rdb, s.kfk
	consumer.Close()
	s.terminate(t.Context())
	require.Nil(t, s.pg)
	require.Nil(t, s.ch)
	require.Nil(t, s.rdb)
	require.Nil(t, s.ms)
	require.Nil(t, s.kfk)
	require.Empty(t, s.containers)
	require.ErrorContains(t, pg.QueryRow(t.Context(), "SELECT 1").Scan(new(int)), "closed")
	_, err = redis.Get(t.Context(), "fixture", nil)
	require.ErrorContains(t, err, "closed")
	require.ErrorIs(t, kafka.Context().Err(), context.Canceled)
	s.terminate(t.Context())
}
