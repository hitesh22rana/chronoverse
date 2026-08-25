//nolint:testpackage // Tests unexported hostConfig isolation wiring (VULN-004a).
package container

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestHostConfigWorkloadIsolation(t *testing.T) {
	w := &DockerWorkflow{workloadNetwork: DefaultWorkloadNetwork, resourceLimits: ResourceLimits{MemoryBytes: 1 << 20, NanoCPUs: 1e9, PidsLimit: 64}}
	hc := w.hostConfig()

	if string(hc.NetworkMode) != DefaultWorkloadNetwork {
		t.Errorf("NetworkMode = %q, want %q", hc.NetworkMode, DefaultWorkloadNetwork)
	}
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" {
		t.Errorf("CapDrop = %v, want [ALL]", hc.CapDrop)
	}
	if len(hc.SecurityOpt) != 1 || hc.SecurityOpt[0] != "no-new-privileges" {
		t.Errorf("SecurityOpt = %v, want [no-new-privileges]", hc.SecurityOpt)
	}
	if !hc.ReadonlyRootfs {
		t.Error("ReadonlyRootfs = false, want true")
	}
	if _, ok := hc.Tmpfs["/tmp"]; !ok {
		t.Errorf("Tmpfs = %v, want /tmp entry", hc.Tmpfs)
	}
	if hc.IpcMode != container.IPCModePrivate {
		t.Errorf("IpcMode = %q, want private", hc.IpcMode)
	}
	if hc.Resources.Memory != 1<<20 || hc.Resources.NanoCPUs != 1e9 || hc.Resources.PidsLimit == nil || *hc.Resources.PidsLimit != 64 {
		t.Errorf("Resources not preserved: %+v", hc.Resources)
	}
	if hc.AutoRemove {
		t.Error("AutoRemove = true, want false")
	}
}

func TestHostConfigDefaultsAndNilReceiver(t *testing.T) {
	// Zero-value workflow still pins the default workload network.
	hc := (&DockerWorkflow{}).hostConfig()
	if string(hc.NetworkMode) != DefaultWorkloadNetwork {
		t.Errorf("NetworkMode = %q, want default %q", hc.NetworkMode, DefaultWorkloadNetwork)
	}

	// Nil receiver must keep producing hardened defaults (legacy guard).
	var nilWorkflow *DockerWorkflow
	hc = nilWorkflow.hostConfig()
	if string(hc.NetworkMode) != DefaultWorkloadNetwork {
		t.Errorf("nil receiver NetworkMode = %q, want %q", hc.NetworkMode, DefaultWorkloadNetwork)
	}
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || !hc.ReadonlyRootfs {
		t.Errorf("nil receiver lost isolation defaults: %+v", hc)
	}
}

func TestWithWorkloadNetworkOption(t *testing.T) {
	w := &DockerWorkflow{workloadNetwork: DefaultWorkloadNetwork}
	WithWorkloadNetwork("custom-net")(w)
	if w.workloadNetwork != "custom-net" {
		t.Errorf("workloadNetwork = %q, want custom-net", w.workloadNetwork)
	}
	// Empty override must not clobber the default.
	WithWorkloadNetwork("")(w)
	if w.workloadNetwork != "custom-net" {
		t.Errorf("empty option clobbered network: %q", w.workloadNetwork)
	}
}
