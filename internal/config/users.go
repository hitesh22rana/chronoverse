package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// UsersConfig holds the configuration for the users service.
type UsersConfig struct {
	Configuration

	Postgres
	Grpc
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

// InitUsersServiceConfig initializes the users service configuration.
func InitUsersServiceConfig() (*UsersConfig, error) {
	var cfg UsersConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
