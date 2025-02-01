package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

const envPrefix = ""

type Configuration struct {
	Environment
	Redis
	Postgres
	Pat
	AuthServer
}

type Environment struct {
	Env string `envconfig:"ENV" default:"development"`
}
type Redis struct {
	Host                     string        `envconfig:"REDIS_HOST" default:"localhost"`
	Port                     int           `envconfig:"REDIS_PORT" default:"6379"`
	Password                 string        `envconfig:"REDIS_PASSWORD" default:"chronoverse"`
	DB                       int           `envconfig:"REDIS_DB" default:"0"`
	PoolSize                 int           `envconfig:"REDIS_POOL_SIZE" default:"10"`
	MinIdleConns             int           `envconfig:"REDIS_MIN_IDLE_CONNS" default:"5"`
	ReadTimeout              time.Duration `envconfig:"REDIS_READ_TIMEOUT" default:"5s"`
	WriteTimeout             time.Duration `envconfig:"REDIS_WRITE_TIMEOUT" default:"5s"`
	MaxMemory                string        `envconfig:"REDIS_MAX_MEMORY" default:"100mb"`
	EvictionPolicy           string        `envconfig:"REDIS_EVICTION_POLICY" default:"allkeys-lru"`
	EvictionPolicySampleSize int           `envconfig:"REDIS_EVICTION_POLICY_SAMPLE_SIZE" default:"5"`
}

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

type Pat struct {
	DefaultExpiry time.Duration `envconfig:"PAT_DEFAULT_EXPIRY" default:"24h"`
}

type AuthServer struct {
	Port int `envconfig:"AUTH_SERVER_PORT" default:"50051"`
}

func Load() (*Configuration, error) {
	var cfg Configuration
	err := envconfig.Process(envPrefix, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
