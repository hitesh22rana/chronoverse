package kafka

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaConnectionConfigAndConsumerOptions(t *testing.T) {
	for _, cfg := range []*Config{nil, {}} {
		client, err := New(t.Context(), cfg)
		require.Nil(t, client)
		require.ErrorContains(t, err, "missing brokers")
	}
	cfg := &Config{Brokers: []string{"127.0.0.1:1"}, TLSConfig: &TLSConfig{Enabled: true}}
	client, err := New(t.Context(), cfg)
	require.Nil(t, client)
	require.ErrorContains(t, err, "client key pair")
	cert, key := adapterTLSIdentity(t)
	cfg.TLSConfig = &TLSConfig{Enabled: true, CertFile: cert, KeyFile: key, CAFile: cert}
	client, err = New(t.Context(), cfg)
	require.NoError(t, err)
	client.Close()
	cfg.TLSConfig.Enabled = false
	cfg.TLSConfig.CertFile = "missing"
	cfg.ConsumerGroup = "original-group"
	lifecycle := NewPartitionLifecycle()
	client, err = New(t.Context(), cfg, WithConsumerGroup("consumer-group"), WithConsumeTopics("topic"), WithDisableAutoCommit(), WithPartitionLifecycle(lifecycle))
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, "original-group", cfg.ConsumerGroup)
	require.Empty(t, cfg.ConsumeTopics)
	require.False(t, cfg.DisableAutoCommit)
	require.Nil(t, cfg.PartitionLifecycle)
	require.Equal(t, "consumer-group", client.OptValue(kgo.ConsumerGroup))
	require.Equal(t, []string{"topic"}, client.GetConsumeTopics())
	require.Equal(t, true, client.OptValue(kgo.DisableAutoCommit))
	require.NotNil(t, client.OptValue(kgo.OnPartitionsAssigned))
	require.NotNil(t, client.OptValue(kgo.OnPartitionsRevoked))
	require.NotNil(t, client.OptValue(kgo.OnPartitionsLost))
	plain, err := New(t.Context(), &Config{Brokers: []string{"127.0.0.1:1"}})
	require.NoError(t, err)
	plain.Close()
}
