// Package blackbox compares mc-go's observable behavior against the
// real Mojang server over the wire. Black-box means: scripted protocol
// clients only, no decompiled code, no log scraping for assertions.
//
// Requirements to run (otherwise the suite skips):
//
//	MCGO_BLACKBOX=1, a working `java`, loopback TCP binds allowed.
//
// Vanilla jar: MCGO_VANILLA_JAR or ../../vanilla/server-26.2.jar.
//
// External targets: MCGO_TARGET=host:port runs the same suite against
// an already-running server instead of an in-process mc-go, which is
// how another implementation is checked for parity — the differential
// tests still boot vanilla as the oracle and compare the target's wire
// behavior against it, and the mc-go-only tests exercise the target
// directly. Tests that need in-process control (a *server.Server
// mutator, world-state injection) skip with a clear reason, and the
// target is expected to run the shared comparison configuration —
// online-mode off, flat world, seed 1, view/simulation distance 8,
// creative default — plus ops for the test player names where a test
// writes vanilla an ops.json (mc-go grants everyone level 4 unless
// OpsRequired is set, so an mc-go target needs no file).
package blackbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

const (
	vanillaProp = "MCGO_VANILLA_JAR"
	enableProp  = "MCGO_BLACKBOX"
	targetProp  = "MCGO_TARGET"
)

// targets holds one live vanilla endpoint and one system-under-test
// endpoint — an in-process mc-go, or the MCGO_TARGET address when the
// suite is pointed at an external server.
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
	// Report target incompatibility before booting an unused vanilla process.
	if os.Getenv(targetProp) != "" && mcgo != nil {
		t.Skipf("%s mode: %s needs in-process mc-go configuration", targetProp, t.Name())
	}
	jar := blackboxJar(t)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	// Two consecutive freePort calls can hand back the same port; a
	// collision makes the probe see mc-go answering on the vanilla
	// addr and the whole vanilla leg silently runs against mc-go.
	mPort, ok := 0, false
	for i := 0; i < 10; i++ {
		if mPort, ok = freePort(); ok && mPort != vPort {
			break
		}
	}
	if !ok || mPort == vPort {
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

// blackboxJar is the real server jar, absolute, or a skip: the suite's
// requirements are one check, and a test that boots the jar itself
// (rather than through requireBlackboxFiles) needs the same guard.
func blackboxJar(t *testing.T) string {
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
	// java runs with cwd set to a temp dir, so the jar path must be
	// absolute before it is handed over.
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatalf("resolve jar path %s: %v", jar, err)
	}
	return jar
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
	return startVanillaInDir(t, jar, port, extra, files, "")
}

// writeVanillaServerDir lays out a server's working directory: the
// eula, server.properties (the shared comparison configuration plus
// extra), and whatever files the caller needs in place before boot.
// Files may name subdirectories; they are created as needed.
func writeVanillaServerDir(t *testing.T, dir string, port int, extra, files map[string]string) {
	t.Helper()
	vanillaDirForDebug = dir
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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
		// A flat comparison world crawls with slimes at the depths the
		// tests play in; a slime eating a survival-mode test player mid
		// run silently kills its window and drains the run's diffs into
		// noise. Tests needing a mob summon it explicitly.
		"spawn-monsters=false",
		// Same rationale: ambient animal spawns emit entity packets on
		// their own schedule and poison packet-level diffs. The flag
		// only stops the per-tick spawn cycle, so the flat world also
		// generates in the_void biome — its empty spawn list suppresses
		// the chunk-generation animals the flag misses.
		"spawn-animals=false",
		"level-type=flat",
		`generator-settings={"layers":[{"block":"minecraft:bedrock","height":1},{"block":"minecraft:dirt","height":2},{"block":"minecraft:grass_block","height":1}],"biome":"minecraft:the_void"}`,
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
}

// vanillaConsole is a real server whose console is a pipe, so a test
// can run commands and ask it to stop gracefully. That last part is
// what world-save tests need and the ordinary harness cannot give:
// vanilla only writes its world on `/save-all` or on a clean stop, and
// the harness ends a test by killing the process.
//
// The returned wait blocks until the server exits and reports its exit
// code; stopping it properly is the caller's job (send "stop"), and any
// leftover process is killed at cleanup.
func vanillaConsole(t *testing.T, jar string, port int, extra, files map[string]string, dir string) (addr string, say func(string), wait func() int) {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	writeVanillaServerDir(t, dir, port, extra, files)
	cmd := exec.Command("java", "-jar", jar, "--nogui")
	cmd.Dir = dir
	log, err := os.Create(filepath.Join(dir, "vanilla.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start vanilla: %v", err)
	}
	done := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		done <- code
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		default:
		}
	})

	addr = fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case <-done:
			out, _ := os.ReadFile(filepath.Join(dir, "vanilla.log"))
			t.Fatalf("vanilla exited before it was ready, log tail:\n%s", tail(out, 3000))
		default:
		}
		if probeStatus(addr) {
			ready = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ready {
		out, _ := os.ReadFile(filepath.Join(dir, "vanilla.log"))
		t.Fatalf("vanilla not ready in 90s, log tail:\n%s", tail(out, 3000))
	}

	say = func(line string) {
		if _, err := io.WriteString(in, line+"\n"); err != nil {
			t.Fatalf("vanilla console %q: %v", line, err)
		}
		// The console reads a line per tick; a short pause keeps a
		// burst of commands from overtaking each other.
		time.Sleep(400 * time.Millisecond)
	}
	wait = func() int {
		select {
		case code := <-done:
			return code
		case <-time.After(90 * time.Second):
			vanillaLogTail(t)
			t.Fatalf("vanilla did not exit in 90s")
			return -1
		}
	}
	return addr, say, wait
}

// startVanillaInDir is startVanillaCfgFiles with the working directory
// chosen by the caller, which is what a test needs when it hands the
// server a world that already exists: the world directory is a
// subdirectory of the server's own working directory, so both have to
// be the caller's to place. An empty dir means a fresh temporary one.
// Files may name subdirectories; they are created as needed.
func startVanillaInDir(t *testing.T, jar string, port int, extra, files map[string]string, dir string) (string, func()) {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	writeVanillaServerDir(t, dir, port, extra, files)
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
// With MCGO_TARGET set the "mc-go" endpoint is an already-running
// server instead: the caller's port goes unused and the mutate hook —
// which only an in-process server could honor — turns the test into a
// skip rather than silently running a different configuration.
func startMcGoCfg(t *testing.T, port int, mutate func(*server.Server)) string {
	t.Helper()
	if target := os.Getenv(targetProp); target != "" {
		if mutate != nil {
			t.Skipf("%s mode: %s needs in-process mc-go configuration", targetProp, t.Name())
		}
		if !probeStatus(target) {
			t.Fatalf("%s=%s did not answer a status ping", targetProp, target)
		}
		return target
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	s := server.New(addr)
	// Match the vanilla harness properties so join values compare.
	s.ViewDistance = 8
	s.SimulationDistance = 8
	s.SpawnProtection = 0
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
	t.Cleanup(func() { _ = ln.Close(); s.Stop() })
	// A generated world finds its spawn before it listens.
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		if probeStatus(addr) {
			return addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mc-go not ready on %s", addr)
	return ""
}

// lockedConn serialises writes to one connection. A framed packet is
// written with two Write calls - the length prefix, then the body - so
// two goroutines writing at once, a test body and the sink goroutine
// answering keep-alives, interleave halves of two frames and desync the
// server's decoder. Everything the harness hands a test writes through
// one of these.
type lockedConn struct {
	mu sync.Mutex
	c  net.Conn
}

func (l *lockedConn) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.c.Write(p)
}

// client dials addr and returns framed reader/writer.
// clientDeadline bounds a fresh connection's login and configuration.
// A normal-terrain world raises it: vanilla's configuration phase
// (PrepareSpawnTask) generates the spawn chunks before it finishes.
var clientDeadline = 10 * time.Second

func client(t *testing.T, addr string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	_ = c.SetDeadline(time.Now().Add(clientDeadline))
	return c, protocol.NewReader(c), protocol.NewWriter(&lockedConn{c: c})
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

// ackChunkBatch answers one finished chunk batch, asking for the highest
// rate the protocol allows - what a real client does every time a batch
// lands, and what releases the next batch of the player's window (see
// server.pumpChunks for the measured pacing).
func ackChunkBatch(pw *protocol.Writer) error {
	var b protocol.Buffer
	b.Float(64)
	return pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes())
}

// drainChunkWindow reads until the connection has been quiet for the
// given period, answering chunk batches so the server streams the whole
// window, and returns the Chunk Data packets it saw in arrival order.
// Keep-alives are answered too: the helper is used where a client would
// otherwise sit idle while its window arrives.
func drainChunkWindow(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, quiet time.Duration) []protocol.Packet {
	t.Helper()
	var chunks []protocol.Packet
	for {
		if err := c.SetReadDeadline(time.Now().Add(quiet)); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("%s chunk stream: %v", tag, err)
		}
		switch p.ID {
		case server.IDPlayChunkData:
			chunks = append(chunks, p)
		case server.IDPlayChunkBatchDone:
			if err := ackChunkBatch(pw); err != nil {
				t.Fatalf("%s: %v", tag, err)
			}
		case server.IDPlayKeepAliveCB:
			if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
				t.Fatalf("%s: %v", tag, err)
			}
		case server.IDPlaySyncPos:
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
					t.Fatalf("%s: %v", tag, err)
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return chunks
}

// awaitPlayReadyBlackbox drives a scripted client through the initial join
// burst. Attributes follow sendWindow's synchronous section/light warmup in
// the pinned 776 sequence. A quiet socket alone does not prove readiness.
// Call immediately after Join Game, before other packet drains; returned
// initial chunks let streaming assertions account for every observed column.
func awaitPlayReadyBlackbox(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) []protocol.Packet {
	t.Helper()
	start := time.Now()
	_ = c.SetDeadline(start.Add(60 * time.Second))
	defer c.SetDeadline(time.Time{})
	var chunks []protocol.Packet
	var lastID int32 = -1
	for count := 0; ; count++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s join readiness after %v (%d packets, last ID %#x): %v", tag, time.Since(start), count, lastID, err)
		}
		lastID = p.ID
		switch p.ID {
		case server.IDPlayChunkData:
			chunks = append(chunks, p)
		case server.IDPlayKeepAliveCB:
			if err = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
				t.Fatal(err)
			}
		case server.IDPlaySyncPos:
			id, err := protocol.NewPacketReader(p.Data).VarInt()
			if err != nil {
				t.Fatal(err)
			}
			var ack protocol.Buffer
			ack.VarInt(id)
			if err = pw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes()); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayChunkBatchDone:
			if err = ackChunkBatch(pw); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayUpdateAttributes:
			if err = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			return chunks
		}
	}
}
