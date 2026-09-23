package blackbox

// TestEffectMobParity pins /effect give|clear on world entities:
// vanilla resolves LivingEntity targets (players and mobs share the
// same addEffect/update/hiddenEffect semantics), instant_health and
// instant_damage flip arms for the inverted_healing_and_harm tag, and
// mob effect changes surface to trackers as entity-data + attribute
// resends (vanilla sends update_mob_effect to passengers only).

import (
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestEffectMobParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	husk := "@e[type=minecraft:husk,limit=1]"
	pig := "@e[type=minecraft:pig,limit=1]"

	// Each leg: command, target selector, absolute Health after it.
	// instant_damage heals undead (4<<amp), instant_health hurts them
	// (6<<amp); on normal mobs the arms flip back.
	legs := []struct {
		cmd, sel string
		want     float64
	}{
		// generic_kill bypasses armor — vanilla can roll armor onto a
		// summoned husk, which would jitter the generic baseline.
		{"damage " + husk + " 4 minecraft:generic_kill", husk, 16},
		{"effect give " + husk + " minecraft:instant_damage", husk, 20},
		{"effect give " + husk + " minecraft:instant_health", husk, 14},
		{"damage " + pig + " 2 minecraft:generic_kill", pig, 8},
		{"effect give " + pig + " minecraft:instant_damage", pig, 2},
		{"effect give " + pig + " minecraft:instant_health", pig, 6},
	}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "A")
			defer ca.Close()
			cc, prc, pwc := joinPlayBlackbox(t, s.addr, "C")
			defer cc.Close()
			sinkC := newChatSinkTP(prc, pwc)
			sendCommand(t, pwc, "tp A 0.5 -60 0.5")
			summonNoAI(t, sinkC, pwc, "minecraft:husk", husk, 4.5, -60, 4.5)
			pigEid := summonTracked(t, ca, pra, pwA, pwc, "minecraft:pig", 8.5, -60, 8.5)
			time.Sleep(1400 * time.Millisecond) // past summon-time invulnerability
			for _, leg := range legs {
				sendCommand(t, pwc, leg.cmd)
				time.Sleep(1300 * time.Millisecond) // clear the hurt cooldown
				st, got := dataGetSel(t, sinkC, pwc, leg.sel, "Health")
				if st != "query" || math.Abs(got-leg.want) > 0.01 {
					t.Fatalf("%s %q: %s/%v, want query/%v", s.label, leg.cmd, st, got, leg.want)
				}
			}

			// Effect on a tracked mob: vanilla's onEffectAdded sends
			// update_mob_effect to passengers only — a tracker sees the
			// entity-data particle resend and an update_attributes with
			// the effect.speed modifier (0.2*(amplifier+1)
			// add_multiplied_total). hiddenEffect still downgrades: a
			// 30s amp-0 + 3s amp-1 pair resurfaces amp 0's modifier when
			// the short instance expires.
			effObs := &mobEffObs{eid: pigEid}
			sendCommand(t, pwc, "effect give "+pig+" minecraft:speed 30 0")
			drainMobEff(t, ca, pra, pwA, 600*time.Millisecond, effObs)
			sendCommand(t, pwc, "effect give "+pig+" minecraft:speed 3 1")
			drainMobEff(t, ca, pra, pwA, 900*time.Millisecond, effObs)
			if math.Abs(effObs.speedMod-0.4) > 0.001 {
				t.Fatalf("%s pig speed modifier at amp1: %v, want 0.4", s.label, effObs.speedMod)
			}
			drainMobEff(t, ca, pra, pwA, 3000*time.Millisecond, effObs)
			if math.Abs(effObs.speedMod-0.2) > 0.001 {
				t.Fatalf("%s pig speed modifier after downgrade: %v, want 0.2", s.label, effObs.speedMod)
			}
			if effObs.effPkts != 0 {
				t.Fatalf("%s mob effect packets to tracker: %d, want 0 (passengers only)", s.label, effObs.effPkts)
			}
			// The stored instance survives on the pig (the zombie's
			// instant instances expired after their 1-tick duration);
			// clearing it resends movement_speed bare.
			sendCommand(t, pwc, "effect clear "+pig+" minecraft:speed")
			drainMobEff(t, ca, pra, pwA, 900*time.Millisecond, effObs)
			if effObs.speedMod != -2 {
				t.Fatalf("%s pig speed modifier after clear: %v, want -2 (attr bare)", s.label, effObs.speedMod)
			}
			time.Sleep(1500 * time.Millisecond) // instant instances long expired
			if st, _ := dataGetSel(t, sinkC, pwc, pig, "active_effects"); st != "nothing_found" {
				t.Fatalf("%s pig active_effects after clear: %q, want nothing_found", s.label, st)
			}
			if st := effectClearState(t, sinkC, pwc, pig); st != "everything.failed" {
				t.Fatalf("%s clear-empty reply: %q, want everything.failed", s.label, st)
			}
		})
	}
}

// summonNoAI runs /summon with a NoAI tag and polls /data get on the
// typed selector until the entity answers — summonAt plus NBT.
func summonNoAI(t *testing.T, sink *chatSink, pw *protocol.Writer, mob, sel string, x, y, z float64) {
	t.Helper()
	sendCommand(t, pw, fmt.Sprintf("summon %s %v %v %v {NoAI:1b}", mob, x, y, z))
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := dataGetSel(t, sink, pw, sel, "Health"); st == "query" {
			return
		}
	}
	t.Fatalf("%s never answered Health", sel)
}

// dataGetSel is dataGetAmp with an explicit entity selector.
func dataGetSel(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) (string, float64) {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel+" "+path)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	st, val := "", -1.0
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if i := strings.Index(line, "data.entity.query"); i >= 0 {
				st, val = "query", lastNumber(line[:i])
			}
			if strings.Contains(line, "nbtpath.nothing_found") {
				st = "nothing_found"
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
	return st, val
}

// summonTracked sends /summon from the op conn and drains the tracker
// conn until the add_entity packet for the mob's type arrives —
// returning that entity id. Retries because cold-chunk tracking can lag.
func summonTracked(t *testing.T, c net.Conn, pr *protocol.Reader, pw, pwc *protocol.Writer, mob string, x, y, z float64) int32 {
	t.Helper()
	return summonTrackedNBT(t, c, pr, pw, pwc, mob, x, y, z, "{NoAI:1b}")
}

// summonTrackedNBT is summonTracked with explicit summon NBT.
func summonTrackedNBT(t *testing.T, c net.Conn, pr *protocol.Reader, pw, pwc *protocol.Writer, mob string, x, y, z float64, nbt string) int32 {
	t.Helper()
	typeID, ok := server.EntityTypeID(strings.TrimPrefix(mob, "minecraft:"))
	if !ok {
		t.Fatalf("unknown entity type %s", mob)
	}
	for tries := 0; tries < 8; tries++ {
		sendCommand(t, pwc, fmt.Sprintf("summon %s %v %v %v %s", mob, x, y, z, nbt))
		deadline := time.Now().Add(2500 * time.Millisecond)
		found := int32(-1)
		for time.Now().Before(deadline) && found < 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == typeID {
					found = eid
				}
			}
		}
		if found >= 0 {
			_ = c.SetReadDeadline(time.Time{})
			return found
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	t.Fatalf("no %s spawn packet", mob)
	return 0
}

// mobEffObs folds the tracker-visible effect surface for one mob:
// the last speed-modifier amount seen on its update_attributes
// (attribute id 26) and a count of update/remove_mob_effect packets
// addressed to it — vanilla sends those to passengers only.
type mobEffObs struct {
	eid      int32
	speedMod float64 // -1 unseen, -2 attr entry present with no speed mod
	effPkts  int
}

// drainMobEff reads the tracker conn for d, answering keepalives /
// teleports / chunk batches, and folds update_attributes + mob-effect
// packets for obs.eid.
func drainMobEff(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, obs *mobEffObs) {
	t.Helper()
	if obs.speedMod == 0 {
		obs.speedMod = -1
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlayUpdateMobEffect, server.IDPlayRemoveMobEffect:
			r := protocol.NewPacketReader(p.Data)
			if eid, err := r.VarInt(); err == nil && eid == obs.eid {
				obs.effPkts++
			}
		case server.IDPlayUpdateAttributes:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil || eid != obs.eid {
				continue
			}
			n, err := r.VarInt()
			if err != nil {
				continue
			}
			for i := int32(0); i < n; i++ {
				attrID, _ := r.VarInt()
				if _, err := r.Double(); err != nil {
					break
				}
				mods, err := r.VarInt()
				if err != nil {
					break
				}
				seen := false
				for j := int32(0); j < mods; j++ {
					key, _ := r.String()
					amount, _ := r.Double()
					if _, err := r.VarInt(); err != nil {
						break
					}
					if attrID == 26 && key == "minecraft:effect.speed" {
						obs.speedMod = amount
						seen = true
					}
				}
				if attrID == 26 && !seen {
					obs.speedMod = -2
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
}

// effectClearState classifies the reply to `effect clear <sel>` (no
// effect arg) by its translate-key tail.
func effectClearState(t *testing.T, sink *chatSink, pw *protocol.Writer, sel string) string {
	t.Helper()
	sendCommand(t, pw, "effect clear "+sel)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if strings.Contains(line, "clear.everything.failed") {
				got = "everything.failed"
			}
			if strings.Contains(line, "clear.everything.success") {
				got = "everything.success"
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
	return got
}
