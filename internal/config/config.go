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

// Grpc holds the gRPC configuration.
type Grpc struct {
	Host           string        `envconfig:"GRPC_HOST" default:"localhost"`
	Port           int           `envconfig:"GRPC_PORT" required:"true"`
	RequestTimeout time.Duration `envconfig:"GRPC_REQUEST_TIMEOUT" default:"500ms"`
	Secure         bool          `envconfig:"GRPC_SECURE" default:"false"`
	CertFile       string        `envconfig:"GRPC_CERT_FILE" default:""`
}

// Kafka holds the configuration for Kafka.
type Kafka struct {
	Brokers       []string `envconfig:"KAFKA_BROKERS" required:"true"`
	ProducerTopic string   `envconfig:"KAFKA_PRODUCER_TOPIC"`
	ConsumeTopics []string `envconfig:"KAFKA_CONSUME_TOPICS"`
	ConsumerGroup string   `envconfig:"KAFKA_CONSUMER_GROUP"`
}

// UsersService holds the configuration for the users service.
type UsersService struct {
	Host     string `envconfig:"USERS_SERVICE_HOST" required:"true"`
	Port     int    `envconfig:"USERS_SERVICE_PORT" required:"true"`
	Secure   bool   `envconfig:"USERS_SERVICE_SECURE" default:"false"`
	CertFile string `envconfig:"USERS_SERVICE_CERT_FILE" default:""`
}

// WorkflowsService holds the configuration for the workflows service.
type WorkflowsService struct {
	Host     string `envconfig:"WORKFLOWS_SERVICE_HOST" required:"true"`
	Port     int    `envconfig:"WORKFLOWS_SERVICE_PORT" required:"true"`
	Secure   bool   `envconfig:"WORKFLOWS_SERVICE_SECURE" default:"false"`
	CertFile string `envconfig:"WORKFLOWS_SERVICE_CERT_FILE" default:""`
}

// JobsService holds the configuration for the jobs service.
type JobsService struct {
	Host     string `envconfig:"JOBS_SERVICE_HOST" required:"true"`
	Port     int    `envconfig:"JOBS_SERVICE_PORT" required:"true"`
	Secure   bool   `envconfig:"JOBS_SERVICE_SECURE" default:"false"`
	CertFile string `envconfig:"JOBS_SERVICE_CERT_FILE" default:""`
}
