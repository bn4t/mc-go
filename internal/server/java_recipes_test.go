package server

import (
	"bytes"
	"testing"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestJava775RecipeEntriesMatchEntireOracleCapture(t *testing.T) {
	assets, err := load775RecipeAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets.entries) != 1498 || len(assets.byCanonical) != 1491 {
		t.Fatalf("775assets displays=%d recipes=%d", len(assets.entries), len(assets.byCanonical))
	}
	profile, _ := javaProfileFor(775)
	sess := &Session{profile: profile, ver: profile.version}
	var body, entries protocol.Buffer
	count := 0
	for index, r := range data.Recipes {
		count += recipeEntryForSession(sess, &entries, index, recipeGiveFlags(r))
	}
	body.VarInt(int32(count))
	body.Raw(entries.Bytes())
	body.Bool(false)
	if !bytes.Equal(body.Bytes(), java775RecipeBook) {
		t.Fatalf("775wholebook encoding differs from independent oracle: got%d want%d", body.Len(), len(java775RecipeBook))
	}
}
func TestJavaRecipeDisplayInverseUsesCanonicalIdentity(t *testing.T) {
	for _, protocolNumber := range []int32{775, 776} {
		profile, _ := javaProfileFor(protocolNumber)
		index := recipeIndex["crafting_table"]
		ids, err := profile.recipeDisplayIDs(index)
		if err != nil || len(ids) != 1 {
			t.Fatalf("displayIDs %v %v", ids, err)
		}
		want := int32(318)
		if protocolNumber == 775 {
			want = 293
		}
		if ids[0] != want || ids[0] == int32(index) {
			t.Fatalf("test must use measured displayIDdistinct fromcanonicalindex: id%d index%d", ids[0], index)
		}
		back, ok := profile.recipeCanonicalIndex(ids[0])
		if !ok || back != index {
			t.Fatalf("inverse%d -> %d (%v)", ids[0], back, ok)
		}
		for _, bad := range []int32{-1, 1000000} {
			if _, ok := profile.recipeCanonicalIndex(bad); ok {
				t.Fatal("unknown displayaccepted")
			}
		}
		expanded, err := profile.recipeDisplayIDs(recipeIndex["map_cloning"])
		if err != nil || len(expanded) != 8 {
			t.Fatalf("expanded%v err%v", expanded, err)
		}
		for _, id := range expanded {
			if back, ok := profile.recipeCanonicalIndex(id); !ok || back != recipeIndex["map_cloning"] {
				t.Fatal("range expansionlostidentity")
			}
		}
	}
}
func TestJavaPlaceRecipeIngressProjectsDisplayIDs(t *testing.T) {
	for _, number := range []int32{775, 776} {
		s := New("unused")
		t.Cleanup(s.Stop)
		sess := placeSession(t, s, "RecipeIngress", .5, 70, .5)
		profile, _ := javaProfileFor(number)
		sess.profile, sess.ver = profile, profile.version
		planks, _ := world.ItemID("oak_planks")
		sess.setStack(slotHotbarStart, ItemStack{ID: planks, Count: 4})
		display, _ := profile.recipeDisplayIDs(recipeIndex["crafting_table"])
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(display[0])
		b.Bool(false)
		s.handlePlaceRecipe(sess, protocol.Packet{Data: b.Bytes()})
		for slot := int16(1); slot <= 4; slot++ {
			stack := sess.stackAt(slot)
			if stack.ID != planks || stack.Count != 1 {
				t.Fatalf("protocol%d craftinggrid slot%d=%+v", number, slot, stack)
			}
		}
		var seen protocol.Buffer
		seen.VarInt(display[0])
		s.handleRecipeBookSeenRecipe(sess, protocol.Packet{Data: seen.Bytes()})
		if !sess.seenRecipes[int32(recipeIndex["crafting_table"])] {
			t.Fatal("seenrecipe storedtargetdisplayIDin canonicalstate")
		}
	}
}
func TestRecipeCaptureParserRejectsTruncationAndTrailingBytes(t *testing.T) {
	for _, body := range [][]byte{java775RecipeBook[:len(java775RecipeBook)-1], append(append([]byte(nil), java775RecipeBook...), 0), {1, 0, 127}} {
		if _, err := splitRecipeBook(body); err == nil {
			t.Fatal("malformed recipebook accepted")
		}
	}
}

func TestJavaStonecutterFiltersProfileRecipeList(t *testing.T) {
	p, _ := javaProfileFor(775)
	for _, name := range []string{"stone", "copper_block", "resin_bricks"} {
		id, ok := world.ItemID(name)
		if !ok {
			t.Fatal(name)
		}
		c := &containerData{}
		stonecutterEnsure(c, ItemStack{ID: id, Count: 1})
		stonecutterEnsureProfile(c, ItemStack{ID: id, Count: 1}, p)
		if c.stoneProtocol != 775 {
			t.Fatal("profile cache not invalidated")
		}
		for _, index := range c.stoneRecipes {
			ids, err := p.recipeDisplayIDs(index)
			if err != nil || len(ids) == 0 {
				t.Fatalf("unrepresentable775button recipe%s", data.Recipes[index].ID)
			}
		}
	}
}
func TestJavaAdvancementIconProjectsOnce(t *testing.T) {
	p, _ := javaProfileFor(775)
	body := data.AdvDefWire["minecraft:story/root"]
	if len(body) == 0 {
		t.Fatal("missing actual advancement fixture")
	}
	projected, err := p.advancementDefinitionFrom776(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(body, projected) {
		t.Fatal("shifted craftingtable icon not projected")
	}
	current, _ := javaProfileFor(776)
	same, err := current.advancementDefinitionFrom776(body)
	if err != nil || !bytes.Equal(same, body) {
		t.Fatal("776 passthrough changed")
	}
	if _, err := p.advancementDefinitionFrom776(body[:3]); err == nil {
		t.Fatal("truncated definition accepted")
	}
}
