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
// with a private per-chain rule store (backend views are separate, and insert
// positions are per-chain like the real binary). withV6Chain controls
// ip6tables chain visibility (IPv4-only host when false); legacyBackend
// selects which v4 backend holds DOCKER-USER. No ip6tables-legacy stub ever
// exists, mirroring reality.
func stubFirewallBins(t *testing.T, withV6Chain, legacyBackend bool) (stateDir, logFile, readyFile string) {
	t.Helper()

	dir := t.TempDir()
	stateDir = filepath.Join(dir, "state")
	logFile = filepath.Join(dir, "log")
	readyFile = filepath.Join(dir, "ready")

	writeStub := func(name string, missing bool) {
		miss := "0"
		if missing {
			miss = "1"
		}
		body := "#!/bin/sh\nTAG=" + name + "\nFAKE_L_MISSING=" + miss + "\nFAKE_STATE_DIR=" + stateDir + "\nmkdir -p \"$FAKE_STATE_DIR\"\n" + `echo "$TAG $*" >> "$FAKE_LOG"
op="$1"; shift
chain_file() { printf '%s/%s-%s' "$FAKE_STATE_DIR" "$TAG" "$1"; }
case "$op" in
-n) exit "$FAKE_L_MISSING" ;;
-N) : >> "$(chain_file "$1")" ;;
-C) grep -qxF "$*" "$(chain_file "$1")" 2>/dev/null ;;
-A) echo "$*" >> "$(chain_file "$1")" ;;
-I)
	chain="$1"; shift
	case "$1" in '') pos=1 ;; *[!0-9]*) pos=1 ;; *) pos="$1"; shift ;; esac
	f="$(chain_file "$chain")"
	n=$(wc -l < "$f" 2>/dev/null || echo 0)
	[ "$pos" -gt $((n + 1)) ] && pos=$((n + 1))
	{ head -n $((pos - 1)) "$f" 2>/dev/null || true; printf '%s %s\n' "$chain" "$*"; tail -n +"$pos" "$f" 2>/dev/null || true; } > "$f.tmp" && mv "$f.tmp" "$f"
	;;
-D)
	f="$(chain_file "$1")"
	grep -qxF "$*" "$f" 2>/dev/null || exit 1
	grep -vxF "$*" "$f" > "$f.tmp" && mv "$f.tmp" "$f"
	;;
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
	return stateDir, logFile, readyFile
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
	_, logFile, readyFile := stubFirewallBins(t, false, false)

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
	_, logFile, readyFile := stubFirewallBins(t, true, false)

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
	_, logFile, readyFile := stubFirewallBins(t, false, true)

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
	_, _, readyFile := stubFirewallBins(t, true, false)

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
	_, _, readyFile := stubFirewallBins(t, false, true)

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
	_, logFile, readyFile := stubFirewallBins(t, true, false)

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

// Re-application must neither duplicate rules nor reorder allows ahead of
// denies. The INPUT chain must equal its exact expected order (positional
// inserts, terminal DROP never deleted), proving continuous protection.
func TestFirewallReapplyKeepsOrderAndNoDuplicates(t *testing.T) {
	// No t.Parallel: stub binaries are installed via process environment.
	stateDir, _, readyFile := stubFirewallBins(t, false, false)

	for i := 0; i < 2; i++ {
		if err := runFirewallScript(t, readyFile); err != nil {
			t.Fatalf("apply %d error = %v", i+1, err)
		}
	}
	chainFile := filepath.Join(stateDir, "iptables-CHRONOVERSE-WORKLOAD-IN")
	raw, err := os.ReadFile(chainFile)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(raw)), "\n")
	lines := make([]string, 0, len(parts))
	seen := map[string]int{}
	for _, line := range parts {
		seen[line]++
		if seen[line] > 1 {
			t.Errorf("duplicated rule after re-apply: %q", line)
		}
		lines = append(lines, strings.TrimPrefix(line, "CHRONOVERSE-WORKLOAD-IN "))
	}
	want := []string{
		`-i chronoverse-br -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT`,
		`-i chronoverse-br -s 198.18.247.0/24 -p tcp --dport 53 -j ACCEPT`,
		`-i chronoverse-br -s 198.18.247.0/24 -p udp --dport 53 -j ACCEPT`,
		`-s 198.18.247.0/24 -m conntrack --ctstate NEW -j DROP`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("INPUT chain order = %q, want %q", lines, want)
	}
}

// The INPUT terminal DROP must never be deleted: positional DNS inserts keep
// deny-before-allow without any fail-open window. Guard the invariant in the
// script source itself so a future edit cannot reintroduce -D on this chain.
func TestFirewallNeverDeletesInputDrop(t *testing.T) {
	_, caller, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	script, err := os.ReadFile(filepath.Join(filepath.Dir(caller), "..", "..", "..", "..", "compose", "firewall", "workload-firewall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(script), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "-D") && strings.Contains(trimmed, "CHAIN_IN") {
			t.Errorf("INPUT chain must never be deleted from: %q", line)
		}
	}
}
