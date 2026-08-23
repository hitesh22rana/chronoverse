package testkit

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"

	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

const (
	kafkaImage = "confluentinc/cp-kafka:7.6.7"
	// kafkaClusterID must be a base64-encoded UUID (exactly 16 decoded bytes,
	// 22 unpadded base64 characters); the Confluent entrypoint refuses to
	// format KRaft storage with anything else and exits with code 1.
	kafkaClusterID = "Y2hyb25vdmVyc2UtdGVzdA" // base64("chronoverse-test")
)

// startKafka starts a single-node Confluent Kafka broker in KRaft mode with a
// plaintext listener on a free host port, creates the standard topics and
// returns the broker seed plus a producer-capable client.
func startKafka(ctx context.Context, s *suite) (string, *kgo.Client, error) {
	hostPort, err := freeHostPort()
	if err != nil {
		return "", nil, fmt.Errorf("reserve kafka host port: %w", err)
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        kafkaImage,
			ExposedPorts: []string{"9092/tcp"},
			Env: map[string]string{
				"KAFKA_KRAFT_MODE":                               "true",
				"KAFKA_NODE_ID":                                  "1",
				"KAFKA_PROCESS_ROLES":                            "controller,broker",
				"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
				"KAFKA_LISTENERS":                                "PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093",
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT",
				"KAFKA_ADVERTISED_LISTENERS":                     fmt.Sprintf("PLAINTEXT://localhost:%s", hostPort),
				"KAFKA_INTER_BROKER_LISTENER_NAME":               "PLAINTEXT",
				"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
				"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
				"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
				"KAFKA_MIN_INSYNC_REPLICAS":                      "1",
				"KAFKA_AUTO_CREATE_TOPICS_ENABLE":                "false",
				"CLUSTER_ID":                                     kafkaClusterID,
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.PortBindings = network.PortMap{
					network.MustParsePort("9092/tcp"): []network.PortBinding{
						{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort},
					},
				}
			},
			WaitingFor: wait.ForAll(
				wait.ForLog("Kafka Server started"),
				wait.ForListeningPort("9092/tcp"),
			).WithDeadline(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return "", nil, fmt.Errorf("start kafka container: %w", err)
	}
	s.containers = append(s.containers, ctr)

	seed := fmt.Sprintf("localhost:%s", hostPort)

	client, err := kafkapkg.New(ctx, kafkapkg.WithBrokers(seed))
	if err != nil {
		return "", nil, fmt.Errorf("create kafka client: %w", err)
	}

	// The container may be up while the controller is still electing itself;
	// retry topic creation until the broker answers.
	if err := kafkapkg.EnsureTopics(ctx, client,
		kafkapkg.TopicWorkflows,
		kafkapkg.TopicJobs,
		kafkapkg.TopicJobLogs,
		kafkapkg.TopicAnalytics,
	); err != nil {
		client.Close()
		return "", nil, fmt.Errorf("create kafka topics: %w", err)
	}

	return seed, client, nil
}

// freeHostPort reserves an ephemeral TCP port on the host. The tiny race between
// releasing it and the container binding it is acceptable for tests.
func freeHostPort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return "", err
	}
	return port, nil
}
