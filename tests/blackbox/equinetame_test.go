package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// rideTameResult is what one leg of the ride-tame probe observed on
// the wire: the mount's set_passengers, the buck loop's eject, the
// entity_event / angry-sound emits, and the save keys from data get.
type rideTameResult struct {
	mounted   bool   // set_passengers listed a rider after the interact
	ejected   bool   // a later set_passengers carried an empty list
	event6    bool   // entity_event 6 — taming failed smoke
	event7    bool   // entity_event 7 — tamed hearts
	angry     bool   // play_sound of the type's angry sound id
	standFlag bool   // DATA_ID_FLAGS byte gained the 0x20 standing bit
	data      string // the data-get compound as visible text
}

// rideTameObserve runs the full mount→sprint→data-get probe on one
// server: summon an untamed adult horse with the given Temper, mount
// it bareback, tick sprint 200 (the 1/25-per-tick temper roll fires
// ~8 times), and read the save keys. Temper 0 always fails the roll
// (nextInt(100)<0), Temper 100 always passes (nextInt(100)<100) — so
// both lanes are deterministic on either server.
func rideTameObserve(t *testing.T, tag, addr string, temper int) rideTameResult {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "P")
	defer c.Close()
	// newChatSinkTP owns the reader and confirms teleports — no
	// drainChunkWindow here: a second reader splits the packet
	// stream and starves both sides.
	sink := newChatSinkTP(pr, pw)
	sink.cap = 1 << 20
	time.Sleep(400 * time.Millisecond)

	sendCommand(t, pw, "kill @e[type=minecraft:horse]")
	sendCommand(t, pw, "fill 13 -61 7 17 -59 11 minecraft:glass hollow")
	snbt := "{Temper:" + itoa(int32(temper)) + ",PersistenceRequired:1b}"
	sendCommand(t, pw, "summon minecraft:horse 15.5 -60 9 "+snbt)
	sendCommand(t, pw, "tp @s 11.5 -60 9")
	// Empty hand — a bare fist interact mounts the untamed adult via
	// AbstractHorse.doPlayerRide.
	sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:air")
	time.Sleep(600 * time.Millisecond)

	horseType, _ := server.EntityTypeID("horse")
	var res rideTameResult
	var horseEid int32 = -1
	for i := 0; i < 30 && horseEid <= 0; i++ {
		for _, l := range sink.take() {
			if eid := aniSpawnID([]string{l}, horseType, 15.5, 9); eid > 0 {
				horseEid = eid
			}
		}
		if horseEid <= 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if horseEid <= 0 {
		t.Fatalf("%s: no horse add_entity observed", tag)
	}

	var ib protocol.Buffer
	ib.VarInt(horseEid)
	ib.VarInt(0) // action: interact
	ib.VarInt(0) // main hand
	ib.Bool(false)
	_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
	time.Sleep(500 * time.Millisecond)

	res.mounted = passengersSeen(sink.take(), horseEid, true)
	if !res.mounted {
		t.Fatalf("%s: empty-hand interact did not mount the untamed horse", tag)
	}

	sendCommand(t, pw, "tick sprint 200")
	// Collect over ~4s — the roll lands somewhere inside the sprint
	// and its emits trickle out with the rest of the tick traffic.
	var lines []string
	for i := 0; i < 8; i++ {
		time.Sleep(500 * time.Millisecond)
		lines = append(lines, sink.take()...)
	}
	for _, l := range lines {
		body, ok := packetBody(l)
		if !ok {
			continue
		}
		switch body[0] {
		case 0x22: // entity_event: Int eid + Byte status
			if len(body) >= 6 && body[1] == 0 && body[2] == 0 && body[3] == 0 &&
				body[4] == byte(horseEid) {
				switch body[5] {
				case 6:
					res.event6 = true
				case 7:
					res.event7 = true
				}
			}
		case 0x6B: // set_passengers — count 0 is the eject broadcast
			if ejectSeen(body, horseEid) {
				res.ejected = true
			}
		case 0x75: // play_sound — entity.horse.angry is wire id 852
			if len(body) > 3 && body[1] == 0xD4 && body[2] == 0x06 && body[3] == 0x06 {
				res.angry = true
			}
		case 0x63: // set_entity_data — DATA_ID_FLAGS idx18, bit 0x20
			if flagsByte18(body) {
				res.standFlag = true
			}
		}
	}
	res.data = dataGetCompound(t, sink, pw, "@e[type=minecraft:horse,limit=1]")
	return res
}

// dataGetCompound is dataGetLine's no-path arm: `execute as sel run
// data get entity @s` returns the whole save compound per matched
// entity (a multi-selector is legal here — each matched entity gets
// itself once), and the replies' text components flatten to visible
// text for substring asserts.
func dataGetCompound(t *testing.T, sink *chatSink, pw *protocol.Writer, sel string) string {
	t.Helper()
	sendCommand(t, pw, "execute as "+sel+" run data get entity @s")
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	var got strings.Builder
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if strings.Contains(line, "data.entity.query") {
				got.WriteString(visibleChat(line))
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	return got.String()
}

// packetBody splits a chatSink line ("0x22:<raw bytes>") into a byte
// slice with the packet id re-attached as body[0] for uniform
// switching; body[1:] is the wire body.
func packetBody(line string) ([]byte, bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 || i < 2 {
		return nil, false
	}
	raw := line[i+1:]
	out := make([]byte, 1+len(raw))
	out[0] = byte(parsePacketID(line[:i]))
	copy(out[1:], raw)
	return out, true
}

func parsePacketID(s string) int64 {
	s = strings.TrimPrefix(s, "0x")
	var v int64
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v += int64(c - '0')
		case c >= 'a' && c <= 'f':
			v += int64(c-'a') + 10
		}
	}
	return v
}

// passengersSeen scans sink lines for a set_passengers packet on veh
// whose list is non-empty (mounted) or empty (eject).
func passengersSeen(lines []string, veh int32, wantRider bool) bool {
	for _, l := range lines {
		body, ok := packetBody(l)
		if !ok || body[0] != 0x6B {
			continue
		}
		r := protocol.NewPacketReader(body[1:])
		if v, err := r.VarInt(); err != nil || v != veh {
			continue
		}
		n, err := r.VarInt()
		if err != nil {
			continue
		}
		if wantRider && n > 0 || !wantRider && n == 0 {
			return true
		}
	}
	return false
}

// ejectSeen is the per-packet arm: set_passengers on veh carrying an
// empty passenger list.
func ejectSeen(body []byte, veh int32) bool {
	r := protocol.NewPacketReader(body[1:])
	if v, err := r.VarInt(); err != nil || v != veh {
		return false
	}
	n, err := r.VarInt()
	return err == nil && n == 0
}

// flagsByte18 reports whether a set_entity_data body carries the
// AbstractHorse DATA_ID_FLAGS entry — idx18 byte — with bit 0x20 set
// (the setStanding pulse).
func flagsByte18(body []byte) bool {
	for i := 1; i+2 < len(body); i++ {
		if body[i] == 18 && body[i+1] == 0x00 && body[i+2]&0x20 != 0 {
			return true
		}
	}
	return false
}

// visibleChat flattens a chat packet's raw bytes to the visible text:
// every NBT string payload named "text" concatenated in order. The
// wire form is tag 0x08 + name-len + "text" + value-len(2B) + bytes.
func visibleChat(line string) string {
	raw := []byte(line)
	var b strings.Builder
	for i := 0; i+6 < len(raw); i++ {
		if string(raw[i:i+4]) != "text" {
			continue
		}
		n := int(raw[i+4])<<8 | int(raw[i+5])
		if i+6+n > len(raw) {
			continue
		}
		b.Write(raw[i+6 : i+6+n])
	}
	return b.String()
}

// TestEquineRideTameParity pins RunAroundLikeCrazyGoal: mounting an
// untamed adult bareback runs the buck loop — 1/25-per-tick temper
// roll, fail → modifyTemper(5) + ejectPassengers + makeMad + event 6;
// pass → tameWithName (event 7, Tame flag, Owner reference).
// Temper:0 always fails the first roll; Temper:100 always passes it.
func TestEquineRideTameParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag+"_buck", func(t *testing.T) {
			res := rideTameObserve(t, tc.tag, tc.addr, 0)
			if !res.ejected {
				t.Fatalf("%s: Temper:0 horse never bucked the rider — want ejectPassengers", tc.tag)
			}
			if !res.event6 {
				t.Fatalf("%s: no entity_event(6) taming-failed smoke", tc.tag)
			}
			if !res.angry {
				t.Fatalf("%s: no entity.horse.angry makeMad sound", tc.tag)
			}
			if !res.standFlag {
				t.Fatalf("%s: no DATA_ID_FLAGS 0x20 standing pulse", tc.tag)
			}
			if !strings.Contains(res.data, "Temper5") {
				t.Fatalf("%s: Temper want 5 after one buck, got %q", tc.tag, res.data)
			}
			if !strings.Contains(res.data, "Tame0b") {
				t.Fatalf("%s: horse should stay untamed, got %q", tc.tag, res.data)
			}
		})
		t.Run(tc.tag+"_tame", func(t *testing.T) {
			res := rideTameObserve(t, tc.tag, tc.addr, 100)
			if res.ejected {
				t.Fatalf("%s: Temper:100 horse bucked — nextInt(100)<100 must always tame", tc.tag)
			}
			if !res.event7 {
				t.Fatalf("%s: no entity_event(7) tamed hearts", tc.tag)
			}
			if res.event6 {
				t.Fatalf("%s: taming-failed smoke on a guaranteed tame roll", tc.tag)
			}
			if !strings.Contains(res.data, "Tame1b") {
				t.Fatalf("%s: horse should be tamed, got %q", tc.tag, res.data)
			}
			if !strings.Contains(res.data, "OwnerI") {
				t.Fatalf("%s: tame should write the Owner reference, got %q", tc.tag, res.data)
			}
		})
	}
}
