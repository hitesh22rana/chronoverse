package main

import (
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

// newDockerHealthProvider uses the local health endpoint, not the advertised address.
func newDockerHealthProvider(cfg *config.RuntimeAgent) (*container.DockerWorkflow, error) {
	return container.NewDockerWorkflow(
		container.WithDockerHost(cfg.RuntimeAgentConfig.DockerHealthEndpoint),
		container.WithDockerProxyTLS(container.DockerProxyTLSConfig{
			CAFile: cfg.DockerProxy.TLS.CAFile, CertFile: cfg.DockerProxy.TLS.CertFile,
			KeyFile: cfg.DockerProxy.TLS.KeyFile, ServerName: cfg.DockerProxy.TLS.ServerName,
		}),
		container.WithDockerProxyToken(cfg.DockerProxy.Token),
	)
}
