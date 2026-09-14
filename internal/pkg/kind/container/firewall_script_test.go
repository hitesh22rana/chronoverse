package container_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubFirewallBins fakes apk/iptables/iptables-legacy/ip6tables on PATH, each
// with private add-if-missing state (the real backend views are separate, so a
// rule installed via one binary is invisible to the other). withV6Chain
// controls ip6tables chain visibility (IPv4-only host when false);
// legacyBackend selects which v4 backend holds DOCKER-USER. No
// ip6tables-legacy stub ever exists, mirroring reality.
func stubFirewallBins(t *testing.T, withV6Chain, legacyBackend bool) (logFile, readyFile string) {
	t.Helper()

	dir := t.TempDir()
	logFile = filepath.Join(dir, "log")
	readyFile = filepath.Join(dir, "ready")

	writeStub := func(name string, missing bool) {
		miss := "0"
		if missing {
			miss = "1"
		}
		body := "#!/bin/sh\nTAG=" + name + "\nFAKE_L_MISSING=" + miss + "\nFAKE_STATE=" + filepath.Join(dir, "state-"+name) + "\n" + `echo "$TAG $*" >> "$FAKE_LOG"
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
		//nolint:gosec // Test-only PATH stubs must be executable; temp dir, no secrets.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeStub("apk", false)
	writeStub("iptables", legacyBackend)
	writeStub("iptables-legacy", !legacyBackend)
	writeStub("ip6tables", !withV6Chain || legacyBackend)

	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile, readyFile
}

func runFirewallScript(t *testing.T, readyFile string) error {
	t.Helper()

	return runFirewallFile(t, "workload-firewall.sh", readyFile)
}

func runProbeScript(t *testing.T) error {
	t.Helper()

	return runFirewallFile(t, "workload-firewall-probe.sh", "")
}

func runFirewallFile(t *testing.T, name, readyFile string) error {
	t.Helper()

	_, caller, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	script := filepath.Join(filepath.Dir(caller), "..", "..", "..", "..", "compose", "firewall", name)
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
	logFile, readyFile := stubFirewallBins(t, false, false)

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
	logFile, readyFile := stubFirewallBins(t, true, false)

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

// On legacy-backend hosts the nft view never sees DOCKER-USER: every rule
// must go through iptables-legacy, never plain iptables.
func TestFirewallScriptUsesLegacyBackend(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	logFile, readyFile := stubFirewallBins(t, false, true)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("script on legacy host error = %v", err)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("ready marker not touched: %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(log), "\n") {
		if strings.HasPrefix(line, "iptables ") && (strings.Contains(line, " -A ") || strings.Contains(line, " -I ")) {
			t.Errorf("nft binary must not install rules on a legacy host: %q", line)
		}
	}
	for _, want := range []string{"iptables-legacy -I INPUT", "iptables-legacy -I DOCKER-USER"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("legacy jump %q not linked; log:\n%s", want, log)
		}
	}
}

// The probe passes only after a complete apply, in the active backend.
func TestFirewallProbePassesAfterApply(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	_, readyFile := stubFirewallBins(t, true, false)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("apply error = %v", err)
	}
	if err := runProbeScript(t); err != nil {
		t.Fatalf("probe after apply error = %v", err)
	}
}

// Without any installed rules the probe must fail in every backend view.
func TestFirewallProbeFailsWithoutRules(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	stubFirewallBins(t, true, false)

	if err := runProbeScript(t); err == nil {
		t.Fatal("probe without rules succeeded, want failure")
	}
}

// On legacy-backend hosts the probe must succeed via iptables-legacy (the nft
// view genuinely misses, thanks to per-binary stub state).
func TestFirewallProbePassesOnLegacyBackend(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	_, readyFile := stubFirewallBins(t, false, true)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("apply on legacy host error = %v", err)
	}
	if err := runProbeScript(t); err != nil {
		t.Fatalf("probe on legacy host error = %v", err)
	}
}

// Every installed ESTABLISHED accept must carry the bridge-interface match,
// or unrelated established flows would be shielded from downstream rules.
func TestFirewallEstablishedRulesAreInterfaceScoped(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	logFile, readyFile := stubFirewallBins(t, true, false)

	if err := runFirewallScript(t, readyFile); err != nil {
		t.Fatalf("apply error = %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	scoped := 0
	for _, line := range strings.Split(string(log), "\n") {
		if !strings.Contains(line, "ESTABLISHED,RELATED -j ACCEPT") {
			continue
		}
		if !strings.Contains(line, "-i chronoverse-br") {
			t.Errorf("unscoped established accept: %q", line)
		}
		scoped++
	}
	if scoped < 4 {
		t.Errorf("scoped established accepts = %d, want >= 4 (v4 FWD+INPUT, v6 FWD+INPUT)", scoped)
	}
}
