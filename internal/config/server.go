package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// ServerConfig holds the configuration for the server.
type ServerConfig struct {
	Configuration

	Crypto
	Redis
	UsersService
	Server
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

// UsersService holds the configuration for the users service.
type UsersService struct {
	Host     string `envconfig:"USERS_SERVICE_HOST" default:"localhost"`
	Port     int    `envconfig:"USERS_SERVICE_PORT" default:"50051"`
	Secure   bool   `envconfig:"USERS_SERVICE_SECURE" default:"false"`
	CertFile string `envconfig:"USERS_SERVICE_CERT_FILE" default:""`
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
	SessionExpiry     time.Duration `envconfig:"SERVER_SESSION_EXPIRY" default:"2h"`
	CSRFExpiry        time.Duration `envconfig:"SERVER_CSRF_EXPIRY" default:"2h"`
	CSRFHMACSecret    string        `envconfig:"SERVER_CSRF_HMAC_SECRET" default:"a&1*~#^2^#!@#$%^&*()-_=+{}[]|<>?"`
}

// InitServerConfig initializes the server configuration.
func InitServerConfig() (*ServerConfig, error) {
	var cfg ServerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
