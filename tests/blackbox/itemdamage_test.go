package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Official 26.2 item health counter, resistance and zero-health load pin.
func TestItemDamageParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("ItemHurt")}, nil)
	defer targets.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, "ItemHurt")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) {
			sendCommand(t, pw, cmd)
			serial++
			marker := fmt.Sprintf("ITEMHURT_BARRIER_%d", serial)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("command timeout: %s", cmd)
		}
		get := func(path string) float64 {
			serial++
			marker := fmt.Sprintf("ITEMHURT_QUERY_%d", serial)
			sendCommand(t, pw, "data get entity @e[tag=itemhurt,limit=1] "+path)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(10 * time.Second)
			reply := ""
			for time.Now().Before(deadline) {
				ended := false
				for _, line := range sink.take() {
					if strings.Contains(line, "data.entity.query") {
						reply = line
					}
					if strings.Contains(line, marker) {
						ended = true
					}
				}
				if ended {
					if i := strings.Index(reply, "data.entity.query"); i >= 0 {
						return lastNumber(reply[:i])
					}
					t.Fatalf("no query reply for %s", path)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("query timeout %s", path)
			return -1
		}

		command("tick freeze")
		command("setblock 0 69 0 stone")
		command("tp @s 0.5 70 0.5")
		command(`summon item 2.5 72 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		out := []float64{get("Health")}
		command("damage @e[tag=itemhurt,limit=1] 0.25")
		out = append(out, get("Health"))
		command("damage @e[tag=itemhurt,limit=1] 0.25")
		out = append(out, get("Health"))
		command("data merge entity @e[tag=itemhurt,limit=1] {Health:0s}")
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command(`summon item 2.5 72 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Health:0s,Item:{id:"minecraft:stone",count:1}}`)
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command(`summon item 2.5 72 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:nether_star",count:1}}`)
		command("damage @e[tag=itemhurt,limit=1] 20 minecraft:explosion")
		out = append(out, get("Health"))
		command("damage @e[tag=itemhurt,limit=1] 1 minecraft:in_fire")
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command(`summon item 2.5 72 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:netherite_ingot",count:1}}`)
		command("damage @e[tag=itemhurt,limit=1] 20 minecraft:lava")
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command("fill 2 71 0 4 74 0 air")
		command("setblock 2 71 0 netherrack")
		command("setblock 2 72 0 fire")
		command(`summon item 2.5 72.25 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		step := func() { command("tick step 1"); time.Sleep(250 * time.Millisecond) }
		step()
		out = append(out, get("Health"))
		step()
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command("setblock 2 72 0 air")
		command("setblock 3 71 0 soul_soil")
		command("setblock 3 72 0 soul_fire")
		command(`summon item 3.5 72.25 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		step()
		out = append(out, get("Health"))
		command("kill @e[tag=itemhurt]")
		command("setblock 3 72 0 air")
		command("setblock 4 70 0 stone")
		command("setblock 4 71 0 sand")
		command("setblock 4 72 0 cactus")
		command(`summon item 4.5 72.95 0.5 {Tags:["itemhurt"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		for i := 0; i < 4; i++ {
			step()
			out = append(out, get("Health"))
		}

		return out
	}
	want := run(targets.vanilla)
	got := run(targets.mcgo)
	t.Logf("vanilla %v; mcgo %v", want, got)
	if !reflect.DeepEqual(want, []float64{5, 4, 3, 0, 0, 5, 4, 5, 4, 2, 3, 4, 3, 2, 1}) {
		t.Errorf("invalid damage fixture: %v", want)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("item damage differs")
	}
}

func TestItemExplosionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("ItemBlast")}, nil)
	defer targets.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, "ItemBlast")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) {
			sendCommand(t, pw, cmd)
			serial++
			marker := fmt.Sprintf("ITEMHURT_BARRIER_%d", serial)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("command timeout: %s", cmd)
		}
		get := func(path string) float64 {
			serial++
			marker := fmt.Sprintf("ITEMHURT_QUERY_%d", serial)
			sendCommand(t, pw, "data get entity @e[tag=itemhurt,limit=1] "+path)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(10 * time.Second)
			reply := ""
			for time.Now().Before(deadline) {
				ended := false
				for _, line := range sink.take() {
					if strings.Contains(line, "data.entity.query") {
						reply = line
					}
					if strings.Contains(line, marker) {
						ended = true
					}
				}
				if ended {
					if i := strings.Index(reply, "data.entity.query"); i >= 0 {
						return lastNumber(reply[:i])
					}
					return -1
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("query timeout %s", path)
			return -1
		}

		command("tick freeze")
		command("setblock -4 69 0 stone")
		command("tp @s -3.5 70 0.5")
		command("fill 0 70 -2 6 76 2 air")
		command("fill 3 70 -2 3 76 2 stone")
		command(`summon item 1.5 72 0.5 {Tags:["itemhurt","open"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		command(`summon item 4.25 72 0.5 {Tags:["shielded"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:1}}`)
		command(`summon item 2.5 73.5 0.5 {Tags:["star"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:nether_star",count:1}}`)
		command(`summon item 1.5 72 0.5 {Tags:["spillparent"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:bundle",count:1,components:{"minecraft:bundle_contents":[{id:"minecraft:ancient_debris",count:3}]}}}`)
		if n := get("Health"); n != 5 {
			t.Fatalf("open blast target absent before explosion: %v", n)
		}
		command("tag @e[tag=open] remove itemhurt")
		command("tag @e[tag=spillparent] add itemhurt")
		if n := get("Health"); n != 5 {
			t.Errorf("source bundle health %v", n)
		}
		if n := get("Item.components"); n != 3 {
			t.Errorf("summoned bundle components missing: %v", n)
		}
		command("tag @e[tag=spillparent] remove itemhurt")
		command("tag @e[tag=open] add itemhurt")
		command(`summon tnt 2.5 72 0.5 {fuse:1,explosion_power:1f,Motion:[0d,0d,0d]}`)
		command("tick step 1")
		time.Sleep(500 * time.Millisecond)
		out := []float64{get("Health")}
		command("tag @e[tag=open] remove itemhurt")
		command("tag @e[tag=shielded] add itemhurt")
		out = append(out, get("Health"))
		command("tag @e[tag=shielded] remove itemhurt")
		command("tag @e[tag=star] add itemhurt")
		out = append(out, get("Health"))
		push := get("Motion[1]")
		if push > 0 {
			push = 1
		}
		out = append(out, push)
		command("tag @e[tag=star] remove itemhurt")
		command("tag @e[type=item,tag=!star,tag=!shielded,tag=!open,tag=!spillparent,sort=nearest,limit=1] add itemhurt")
		if n := get("Item.count"); n != 3 {
			t.Errorf("spill child count %v", n)
		}
		out = append(out, get("Health"))
		return out
	}
	want := run(targets.vanilla)
	got := run(targets.mcgo)
	t.Logf("vanilla %v; mcgo %v", want, got)
	if !reflect.DeepEqual(want, []float64{-1, 4, 5, 1, 5}) {
		t.Errorf("invalid blast fixture: %v", want)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("item explosion differs")
	}
}
