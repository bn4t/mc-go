package server

import (
	"reflect"
	"strings"
	"testing"

	"mc-go/internal/world"
)

// These canonical field shapes were observed in the vanilla native-record
// census, including filtered book wrappers, list colors and byte flight.
const jukeboxModeledRecord = `{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:custom_name":"Record","minecraft:enchantments":{"minecraft:unbreaking":2},"minecraft:repair_cost":3,"minecraft:stored_enchantments":{"minecraft:mending":1},"minecraft:map_id":4,"minecraft:banner_patterns":[{pattern:"minecraft:cross",color:"red"}],"minecraft:base_color":"blue","minecraft:firework_explosion":{shape:"star",colors:[16711680],fade_colors:[65280],has_trail:1b,has_twinkle:1b},"minecraft:fireworks":{flight_duration:2b,explosions:[{shape:"burst",colors:[255]}]},"minecraft:dye":"orange","minecraft:dyed_color":1234567,"minecraft:suspicious_stew_effects":[{id:"minecraft:speed",duration:123}],"minecraft:ominous_bottle_amplifier":2,"minecraft:pot_decorations":["minecraft:brick","minecraft:angler_pottery_sherd","minecraft:archer_pottery_sherd","minecraft:brick"],"minecraft:instrument":"minecraft:ponder_goat_horn","minecraft:trim":{material:"minecraft:gold",pattern:"minecraft:sentry"},"minecraft:block_state":{honey_level:"4"},"minecraft:potion_contents":{potion:"minecraft:swiftness"},"minecraft:potion_duration_scale":1.5f,"minecraft:writable_book_content":{pages:[{raw:"Page"}]},"minecraft:written_book_content":{title:{raw:"Title"},author:"A",pages:[{raw:"Page"}],generation:1,resolved:1b},"minecraft:container_loot":{loot_table:"minecraft:chests/simple_dungeon",seed:3L},"minecraft:container":[{slot:0,item:{id:"minecraft:diamond",count:2,components:{"minecraft:repair_cost":3}}}],"minecraft:bundle_contents":[{id:"minecraft:diamond",count:2,components:{"minecraft:repair_cost":3}}],"minecraft:damage":3}}`

func TestJukeboxModeledComponentDiskAndEjection(t *testing.T) {
	for name, record := range map[string]string{
		"modeled":            jukeboxModeledRecord,
		"ominous":            jukeboxOminousRecord,
		"empty_container":    `{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:container":[]}}`,
		"zero_fireworks":     `{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:fireworks":{}}}`,
		"one_fireworks":      `{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:fireworks":{flight_duration:1b}}}`,
		"unsigned_fireworks": `{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:fireworks":{flight_duration:-1b}}}`,
	} {
		t.Run(name, func(t *testing.T) { jukeboxRecordDiskAndEjection(t, record) })
	}
}
func jukeboxRecordDiskAndEjection(t *testing.T, input string) {
	tag, end, err := parseSNBTTag(input, 0)
	if err != nil || end != len(input) {
		t.Fatalf("fixture parse %d %v", end, err)
	}
	record := snbtToValue(tag).(map[string]any)

	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", "jukebox[has_record=true]"})
	s.restoreJukebox(d, pos, map[string]any{"RecordItem": record, "components": map[string]any{"minecraft:custom_name": "Box"}})
	assert := func(core *Server, phase string) {
		fields := map[string]any{}
		core.jukeboxSaveFields(core.dims[dimOverworld], pos, fields)
		if !reflect.DeepEqual(fields["RecordItem"], record) {
			t.Fatalf("%s record=%#v want=%#v", phase, fields["RecordItem"], record)
		}
	}
	assert(s, "hydrate")
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	r := nativeSaveTestServer(t)
	r.WorldDir = s.WorldDir
	if err := r.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	assert(r, "disk")
	rd := r.dims[dimOverworld]
	r.jukeboxPopRecord(rd, pos)
	count := 0
	for _, e := range r.entities.All() {
		if e.Item.ID == world.ItemIDs[strings.TrimPrefix(record["id"].(string), "minecraft:")] {
			count++
			if !reflect.DeepEqual(jukeboxDiscNBT(e.Item), record) || e.PickupDelay != 10 {
				t.Fatalf("ejected=%#v delay%d", jukeboxDiscNBT(e.Item), e.PickupDelay)
			}
		}
	}
	if count != 1 {
		t.Fatalf("ejected records=%d", count)
	}
}

func TestJukeboxZeroCountDefaultsOne(t *testing.T) {
	s := nativeSaveTestServer(t)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	fields := map[string]any{"RecordItem": map[string]any{"id": "minecraft:music_disc_cat", "count": int32(2)}, "ticks_since_song_started": int64(7)}
	s.restoreJukebox(d, pos, fields)
	fields["RecordItem"].(map[string]any)["count"] = int32(0)
	delete(fields, "ticks_since_song_started")
	s.restoreJukebox(d, pos, fields)
	jb := s.jukeboxAt(d, pos)
	if jb == nil || jb.disc.Count != 1 || !jb.playing || s.TickCount()-jb.started != 7 {
		t.Fatalf("zero count=%+v", jb)
	}
}

// Item defaults are factual report values from the 26.2 item component report.
func TestJukeboxEffectiveComponentEquality(t *testing.T) {
	box := ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1}
	boxEmpty := box
	boxEmpty.Contents = new([shulkerSlots]ItemStack)
	rocket := ItemStack{ID: world.ItemIDs["firework_rocket"], Count: 1}
	rocketExplicit := rocket
	rocketExplicit.Fireworks = "1"
	horn := ItemStack{ID: world.ItemIDs["goat_horn"], Count: 1}
	hornExplicit := horn
	hornExplicit.Instrument = "ponder_goat_horn"
	disc := ItemStack{ID: world.ItemIDs["music_disc_cat"], Count: 1}
	removed := disc
	removed.InstrumentRemoved = true
	for _, pair := range [][2]ItemStack{{box, boxEmpty}, {rocket, rocketExplicit}, {horn, hornExplicit}, {disc, removed}} {
		if !jukeboxSameDisc(pair[0], pair[1]) {
			t.Fatalf("equivalent %+v %+v", pair[0], pair[1])
		}
	}
	empty := disc
	empty.Contents = new([shulkerSlots]ItemStack)
	fireworks := disc
	fireworks.Fireworks = "0"
	removedHorn := horn
	removedHorn.InstrumentRemoved = true
	for _, pair := range [][2]ItemStack{{disc, empty}, {disc, fireworks}, {horn, removedHorn}} {
		if jukeboxSameDisc(pair[0], pair[1]) {
			t.Fatalf("distinct %+v %+v", pair[0], pair[1])
		}
	}
}

const jukeboxOminousRecord = `{id:"minecraft:white_banner",count:2,components:{"minecraft:banner_patterns":[{pattern:"minecraft:rhombus",color:"cyan"},{pattern:"minecraft:stripe_bottom",color:"light_gray"},{pattern:"minecraft:stripe_center",color:"gray"},{pattern:"minecraft:border",color:"light_gray"},{pattern:"minecraft:stripe_middle",color:"black"},{pattern:"minecraft:half_horizontal",color:"light_gray"},{pattern:"minecraft:circle",color:"light_gray"},{pattern:"minecraft:border",color:"black"}],"minecraft:tooltip_display":{hidden_components:["minecraft:banner_patterns"]},"minecraft:item_name":{translate:"block.minecraft.ominous_banner"},"minecraft:rarity":"uncommon"}}`
