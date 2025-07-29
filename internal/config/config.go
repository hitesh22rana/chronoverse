package config

import "time"

const envPrefix = ""

// Environment holds the environment configuration.
type Environment struct {
	Env string `envconfig:"ENV" default:"development"`
}

// Crypto holds the configuration for the crypto service.
type Crypto struct {
	Secret string `envconfig:"CRYPTO_SECRET" default:"a&1*~#^2^#!@#$%^&*()-_=+{}[]|<>?"`
}

// ClientTLS holds the TLS configuration for the client.
type ClientTLS struct {
	CertFile string `envconfig:"CLIENT_TLS_CERT_FILE" default:""`
	KeyFile  string `envconfig:"CLIENT_TLS_KEY_FILE" default:""`
}

// Redis holds the Redis configuration.
type Redis struct {
	Host                     string        `envconfig:"REDIS_HOST" default:"localhost"`
	Port                     int           `envconfig:"REDIS_PORT" default:"6379"`
	Password                 string        `envconfig:"REDIS_PASSWORD" default:""`
	DB                       int           `envconfig:"REDIS_DB" default:"0"`
	PoolSize                 int           `envconfig:"REDIS_POOL_SIZE" default:"10"`
	MinIdleConns             int           `envconfig:"REDIS_MIN_IDLE_CONNS" default:"5"`
	ReadTimeout              time.Duration `envconfig:"REDIS_READ_TIMEOUT" default:"5s"`
	WriteTimeout             time.Duration `envconfig:"REDIS_WRITE_TIMEOUT" default:"5s"`
	MaxMemory                string        `envconfig:"REDIS_MAX_MEMORY" default:"100mb"`
	EvictionPolicy           string        `envconfig:"REDIS_EVICTION_POLICY" default:"allkeys-lru"`
	EvictionPolicySampleSize int           `envconfig:"REDIS_EVICTION_POLICY_SAMPLE_SIZE" default:"5"`
}

// Postgres holds the PostgreSQL configuration.
type Postgres struct {
	Host        string        `envconfig:"POSTGRES_HOST" default:"localhost"`
	Port        int           `envconfig:"POSTGRES_PORT" default:"5432"`
	User        string        `envconfig:"POSTGRES_USER" default:"postgres"`
	Password    string        `envconfig:"POSTGRES_PASSWORD" default:"postgres"`
	Database    string        `envconfig:"POSTGRES_DB" default:"chronoverse"`
	MaxConns    int32         `envconfig:"POSTGRES_MAX_CONNS" default:"10"`
	MinConns    int32         `envconfig:"POSTGRES_MIN_CONNS" default:"5"`
	MaxConnLife time.Duration `envconfig:"POSTGRES_MAX_CONN_LIFE" default:"1h"`
	MaxConnIdle time.Duration `envconfig:"POSTGRES_MAX_CONN_IDLE" default:"30m"`
	DialTimeout time.Duration `envconfig:"POSTGRES_DIAL_TIMEOUT" default:"5s"`
	SSLMode     string        `envconfig:"POSTGRES_SSL_MODE" default:"disable"`
}

// ClickHouse holds the ClickHouse configuration.
type ClickHouse struct {
	Hosts           []string      `envconfig:"CLICKHOUSE_HOSTS" default:"localhost:9000"`
	Database        string        `envconfig:"CLICKHOUSE_DATABASE" default:"default"`
	Username        string        `envconfig:"CLICKHOUSE_USERNAME" default:"default"`
	Password        string        `envconfig:"CLICKHOUSE_PASSWORD" default:""`
	MaxOpenConns    int           `envconfig:"CLICKHOUSE_MAX_OPEN_CONNS" default:"10"`
	MaxIdleConns    int           `envconfig:"CLICKHOUSE_MAX_IDLE_CONNS" default:"5"`
	ConnMaxLifetime time.Duration `envconfig:"CLICKHOUSE_CONN_MAX_LIFETIME" default:"1h"`
	DialTimeout     time.Duration `envconfig:"CLICKHOUSE_DIAL_TIMEOUT" default:"5s"`
}

// Grpc holds the gRPC configuration.
type Grpc struct {
	Host           string        `envconfig:"GRPC_HOST" default:"localhost"`
	Port           int           `envconfig:"GRPC_PORT" required:"true"`
	RequestTimeout time.Duration `envconfig:"GRPC_REQUEST_TIMEOUT" default:"500ms"`
	TLS            struct {
		Enabled  bool   `envconfig:"GRPC_TLS_ENABLED" default:"false"`
		CAFile   string `envconfig:"GRPC_TLS_CA_FILE" default:""`
		CertFile string `envconfig:"GRPC_TLS_CERT_FILE" default:""`
		KeyFile  string `envconfig:"GRPC_TLS_KEY_FILE" default:""`
	}
}

// Kafka holds the configuration for Kafka.
type Kafka struct {
	Brokers       []string `envconfig:"KAFKA_BROKERS" required:"true"`
	ConsumerGroup string   `envconfig:"KAFKA_CONSUMER_GROUP"`
}

// UsersService holds the configuration for the users service.
type UsersService struct {
	Host string `envconfig:"USERS_SERVICE_HOST" required:"true"`
	Port int    `envconfig:"USERS_SERVICE_PORT" required:"true"`
	TLS  struct {
		Enabled bool   `envconfig:"USERS_SERVICE_TLS_ENABLED" default:"false"`
		CAFile  string `envconfig:"USERS_SERVICE_TLS_CA_FILE" default:""`
	}
}

// WorkflowsService holds the configuration for the workflows service.
type WorkflowsService struct {
	Host string `envconfig:"WORKFLOWS_SERVICE_HOST" required:"true"`
	Port int    `envconfig:"WORKFLOWS_SERVICE_PORT" required:"true"`
	TLS  struct {
		Enabled bool   `envconfig:"WORKFLOWS_SERVICE_TLS_ENABLED" default:"false"`
		CAFile  string `envconfig:"WORKFLOWS_SERVICE_TLS_CA_FILE" default:""`
	}
}

// JobsService holds the configuration for the jobs service.
type JobsService struct {
	Host string `envconfig:"JOBS_SERVICE_HOST" required:"true"`
	Port int    `envconfig:"JOBS_SERVICE_PORT" required:"true"`
	TLS  struct {
		Enabled bool   `envconfig:"JOBS_SERVICE_TLS_ENABLED" default:"false"`
		CAFile  string `envconfig:"JOBS_SERVICE_TLS_CA_FILE" default:""`
	}
}

// NotificationsService holds the configuration for the notifications service.
type NotificationsService struct {
	Host string `envconfig:"NOTIFICATIONS_SERVICE_HOST" required:"true"`
	Port int    `envconfig:"NOTIFICATIONS_SERVICE_PORT" required:"true"`
	TLS  struct {
		Enabled bool   `envconfig:"NOTIFICATIONS_SERVICE_TLS_ENABLED" default:"false"`
		CAFile  string `envconfig:"NOTIFICATIONS_SERVICE_TLS_CA_FILE" default:""`
	}
}

// AnalyticsService holds the configuration for the analytics service.
type AnalyticsService struct {
	Host string `envconfig:"ANALYTICS_SERVICE_HOST" required:"true"`
	Port int    `envconfig:"ANALYTICS_SERVICE_PORT" required:"true"`
	TLS  struct {
		Enabled bool   `envconfig:"ANALYTICS_SERVICE_TLS_ENABLED" default:"false"`
		CAFile  string `envconfig:"ANALYTICS_SERVICE_TLS_CA_FILE" default:""`
	}
}
