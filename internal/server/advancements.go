package server

import (
	"sort"
	"strings"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
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
	// A returning player replays its saved progress through the first
	// flush: every progressed advancement joins the dirty set so the
	// packet carries its def (when unsent this session) and full
	// criterion state — vanilla's rejoin sync is the same shape.
	for id, a := range sess.pendingAdv {
		if len(a.Criteria) > 0 {
			if sess.advCrits[id] == nil {
				sess.advCrits[id] = map[string]int64{}
			}
			for c, t := range a.Criteria {
				sess.advCrits[id][c] = t
			}
		}
		if a.Done {
			sess.advDone[id] = true
		}
		if sess.advDirty == nil {
			sess.advDirty = map[string]bool{}
		}
		if !sess.advDirty[id] {
			sess.advDirty[id] = true
			sess.advDirtyOrder = append(sess.advDirtyOrder, id)
		}
	}
	sess.pendingAdv = nil
}

// grantRecipeCriterion marks the recipe's "has_the_recipe" criterion
// done and queues the advancement for the next flush; a criterion that
// is already earned changes nothing, so re-grants emit no packet.
func (sess *Session) grantRecipeCriterion(idx int) {
	adv := recipeAdvIndex[idx]
	if adv == nil {
		return
	}
	sess.grantAdvCriterion(adv, "has_the_recipe")
}

// grantAdvCriterion records criterion cname earned on adv. When the
// grant finishes a requirements group the advancement completes: its
// recipe reward unlocks inline (RecipeBookAdd), and further criteria
// grants become no-ops - vanilla unregisters a completed advancement's
// listeners, which is why a give * never reports the crafting_table
// criterion.
func (sess *Session) grantAdvCriterion(adv *data.RecipeAdv, cname string) {
	sess.advMu.Lock()
	if sess.advDone == nil {
		sess.advDone = map[string]bool{}
	}
	if sess.advDone[adv.ID] {
		sess.advMu.Unlock()
		return
	}
	if sess.advCrits == nil {
		sess.advCrits = map[string]map[string]int64{}
	}
	if sess.advCrits[adv.ID] == nil {
		sess.advCrits[adv.ID] = map[string]int64{}
	}
	if _, done := sess.advCrits[adv.ID][cname]; done {
		sess.advMu.Unlock()
		return
	}
	sess.advCrits[adv.ID][cname] = time.Now().UnixMilli()
	// A requirements group is a set of alternative criteria: any one
	// satisfied meets it, and the advancement completes when every
	// group is met (AdvancementRequirements.test).
	complete := len(adv.Reqs) > 0
	for _, group := range adv.Reqs {
		met := false
		for _, c := range group {
			if _, ok := sess.advCrits[adv.ID][c]; ok {
				met = true
				break
			}
		}
		if !met {
			complete = false
			break
		}
	}
	if complete {
		sess.advDone[adv.ID] = true
	}
	if sess.advDirty == nil {
		sess.advDirty = map[string]bool{}
	}
	if !sess.advDirty[adv.ID] {
		sess.advDirty[adv.ID] = true
		sess.advDirtyOrder = append(sess.advDirtyOrder, adv.ID)
	}
	sess.advMu.Unlock()

	if complete {
		sess.server.awardRecipeAdvancement(sess, adv)
	}
}

// awardRecipeAdvancement runs the advancement's recipe reward: the
// recipe unlocks through the same RecipeBookAdd path /recipe give uses
// (already-known entries skip silently), and the unlock re-grants the
// advancement's own recipe criterion - a no-op now that it's done.
func (s *Server) awardRecipeAdvancement(sess *Session, adv *data.RecipeAdv) {
	name := strings.TrimPrefix(adv.Recipe, "minecraft:")
	idx, ok := recipeIndex[name]
	if !ok || !recipeBookable(data.Recipes[idx]) {
		return
	}
	if !sess.unlockRecipe(idx) {
		return
	}
	var b protocol.Buffer
	var entries protocol.Buffer
	n := recipeEntry(&entries, idx, recipeGiveFlags(data.Recipes[idx]))
	b.VarInt(int32(n))
	b.Raw(entries.Bytes())
	b.Bool(false)
	sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
	sess.grantRecipeCriterion(idx)
}

// fireInventoryChanged is the inventory listener: vanilla attaches a
// container.changed() hook that evaluates every inventory_changed
// criterion against the player's whole inventory each time a slot
// mutates. addStack and the slot setters call this after writing.
func (s *Server) fireInventoryChanged(sess *Session) {
	sess.invMu.RLock()
	inv := append([]ItemStack(nil), sess.inv[:]...)
	sess.invMu.RUnlock()

	occupied := 0
	items := map[string]bool{}
	for _, it := range inv {
		if it.Empty() {
			continue
		}
		occupied++
		if n, ok := world.ItemName(it.ID); ok {
			items[n] = true
		}
	}
	for i := range data.RecipeAdvs {
		adv := &data.RecipeAdvs[i]
		for _, c := range adv.Conds {
			if c.Trigger != "inventory_changed" {
				continue
			}
			sess.advMu.Lock()
			done := sess.advCrits[adv.ID] != nil
			if done {
				_, done = sess.advCrits[adv.ID][c.Name]
			}
			sess.advMu.Unlock()
			if done {
				continue
			}
			match := false
			if c.SlotsMin > 0 && occupied >= c.SlotsMin {
				match = true
			}
			for _, pred := range c.Items {
				if strings.HasPrefix(pred, "#") {
					for _, member := range data.ItemTags[strings.TrimPrefix(strings.TrimPrefix(pred, "#"), "minecraft:")] {
						if items[member] {
							match = true
						}
					}
					continue
				}
				if items[strings.TrimPrefix(pred, "minecraft:")] {
					match = true
				}
			}
			if match {
				sess.grantAdvCriterion(adv, c.Name)
			}
		}
	}
	s.fireGameInventoryChanged(sess)
}

// fireEnterBlock grants enter_block criteria when the player's feet sit
// inside the named block - the boat/raft recipe advancements fire on
// "water" this way.
func (s *Server) fireEnterBlock(sess *Session) {
	sess.posMu.RLock()
	bx, by, bz := int(sess.px), int(sess.py), int(sess.pz)
	sess.posMu.RUnlock()
	blk := sess.world().GetBlock(world.Pos{X: bx, Y: by, Z: bz})
	feet, _ := world.StateOwner(blk)
	if feet == "" {
		return
	}
	for i := range data.RecipeAdvs {
		adv := &data.RecipeAdvs[i]
		for _, c := range adv.Conds {
			if c.Trigger != "enter_block" || c.Block != feet {
				continue
			}
			sess.advMu.Lock()
			done := sess.advCrits[adv.ID] != nil
			if done {
				_, done = sess.advCrits[adv.ID][c.Name]
			}
			sess.advMu.Unlock()
			if !done {
				sess.grantAdvCriterion(adv, c.Name)
			}
		}
	}
	s.fireGameEnterBlock(sess, feet)
}

// flushAdvancements emits the queued UpdateAdvancements packet:
// definitions for dirty advancements the client has not seen plus the
// full criterion progress of each dirty one. Both collections iterate in
// the order of the java.util containers vanilla builds them in, so the
// emitted order is the HashMap/HashSet bucket order of the advancement
// identifiers rather than grant order.
func (s *Server) flushAdvancements(sess *Session) {
	sess.advMu.Lock()
	// The minecraft:tick trigger re-obtains unlock_right_away every
	// tick, so a revoke of crafting_table never sticks: the criterion
	// (and the done it completes) is back before the packet goes out.
	if sess.advCrits != nil {
		if _, ok := sess.advCrits[advCraftingTable]["unlock_right_away"]; !ok {
			if sess.advCrits[advCraftingTable] == nil {
				sess.advCrits[advCraftingTable] = map[string]int64{}
			}
			sess.advCrits[advCraftingTable]["unlock_right_away"] = time.Now().UnixMilli()
			if sess.advDone == nil {
				sess.advDone = map[string]bool{}
			}
			sess.advDone[advCraftingTable] = true
			if sess.advDirty == nil {
				sess.advDirty = map[string]bool{}
			}
			if !sess.advDirty[advCraftingTable] {
				sess.advDirty[advCraftingTable] = true
				sess.advDirtyOrder = append(sess.advDirtyOrder, advCraftingTable)
			}
		}
	}
	if len(sess.advDirtyOrder) == 0 {
		sess.advMu.Unlock()
		return
	}
	crits := sess.advCrits
	sent := sess.advSent
	if sent == nil {
		// Sessions that skipped the join path (unit-test fixtures) never
		// ran initAdvancements; an empty set is their correct state.
		sent = map[string]bool{}
		sess.advSent = sent
	}
	if crits == nil {
		crits = map[string]map[string]int64{}
		sess.advCrits = crits
	}
	if sess.advDone == nil {
		sess.advDone = map[string]bool{}
	}
	sess.advDirty = nil
	sess.advMu.Unlock()

	// Dirty = progressed ∪ newly visible. Vanilla emits a def entry for
	// every dirty advancement it has not sent before and a progress
	// entry for every dirty advancement — even ones whose criteria are
	// all still locked (the client needs the criteria list to render).
	visible := computeAdvVisibility(sess.advDone)
	// Vanilla emits progress only for dirty ids that are visible — an
	// invisible advancement's progress stays server-side (measured: a
	// criterion grant on an unlit advancement sends nothing).
	var progressIDs []string
	progressSeen := map[string]bool{}
	for _, id := range sess.advDirtyOrder {
		if !visible[id] {
			continue
		}
		progressSeen[id] = true
		progressIDs = append(progressIDs, id)
	}
	for id, v := range visible {
		if v && !sent[id] && !progressSeen[id] {
			progressIDs = append(progressIDs, id)
		}
	}
	sess.advDirtyOrder = nil

	var added []string
	for _, id := range progressIDs {
		if !sent[id] {
			added = append(added, id)
		}
	}
	// Revokes drop sent-but-no-longer-visible advancements into the
	// packet's removed set; they may be sent again if relit.
	var removed []string
	for id := range sent {
		if !visible[id] {
			removed = append(removed, id)
			delete(sent, id)
		}
	}
	javaHashOrder(added, func(id string) string { return id })
	javaHashOrder(progressIDs, func(id string) string { return id })
	javaHashOrder(removed, func(id string) string { return id })

	var b protocol.Buffer
	b.Bool(false) // reset: the first sync (join) already went out
	b.VarInt(int32(len(added)))
	for _, id := range added {
		if def := data.AdvDefWire[id]; def != nil {
			b.Raw(def)
			continue
		}
		adv := recipeAdvByID[id]
		b.String(adv.ID)
		b.Bool(true)
		b.String(adv.Parent)
		b.Bool(false) // no display info
		b.VarInt(int32(len(adv.Reqs)))
		for _, group := range adv.Reqs {
			b.VarInt(int32(len(group)))
			for _, c := range group {
				b.String(c)
			}
		}
		b.Bool(false) // sends_telemetry_event
	}
	b.VarInt(int32(len(removed)))
	for _, id := range removed {
		b.String(id)
	}
	b.VarInt(int32(len(progressIDs)))
	for _, id := range progressIDs {
		criteria, reqs := advCriteriaReqs(id)
		b.String(id)
		ordered := criteriaEmitOrderOf(criteria, reqs)
		b.VarInt(int32(len(ordered)))
		for _, c := range ordered {
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
	for _, id := range added {
		sess.advSent[id] = true
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
	return criteriaEmitOrderOf(adv.Criteria, adv.Reqs)
}

// criteriaEmitOrderOf is criteriaEmitOrder for an advancement given as
// (criteria names, requirement groups) — the gameplay tree carries the
// same shape outside data.RecipeAdv.
func criteriaEmitOrderOf(criteria []string, reqs [][]string) []string {
	var flat []string
	for _, g := range reqs {
		flat = append(flat, g...)
	}
	ins := fastutilSetOrder(flat)
	inCrit := make(map[string]bool, len(criteria))
	for _, c := range criteria {
		inCrit[c] = true
	}
	ordered := make([]string, 0, len(criteria))
	for _, c := range ins {
		if inCrit[c] {
			ordered = append(ordered, c)
		}
	}
	for _, c := range criteria {
		if len(ordered) == len(criteria) {
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

// advCriteriaReqs returns an advancement's criteria names and
// requirement groups regardless of which table it lives in.
func advCriteriaReqs(id string) ([]string, [][]string) {
	if a := gameAdvs[id]; a != nil {
		return a.criteria, a.reqs
	}
	if a := recipeAdvByID[id]; a != nil {
		return a.Criteria, a.Reqs
	}
	return nil, nil
}
