package config_test

import (
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestInitRuntimeAgentConfigSeparatesHealthAndAdvertisedEndpoints(t *testing.T) {
	t.Setenv("NODE_NAME", "runtime-a")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2376")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_HOST", "10.0.0.8")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_PORT", "2376")

	cfg, err := config.InitRuntimeAgentConfig()
	if err != nil {
		t.Fatalf("InitRuntimeAgentConfig() error = %v", err)
	}
	if got := cfg.RuntimeAgentConfig.DockerHealthEndpoint; got != "tcp://127.0.0.1:2376" {
		t.Fatalf("DockerHealthEndpoint = %q, want loopback endpoint", got)
	}
	if got := cfg.RuntimeAgentConfig.DockerEndpoint; got != "tcp://10.0.0.8:2376" {
		t.Fatalf("DockerEndpoint = %q, want node endpoint", got)
	}
}

func TestInitRuntimeAgentConfigFormatsIPv6AdvertisedEndpoint(t *testing.T) {
	t.Setenv("NODE_NAME", "runtime-v6")
	t.Setenv("DOCKER_HOST", "tcp://[::1]:2376")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_HOST", "2001:db8::8")

	cfg, err := config.InitRuntimeAgentConfig()
	if err != nil {
		t.Fatalf("InitRuntimeAgentConfig() error = %v", err)
	}
	if got := cfg.RuntimeAgentConfig.DockerEndpoint; got != "tcp://[2001:db8::8]:2376" {
		t.Fatalf("DockerEndpoint = %q, want bracketed IPv6 endpoint", got)
	}
}

func TestInitRuntimeAgentConfigFallsBackToAdvertisedEndpointForHealth(t *testing.T) {
	t.Setenv("NODE_NAME", "runtime-a")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("RUNTIME_AGENT_DOCKER_ENDPOINT", "tcp://docker-proxy:2376")

	cfg, err := config.InitRuntimeAgentConfig()
	if err != nil {
		t.Fatalf("InitRuntimeAgentConfig() error = %v", err)
	}
	if got := cfg.RuntimeAgentConfig.DockerHealthEndpoint; got != "tcp://docker-proxy:2376" {
		t.Fatalf("DockerHealthEndpoint = %q, want advertised endpoint fallback", got)
	}
}

func TestInitRuntimeAgentConfigRejectsInvalidAdvertisePort(t *testing.T) {
	t.Setenv("NODE_NAME", "runtime-a")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_HOST", "10.0.0.8")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_PORT", "70000")

	if _, err := config.InitRuntimeAgentConfig(); err == nil {
		t.Fatal("InitRuntimeAgentConfig() error = nil, want invalid port error")
	}
}
