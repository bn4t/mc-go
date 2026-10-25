package server

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// The player attack path passes a detached Manager.Get value to damageMob.
// A lethal first hit must use the newly committed player credit, both for XP
// packets and for killed statistics, including replacement of older credit.
func TestFirstLethalMobAttackUsesCurrentPlayerCredit(t *testing.T) {
	for _, priorCredit := range []bool{false, true} {
		name := "first_player_hit"
		if priorCredit {
			name = "replaces_previous_player"
		}
		t.Run(name, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			s.GameMode = GameModeSurvival
			killer := placeSession(t, s, "LethalPlayer", 0.5, -60, 0.5)
			// placeSession's single-viewer fixtures use id zero; retain both
			// real admissions in the session map for UUID credit resolution.
			s.sessions.Delete(killer.id)
			killer.id = s.nextID.Add(1)
			s.track(killer)
			previous := placeSession(t, s, "PriorPlayer", 0.5, -60, 0.5)
			killer.spawned.Store(true)
			previous.spawned.Store(true)
			killer.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["netherite_axe"], Count: 1}
			killer.attackTicker = 200
			pig, ok := s.summonMob(s.dims[dimOverworld], "pig", 1.5, -60, 0.5, false)
			if !ok {
				t.Fatal("summon failed")
			}
			if priorCredit {
				pig.LastHurtByPlayerTicks = 70
				pig.LastHurtByPlayerUUID = previous.getUUID()
			}
			for len(killer.sendCh) != 0 {
				materializeOutboundPacket(t, killer, <-killer.sendCh)
			}
			s.simulationAction(func() { s.attackEntity(killer, pig.ID) })
			if !pig.Dying || pig.Health != 0 || len(pig.dataSync) == 0 || pig.LastHurtByPlayerTicks != 100 || pig.LastHurtByPlayerUUID != killer.getUUID() {
				t.Fatalf("lethal state: dying=%v health=%v credit=%d/%x", pig.Dying, pig.Health, pig.LastHurtByPlayerTicks, pig.LastHurtByPlayerUUID)
			}
			var orb *Entity
			s.entities.mu.RLock()
			for _, e := range s.entities.byID {
				if e.Type == mustEntityType("experience_orb") {
					orb = e
				}
			}
			s.entities.mu.RUnlock()
			if orb == nil || orb.OrbValue < 1 || orb.OrbValue > 3 {
				t.Fatal("first lethal player hit did not spawn pig XP")
			}
			key := statKey{statKilled, pig.Type}
			if killer.ensureStats().counts[key] != 1 || previous.ensureStats().counts[key] != 0 {
				t.Fatalf("kill credited to wrong player: killer=%d previous=%d", killer.ensureStats().counts[key], previous.ensureStats().counts[key])
			}
			spawned, metadata := false, false
			for len(killer.sendCh) != 0 {
				packet := materializeOutboundPacket(t, killer, <-killer.sendCh)
				r := protocol.NewPacketReader(packet.data)
				id, err := r.VarInt()
				if err != nil || id != orb.ID {
					continue
				}
				if packet.id == killer.ids().PlaySpawnEntity {
					if _, err := r.UUID(); err != nil {
						t.Fatal(err)
					}
					typ, err := r.VarInt()
					spawned = err == nil && typ == orb.Type
				}
				if packet.id == killer.ids().PlayEntityMetadata {
					index, _ := r.Byte()
					typ, _ := r.VarInt()
					value, _ := r.VarInt()
					metadata = index == 8 && typ == 1 && value == orb.OrbValue
				}
			}
			if !spawned || !metadata {
				t.Fatalf("first lethal hit XP packets: spawn=%v value metadata=%v", spawned, metadata)
			}
		})
	}
}
