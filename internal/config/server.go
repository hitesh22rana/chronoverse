package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// ServerConfig holds the configuration for the server.
type ServerConfig struct {
	Configuration
	Auth

	Server
}

// Server holds the configuration for the server.
type Server struct {
	Host              string        `envconfig:"SERVER_HOST" default:"localhost"`
	Port              int           `envconfig:"SERVER_PORT" default:"8080"`
	RequestTimeout    time.Duration `envconfig:"SERVER_REQUEST_TIMEOUT" default:"5s"`
	ReadTimeout       time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"2s"`
	ReadHeaderTimeout time.Duration `envconfig:"SERVER_READ_HEADER_TIMEOUT" default:"1s"`
	WriteTimeout      time.Duration `envconfig:"SERVER_WRITE_TIMEOUT" default:"5s"`
	IdleTimeout       time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"30s"`
	RequestBodyLimit  int64         `envconfig:"SERVER_REQUEST_BODY_LIMIT" default:"4194304"`
}

// InitServerConfig initializes the server configuration.
func InitServerConfig() (*ServerConfig, error) {
	var cfg ServerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
