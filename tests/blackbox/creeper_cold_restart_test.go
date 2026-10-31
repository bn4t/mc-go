//go:build blackbox

package blackbox

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type coldCreeperWire struct {
	t                 *testing.T
	p                 *scheduledProbeBB
	ids               map[int]int32
	meta              map[int]map[int]metaEnt
	removed, exploded map[int]int
	power             map[int]float32
	typ               int32
	uuids             map[int][16]byte
	primed            int
}

func (w *coldCreeperWire) consume(lines []string) {
	for _, sound := range aniParseSounds(lines) {
		if sound.snd == 481 && sound.src == 5 && sound.x == 2.5 && sound.y == 100 && sound.z == 0.5 {
			w.primed++
		}
	}
	for _, line := range lines {
		if body, ok := gravityPacketBody(line, server.IDPlaySpawnEntity); ok {
			r := protocol.NewPacketReader(body)
			id, e1 := r.VarInt()
			uuid, e2 := r.UUID()
			typ, e3 := r.VarInt()
			x, e4 := r.Double()
			y, e5 := r.Double()
			z, e6 := r.Double()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && typ == w.typ && y == 100 && z == 0.5 {
				for i := 0; i < 4; i++ {
					if x == float64(2*i)+2.5 {
						w.ids[i] = id
						w.uuids[i] = uuid
					}
				}
			}
		}
		if body, ok := gravityPacketBody(line, server.IDPlayEntityMetadata); ok {
			r := protocol.NewPacketReader(body)
			id, _ := r.VarInt()
			for i, eid := range w.ids {
				if id == eid {
					rest, _ := r.Read(r.Remaining())
					if w.meta[i] == nil {
						w.meta[i] = map[int]metaEnt{}
					}
					for k, v := range decodeMetaEntries(w.t, rest) {
						w.meta[i][k] = v
					}
				}
			}
		}
		if body, ok := gravityPacketBody(line, server.IDPlayRemoveEntity); ok {
			r := protocol.NewPacketReader(body)
			n, _ := r.VarInt()
			for j := int32(0); j < n; j++ {
				id, err := r.VarInt()
				if err != nil {
					w.t.Fatal(err)
				}
				for i, eid := range w.ids {
					if id == eid {
						w.removed[i]++
					}
				}
			}
		}
		if body, ok := gravityPacketBody(line, 0x24); ok {
			r := protocol.NewPacketReader(body)
			x, e1 := r.Double()
			y, e2 := r.Double()
			z, e3 := r.Double()
			power, e4 := r.Float()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil && y == 100 && z == 0.5 {
				for i := 0; i < 4; i++ {
					if x == float64(2*i)+2.5 {
						w.exploded[i]++
						w.power[i] = power
					}
				}
			}
		}
	}
}
func (w *coldCreeperWire) command(cmd string) []string {
	lines := w.p.command(cmd)
	w.consume(lines)
	return lines
}
func (w *coldCreeperWire) age() int64 {
	for _, line := range w.command("time query gametime") {
		key, args, ok := componentArgs(line)
		if ok && key == "commands.time.query.gametime" && len(args) > 0 {
			if n, ok := args[0].(int64); ok {
				return n
			}
		}
	}
	w.t.Fatal("age receipt absent")
	return 0
}
func (w *coldCreeperWire) step(n int) {
	before := w.age()
	w.command(fmt.Sprintf("tick step %d", n))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		age := w.age()
		if age == before+int64(n) {
			return
		}
		if age > before+int64(n) {
			w.t.Fatal("extra ticks", age-before)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.t.Fatal("step incomplete")
}
func (w *coldCreeperWire) expect(condition, label string) {
	if !structureSaidBB(w.command("execute "+condition+" run say "+label), label) {
		w.t.Errorf("missing %s: %s", label, condition)
	}
}
func (w *coldCreeperWire) tracked(i int) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := w.meta[i][9]; ok {
			return
		}
		w.consume(w.p.sink.take())
		time.Sleep(10 * time.Millisecond)
	}
	w.t.Fatal("health tracking absent", i, w.ids)
}
func (w *coldCreeperWire) retired(i int, power float32) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w.command("say COLD_SETTLED")
		if w.removed[i] == 1 && w.exploded[i] == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w.removed[i] != 1 || w.exploded[i] != 1 || w.power[i] != power {
		w.t.Errorf("body%d removal/explosion/power=%d/%d/%v want1/1/%v", i, w.removed[i], w.exploded[i], w.power[i], power)
	}
}
func TestCreeperColdRestartParity(t *testing.T) {
	jar := blackboxJar(t)
	name := "ColdCreeper"
	pack := map[string]string{
		"world/datapacks/creepcold/pack.mcmeta":                             `{"pack":{"description":"creeper cold freeze","min_format":107,"max_format":107}}`,
		"world/datapacks/creepcold/data/minecraft/tags/function/load.json":  `{"values":["creepcold:load"]}`,
		"world/datapacks/creepcold/data/creepcold/function/load.mcfunction": "tick freeze\nscoreboard objectives add coldBoot dummy\nscoreboard players add #boots coldBoot 1\n"}
	start := func(t *testing.T, kind, dir string) (string, func(), *server.Server) {
		port, ok := freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		if kind == "native" {
			files := map[string]string{"ops.json": opsJSONFor(name)}
			for p, v := range pack {
				files[p] = v
			}
			addr, say, wait := vanillaConsole(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2", "function-permission-level": "4"}, files, dir)
			var once sync.Once
			return addr, func() {
				once.Do(func() {
					say("stop")
					if code := wait(); code != 0 {
						t.Errorf("native stop exit%d", code)
					}
				})
			}, nil
		}
		var core *server.Server
		addr := startMcGoCfg(t, port, func(s *server.Server) {
			core = s
			s.WorldDir = filepath.Join(dir, "world")
			s.ServerDir = dir
			s.ViewDistance = 2
			s.SimulationDistance = 2
			s.SetOpLevel(name, 4)
			if err := s.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			s.SetFrozen(true)
		})
		return addr, core.Stop, core
	}
	join := func(t *testing.T, addr string) (*coldCreeperWire, func()) {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		typ, _ := server.EntityTypeID("creeper")
		w := &coldCreeperWire{t: t, typ: typ, ids: map[int]int32{}, uuids: map[int][16]byte{}, meta: map[int]map[int]metaEnt{}, removed: map[int]int{}, exploded: map[int]int{}, power: map[int]float32{}}
		// Existing entities may precede the first attribute readiness packet.
		// Keep their complete pairing burst instead of draining it away.
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		ready := false
		for !ready {
			packet, err := pr.ReadPacket()
			if err != nil {
				t.Fatal("cold readiness", err)
			}
			switch packet.ID {
			case server.IDPlaySpawnEntity, server.IDPlayEntityMetadata, server.IDPlayRemoveEntity, 0x24:
				w.consume([]string{fmt.Sprintf("%#x:", packet.ID) + string(packet.Data)})
			case server.IDPlayKeepAliveCB:
				if err := pw.WritePacket(server.IDPlayKeepAliveSB, packet.Data); err != nil {
					t.Fatal(err)
				}
			case server.IDPlaySyncPos:
				id, err := protocol.NewPacketReader(packet.Data).VarInt()
				if err != nil {
					t.Fatal(err)
				}
				var ack protocol.Buffer
				ack.VarInt(id)
				if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes()); err != nil {
					t.Fatal(err)
				}
			case server.IDPlayChunkBatchDone:
				if err := ackChunkBatch(pw); err != nil {
					t.Fatal(err)
				}
			case server.IDPlayUpdateAttributes:
				ready = true
			}
		}
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkResolvedTP(pr, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		w.p = &scheduledProbeBB{t: t, pw: pw, sink: sink}

		w.command("tick freeze")
		w.command("tp @s 0.5 100 4.5")
		ready = false
		deadline := time.Now().Add(10 * time.Second)
		for !ready && time.Now().Before(deadline) {
			var move protocol.Buffer
			move.Double(0.75)
			move.Double(100)
			move.Double(4.5)
			move.Byte(1)
			if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			_ = pw.WritePacket(server.IDPlayClientTickEndSB, nil)
			ready = structureSaidBB(w.command(`execute if entity @s[nbt={Pos:[0.75d,100d,4.5d]}] run say COLD_MOVE_READY`), "COLD_MOVE_READY")
		}
		if !ready {
			t.Fatal("authoritative actor movement receipt absent")
		}
		setupPlayerTickBarrierBB(t, pw)
		waitPlayerTicksBB(t, pw, func(marker string) []string {
			lines := waitForExactSayBB(t, sink, marker, 10*time.Second)
			w.consume(lines)
			return lines
		}, "cold_admit", 2)

		return w, func() { c.Close() }
	}
	readRows := func(t *testing.T, dir, witness string) map[int]map[string]any {
		out := map[int]map[string]any{}
		var lastErr error
		deadline := time.Now().Add(15 * time.Second)
		for {
			rows, err := world.LoadEntityRegions(world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld"))
			lastErr = err
			if err == nil {

				out = map[int]map[string]any{}
				for _, row := range rows {
					tags, _ := row["Tags"].(nbt.List)
					fresh := false
					index := -1
					for _, v := range tags.Items {
						if v == witness {
							fresh = true
						}
						for i := 0; i < 4; i++ {
							if v == fmt.Sprintf("Cold%d", i) {
								index = i
							}
						}
					}
					if index >= 0 && fresh {
						out[index] = row
					}
				}
			}
			if len(out) == 4 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fresh complete four-body disk generation absent", lastErr, out)
			}
			time.Sleep(20 * time.Millisecond)
		}

		paths, _ := filepath.Glob(filepath.Join(world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld"), "entities", "r.*.mca"))
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("saved bytes%s sha256%x", filepath.Base(p), sha256.Sum256(raw))
			if evidence := os.Getenv("MCGO_COLD_EVIDENCE_DIR"); evidence != "" {
				if err := os.MkdirAll(evidence, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(evidence, witness+"-"+filepath.Base(p)), raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		for i, row := range out {
			t.Logf("saved row%d Fuse=%#v Radius=%#v powered=%#v ignited=%#v Swell=%#v", i, row["Fuse"], row["ExplosionRadius"], row["powered"], row["ignited"], row["Swell"])
		}
		return out
	}
	source := t.TempDir()
	addr, stop, _ := start(t, "native", source)
	defer stop()
	w, closeSource := join(t, addr)
	w.expect("if score #boots coldBoot matches 1", "SOURCE_BOOT")
	w.command("gamerule minecraft:spawn_mobs false")
	w.command("gamerule minecraft:mob_griefing false")
	// Admit the empty source arena before creating the measured bodies.
	w.step(1)
	w.command(`summon minecraft:creeper 2.5 100 0.5 {Tags:["Cold0"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:5s,ExplosionRadius:-1b,ignited:1b,powered:1b}`)
	w.command(`summon minecraft:creeper 4.5 100 0.5 {Tags:["Cold1"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:7s,ExplosionRadius:0b}`)
	w.tracked(0)
	w.tracked(1)
	w.step(1)
	firstTickAge := w.age()
	deadline := time.Now().Add(10 * time.Second)
	for w.primed == 0 && time.Now().Before(deadline) {
		w.command("say PRIME_SOURCE")
		time.Sleep(10 * time.Millisecond)
	}
	if w.primed != 1 || w.age() != firstTickAge {
		t.Fatal("first source entity tick prime receipt absent", w.primed)
	}

	w.step(1)
	w.command(`data merge entity @e[tag=Cold0,limit=1] {ignited:0b,powered:0b}`)
	w.expect("if entity @e[tag=Cold0,nbt={ignited:1b},limit=1]", "IGNITION_NOT_CLEARED")
	w.expect("if entity @e[tag=Cold0,nbt={powered:0b},limit=1]", "POWER_CLEARED")
	w.command(`data merge entity @e[tag=Cold0,limit=1] {powered:1b}`)
	w.command(`data remove entity @e[tag=Cold0,limit=1] powered`)
	w.expect("if entity @e[tag=Cold0,nbt={powered:0b},limit=1]", "POWER_ABSENT_DEFAULT")
	w.command(`data remove entity @e[tag=Cold0,limit=1] ignited`)
	w.expect("if entity @e[tag=Cold0,nbt={ignited:1b},limit=1]", "IGNITION_ABSENT_STICKY")
	w.command(`data merge entity @e[tag=Cold0,limit=1] {powered:1b}`)

	w.command(`summon minecraft:creeper 6.5 100 0.5 {Tags:["Cold2"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:-1s,ExplosionRadius:-128b,powered:1b}`)
	w.command(`summon minecraft:creeper 8.5 100 0.5 {Tags:["Cold3"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:0s,ExplosionRadius:0b,ignited:1b}`)
	w.tracked(2)
	w.tracked(3)
	w.command("tag @e[type=minecraft:creeper] add SavedAfterTwo")
	for i := 0; i < 4; i++ {
		w.expect(fmt.Sprintf("if entity @e[tag=Cold%d,tag=SavedAfterTwo,limit=1]", i), fmt.Sprintf("FRESH_TAG%d", i))
	}
	savedAge := w.age()
	w.command("save-all flush")
	openPaths, _ := filepath.Glob(filepath.Join(world.DimensionDir(filepath.Join(source, "world"), "minecraft:overworld"), "entities", "r.*.mca"))
	for _, p := range openPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("open native region%s bytes%d", filepath.Base(p), len(raw))
		if evidence := os.Getenv("MCGO_COLD_EVIDENCE_DIR"); evidence != "" {
			_ = os.MkdirAll(evidence, 0755)
			if err := os.WriteFile(filepath.Join(evidence, "OpenBeforeClose-"+filepath.Base(p)), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	closeSource()
	stop()
	sourceRows := readRows(t, source, "SavedAfterTwo")
	expectedPowered := []int8{1, 0, 1, 0}
	expectedIgnited := []int8{1, 0, 0, 1}
	for i, row := range sourceRows {
		if row["powered"] != expectedPowered[i] || row["ignited"] != expectedIgnited[i] {
			t.Fatal("native saved flag presence/types differ", i, row["powered"], row["ignited"])
		}
		if _, present := row["Swell"]; present {
			t.Fatal("elapsed swell unexpectedly saved")
		}
	}

	if evidence := os.Getenv("MCGO_COLD_EVIDENCE_DIR"); evidence != "" {
		copyTree(t, filepath.Join(source, "world"), filepath.Join(evidence, "native-world"))
	}
	snapshot := t.TempDir()
	copyTree(t, source, snapshot)
	kinds := []string{"native", "go"}
	if os.Getenv("MCGO_COLD_ENTITY_EXPORT_FILE") != "" {
		kinds = append(kinds, "native-export")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			copyTree(t, snapshot, target)
			expectedRows := sourceRows
			runtimeKind := kind
			if kind == "native-export" {
				runtimeKind = "native"
				raw, err := os.ReadFile(os.Getenv("MCGO_COLD_ENTITY_EXPORT_FILE"))
				if err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(world.DimensionDir(filepath.Join(target, "world"), "minecraft:overworld"), "entities", "r.0.0.mca")
				if err := os.WriteFile(dest, raw, 0644); err != nil {
					t.Fatal(err)
				}
				expectedRows = readRows(t, target, "SavedAfterTwo")
				for _, row := range expectedRows {
					if row["Air"] != int16(299) {
						t.Fatal("fresh Go entity-only export witness absent")
					}
				}
			}
			addr, stop, core := start(t, runtimeKind, target)
			defer stop()
			v, close := join(t, addr)
			defer close()
			v.t = t
			v.p.t = t
			if runtimeKind == "native" {
				v.expect("if score #boots coldBoot matches 2", "COLD_BOOT")
			}
			for i := 0; i < 4; i++ {
				v.tracked(i)
			}
			for i, row := range expectedRows {
				uuidInts := row["UUID"].([]int32)
				var expected [16]byte
				for j, n := range uuidInts {
					binary.BigEndian.PutUint32(expected[j*4:], uint32(n))
				}
				if v.uuids[i] != expected {
					t.Error("cold UUID binding differs", i, v.uuids[i], expected)
				}
				for _, field := range []struct {
					index int
					want  int8
				}{{17, expectedPowered[i]}, {18, expectedIgnited[i]}} {
					entry, present := v.meta[i][field.index]
					if field.want == 1 {
						if !present || entry.typ != 8 || len(entry.val) != 1 || entry.val[0] != 1 {
							t.Error("restored true flag metadata missing", i, field.index, entry)
						}
					} else if present {
						t.Error("default false flag metadata emitted", i, field.index, entry)
					}
				}
			}

			t.Logf("source savedAge%d cold admittedAge%d", savedAge, v.age())
			for i := 0; i < 4; i++ {
				row := expectedRows[i]
				fuse := row["Fuse"].(int16)
				radius := row["ExplosionRadius"].(int8)
				v.expect(fmt.Sprintf(`if entity @e[tag=Cold%d,nbt={Fuse:%ds,ExplosionRadius:%db},limit=1]`, i, fuse, radius), fmt.Sprintf("LOADED_FIELDS%d", i))
			}
			if core != nil && os.Getenv("MCGO_COLD_FULL_SAVE_DIAGNOSTIC") == "1" {
				v.command("tag @e[type=minecraft:creeper] add GoSaveWitness")
				age := v.age()
				err := core.SaveWorld()
				t.Logf("SEPARATE four-body full-world native-copy SaveWorld diagnostic: %v", err)
				if v.age() != age {
					t.Fatal("full save diagnostic advanced frozen age")
				}
			}

			v.step(1)
			v.retired(2, -256)
			v.retired(3, 0)
			if v.removed[0] != 0 || v.exploded[0] != 0 {
				t.Fatal("positive fuse expired on cold admission")
			}
			v.step(3)
			v.expect("if entity @e[tag=Cold0,limit=1]", "RESET_COUNTER_ALIVE_AT4")
			if v.removed[0] != 0 || v.exploded[0] != 0 {
				t.Fatal("elapsed swell retained across restart")
			}
			v.step(1)
			v.retired(0, -2)
			v.step(2)
			v.expect("if entity @e[tag=Cold1,limit=1]", "UNIGNITED_SURVIVES7")
			if core != nil && os.Getenv("MCGO_COLD_FULL_SAVE_DIAGNOSTIC") == "1" {
				err := core.SaveWorld()
				t.Logf("SEPARATE full-world native-copy SaveWorld diagnostic: %v (entity-only export verified separately)", err)
			}

		})
	}
}
