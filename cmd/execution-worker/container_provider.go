package main

import (
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	executorrepo "github.com/hitesh22rana/chronoverse/internal/repository/executor"
)

// newContainerProvider binds runtime identity, endpoint reuse and image-pull policy.
func newContainerProvider(cfg *config.ExecutionWorker, limits container.ResourceLimits, locks imagepull.LockStore) (func(string, string) (executorrepo.ContainerSvc, error), func() error) {
	cache := container.NewEndpointCache(func(endpoint string) (*container.DockerWorkflow, error) {
		return container.NewDockerWorkflow(
			container.WithDockerHost(endpoint), container.WithResourceLimits(limits), container.WithWorkloadNetwork(cfg.WorkloadNetwork), container.WithWorkloadSubnet(cfg.WorkloadSubnet),
			container.WithDockerProxyTLS(container.DockerProxyTLSConfig{
				CAFile: cfg.DockerProxy.TLS.CAFile, CertFile: cfg.DockerProxy.TLS.CertFile,
				KeyFile: cfg.DockerProxy.TLS.KeyFile, ServerName: cfg.DockerProxy.TLS.ServerName,
			}), container.WithDockerProxyToken(cfg.DockerProxy.Token),
		)
	})
	policy := imagepull.Config{TTL: cfg.ImagePullLockTTL, WaitTimeout: cfg.ImagePullLockWaitTimeout, RetryInterval: cfg.ImagePullLockRetryInterval}
	provider := func(runtimeID, endpoint string) (executorrepo.ContainerSvc, error) {
		endpoint = container.NormalizeDockerProxyEndpoint(endpoint, cfg.DockerProxy.TLS.CAFile != "")
		client, err := cache.Get(endpoint)
		if err != nil {
			return nil, err
		}
		scoped := policy
		scoped.LockScope = runtimeID
		return executorrepo.NewImagePullLockedContainerSvc(client, locks, scoped), nil
	}
	return provider, cache.Close
}
