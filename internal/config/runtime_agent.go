package config

import (
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// RuntimeAgent holds the runtime agent configuration.
type RuntimeAgent struct {
	Environment

	Postgres
	DockerProxy
	RuntimeAgentConfig
}

// RuntimeAgentConfig holds runtime node registration configuration.
type RuntimeAgentConfig struct {
	ID                string        `envconfig:"RUNTIME_AGENT_ID" default:""`
	NodeName          string        `envconfig:"RUNTIME_AGENT_NODE_NAME" default:""`
	DockerEndpoint    string        `envconfig:"RUNTIME_AGENT_DOCKER_ENDPOINT" default:""`
	HeartbeatInterval time.Duration `envconfig:"RUNTIME_AGENT_HEARTBEAT_INTERVAL" default:"5s"`
	MaxConcurrency    int32         `envconfig:"RUNTIME_AGENT_MAX_CONCURRENCY" default:"4"`
}

// InitRuntimeAgentConfig initializes the runtime agent configuration.
func InitRuntimeAgentConfig() (*RuntimeAgent, error) {
	var cfg RuntimeAgent
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}

	if cfg.RuntimeAgentConfig.NodeName == "" {
		if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
			cfg.RuntimeAgentConfig.NodeName = nodeName
		}
	}
	if cfg.RuntimeAgentConfig.NodeName == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			cfg.RuntimeAgentConfig.NodeName = hostname
		}
	}
	if cfg.RuntimeAgentConfig.ID == "" {
		cfg.RuntimeAgentConfig.ID = cfg.RuntimeAgentConfig.NodeName
	}
	if cfg.RuntimeAgentConfig.DockerEndpoint == "" {
		cfg.RuntimeAgentConfig.DockerEndpoint = os.Getenv("DOCKER_HOST")
	}

	return &cfg, nil
}
