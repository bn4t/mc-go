// Package blackbox compares mc-go's observable behavior against the
// real Mojang server over the wire. Black-box means: scripted protocol
// clients only, no decompiled code, no log scraping for assertions.
//
// Requirements to run (otherwise the suite skips):
//
//	MCGO_BLACKBOX=1, a working `java`, loopback TCP binds allowed.
//
// Vanilla jar: MCGO_VANILLA_JAR or ../../vanilla/server-26.2.jar.
package blackbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

const (
	vanillaProp = "MCGO_VANILLA_JAR"
	enableProp  = "MCGO_BLACKBOX"
)

// targets holds one live vanilla and one live mc-go endpoint.
type targets struct {
	vanilla string
	mcgo    string
	cleanup func()
}

// requireBlackbox starts both servers in the shared comparison
// configuration (creative, flat, distance 8).
func requireBlackbox(t *testing.T) targets {
	t.Helper()
	return requireBlackboxCfg(t, nil, nil)
}

// requireBlackboxCfg starts vanilla with extra server.properties and
// mc-go with a mutator, so tests can compare other configurations.
func requireBlackboxCfg(t *testing.T, vanillaProps map[string]string, mcgo func(*server.Server)) targets {
	t.Helper()
	return requireBlackboxFiles(t, vanillaProps, nil, mcgo)
}

// requireBlackboxFiles is requireBlackboxCfg plus extra files written
// into the vanilla working directory before the server boots (ops.json
// is the main user: several serverbound packets are op-gated, so
// comparing them against vanilla needs an operator).
func requireBlackboxFiles(t *testing.T, vanillaProps, vanillaFiles map[string]string, mcgo func(*server.Server)) targets {
	t.Helper()
	if os.Getenv(enableProp) != "1" {
		t.Skipf("set %s=1 to run comparison against the Mojang server", enableProp)
	}
	if out, err := exec.Command("java", "-version").CombinedOutput(); err != nil {
		t.Skipf("no working java runtime (%v): %s", err, firstLine(out))
	}
	jar := os.Getenv(vanillaProp)
	if jar == "" {
		jar = filepath.Join("..", "..", "vanilla", "server-26.2.jar")
	}
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("vanilla jar not found at %s: %v", jar, err)
	}
	// startVanilla runs java with cwd set to a temp dir, so the jar
	// path must be absolute before it is handed over.
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatalf("resolve jar path %s: %v", jar, err)
	}
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}

	vAddr, stopVanilla := startVanillaCfgFiles(t, jar, vPort, vanillaProps, vanillaFiles)
	mAddr := startMcGoCfg(t, mPort, mcgo)
	return targets{
		vanilla: vAddr,
		mcgo:    mAddr,
		cleanup: stopVanilla,
	}
}

func freePort() (int, bool) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, false
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, true
}

// startVanilla boots the Mojang server offline-mode in a temp dir.
func startVanilla(t *testing.T, jar string, port int) (string, func()) {
	t.Helper()
	return startVanillaCfg(t, jar, port, nil)
}

// startVanillaCfg boots vanilla with server.properties overrides.
// vanillaDirForDebug is set by startVanillaCfg so diagnostics can read
// the server log during a test.
var vanillaDirForDebug string

func startVanillaCfg(t *testing.T, jar string, port int, extra map[string]string) (string, func()) {
	t.Helper()
	return startVanillaCfgFiles(t, jar, port, extra, nil)
}

// startVanillaCfgFiles boots vanilla with server.properties overrides
// and any extra files the caller needs in place before boot.
func startVanillaCfgFiles(t *testing.T, jar string, port int, extra, files map[string]string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	vanillaDirForDebug = dir
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("eula.txt", "eula=true\n")
	props := []string{
		fmt.Sprintf("server-port=%d", port),
		"online-mode=false",
		"motd=vanilla-baseline",
		"max-players=20",
		"enforce-secure-profile=false",
		"spawn-protection=0",
		// Match mc-go's current world/game configuration so behavior
		// comparisons are apples-to-apples: creative, normal, flat,
		// and the same view/simulation distance.
		"gamemode=creative",
		"difficulty=normal",
		"level-type=flat",
		"level-seed=1",
		"view-distance=8",
		"simulation-distance=8",
	}
	values := map[string]string{}
	order := []string{}
	for _, p := range props {
		k := strings.SplitN(p, "=", 2)[0]
		values[k] = strings.SplitN(p, "=", 2)[1]
		order = append(order, k)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := values[k]; !ok {
			order = append(order, k)
		}
		values[k] = extra[k]
	}
	lines := make([]string, 0, len(order))
	for _, k := range order {
		lines = append(lines, k+"="+values[k])
	}
	write("server.properties", strings.Join(lines, "\n")+"\n")
	for name, body := range files {
		write(name, body)
	}

	cmd := exec.Command("java", "-jar", jar, "--nogui")
	cmd.Dir = dir
	log, err := os.Create(filepath.Join(dir, "vanilla.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start vanilla: %v", err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	t.Cleanup(stop)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if probeStatus(addr) {
			return addr, stop
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "vanilla.log"))
	t.Fatalf("vanilla not ready in 90s, log tail:\n%s", tail(out, 3000))
	return "", stop
}

// startMcGo serves mc-go on port.
func startMcGo(t *testing.T, port int) string {
	t.Helper()
	return startMcGoCfg(t, port, nil)
}

// startMcGoCfg serves mc-go with an optional mutator (e.g. survival).
func startMcGoCfg(t *testing.T, port int, mutate func(*server.Server)) string {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	s := server.New(addr)
	// Match the vanilla harness properties so join values compare.
	s.ViewDistance = 8
	s.SimulationDistance = 8
	if mutate != nil {
		mutate(s)
	}
	if err := s.InitPlugins(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("bind blocked: %v", err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if probeStatus(addr) {
			return addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mc-go not ready on %s", addr)
	return ""
}

// client dials addr and returns framed reader/writer.
func client(t *testing.T, addr string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c, protocol.NewReader(c), protocol.NewWriter(c)
}

func handshake(pw *protocol.Writer, next int32) error {
	var b protocol.Buffer
	b.VarInt(server.ProtocolVersion)
	b.String("localhost")
	b.UShort(25565)
	b.VarInt(next)
	return pw.WritePacket(server.IDHandshake, b.Bytes())
}

// probeStatus reports whether a status round-trip succeeds.
func probeStatus(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	pr := protocol.NewReader(c)
	pw := protocol.NewWriter(c)
	if err := handshake(pw, server.NextStatus); err != nil {
		return false
	}
	if err := pw.WritePacket(server.IDStatusRequest, nil); err != nil {
		return false
	}
	resp, err := pr.ReadPacket()
	return err == nil && resp.ID == server.IDStatusResponse
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
