package config

import (
	"github.com/kelseyhightower/envconfig"
)

const envPrefix = ""

type Configuration struct {
	Redis
	Environment
}

type Redis struct {
	Host                     string `envconfig:"REDIS_HOST" default:"localhost"`
	Port                     int    `envconfig:"REDIS_PORT" default:"6379"`
	Password                 string `envconfig:"REDIS_PASSWORD" default:""`
	DB                       int    `envconfig:"REDIS_DB" default:"0"`
	PoolSize                 int    `envconfig:"REDIS_POOL_SIZE" default:"10"`
	MinIdleConns             int    `envconfig:"REDIS_MIN_IDLE_CONNS" default:"5"`
	MaxMemory                string `envconfig:"REDIS_MAX_MEMORY" default:"100mb"`
	EvictionPolicy           string `envconfig:"REDIS_EVICTION_POLICY" default:"all-keys-lru"`
	EvictionPolicySampleSize int    `envconfig:"REDIS_EVICTION_POLICY_SAMPLE_SIZE" default:"5"`
}

type Environment struct {
	Env string `envconfig:"ENV" default:"development"`
}

func Load() (*Configuration, error) {
	var cfg Configuration
	err := envconfig.Process(envPrefix, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
