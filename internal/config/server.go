package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// ServerConfig holds the configuration for the server.
type ServerConfig struct {
	Environment

	Crypto
	ClientTLS
	Redis
	UsersService
	WorkflowsService
	JobsService
	NotificationsService
	AnalyticsService
	Server
}

// Server holds the configuration for the server.
type Server struct {
	Host              string        `envconfig:"SERVER_HOST" default:"localhost"`
	Port              int           `envconfig:"SERVER_PORT" default:"8080"`
	RequestTimeout    time.Duration `envconfig:"SERVER_REQUEST_TIMEOUT" default:"5s"`
	ReadTimeout       time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"2s"`
	ReadHeaderTimeout time.Duration `envconfig:"SERVER_READ_HEADER_TIMEOUT" default:"1s"`
	IdleTimeout       time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"30s"`
	RequestBodyLimit  int64         `envconfig:"SERVER_REQUEST_BODY_LIMIT" default:"4194304"`
	SessionExpiry     time.Duration `envconfig:"SERVER_SESSION_EXPIRY" default:"2h"`
	CSRFExpiry        time.Duration `envconfig:"SERVER_CSRF_EXPIRY" default:"2h"`
	CSRFHMACSecret    string        `envconfig:"SERVER_CSRF_HMAC_SECRET" default:"a&1*~#^2^#!@#$%^&*()-_=+{}[]|<>?"`
	HostURL           string        `envconfig:"SERVER_HOST_URL" default:"http://localhost:8080"`
	AllowedOrigins    []string      `envconfig:"SERVER_ALLOWED_ORIGINS" default:"http://localhost:3001,"`
	SameSiteMode      string        `envconfig:"SERVER_SAME_SITE_MODE" default:"STRICT"`
}

// InitServerConfig initializes the server configuration.
func InitServerConfig() (*ServerConfig, error) {
	var cfg ServerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
