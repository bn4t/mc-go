package blackbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/conformance"
)

// This is the same public runner used by the standalone CLI. The harness only
// supplies an endpoint; every assertion crosses the TCP boundary, and an
// MCGO_TARGET can be another implementation without a vanilla oracle.
func TestPortableConformanceProfile(t *testing.T) {
	addr := requireMcGoOnly(t)
	report := conformance.Run(context.Background(), conformance.Config{
		Endpoint: addr, Protocol: 776, Version: "26.2", Username: "Conformance",
		Capabilities: []string{"status", "ping", "offline-login", "configuration", "malformed"},
		Timeout:      10 * time.Second,
	})
	assertPortableDeclaredPasses(t, report, 7)
}

// The exact same public profile must also accept the independently running
// vanilla oracle. It remains explicitly gated with the differential suite;
// missing Java/jar requirements skip and are not a portable-runner pass.
func TestPortableConformanceVanilla(t *testing.T) {
	runPortableConformanceOracle(t, blackboxJar(t), 776, "26.2")
}

// The independent 775 runner profile is also checked against the official
// oracle. Server admission and mixed gameplay have separate TCP regressions.
func TestPortableConformanceVanilla775(t *testing.T) {
	if os.Getenv(enableProp) != "1" {
		t.Skipf("set %s=1 to run the protocol 775 vanilla oracle", enableProp)
	}
	if out, err := exec.Command("java", "-version").CombinedOutput(); err != nil {
		t.Skipf("no working java runtime (%v): %s", err, firstLine(out))
	}
	jar := os.Getenv("MCGO_VANILLA_775_JAR")
	if jar == "" {
		jar = filepath.Join("..", "..", "vanilla", "server-26.1.jar")
	}
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("protocol 775 vanilla jar not found at %s: %v", jar, err)
	}
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatal(err)
	}
	runPortableConformanceOracle(t, jar, 775, "26.1")
}

func runPortableConformanceOracle(t *testing.T, jar string, protocol int32, version string) {
	t.Helper()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr, stop := startVanilla(t, jar, port)
	defer stop()
	report := conformance.Run(context.Background(), conformance.Config{
		Endpoint: addr, Protocol: protocol, Version: version, Username: "Conformance",
		Capabilities: []string{"status", "ping", "offline-login", "configuration", "malformed"}, Timeout: 10 * time.Second,
	})
	if path := os.Getenv("MCGO_CONFORMANCE_REPORT"); path != "" {
		if protocol == 775 {
			ext := filepath.Ext(path)
			base := strings.TrimSuffix(path, ext)
			if ext == "" {
				ext = ".json"
			}
			path = base + ".775" + ext
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range report.Results {
		t.Logf("%s: %s (%dms)", result.Scenario, result.Status, result.DurationMS)
	}
	assertPortableDeclaredPasses(t, report, 7)
}

// An omitted capability remains visible as unsupported. Keep smoke coverage
// explicit so an accidentally omitted declaration cannot turn a check into a
// skipped result while this test still reports success.
func assertPortableDeclaredPasses(t *testing.T, report conformance.Report, want int) {
	t.Helper()
	declared := 0
	for _, result := range report.Results {
		if result.Declared {
			declared++
			if result.Status != conformance.Pass {
				t.Errorf("%s: %s: %s", result.Scenario, result.Status, result.Detail)
			}
		} else if result.Status != conformance.Unsupported {
			t.Errorf("omitted %s: %s, want unsupported", result.Scenario, result.Status)
		}
	}
	if declared != want {
		t.Errorf("declared scenarios = %d, want %d", declared, want)
	}
	if report.ExitCode() != 0 {
		t.Errorf("report exit code = %d, want 0", report.ExitCode())
	}
}
