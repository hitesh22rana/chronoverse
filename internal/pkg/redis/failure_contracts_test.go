//nolint:testpackage // Exercises private client initialization and command failures.
package redis

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// redisProtocolPeer models RESP2 handshake and server command errors. It does
// not emulate data operations or pretend to execute Redis Lua scripts.
func redisProtocolPeer(t *testing.T, tlsConfig *tls.Config, reply func([]string) string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
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

func redisHandshakeReply(args []string) string {
	if strings.EqualFold(args[0], "hello") {
		return "-ERR unknown command 'hello'\r\n"
	}
	return "+OK\r\n"
}

func TestRedisConstructorServerRejections(t *testing.T) {
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(noop.NewMeterProvider())
	defer otel.SetMeterProvider(previous)
	for _, tc := range []struct{ name, rejected, message string }{
		{"max memory", "maxmemory", "set max memory"},
		{"eviction policy", "maxmemory-policy", "set eviction policy"},
		{"sample size", "maxmemory-samples", "sample size"},
		{"health", "ping", "connect to Redis"},
		{"healthy", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var commands []string
			var mu sync.Mutex
			address := redisProtocolPeer(t, nil, func(args []string) string {
				mu.Lock()
				defer mu.Unlock()
				command := strings.ToLower(args[0])
				if command == "config" {
					command = strings.ToLower(args[2])
				}
				commands = append(commands, command)
				if command == tc.rejected {
					return "-ERR fixture rejected command\r\n"
				}
				if command == "ping" {
					return "+PONG\r\n"
				}
				return redisHandshakeReply(args)
			})
			store, err := New(t.Context(), redisProtocolConfig(t, address))
			if tc.rejected == "" {
				if err != nil || store == nil {
					t.Fatalf("healthy constructor = %v, %v", store, err)
				}
				if err = store.Close(); err != nil {
					t.Fatal(err)
				}
			} else if store != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "fixture rejected") {
				t.Fatalf("constructor = %v, %v", store, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.rejected == "ping" {
				count := countRedisCommand(commands, "ping")
				if count != MaxHealthCheckRetries {
					t.Fatalf("PING attempts = %d; want %d", count, MaxHealthCheckRetries)
				}
			}
		})
	}
}

func TestRedisConstructorTLSFailureAndSuccess(t *testing.T) {
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(noop.NewMeterProvider())
	defer otel.SetMeterProvider(previous)
	store, err := New(t.Context(), &Config{TLSConfig: &TLSConfig{Enabled: true, CertFile: "missing", KeyFile: "missing", CAFile: "missing"}})
	if store != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client key pair") {
		t.Fatalf("invalid TLS = %v, %v", store, err)
	}
	cert, key := adapterTLSIdentity(t)
	identity, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	address := redisProtocolPeer(t, &tls.Config{Certificates: []tls.Certificate{identity}, MinVersion: tls.VersionTLS12}, func(args []string) string {
		if strings.EqualFold(args[0], "ping") {
			return "+PONG\r\n"
		}
		return redisHandshakeReply(args)
	})
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	store, err = New(t.Context(), &Config{Host: host, Port: port, MaxMemory: "8mb", EvictionPolicy: "volatile-ttl", EvictionPolicySampleSize: 5, TLSConfig: &TLSConfig{
		Enabled: true, CertFile: cert, KeyFile: key, CAFile: cert,
	}})
	if err != nil || store == nil {
		t.Fatalf("TLS constructor = %v, %v", store, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisClosedClientLockAndWriteFailures(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	store := &Store{client: client}
	for name, operation := range map[string]func() error{
		"set": func() error { return store.Set(t.Context(), "key", "value", time.Minute) },
		"acquire": func() error {
			acquired, err := store.AcquireDistributedLock(t.Context(), "key", time.Minute)
			if acquired {
				t.Error("closed client acquired lock")
			}
			return err
		},
		"acquire token": func() error {
			token, acquired, err := store.AcquireDistributedLockWithToken(t.Context(), "key", time.Minute)
			if acquired || token != "" {
				t.Errorf("failed acquire leaked ownership %q %v", token, acquired)
			}
			return err
		},
		"extend token": func() error {
			extended, err := store.ExtendDistributedLockWithToken(t.Context(), "key", "owner", time.Minute)
			if extended {
				t.Error("closed client extended lock")
			}
			return err
		},
		"release":       func() error { return store.ReleaseDistributedLock(t.Context(), "key") },
		"release token": func() error { return store.ReleaseDistributedLockWithToken(t.Context(), "key", "owner") },
		"stats": func() error {
			value, err := store.EvictedKeys(t.Context())
			if value != 0 {
				t.Errorf("failed stats returned %d", value)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "client is closed") {
				t.Fatalf("closed client = %v", err)
			}
		})
	}
	if err := store.Set(context.Background(), "key", make(chan int), time.Minute); status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("unserializable write = %v", err)
	}
}

func TestRedisStatsRejectsMalformedServerData(t *testing.T) {
	for _, info := range []string{"# Stats\r\nevicted_keys:invalid\r\n", "# Stats\r\nkeyspace_hits:12\r\n"} {
		t.Run(info, func(t *testing.T) {
			address := redisProtocolPeer(t, nil, func(args []string) string {
				if strings.EqualFold(args[0], "info") {
					return fmt.Sprintf("$%d\r\n%s\r\n", len(info), info)
				}
				return redisHandshakeReply(args)
			})
			client := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: -1})
			defer client.Close()
			value, err := (&Store{client: client}).EvictedKeys(t.Context())
			if value != 0 || status.Code(err) != codes.Internal {
				t.Fatalf("malformed stats = %d, %v", value, err)
			}
		})
	}
}

func TestRedisLegacyLockProtocolOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want           codes.Code
	}{{"released", ":1\r\n", codes.OK}, {"not owned", ":0\r\n", codes.FailedPrecondition}} {
		t.Run(tc.name, func(t *testing.T) {
			address := redisProtocolPeer(t, nil, func(args []string) string {
				if strings.EqualFold(args[0], "eval") {
					if len(args) != 5 || args[2] != "1" || args[3] != "legacy-lock" || args[4] != "locked" {
						t.Errorf("EVAL arguments = %v", args)
					}
					return tc.response
				}
				return redisHandshakeReply(args)
			})
			client := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: -1})
			defer client.Close()
			if err := (&Store{client: client}).ReleaseDistributedLock(t.Context(), "legacy-lock"); status.Code(err) != tc.want {
				t.Fatalf("release = %v", err)
			}
		})
	}
}

func TestRedisEvictionGaugeCollection(t *testing.T) {
	previous := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	defer otel.SetMeterProvider(previous)
	defer func() {
		if err := provider.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	address := redisProtocolPeer(t, nil, func(args []string) string {
		if strings.EqualFold(args[0], "info") {
			const stats = "evicted_keys:17\r\n"
			return fmt.Sprintf("$%d\r\n%s\r\n", len(stats), stats)
		}
		return redisHandshakeReply(args)
	})
	client := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: -1})
	registerEvictedKeysGauge(&Store{client: client})
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	gauge, found := findEvictionGauge(data)
	if !found || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != 17 {
		t.Fatalf("eviction gauge = %#v, found=%v", gauge, found)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	data = metricdata.ResourceMetrics{}
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatalf("failed scrape escaped best-effort callback: %v", err)
	}
	gauge, found = findEvictionGauge(data)
	if found && len(gauge.DataPoints) != 0 {
		t.Fatalf("failed scrape exported stale observation: %#v", gauge)
	}
}

type rejectingRedisMeter struct{ metric.Meter }

func (rejectingRedisMeter) Int64ObservableUpDownCounter(string, ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	return nil, errors.New("metric provider rejected instrument")
}

type rejectingRedisMeterProvider struct{ metric.MeterProvider }

func (rejectingRedisMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return rejectingRedisMeter{Meter: noop.NewMeterProvider().Meter("test")}
}

func TestRedisConstructorMetricsProviderFailure(t *testing.T) {
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(rejectingRedisMeterProvider{MeterProvider: noop.NewMeterProvider()})
	defer otel.SetMeterProvider(previous)
	address := redisProtocolPeer(t, nil, func(args []string) string {
		if strings.EqualFold(args[0], "ping") {
			return "+PONG\r\n"
		}
		return redisHandshakeReply(args)
	})
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(t.Context(), &Config{Host: host, Port: port, MaxMemory: "8mb", EvictionPolicy: "volatile-ttl", EvictionPolicySampleSize: 5, TLSConfig: &TLSConfig{}})
	if store != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to instrument metrics") || !strings.Contains(err.Error(), "metric provider rejected instrument") {
		t.Fatalf("metrics rejection = %v, %v", store, err)
	}
}

func countRedisCommand(commands []string, expected string) int {
	count := 0
	for _, command := range commands {
		if command == expected {
			count++
		}
	}
	return count
}

func findEvictionGauge(data metricdata.ResourceMetrics) (metricdata.Gauge[int64], bool) {
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "redis.evicted_keys" {
				gauge, ok := measurement.Data.(metricdata.Gauge[int64])
				return gauge, ok
			}
		}
	}
	return metricdata.Gauge[int64]{}, false
}

func redisProtocolConfig(t *testing.T, address string) *Config {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return &Config{Host: host, Port: port, MaxMemory: "8mb", EvictionPolicy: "volatile-ttl", EvictionPolicySampleSize: 5, TLSConfig: &TLSConfig{}}
}
