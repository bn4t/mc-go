package server

import (
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

// runRecipe implements /recipe give|take <targets> <recipe|*>: it adds
// or removes recipe-book entries on the target session, emitting the
// RecipeBookAdd/Remove packets vanilla does and replying with the same
// translatable components.
func (s *Server) runRecipe(player string, args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	verb := args[0]
	if verb != "give" && verb != "take" {
		return errToken("command.unknown.argument", 1)
	}
	target := s.resolveTarget(player, args[1])
	if target == nil {
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	name := strings.TrimPrefix(args[2], "minecraft:")

	if name == "*" {
		return s.recipeAll(target, verb == "give")
	}
	idx, ok := recipeIndex[name]
	if !ok {
		return errToken("recipe.notFound", 3, args[2])
	}
	return s.recipeOne(target, idx, verb == "give")
}

// recipeIndex maps recipe names to their data.Recipes index.
var recipeIndex = func() map[string]int {
	m := make(map[string]int, len(data.Recipes))
	for i, r := range data.Recipes {
		m[r.ID] = i
	}
	return m
}()

// recipeOne handles give/take of a single recipe. Special recipes and
// already-settled unlocks fail the whole command, as vanilla does.
func (s *Server) recipeOne(sess *Session, idx int, give bool) reply {
	r := data.Recipes[idx]
	if !recipeBookable(r) || len(recipeDisplayIDsForSession(sess, idx)) == 0 {
		return recipeFailure(give)
	}
	if give {
		newly := sess.unlockRecipe(idx)
		if !newly {
			return recipeFailure(give)
		}
		sess.grantRecipeCriterion(idx)
		var b protocol.Buffer
		var entries protocol.Buffer
		n := recipeEntryForSession(sess, &entries, idx, recipeGiveFlags(r))
		b.VarInt(int32(n))
		b.Raw(entries.Bytes())
		b.Bool(false)
		sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
	} else {
		if !sess.lockRecipe(idx) {
			return recipeFailure(give)
		}
		s.writeRecipeRemove(sess, idx)
	}
	return s.recipeReply(sess, give, 1)
}

// recipeAll handles give/take *. Every bookable recipe is re-sent with
// the notify flag only for the ones the book did not yet know; the
// command fails when nothing changed.
func (s *Server) recipeAll(sess *Session, give bool) reply {
	var changed int
	var b protocol.Buffer
	var n int32
	var ids []int32
	for i, r := range data.Recipes {
		if !recipeBookable(r) || len(recipeDisplayIDsForSession(sess, i)) == 0 {
			continue
		}
		if give {
			if !sess.unlockRecipe(i) {
				// already-known recipes are skipped entirely
				continue
			}
			sess.grantRecipeCriterion(i)
			changed++
			n += int32(recipeEntryForSession(sess, &b, i, recipeGiveFlags(r)))
			continue
		}
		if sess.lockRecipe(i) {
			changed++
			ids = append(ids, recipeDisplayIDsForSession(sess, i)...)
		}
	}
	if changed == 0 {
		return recipeFailure(give)
	}
	if give {
		var pkt protocol.Buffer
		pkt.VarInt(n)
		pkt.Raw(b.Bytes())
		pkt.Bool(false)
		sess.write(sess.ids().PlayRecipeBookAdd, pkt.Bytes())
	} else {
		var pkt protocol.Buffer
		pkt.VarInt(int32(len(ids)))
		for _, id := range ids {
			pkt.VarInt(id)
		}
		sess.write(IDPlayRecipeBookRemove, pkt.Bytes())
	}
	count := int32(len(data.Recipes))
	if p, err := sess.javaProfile(); err == nil && p.version.Protocol == 775 {
		count = java775RecipeCount
	}
	reply := s.recipeReply(sess, give, count)
	reply.result = changed
	return reply
}

// writeRecipeRemove sends RecipeBookRemove (0x4B): a count and the
// display ids of every display the recipe produced.
func (s *Server) writeRecipeRemove(sess *Session, idx int) {
	ids := recipeDisplayIDsForSession(sess, idx)
	var b protocol.Buffer
	b.VarInt(int32(len(ids)))
	for _, id := range ids {
		b.VarInt(id)
	}
	sess.write(IDPlayRecipeBookRemove, b.Bytes())
}

// recipeReply builds the shared success component: the translatable
// key takes the processed recipe count and the player entity.
func (s *Server) recipeReply(sess *Session, give bool, n int32) reply {
	key := "commands.recipe.give.success.single"
	if !give {
		key = "commands.recipe.take.success.single"
	}
	reply := opsEchoReply(translatable(key,
		nestedCompound{key: "", value: n},
		s.entityTargetComponent(sess)))
	reply.result, reply.hasResult = int(n), true
	return reply
}

func recipeFailKey(give bool) string {
	if give {
		return "commands.recipe.give.failed"
	}
	return "commands.recipe.take.failed"
}

// markRecipesUnlocked records recipe names as known without emitting
// packets; used when the join-time unlock blob goes out.
func (sess *Session) markRecipesUnlocked(names ...string) {
	sess.recipesMu.Lock()
	defer sess.recipesMu.Unlock()
	if sess.unlockedRecipes == nil {
		sess.unlockedRecipes = map[int]bool{}
	}
	for _, n := range names {
		if idx, ok := recipeIndex[n]; ok {
			sess.unlockedRecipes[idx] = true
		}
	}
}

// unlockRecipe adds a recipe to the book; it reports false when the
// recipe was already known.
func (sess *Session) unlockRecipe(idx int) bool {
	sess.recipesMu.Lock()
	defer sess.recipesMu.Unlock()
	if sess.unlockedRecipes == nil {
		sess.unlockedRecipes = map[int]bool{}
	}
	if sess.unlockedRecipes[idx] {
		return false
	}
	sess.unlockedRecipes[idx] = true
	return true
}

// lockRecipe removes a recipe from the book; it reports false when the
// recipe was not known.
func (sess *Session) lockRecipe(idx int) bool {
	sess.recipesMu.Lock()
	defer sess.recipesMu.Unlock()
	if !sess.unlockedRecipes[idx] {
		return false
	}
	delete(sess.unlockedRecipes, idx)
	return true
}

func recipeFailure(give bool) reply {
	r := componentReply(commandError(recipeFailKey(give)))
	r.result, r.hasResult = 0, true
	return r
}
