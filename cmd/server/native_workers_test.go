package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func searchStoreEnv(t *testing.T) []string {
	t.Helper()
	uri, key := testkit.MeilisearchEndpoint(t)
	return []string{
		"CLICKHOUSE_HOSTS=" + testkit.ClickHouseAddr(t),
		"CLICKHOUSE_DATABASE=chronoverse", "CLICKHOUSE_USERNAME=chronoverse-client", "CLICKHOUSE_PASSWORD=chronoverse",
		"MEILISEARCH_URI=" + uri, "MEILISEARCH_MASTER_KEY=" + key,
	}
}

// Real stores let startup advance through acquisition and then reject the
// next dependency's invalid certificate without replacing production clients.
func TestIntegrationNativeSearchDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("real store integration")
	}
	dependencies := append(postgresEnv(t), redisEnv(t)...)
	dependencies = append(dependencies, searchStoreEnv(t)...)
	for _, name := range []string{"jobs-service", "joblogs-processor", "workflow-worker"} {
		t.Run(name, func(t *testing.T) {
			bin, cover := build(t, name)
			dir := keys(t, name)
			if name == "joblogs-processor" {
				changes := append(append([]string{}, dependencies...), "POSTGRES_TLS_ENABLED=true", "POSTGRES_TLS_CA_FILE=missing-ca.pem")
				reject(t, bin, dir, cover, "missing-ca.pem", changes...)
			}
			if name != "jobs-service" {
				changes := append(append([]string{}, dependencies...), "CLICKHOUSE_TLS_ENABLED=true", "CLICKHOUSE_TLS_CERT_FILE=missing-client.pem", "CLICKHOUSE_TLS_KEY_FILE=missing-client.key")
				reject(t, bin, dir, cover, "missing-client.pem", changes...)
			}
			for _, dependency := range []string{"MEILISEARCH", "KAFKA"} {
				if dependency == "KAFKA" && name == "jobs-service" {
					continue
				}
				changes := append(append([]string{}, dependencies...), dependency+"_TLS_ENABLED=true", dependency+"_TLS_CERT_FILE=missing-client.pem", dependency+"_TLS_KEY_FILE=missing-client.key")
				reject(t, bin, dir, cover, "missing-client.pem", changes...)
			}
			if name != "joblogs-processor" {
				services := []string{"WORKFLOWS"}
				if name == "workflow-worker" {
					services = append(services, "JOBS", "NOTIFICATIONS")
				}
				for _, service := range services {
					changes := append(append([]string{}, dependencies...), service+"_SERVICE_TLS_ENABLED=true", service+"_SERVICE_TLS_CA_FILE=missing-ca.pem")
					reject(t, bin, dir, cover, "missing-ca.pem", changes...)
				}
			}
			if name == "jobs-service" {
				port := availableCommandPort(t, name, bin, dir, cover, dependencies)
				changes := append(append([]string{}, dependencies...), "GRPC_PORT="+strconv.Itoa(port))
				runNativeLifecycle(t, bin, dir, cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
					waitNativeReady(ctx, t, name, port, done, output)
				})
			}
			assertCoverage(t, cover, name)
		})
	}
}

// Broker-confirmed assignment proves the real worker entered its poll loop;
// SIGTERM must unwind the consumer and all previously acquired resources.
func TestIntegrationNativeKafkaWorkerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Kafka worker integration")
	}
	dependencies := append(postgresEnv(t), redisEnv(t)...)
	dependencies = append(dependencies, searchStoreEnv(t)...)
	dependencies = append(dependencies, "KAFKA_BROKERS="+testkit.KafkaSeed(t))
	for _, worker := range []struct{ name, topic string }{
		{"joblogs-processor", kafka.TopicJobLogs},
		{"workflow-worker", kafka.TopicWorkflows},
	} {
		t.Run(worker.name, func(t *testing.T) {
			bin, cover := build(t, worker.name)
			group := "native-" + worker.name + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			changes := append(append([]string{}, dependencies...), "KAFKA_CONSUMER_GROUP="+group)
			runNativeLifecycle(t, bin, keys(t, worker.name), cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
				waitReady(ctx, t, done, output, func() bool { return assignedNativeWorker(ctx, t, group, worker.topic) })
			})
			assertCoverage(t, cover, worker.name)
		})
	}
}

func assignedNativeWorker(ctx context.Context, t *testing.T, group, topic string) bool {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request := kmsg.NewPtrDescribeGroupsRequest()
	request.Groups = []string{group}
	response, err := request.RequestWith(callCtx, testkit.Kafka(t))
	if err != nil || len(response.Groups) != 1 {
		return false
	}
	state := response.Groups[0]
	if state.ErrorCode != 0 || state.State != "Stable" || len(state.Members) != 1 {
		return false
	}
	var assignment kmsg.ConsumerMemberAssignment
	if decodeErr := assignment.ReadFrom(state.Members[0].MemberAssignment); decodeErr != nil {
		return false
	}
	for _, assigned := range assignment.Topics {
		if assigned.Topic == topic && len(assigned.Partitions) > 0 {
			return true
		}
	}
	return false
}

func runNativeLifecycle(t *testing.T, bin, dir, cover string, changes []string, ready func(context.Context, <-chan error, *outputBuffer)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = environment(cover, changes...)
	var output outputBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(exited)
	}()
	defer func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("cleanup native command: %v", err)
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("native command was not reaped after cleanup")
		}
	}()
	ready(ctx, done, &output)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v\n%s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatalf("shutdown timed out\n%s", output.String())
	}
}
