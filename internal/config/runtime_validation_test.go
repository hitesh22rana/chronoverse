//nolint:testpackage // Checks individual fields after initializer fallback resolution.
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeAgentValidationRequiresResolvedFields(t *testing.T) {
	base := RuntimeAgentConfig{ID: "node-id", NodeName: "node-name", DockerEndpoint: "tcp://advertised:2376", DockerHealthEndpoint: "unix:///var/run/docker.sock"}
	require.NoError(t, base.validate())
	for _, field := range []string{"id", "name", "advertised endpoint", "health endpoint"} {
		t.Run(field, func(t *testing.T) {
			cfg := base
			switch field {
			case "id":
				cfg.ID = ""
			case "name":
				cfg.NodeName = ""
			case "advertised endpoint":
				cfg.DockerEndpoint = ""
			case "health endpoint":
				cfg.DockerHealthEndpoint = ""
			}
			require.ErrorContains(t, cfg.validate(), "runtime agent id, node name, Docker health endpoint, and advertised Docker endpoint are required")
		})
	}
	for _, port := range []int{-1, 0, 65536} {
		cfg := base
		cfg.DockerAdvertiseHost = "node"
		cfg.DockerAdvertisePort = port
		require.ErrorContains(t, cfg.validate(), "between 1 and 65535")
	}
	for _, port := range []int{1, 65535} {
		cfg := base
		cfg.DockerAdvertiseHost = "node"
		cfg.DockerAdvertisePort = port
		require.NoError(t, cfg.validate())
	}
}
