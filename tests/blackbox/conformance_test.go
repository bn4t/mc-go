package blackbox

import (
	"context"
	"encoding/json"
	"os"
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
	for _, r := range report.Results {
		if r.Status != conformance.Pass {
			t.Errorf("%s: %s: %s", r.Scenario, r.Status, r.Detail)
		}
	}
}

// The exact same public profile must also accept the independently running
// vanilla oracle. It remains explicitly gated with the differential suite;
// missing Java/jar requirements skip and are not a portable-runner pass.
func TestPortableConformanceVanilla(t *testing.T) {
	jar := blackboxJar(t)
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr, stop := startVanilla(t, jar, port)
	defer stop()
	report := conformance.Run(context.Background(), conformance.Config{
		Endpoint: addr, Protocol: 776, Version: "26.2", Username: "Conformance",
		Capabilities: []string{"status", "ping", "offline-login", "configuration", "malformed"}, Timeout: 10 * time.Second,
	})
	if path := os.Getenv("MCGO_CONFORMANCE_REPORT"); path != "" {
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
		if result.Status != conformance.Pass {
			t.Errorf("%s: %s: %s", result.Scenario, result.Status, result.Detail)
		}
	}
}
