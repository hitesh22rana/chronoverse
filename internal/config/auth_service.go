package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// AuthService holds the configuration for the auth-service.
type AuthService struct {
	Configuration

	Redis
	Postgres
	Pat
	AuthServer
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

// Pat holds the Personal Access Token configuration.
type Pat struct {
	DefaultExpiry time.Duration `envconfig:"PAT_DEFAULT_EXPIRY" default:"12h"`
	JWTSecret     string        `envconfig:"PAT_JWT_SECRET" default:"abcdefghijklmnopqrstuvwxyz123456"`
}

// AuthServer holds the authentication server configuration.
type AuthServer struct {
	Port int `envconfig:"AUTH_SERVER_PORT" default:"50051"`
}

// InitAuthServiceConfig initializes the application configuration.
func InitAuthServiceConfig() (*AuthService, error) {
	var cfg AuthService
	err := envconfig.Process(envPrefix, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
