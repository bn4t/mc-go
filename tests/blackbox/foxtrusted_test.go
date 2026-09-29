//go:build blackbox

// Fox.FoxBreedGoal.breed() — the trusted-list runtime path,
// measured on the vanilla 26.2 jar (Fox.java):
//
//   - isFood is ItemTags.FOX_FOOD (sweet_berries/glow_berries) —
//     Animal's interact sets inLove + loveCause to the feeder.
//   - breed() gets the offspring, then addTrustedEntity(
//     animal.getLoveCause()) — the feeder's EntityReference — and
//     addTrustedEntity(partner.getLoveCause()) when it is a
//     different player. addTrustedEntity fills slot0 then slot1.
//   - DATA_TRUSTED_ID_0/1 (metadata 20/21,
//     optional_living_entity_reference serializer 13) pack only
//     populated slots: bool present + the 16-byte uuid. On an
//     offline-mode server the feeder uuid is the deterministic
//     OfflinePlayer:<name> uuid — identical on both legs.
//
// Pinned here: two different players feed one fox each; the bred
// baby's metadata carries both uuids across indices 20 and 21.
package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFoxTrustedBreedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	uuidA := server.OfflineUUID("BOATA")
	uuidB := server.OfflineUUID("BOATB")

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "foxtrusted", v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "fill 2 -61 6 12 -61 12 minecraft:stone")
		sendCommand(t, v.pwa, "tp BOATA 4.5 -60 10 0 0")
		sendCommand(t, v.pwa, "tp BOATB 8.5 -60 10 0 0")
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:sweet_berries")
		sendCommand(t, v.pwa, "item replace entity BOATB weapon.mainhand with minecraft:sweet_berries")
		var hb protocol.Buffer
		hb.Short(0)
		_ = v.pwa.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())
		_ = v.pwb.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())

		fox1 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "fox",
			4.0, -60, 8.0, "{PersistenceRequired:1b}")
		fox2 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "fox",
			8.0, -60, 8.0, "{PersistenceRequired:1b}")
		if fox1 == 0 || fox2 == 0 {
			t.Fatalf("foxes not tracked (f1=%d f2=%d)", fox1, fox2)
		}

		// Each player feeds its own fox — the baby's trusted slots
		// fill with both feeder uuids.
		feed := func(pw *protocol.Writer, eid int32) {
			var ib protocol.Buffer
			ib.VarInt(eid)
			ib.VarInt(0) // interact
			ib.VarInt(0) // main hand
			ib.Bool(false)
			_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		}
		feed(v.pwa, fox1)
		feed(v.pwb, fox2)

		trusted := map[[16]byte]bool{}
		idx20, idx21 := false, false
		win := time.Now().Add(20 * time.Second)
		for time.Now().Before(win) && !(idx20 && idx21) {
			o := vehWatch(t, v, false, fox1, 700*time.Millisecond)
			for key, u := range o.uuidMeta {
				if key[1] == 20 {
					idx20 = true
				}
				if key[1] == 21 {
					idx21 = true
				}
				trusted[u] = true
			}
		}
		if !idx20 {
			t.Fatalf("DATA_TRUSTED_ID_0 never populated on the baby fox")
		}
		if !idx21 {
			t.Fatalf("DATA_TRUSTED_ID_1 never populated — both feeders' uuids should land")
		}
		if !trusted[uuidA] || !trusted[uuidB] || len(trusted) != 2 {
			t.Fatalf("trusted uuids = %v; want the two feeder uuids", trusted)
		}
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}
