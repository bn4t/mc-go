package blackbox

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

var itemCountRe = regexp.MustCompile(`count(\d+)`)

// dropScan runs `data get entity @s Item` on every item entity and
// returns the per-item stack counts plus the entity total.
func dropScan(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs) (counts []int, entities int) {
	t.Helper()
	sendCommand(t, pw, "execute as @e[type=minecraft:item] run data get entity @s Item")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
	for _, ln := range obs.chats[base:] {
		if !strings.Contains(ln, "minecraft:dirt") {
			continue
		}
		n := 1
		if m := itemCountRe.FindStringSubmatch(ln); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		counts = append(counts, n)
		entities++
	}
	return counts, entities
}

// ItemEntity.mergeWithNeighbours + ServerExplosion.StackCollector.
//
// A) Two same-item stacks sharing a cell merge into one entity whose
//
//	count is the sum — larger absorbs smaller.
//
// B) Explosion drops pool: every stack of a type pours into the first
//
//	collectors up to count 16, so a powered blast leaves a handful of
//	≤16-count entities instead of one entity per destroyed cell.
func TestItemDropMergeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		aEntities, aCount int
		bEntities         int
		bCounts           []int
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(150 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// A: two single-dirt item entities at the same spot.
		sendCommand(t, pw, `summon minecraft:item 130.5 -59 0.5 {Item:{id:"minecraft:dirt",count:1b}}`)
		sendCommand(t, pw, `summon minecraft:item 130.5 -59 0.5 {Item:{id:"minecraft:dirt",count:1b}}`)
		fleePump(t, c, pr, pw, obs, 2500*time.Millisecond)
		aCounts, aEnt := dropScan(t, c, pr, pw, obs)
		r.aEntities = aEnt
		for _, n := range aCounts {
			r.aCount += n
		}
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		fleePump(t, c, pr, pw, obs, 300*time.Millisecond)

		// B: powered creeper clears a ~185-cell crater → pooled drops.
		sendCommand(t, pw, "summon minecraft:creeper 108.5 -60 0.5 {ignited:1b,NoAI:1b,powered:1b}")
		fleePump(t, c, pr, pw, obs, 3500*time.Millisecond)
		r.bCounts, r.bEntities = dropScan(t, c, pr, pw, obs)
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van A entities=%d count=%d | B entities=%d counts=%v",
		van.aEntities, van.aCount, van.bEntities, van.bCounts)
	t.Logf("mc  A entities=%d count=%d | B entities=%d counts=%v",
		mc.aEntities, mc.aCount, mc.bEntities, mc.bCounts)

	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if r.aEntities != 1 || r.aCount != 2 {
			t.Errorf("%s A: want 1 entity holding 2 dirt, got %d entities holding %d",
				name, r.aEntities, r.aCount)
		}
		sum := 0
		for _, n := range r.bCounts {
			if n > 16 {
				t.Errorf("%s B: pooled stack count %d exceeds the 16 cap", name, n)
			}
			sum += n
		}
		// Powered blast destroys ~185 cells → ~20-40 drops pooled into
		// ceil(total/16) entities; never one entity per cell.
		if r.bEntities < 1 || r.bEntities > 8 {
			t.Errorf("%s B: entity count %d outside pooled band", name, r.bEntities)
		}
		if sum < 8 {
			t.Errorf("%s B: only %d dropped dirt — pooling untested", name, sum)
		}
	}
	// Same pool shape: entity counts track ceil(total/16) on both.
	if van.bEntities != 0 && mc.bEntities != 0 && (van.bEntities-mc.bEntities > 1 || mc.bEntities-van.bEntities > 1) {
		t.Errorf("entity counts diverged: van=%d mc=%d", van.bEntities, mc.bEntities)
	}
}
