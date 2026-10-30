package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// AdvancementRewards grants XP, loot, recipes, then the function. Progress is
// already marked complete before callbacks, so recursive inventory listeners
// cannot pay this same reward twice.
func (s *Server) grantAdvancementRewards(sess *Session, meta *advMeta) {
	sess.giveExperiencePoints(meta.experience)
	reward, _ := meta.raw["rewards"].(map[string]any)
	sess.invMu.RLock()
	before := sess.inv
	sess.invMu.RUnlock()
	anyComplete := false
	loot, _ := reward["loot"].([]any)
	x, y, z, _, _, _ := sess.Pos()
	ctx := &lootCtx{player: sess, dim: sess.dimension(), pos: world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}, rng: rand.New(rand.NewSource(rand.Int63()))}
	for _, entry := range loot {
		id, ok := entry.(string)
		if !ok {
			continue
		}
		for _, item := range rollLootDepth(ctx, strings.TrimPrefix(id, "minecraft:"), 0) {
			remaining, changed := sess.insertPickupStack(item)
			if sess.mode() == GameModeCreative {
				remaining = ItemStack{}
			}
			if changed {
				s.fireInventoryChanged(sess)
			}
			if remaining.Empty() {
				anyComplete = true
				sx, sy, sz, _, _, _ := sess.Pos()
				s.playSoundSrc(sess.dimName(), sx, sy, sz, soundEventIDs["entity.item.pickup"], 7, .2, ((rand.Float32()-rand.Float32())*.7+1)*2, nil)
			} else if drop := s.dropMenuItem(sess, remaining); drop != nil {
				drop.PickupDelay = 0
				drop.Owner = sess.getUUID()
				drop.HasOwner = true
			}
		}
	}
	if anyComplete {
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w != nil {
			var changed []int
			for i := int16(slotMainStart); i < slotOffhand; i++ {
				if menuRemoteStackEqual(before[i], sess.stackAt(i)) {
					continue
				}
				slot := w.containerSize() + int(i-slotMainStart)
				if i >= slotHotbarStart {
					slot = w.containerSize() + 27 + int(i-slotHotbarStart)
				}
				changed = append(changed, slot)
			}
			s.syncContainerMenuSlots(sess, w, changed)
		}
	}
	recipes, _ := reward["recipes"].([]any)
	for _, entry := range recipes {
		if id, ok := entry.(string); ok {
			s.awardRecipeAdvancement(sess, &data.RecipeAdv{Recipe: normalizeID(id)})
		}
	}
	if fn, ok := reward["function"].(string); ok {
		name := normalizeID(fn)
		if _, exists := s.functions[name]; exists {
			s.executeFunctionNames(sess.getName(), []string{name}, false)
		}
	}
}
