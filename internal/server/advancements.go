package server

import (
	"sort"
	"strings"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

// Advancement progress is only modelled for recipe advancements so far:
// a grant fires the recipe's "has_the_recipe" criterion and the dirty
// advancement flushes on the next player tick, mirroring vanilla's
// PlayerAdvancements.flushDirty. Join state (the recipes/root and
// crafting_table definitions plus the unlock_right_away criterion) is
// what the static join blob already encodes, so it seeds the tracking
// maps rather than re-sending anything.

// recipeAdvIndex maps a data.Recipes index to its recipe advancement,
// for the recipes that have one.
var recipeAdvIndex = func() map[int]*data.RecipeAdv {
	m := map[int]*data.RecipeAdv{}
	for i, r := range data.Recipes {
		for _, a := range data.RecipeAdvs {
			if a.Recipe == "minecraft:"+r.ID {
				a := a
				m[i] = &a
				break
			}
		}
	}
	return m
}()

const (
	advRecipesRoot   = "minecraft:recipes/root"
	advCraftingTable = "minecraft:recipes/decorations/crafting_table"
)

// initAdvancements seeds the join-time advancement state: the two defs
// the join blob already synced plus the criteria it recorded.
func (sess *Session) initAdvancements(joinMillis int64) {
	sess.advMu.Lock()
	defer sess.advMu.Unlock()
	sess.advSent = map[string]bool{advRecipesRoot: true, advCraftingTable: true}
	sess.advCrits = map[string]map[string]int64{
		advCraftingTable: {"unlock_right_away": joinMillis},
	}
	// unlock_right_away satisfies crafting_table's whole requirement
	// group at join; a completed advancement stops reporting further
	// criteria, which is why a later `give *` omits it from progress.
	sess.advDone = map[string]bool{advCraftingTable: true}
}

// grantRecipeCriterion marks the recipe's "has_the_recipe" criterion
// done and queues the advancement for the next flush; a criterion that
// is already earned changes nothing, so re-grants emit no packet.
func (sess *Session) grantRecipeCriterion(idx int) {
	adv := recipeAdvIndex[idx]
	if adv == nil {
		return
	}
	sess.advMu.Lock()
	defer sess.advMu.Unlock()
	if sess.advDone == nil {
		sess.advDone = map[string]bool{}
	}
	if sess.advDone[adv.ID] {
		return
	}
	if sess.advCrits == nil {
		sess.advCrits = map[string]map[string]int64{}
	}
	if sess.advCrits[adv.ID] == nil {
		sess.advCrits[adv.ID] = map[string]int64{}
	}
	if _, done := sess.advCrits[adv.ID]["has_the_recipe"]; done {
		return
	}
	sess.advCrits[adv.ID]["has_the_recipe"] = time.Now().UnixMilli()
	for _, group := range adv.Reqs {
		done := true
		for _, c := range group {
			if _, ok := sess.advCrits[adv.ID][c]; !ok {
				done = false
				break
			}
		}
		if done {
			sess.advDone[adv.ID] = true
			break
		}
	}
	if sess.advDirty == nil {
		sess.advDirty = map[string]bool{}
	}
	if !sess.advDirty[adv.ID] {
		sess.advDirty[adv.ID] = true
		sess.advDirtyOrder = append(sess.advDirtyOrder, adv.ID)
	}
}

// flushAdvancements emits the queued UpdateAdvancements packet:
// definitions for dirty advancements the client has not seen plus the
// full criterion progress of each dirty one. Both collections iterate in
// the order of the java.util containers vanilla builds them in, so the
// emitted order is the HashMap/HashSet bucket order of the advancement
// identifiers rather than grant order.
func (s *Server) flushAdvancements(sess *Session) {
	sess.advMu.Lock()
	if len(sess.advDirtyOrder) == 0 {
		sess.advMu.Unlock()
		return
	}
	dirty := sess.advDirtyOrder
	crits := sess.advCrits
	sent := sess.advSent
	sess.advDirty = nil
	sess.advDirtyOrder = nil
	sess.advMu.Unlock()

	advsByID := map[string]*data.RecipeAdv{}
	var added []*data.RecipeAdv
	var progressIDs []string
	for _, id := range dirty {
		adv := recipeAdvByID[id]
		advsByID[id] = adv
		progressIDs = append(progressIDs, id)
		if !sent[id] {
			added = append(added, adv)
		}
	}
	javaHashOrder(added, func(a *data.RecipeAdv) string { return a.ID })
	javaHashOrder(progressIDs, func(id string) string { return id })

	var b protocol.Buffer
	b.Bool(false) // reset: the first sync (join) already went out
	b.VarInt(int32(len(added)))
	for _, a := range added {
		b.String(a.ID)
		b.Bool(true)
		b.String(a.Parent)
		b.Bool(false) // no display info
		b.VarInt(int32(len(a.Reqs)))
		for _, group := range a.Reqs {
			b.VarInt(int32(len(group)))
			for _, c := range group {
				b.String(c)
			}
		}
		b.Bool(false) // sends_telemetry_event
	}
	b.VarInt(0) // removed
	b.VarInt(int32(len(progressIDs)))
	for _, id := range progressIDs {
		adv := advsByID[id]
		b.String(id)
		criteria := criteriaEmitOrder(adv)
		b.VarInt(int32(len(criteria)))
		for _, c := range criteria {
			b.String(c)
			t, done := crits[id][c]
			b.Bool(done)
			if done {
				b.Long(t)
			}
		}
	}
	b.Bool(true) // showAdvancements
	_ = sess.write(sess.ids().PlayUpdateAdvance, b.Bytes())

	sess.advMu.Lock()
	for _, a := range added {
		sess.advSent[a.ID] = true
	}
	sess.advMu.Unlock()
}

// recipeAdvByID looks up recipe advancements by namespaced id.
var recipeAdvByID = func() map[string]*data.RecipeAdv {
	m := map[string]*data.RecipeAdv{}
	for _, a := range data.RecipeAdvs {
		a := a
		m[a.ID] = &a
	}
	return m
}()

// javaStringHash is java.lang.String.hashCode; the names hashed here are
// all ASCII, so byte-wise UTF-16 accumulation is exact.
func javaStringHash(s string) int32 {
	var h int32
	for i := 0; i < len(s); i++ {
		h = 31*h + int32(s[i])
	}
	return h
}

// identifierHash is net.minecraft.resources.Identifier.hashCode:
// 31*namespace.hashCode() + path.hashCode().
func identifierHash(id string) int32 {
	ns, path, _ := strings.Cut(id, ":")
	return 31*javaStringHash(ns) + javaStringHash(path)
}

// hashMapCap is the backing-table capacity a java.util.HashMap holds
// after n insertions (default capacity 16, load factor 0.75).
func hashMapCap(n int) int {
	c := 16
	for n > c*3/4 {
		c *= 2
	}
	return c
}

// javaHashOrder reorders items the way HashMap/HashSet iteration does:
// ascending bucket index of the spread key hash, insertion order inside
// a bucket (resize keeps each split chain's order). Keys hashed are the
// Minecraft namespaced ids (Identifier.hashCode) or raw criterion names
// (String.hashCode) depending on which collection produced them.
func javaHashOrder[T any](items []T, key func(T) string) {
	if len(items) < 2 {
		return
	}
	c := hashMapCap(len(items))
	hash := func(s string) int32 {
		if strings.Contains(s, ":") {
			return identifierHash(s)
		}
		return javaStringHash(s)
	}
	spread := func(h int32) int32 { return h ^ int32(uint32(h)>>16) }
	sort.SliceStable(items, func(i, j int) bool {
		return spread(hash(key(items[i])))&int32(c-1) < spread(hash(key(items[j])))&int32(c-1)
	})
}

// fastutilMix is it.unimi.dsi.fastutil.HashCommon.mix for ints: open-
// addressed fastutil sets place keys at mix(hash)&mask.
func fastutilMix(h int32) int32 {
	x := uint32(h) * 0x9E3779B9
	return int32(x ^ (x >> 16))
}

// fastutilSetOrder returns names in ObjectOpenHashSet iteration order:
// slots probed forward on collision (insertion is the given order) and
// iterated descending, at the default 0.75 load factor.
func fastutilSetOrder(names []string) []string {
	dedup := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, s := range names {
		if !seen[s] {
			seen[s] = true
			dedup = append(dedup, s)
		}
	}
	c := 16
	for len(dedup) > c*3/4 {
		c *= 2
	}
	tbl := make([]string, c)
	for _, s := range dedup {
		p := int(fastutilMix(javaStringHash(s))) & (c - 1)
		for tbl[p] != "" {
			p = (p + 1) & (c - 1)
		}
		tbl[p] = s
	}
	out := make([]string, 0, len(dedup))
	for i := c - 1; i >= 0; i-- {
		if tbl[i] != "" {
			out = append(out, tbl[i])
		}
	}
	return out
}

// criteriaEmitOrder is the order vanilla's progress map reports an
// advancement's criteria in. AdvancementProgress.criteria is a HashMap
// filled from AdvancementRequirements.names() — a fastutil
// ObjectOpenHashSet — so ties inside a bucket land in that set's
// descending-slot order.
func criteriaEmitOrder(adv *data.RecipeAdv) []string {
	var flat []string
	for _, g := range adv.Reqs {
		flat = append(flat, g...)
	}
	ins := fastutilSetOrder(flat)
	inCrit := make(map[string]bool, len(adv.Criteria))
	for _, c := range adv.Criteria {
		inCrit[c] = true
	}
	ordered := make([]string, 0, len(adv.Criteria))
	for _, c := range ins {
		if inCrit[c] {
			ordered = append(ordered, c)
		}
	}
	for _, c := range adv.Criteria {
		if len(ordered) == len(adv.Criteria) {
			break
		}
		have := false
		for _, o := range ordered {
			if o == c {
				have = true
				break
			}
		}
		if !have {
			ordered = append(ordered, c)
		}
	}
	javaHashOrder(ordered, func(s string) string { return s })
	return ordered
}
