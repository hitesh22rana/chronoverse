package container_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubFirewallBins fakes apk/iptables/ip6tables on PATH; with withV6Chain
// false every DOCKER-USER op fails, reproducing an IPv4-only Docker host.
func stubFirewallBins(t *testing.T, withV6Chain bool) (logFile, readyFile string) {
	t.Helper()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state")
	logFile = filepath.Join(dir, "log")
	readyFile = filepath.Join(dir, "ready")

	writeStub := func(name, body string) {
		//nolint:gosec // Test-only PATH stubs must be executable; temp dir, no secrets.
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeStub("apk", "exit 0")
	emulator := `echo "$*" >> "$FAKE_LOG"
op="$1"; shift
case "$op" in
-n) exit "$FAKE_L_MISSING" ;;
-N) exit 0 ;;
-C) grep -qxF "$*" "$FAKE_STATE" 2>/dev/null ;;
-A|-I) echo "$*" >> "$FAKE_STATE"; exit 0 ;;
-D) grep -qxF "$*" "$FAKE_STATE" 2>/dev/null && exit 0; exit 1 ;;
*) exit 0 ;;
esac
`
	writeStub("iptables", "FAKE_L_MISSING=1\n"+emulator)
	if withV6Chain {
		writeStub("ip6tables", "FAKE_L_MISSING=0\n"+emulator)
	} else {
		writeStub("ip6tables", `echo "$*" >> "$FAKE_LOG"
case "$*" in
*DOCKER-USER*) exit 1 ;;
esac
FAKE_L_MISSING=1
`+emulator)
	}

	t.Setenv("FAKE_STATE", stateFile)
	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile, readyFile
}

func runFirewallScript(t *testing.T, readyFile string) error {
	t.Helper()

	script, err := filepath.Abs("../../../../compose/firewall/workload-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(),
		"WORKLOAD_SUBNET=198.18.247.0/24",
		"WORKLOAD_BRIDGE_IF=chronoverse-br",
		"CLUSTER_CIDRS=",
		"READY_FILE="+readyFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("script output: %s", out)
		return err
	}
	return nil
}

// IPv4-only hosts lack the ip6tables DOCKER-USER chain: v6 must be skipped
// while v4 still links and the ready marker is touched.
func TestFirewallScriptSkipsMissingV6Chain(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	logFile, readyFile := stubFirewallBins(t, false)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("script on IPv4-only host error = %v", err)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("ready marker not touched: %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-I INPUT", "-I DOCKER-USER"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("v4 jump %q not linked; log:\n%s", want, log)
		}
	}
	if strings.Contains(string(log), "-j CHRONOVERSE-WORKLOAD6") || strings.Contains(string(log), "-j CHRONOVERSE-WORKLOAD-IN6") {
		t.Errorf("v6 jumps must not be linked without the chain; log:\n%s", log)
	}
}

// With the v6 chain present, both families must be linked.
func TestFirewallScriptLinksV6ChainWhenPresent(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	logFile, readyFile := stubFirewallBins(t, true)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("script error = %v", err)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("ready marker not touched: %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "DOCKER-USER -j CHRONOVERSE-WORKLOAD6") {
		t.Errorf("v6 DOCKER-USER jump not linked; log:\n%s", log)
	}
}
