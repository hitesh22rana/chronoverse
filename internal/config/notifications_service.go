package config

import "github.com/kelseyhightower/envconfig"

// NotificationsConfig holds the notifications service configuration.
type NotificationsConfig struct {
	Environment

	ClientTLS
	UsersService
	Grpc
	Postgres
	NotificationsServiceConfig
}

// NotificationsServiceConfig holds the configuration for the notifications service.
type NotificationsServiceConfig struct {
	FetchLimit int `envconfig:"NOTIFICATIONS_SERVICE_CONFIG_FETCH_LIMIT" default:"1000"`
}

// InitNotificationsServiceConfig initializes the notifications service configuration.
func InitNotificationsServiceConfig() (*NotificationsConfig, error) {
	var cfg NotificationsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
