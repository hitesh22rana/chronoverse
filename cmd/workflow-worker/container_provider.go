package main

import (
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
)

// newContainerProvider binds runtime identity, endpoint reuse and image-pull policy.
func newContainerProvider(cfg *config.WorkflowWorker, locks imagepull.LockStore) (provider func(string, string) (workflowrepo.ContainerSvc, error), closeClients func() error) {
	cache := container.NewEndpointCache(func(endpoint string) (*container.DockerWorkflow, error) {
		return container.NewDockerWorkflow(
			container.WithDockerHost(endpoint),
			container.WithDockerProxyTLS(container.DockerProxyTLSConfig{
				CAFile: cfg.DockerProxy.TLS.CAFile, CertFile: cfg.DockerProxy.TLS.CertFile,
				KeyFile: cfg.DockerProxy.TLS.KeyFile, ServerName: cfg.DockerProxy.TLS.ServerName,
			}), container.WithDockerProxyToken(cfg.DockerProxy.Token),
		)
	})
	policy := imagepull.Config{TTL: cfg.ImagePullLockTTL, WaitTimeout: cfg.ImagePullLockWaitTimeout, RetryInterval: cfg.ImagePullLockRetryInterval}
	provider = func(runtimeID, endpoint string) (workflowrepo.ContainerSvc, error) {
		client, err := cache.Get(endpoint)
		if err != nil {
			return nil, err
		}
		scoped := policy
		scoped.LockScope = runtimeID
		return workflowrepo.NewImagePullLockedContainerSvc(client, locks, scoped), nil
	}
	return provider, cache.Close
}
