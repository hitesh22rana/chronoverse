package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Prefix registration is process-wide, so exercise it in an isolated child.
func TestHeartbeatProviderConfiguresEgressPolicy(t *testing.T) {
	const child = "CHRONOVERSE_HEARTBEAT_PROVIDER_CHILD"
	if os.Getenv(child) == "1" {
		for _, prefix := range []string{"not-a-cidr", "10.0.0.0/8", "2001:4860::/24", "2001:4860:64:0:100::/96"} {
			provider, err := newHeartbeatProvider([]string{prefix})
			require.Nil(t, provider)
			require.ErrorContains(t, err, "invalid NAT64 prefix")
			require.ErrorContains(t, err, prefix)
		}
		for _, tc := range []struct {
			name     string
			prefixes []string
			endpoint string
		}{
			{"default IPv4", nil, "http://10.1.2.3/"},
			{"custom Pref64", []string{"2001:4860:64::/96"}, "http://[2001:4860:64::a01:203]/"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				provider, err := newHeartbeatProvider(tc.prefixes)
				require.NoError(t, err)
				require.NotNil(t, provider)
				err = provider.Execute(t.Context(), 200*time.Millisecond, tc.endpoint, 200, nil)
				require.Equal(t, codes.FailedPrecondition, status.Code(err))
				require.Contains(t, err.Error(), "endpoint is not allowed")
			})
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	args := []string{"-test.run=^TestHeartbeatProviderConfiguresEgressPolicy$"}
	if directory := flag.Lookup("test.gocoverdir").Value.String(); directory != "" {
		args = append(args, "-test.gocoverdir="+directory)
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), child+"=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
