package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// Sign editor lock parity: one sign, two players.
//
// The real server shows a sign's editor to one player at a time, and the
// same script is run against both servers to pin the rule down: who gets
// the editor, what a refused player is told (nothing at all, even when
// holding a dye), what releases the lock (the holder writing the sign,
// leaving the game, or walking more than ten blocks away), and that the
// lock is per sign rather than per position or per player - a second
// player is given a different sign's editor throughout.
const signLockName = "SignLock"

var (
	lockSign  = [3]int{8, -60, 0}
	lockOther = [3]int{32, -60, 0}
	lockFar   = [3]int{56, -60, 0}
)

// lockRun is one client's side of the script: the packets it was sent,
// one line per step, and the connection it holds.
type lockRun struct {
	t     *testing.T
	tag   string
	name  string
	c     net.Conn
	sink  *signSink
	pw    *protocol.Writer
	steps []string
}

// step marks the packet stream, runs one action, and records everything
// the server said in answer - "none" when it said nothing, which is the
// answer a refused use gets.
func (r *lockRun) step(label string, act func()) []string {
	mark := r.sink.mark()
	act()
	time.Sleep(550 * time.Millisecond)
	events := r.sink.since(mark)
	if len(events) == 0 {
		events = []string{"none"}
	}
	for _, e := range events {
		r.steps = append(r.steps, label+": "+e)
	}
	return events
}

// use holds nothing and clicks the sign.
func (r *lockRun) use(pos [3]int) func() {
	return func() {
		signHold(r.t, r.pw, "")
		signUse(r.t, r.pw, pos, 3)
	}
}

// tp teleports this client and waits for the teleport it has to accept,
// which the real server requires before it will act on an interaction
// again, whichever player sent it.
func (r *lockRun) tp(x, y, z float64) func() {
	return func() {
		before := r.sink.syncCount()
		sendCommand(r.t, r.pw, fmt.Sprintf("tp %s %g %g %g %g %v", r.name, x, y, z, 180.0, 10.0))
		if !r.sink.waitSync(before, 5*time.Second) {
			r.t.Fatalf("%s: no teleport to accept after /tp %s", r.tag, r.name)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// join brings one scripted player in and puts it in front of a sign.
func signLockJoin(t *testing.T, tag, addr, name string, at [3]int) *lockRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	_ = c.SetDeadline(time.Time{})
	r := &lockRun{t: t, tag: tag, name: name, c: c, sink: newSignSink(pr, pw), pw: pw}
	r.step("join and walk up to the sign", r.tp(float64(at[0])+0.5, float64(at[1]), float64(at[2])+3.5))
	return r
}

// signLockSteps runs the whole script against one server and returns
// every client's recorded steps, in the order the steps ran.
func signLockSteps(t *testing.T, tag, addr, name string) (a, b, c []string) {
	t.Helper()
	// An operator to place the signs: without it the real server refuses
	// the setblock and every use is answered with nothing, which is
	// exactly what the lock looks like.
	setup, spr, spw := joinPlayBlackbox(t, addr, name+"Setup")
	defer setup.Close()
	_ = setup.SetDeadline(time.Time{})
	ssink := newChatSink(spr, spw)
	// The chunks are forced before the signs are placed, not after. The
	// far sign is three chunks out, which is outside the window the setup
	// player's own connection loads, and the real server refuses a
	// setblock in a chunk that is not loaded - with "That position is not
	// loaded", which is neither unknown nor a permission error and so got
	// past the check below. Measured: with the two commands in the other
	// order the far sign is missing from the real server's world, and
	// every use of it is answered with nothing, which is exactly what the
	// lock looks like - the script was passing its early steps while the
	// sign it was aiming at did not exist.
	sendCommand(t, spw, "forceload add -64 -64 128 128")
	waitDrained(t, spw, ssink)
	ssink.take()
	for _, pos := range [][3]int{lockSign, lockOther, lockFar} {
		sendCommand(t, spw, fmt.Sprintf("setblock %d %d %d minecraft:oak_sign[rotation=0]", pos[0], pos[1], pos[2]))
	}
	time.Sleep(600 * time.Millisecond)
	waitDrained(t, spw, ssink)
	for _, line := range ssink.take() {
		for _, bad := range []string{"nknown or incomplete", "ermission", "o such", "not loaded"} {
			if strings.Contains(line, bad) {
				t.Errorf("%s: the setup command was refused (%s): %s", tag, bad, line)
			}
		}
	}

	ra := signLockJoin(t, tag, addr, name, lockSign)
	defer ra.c.Close()
	rb := signLockJoin(t, tag, addr, name+"B", lockOther)
	defer rb.c.Close()
	rc := signLockJoin(t, tag, addr, name+"C", lockFar)
	defer rc.c.Close()

	// 1. A opens the sign's editor and keeps it open.
	ra.step("A uses the sign, empty hand", ra.use(lockSign))
	// 2. B, standing at the same sign, gets nothing at all - not even the
	//    block entity data an empty hand could bring.
	rb.step("B uses the sign A is editing, empty hand", rb.use(lockSign))
	// 3. ...but B is still given the editor of another sign, so what
	//    stops it is the lock and not the player or the position.
	rb.step("B uses a sign nobody is editing", rb.tp(float64(lockOther[0])+0.5, -60, 3.5))
	rb.step("B uses a sign nobody is editing, empty hand", rb.use(lockOther))
	// 4. A dye aimed at a sign somebody else is editing changes nothing
	//    and is answered with nothing.
	rb.step("B walks back to the sign A is editing", rb.tp(float64(lockSign[0])+0.5, -60, 3.5))
	rb.step("B dyes the sign A is editing", func() {
		signHold(t, rb.pw, "red_dye")
		signUse(t, rb.pw, lockSign, 3)
	})
	// 5. The holder writes the side it was given, which is broadcast to
	//    every player tracking the sign - B included.
	ra.step("A writes the side the editor named", func() {
		signUpdate(t, ra.pw, lockSign, true, [4]string{"hi", "", "", ""})
	})
	// 6. Writing the sign is what the editor was open for: the next
	//    player to use it is given the editor.
	rb.step("B uses the sign A just wrote", rb.use(lockSign))
	// 7. ...and B is the holder now: A is refused in its turn.
	ra.step("A uses the sign B is editing", ra.use(lockSign))
	rb.step("B writes the sign it holds", func() {
		signUpdate(t, rb.pw, lockSign, true, [4]string{"b", "", "", ""})
	})
	// 8. The lock follows the holder's distance: A takes it back, walks
	//    five blocks away and still holds it, then twelve blocks away and
	//    lets go.
	ra.step("A uses the sign B just wrote", ra.use(lockSign))
	ra.step("A uses its own sign again, editor still open", ra.use(lockSign))
	ra.step("A walks five blocks off, still holding it", ra.tp(float64(lockSign[0])+5.5, -60, 3.5))
	rb.step("B uses the sign A is holding five blocks away", rb.use(lockSign))
	ra.step("A walks twelve blocks off", ra.tp(float64(lockSign[0])+12.5, -60, 3.5))
	rb.step("B uses the sign A walked away from", rb.use(lockSign))
	ra.step("A uses the sign B took over", ra.use(lockSign))
	// 9. A sign whose holder has left the game is free at once.
	ra.step("A walks over to the far sign", ra.tp(float64(lockFar[0])+0.5, -60, 3.5))
	ra.step("A opens the far sign", ra.use(lockFar))
	ra.c.Close()
	time.Sleep(700 * time.Millisecond)
	rc.step("C uses the sign whose holder left", rc.use(lockFar))

	return ra.steps, rb.steps, rc.steps
}

// TestSignEditorLockParity runs the lock script against both servers and
// requires the same outcome for every step, on every client.
func TestSignEditorLockParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(signLockName, signLockName+"Setup", signLockName+"B", signLockName+"C"),
	}, nil)
	defer tg.cleanup()

	va, vb, vc := signLockSteps(t, "vanilla", tg.vanilla, signLockName)
	ma, mb, mc := signLockSteps(t, "mc-go", tg.mcgo, signLockName)

	// The script only means something if each answer really happened, and
	// in the order the measurements say. Each of these is required of
	// both servers, so two servers that both said nothing cannot pass by
	// agreeing.
	want := []struct {
		tag      string
		steps    []string
		step     string
		contains string
	}{
		{"A", va, "A uses the sign, empty hand", "editor(8,-60,0) side=front"},
		{"A", ma, "A uses the sign, empty hand", "editor(8,-60,0) side=front"},
		{"B", vb, "B uses the sign A is editing, empty hand", "none"},
		{"B", mb, "B uses the sign A is editing, empty hand", "none"},
		{"B", vb, "B uses a sign nobody is editing, empty hand", "editor(32,-60,0) side=front"},
		{"B", mb, "B uses a sign nobody is editing, empty hand", "editor(32,-60,0) side=front"},
		{"B", vb, "B dyes the sign A is editing", "none"},
		{"B", mb, "B dyes the sign A is editing", "none"},
		{"A", va, "A writes the side the editor named", "be(8,-60,0) type=7"},
		{"A", ma, "A writes the side the editor named", "be(8,-60,0) type=7"},
		{"B", vb, "B uses the sign A just wrote", "editor(8,-60,0)"},
		{"B", mb, "B uses the sign A just wrote", "editor(8,-60,0)"},
		{"A", va, "A uses the sign B is editing", "none"},
		{"A", ma, "A uses the sign B is editing", "none"},
		{"B", vb, "B writes the sign it holds", "be(8,-60,0) type=7"},
		{"B", mb, "B writes the sign it holds", "be(8,-60,0) type=7"},
		{"A", va, "A uses the sign B just wrote", "editor(8,-60,0)"},
		{"A", ma, "A uses the sign B just wrote", "editor(8,-60,0)"},
		{"A", va, "A uses its own sign again, editor still open", "editor(8,-60,0)"},
		{"A", ma, "A uses its own sign again, editor still open", "editor(8,-60,0)"},
		{"A", va, "A walks five blocks off, still holding it", "none"},
		{"A", ma, "A walks five blocks off, still holding it", "none"},
		{"B", vb, "B uses the sign A is holding five blocks away", "none"},
		{"B", mb, "B uses the sign A is holding five blocks away", "none"},
		{"B", vb, "B uses the sign A walked away from", "editor(8,-60,0)"},
		{"B", mb, "B uses the sign A walked away from", "editor(8,-60,0)"},
		{"A", va, "A opens the far sign", "editor(56,-60,0)"},
		{"A", ma, "A opens the far sign", "editor(56,-60,0)"},
		{"C", vc, "C uses the sign whose holder left", "editor(56,-60,0)"},
		{"C", mc, "C uses the sign whose holder left", "editor(56,-60,0)"},
	}
	for _, w := range want {
		if !sawStep(w.steps, w.step+": ", w.contains) {
			t.Errorf("%s never produced a step %q containing %q; steps:\n%s",
				w.tag, w.step, w.contains, strings.Join(w.steps, "\n"))
		}
	}

	compareSteps(t, "the holder", va, ma)
	compareSteps(t, "the second player", vb, mb)
	compareSteps(t, "the third player", vc, mc)
}
