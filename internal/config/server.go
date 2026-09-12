package config

import (
	"errors"
	"net/url"
	"strings"
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
	CSRFHMACSecret    string        `envconfig:"SERVER_CSRF_HMAC_SECRET" required:"true"`
	HostURL           string        `envconfig:"SERVER_HOST_URL" default:"http://localhost:8080"`
	AllowedOrigins    []string      `envconfig:"SERVER_ALLOWED_ORIGINS" default:"http://localhost:3001,"`
	SameSiteMode      string        `envconfig:"SERVER_SAME_SITE_MODE" default:"STRICT"`
	CookieDomain      string        `envconfig:"SERVER_COOKIE_DOMAIN" default:""`
}

// InitServerConfig initializes the server configuration.
func InitServerConfig() (*ServerConfig, error) {
	var cfg ServerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	if err := validateServerSecrets(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Secret length requirements for server authentication.
const (
	// cryptoSecretLength is the exact required CRYPTO_SECRET length in bytes.
	cryptoSecretLength = 32
	// minCSRFHMACSecretLength is the minimum SERVER_CSRF_HMAC_SECRET length in bytes.
	minCSRFHMACSecretLength = 32
)

func validateServerSecrets(cfg *ServerConfig) error {
	if cfg.Secret == "" {
		return errors.New("CRYPTO_SECRET must not be empty")
	}
	if cfg.CSRFHMACSecret == "" {
		return errors.New("SERVER_CSRF_HMAC_SECRET must not be empty")
	}
	if cfg.Secret == cfg.CSRFHMACSecret {
		return errors.New("CRYPTO_SECRET and SERVER_CSRF_HMAC_SECRET must be different")
	}
	if len(cfg.Secret) != cryptoSecretLength {
		return errors.New("CRYPTO_SECRET must be 32 bytes long")
	}
	if len(cfg.CSRFHMACSecret) < minCSRFHMACSecretLength {
		return errors.New("SERVER_CSRF_HMAC_SECRET must be at least 32 bytes long")
	}
	return validateCookieDomain(cfg.HostURL, cfg.CookieDomain)
}

// validateCookieDomain keeps cookies host-only by default. An explicit domain
// must match or parent the public host, else browsers drop the cookies.
func validateCookieDomain(hostURL, domain string) error {
	if domain == "" {
		return nil
	}
	u, err := url.Parse(hostURL)
	if err != nil {
		return errors.New("SERVER_HOST_URL must be a valid URL to use SERVER_COOKIE_DOMAIN")
	}
	if host := u.Hostname(); host != domain && !strings.HasSuffix(host, "."+domain) {
		return errors.New("SERVER_COOKIE_DOMAIN must match or parent the SERVER_HOST_URL host")
	}
	return nil
}
