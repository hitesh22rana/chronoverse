//nolint:testpackage // Probe needs the unexported network-ensure and host-config helpers.
package container

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// TestIntegrationWorkloadEgressDropsHostListener proves the host firewall
// denies workload→infrastructure traffic: a listener on the bridge gateway
// must be unreachable from inside a workload container (nc exits non-zero on
// the DROP timeout; exit zero means the packet arrived and the firewall is
// missing). Requires the firewall applied on the test host (Linux):
// CHRONOVERSE_WORKLOAD_FIREWALL=1. CHRONOVERSE_WORKLOAD_SUBNET overrides the
// default subnet and must match the host rules.
func TestIntegrationWorkloadEgressDropsHostListener(t *testing.T) {
	if os.Getenv("CHRONOVERSE_WORKLOAD_FIREWALL") == "" {
		t.Skip("requires host workload firewall (CHRONOVERSE_WORKLOAD_FIREWALL=1)")
	}
	if testing.Short() {
		t.Skip("requires a Docker daemon")
	}

	subnet := os.Getenv("CHRONOVERSE_WORKLOAD_SUBNET")
	if subnet == "" {
		subnet = DefaultWorkloadSubnet
	}

	ctx := t.Context()
	w, err := NewDockerWorkflow(WithWorkloadSubnet(subnet))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.Close()
	})
	if err = w.healthCheck(ctx); err != nil {
		t.Skipf("no Docker daemon: %v", err)
	}
	require.NoError(t, w.ensureWorkloadNetwork(ctx))
	require.NoError(t, w.Build(ctx, "alpine:3.22.2"))

	gateway := firstSubnetIP(t, subnet)
	ln, err := net.Listen("tcp", net.JoinHostPort(gateway, "0"))
	if err != nil {
		t.Skipf("no bridge gateway %s on this host: %v", gateway, err)
	}
	defer func() {
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address is not TCP")
	port := tcpAddr.Port

	resp, err := w.Client.ContainerCreate(ctx,
		&container.Config{
			Image: "alpine:3.22.2",
			Cmd:   []string{"sh", "-c", fmt.Sprintf("echo probe | nc -w 5 %s %d", gateway, port)},
		},
		w.hostConfig(),
		nil, nil, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // Best-effort probe cleanup on a detached context.
		_ = w.Client.ContainerRemove(context.WithoutCancel(ctx), resp.ID, container.RemoveOptions{Force: true})
	})
	require.NoError(t, w.Client.ContainerStart(ctx, resp.ID, container.StartOptions{}))

	statusCh, errCh := w.Client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case result := <-statusCh:
		if result.StatusCode == 0 {
			t.Fatalf("workload container reached gateway listener %s:%d — firewall absent", gateway, port)
		}
	case err := <-errCh:
		t.Fatalf("ContainerWait() error = %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("probe container did not exit in 30s")
	}
}

// firstSubnetIP returns the bridge gateway address (first host address).
func firstSubnetIP(t *testing.T, cidr string) string {
	t.Helper()

	ip, _, err := net.ParseCIDR(cidr)
	require.NoError(t, err)
	ip4 := ip.To4()
	require.NotNil(t, ip4, "workload subnet must be IPv4")
	ip4[3]++
	return ip4.String()
}
