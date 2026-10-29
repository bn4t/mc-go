package server

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Jukebox block entity measured from vanilla 26.2
// (JukeboxBlock/JukeboxBlockEntity/JukeboxSongPlayer):
//   - useItemOn: has_record already set answers TRY_WITH_EMPTY_HAND
//     regardless of the held item → the click falls through to
//     useWithoutItem's popOutTheItem on the default main-hand path;
//     offhand skips that default (a held disc never places). An empty jukebox takes a
//     JUKEBOX_PLAYABLE item (a music disc) via setTheItem; anything
//     else passes both paths → the item places.
//   - setTheItem → notifyItemChangedInJukebox writes has_record with
//     setBlock flag 2 (client update, no neighbours) + a BLOCK_CHANGE
//     game event; the song player then emits level_event 1010 carrying
//     the song's jukebox_song registry id.
//   - JukeboxSongPlayer.tick re-emits GameEvent.JUKEBOX_PLAY every 20
//     ticks (a note particle rides it) until length_in_seconds*20,
//     then stop(): GameEvent.JUKEBOX_STOP_PLAY + level_event 1011.
//   - popOutTheItem clears has_record/BLOCK_CHANGE and stops playback,
//     then drops the disc at pos+(0.5,1.01,0.5) scattered ±0.7 on xz
//     with the default 10-tick pickup delay.
type jukeboxEnt struct {
	components map[string]any
	disc       ItemStack
	song       string
	songID     int32
	started    int64 // s.TickCount() at play start
	playing    bool
}

// jukeboxSongID is the jukebox_song registry id level_event 1010
// carries, in the order vanilla-26.2's registries/022 bin ships
// (11,13,5,blocks,bounce,cat,…). A music_disc_<song> item maps to
// the song of the same name.
var jukeboxSongID = map[string]int32{
	"11": 0, "13": 1, "5": 2, "blocks": 3, "bounce": 4, "cat": 5,
	"chirp": 6, "creator": 7, "creator_music_box": 8, "far": 9,
	"lava_chicken": 10, "mall": 11, "mellohi": 12, "otherside": 13,
	"pigstep": 14, "precipice": 15, "relic": 16, "stal": 17,
	"strad": 18, "tears": 19, "wait": 20, "ward": 21,
}

// jukeboxSongTicks is each song's length_in_seconds*20 — the
// JukeboxSongPlayer.hasFinished threshold.
var jukeboxSongTicks = map[string]int64{
	"11": 1420, "13": 3560, "5": 3560, "blocks": 6900, "bounce": 4680,
	"cat": 3700, "chirp": 3700, "creator": 3520, "creator_music_box": 1460,
	"far": 3480, "lava_chicken": 2680, "mall": 3940, "mellohi": 1920,
	"otherside": 3900, "pigstep": 2980, "precipice": 5980, "relic": 4360,
	"stal": 3000, "strad": 3760, "tears": 3500, "wait": 4760, "ward": 5020,
}

// jukeboxSongForItem resolves the disc item's JUKEBOX_PLAYABLE
// component: every music_disc_* item names its song after the
// prefix.
func jukeboxSongForItem(item string) string {
	song, ok := strings.CutPrefix(item, "music_disc_")
	if !ok {
		return ""
	}
	if _, ok := jukeboxSongTicks[song]; !ok {
		return ""
	}
	return song
}

// useJukebox is JukeboxBlock's use pair folded: true means the click
// was consumed (insert or eject); false passes to item placement —
// vanilla's double PASS on an empty jukebox with a non-disc item.
func (s *Server) useJukebox(sess *Session, d *dimension, pos world.Pos) bool {
	return s.useJukeboxInHand(sess, d, pos, 0)
}

func (s *Server) useJukeboxInHand(sess *Session, d *dimension, pos world.Pos, hand int32) bool {
	cur := d.w.GetBlock(pos)
	if n, _ := world.StateOwner(cur); n != "jukebox" {
		return false
	}
	s.jukeboxAt(d, pos)
	if rec, _ := world.StateProperty(cur, "has_record"); rec == "true" {
		if hand != 0 {
			return false
		}
		s.jukeboxEject(d, pos)
		return true
	}
	held, slot := sess.stackForHand(hand)
	iname, _ := itemNameOf(held.ID)
	song := jukeboxSongForItem(iname)
	if song == "" {
		return false
	}
	// setTheItem: the BE keeps the stack (consumeAndReturn strips it
	// to count-1), notifyItemChangedInJukebox flips has_record flag-2
	// + BLOCK_CHANGE, then the song player plays.
	disc := held
	disc.Count = 1
	if nb, ok := world.WithProperty(cur, "has_record", "true"); ok {
		s.rsWriteBlockFlag2(d, pos, nb)
	}
	if d.jukeboxes == nil {
		d.jukeboxes = map[world.Pos]*jukeboxEnt{}
	}
	var components map[string]any
	if old := d.jukeboxes[pos]; old != nil {
		components = old.components
	}
	d.jukeboxes[pos] = &jukeboxEnt{
		components: components,
		disc:       disc,
		song:       song,
		songID:     jukeboxSongID[song],
		started:    s.TickCount(),
		playing:    true,
	}
	s.emitGameEvent(d, "block_change",
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, sess, "jukebox")
	s.broadcastLevelEvent(d.name, 1010, pos, jukeboxSongID[song])
	sess.addCustomStat("play_record", 1)
	// consumeAndReturn(1, player): ItemStack.consume's
	// hasInfiniteMaterials gate keeps creative stacks whole.
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
	}
	return true
}

// Ejection clears the block's record flag and stops playback before spawning
// the scattered record item. Block-update packets may be batched separately.
func (s *Server) jukeboxEject(d *dimension, pos world.Pos) {
	s.jukeboxPopRecord(d, pos)
}

// jukeboxStopSong is JukeboxSongPlayer.stop: JUKEBOX_STOP_PLAY at the
// jukebox pos (allays whose remembered pos matches stop dancing) +
// level_event 1011 (record-stop sound kill).
func (s *Server) jukeboxStopSong(d *dimension, pos world.Pos) {
	s.allayJukeboxEvent(d, pos, false)
	s.broadcastLevelEvent(d.name, 1011, pos, 0)
}

// jukeboxDrop is the destroy path: JukeboxBlock's contents pop at
// the cell like every block-entity drop, the entry dies with the
// block, and a playing song stops the usual way.
func (s *Server) jukeboxDrop(d *dimension, pos world.Pos) {
	s.jukeboxPopRecord(d, pos)
}

// jukeboxPopRecord retains the block entity's own components after ejection.
func (s *Server) jukeboxPopRecord(d *dimension, pos world.Pos) bool {
	jb := s.jukeboxAt(d, pos)
	if jb == nil || jb.disc.Empty() {
		return false
	}
	disc, playing := jb.disc, jb.playing
	jb.disc, jb.song, jb.songID, jb.started, jb.playing = ItemStack{}, "", 0, 0, false
	if len(jb.components) == 0 {
		delete(d.jukeboxes, pos)
	}
	cur := d.w.GetBlock(pos)
	name, _ := world.StateOwner(cur)
	if name == "jukebox" {
		if next, ok := world.WithProperty(cur, "has_record", "false"); ok {
			s.rsWriteBlockFlag2(d, pos, next)
		}
		s.emitGameEvent(d, "block_change", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, nil, "jukebox")
	}
	if playing {
		s.jukeboxStopSong(d, pos)
	}

	kind, ok := EntityTypeID("item")
	if ok {
		radius := float64(float32(.7))
		e := s.entities.Spawn(&Entity{Type: kind, Dim: d.name, X: float64(pos.X) + .5 + (rand.Float64()*2-1)*radius, Y: float64(pos.Y) + 1.01, Z: float64(pos.Z) + .5 + (rand.Float64()*2-1)*radius, MotionX: rand.Float64()*.2 - .1, MotionY: .2, MotionZ: rand.Float64()*.2 - .1, Item: disc, PickupDelay: 10})
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(disc))
	}
	return true
}

// Removal always sends the stop notification. Commands can suppress content
// ejection independently of retiring the actual block entity.
func (s *Server) jukeboxOnWrite(d *dimension, pos world.Pos, next, old world.Block, drop bool) {
	oldName, _ := world.StateOwner(old)
	nextName, _ := world.StateOwner(next)
	if oldName != "jukebox" || nextName == "jukebox" {
		return
	}
	if drop {
		s.jukeboxPopRecord(d, pos)
	}
	delete(d.jukeboxes, pos)
	s.jukeboxStopSong(d, pos)
}

// jukeboxTickStep is JukeboxBlockEntity.tick's songPlayer arm:
// length_in_seconds*20 elapsed → stop; every 20th tick of the song
// → JUKEBOX_PLAY (the event keeps every in-range allay dancing —
// setJukeboxPlaying ignores it while already dancing) + the note
// particle. A write that left the jukebox behind kills the entry:
// vanilla's onRemove stops the song with the block entity.
func (s *Server) jukeboxTickStep() {
	now := s.TickCount()
	for _, d := range s.dims {
		for pos, jb := range d.jukeboxes {
			if n, _ := world.StateOwner(d.w.GetBlock(pos)); n != "jukebox" {
				if jb.playing {
					s.jukeboxStopSong(d, pos)
				}
				delete(d.jukeboxes, pos)
				continue
			}
			if !jb.playing {
				continue
			}
			age := now - jb.started
			if age%20 == 0 {
				s.allayJukeboxEvent(d, pos, true)
				s.broadcastJukeboxNote(d, pos)
			}
			if age >= jukeboxSongTicks[jb.song] {
				jb.playing = false
				s.jukeboxStopSong(d, pos)
			}
		}
	}
}

// broadcastJukeboxNote is spawnMusicParticles: NOTE (id 65 in 26.2's
// particle_type registry) at bottom-center +1.2 up, count 0 (one
// directional emit), xd = nextInt(4)/24, speed 1.
func (s *Server) broadcastJukeboxNote(d *dimension, pos world.Pos) {
	s.broadcastParticles(d.name, 65,
		float64(pos.X)+0.5, float64(pos.Y)+1.2, float64(pos.Z)+0.5,
		float64(rand.Intn(4))/24.0, 0, 0, 1, 0)
}

// jukeboxSaveFields exposes the same native record used by interaction and
// ticking. An absent song omits its elapsed-time field.
func (s *Server) jukeboxSaveFields(d *dimension, pos world.Pos, fields map[string]any) {
	if jb := s.jukeboxAt(d, pos); jb != nil {
		if len(jb.components) > 0 {
			fields["components"] = cloneNBTValue(jb.components)
		}
		if !jb.disc.Empty() {
			fields["RecordItem"] = jukeboxDiscNBT(jb.disc)
		}
		if jb.playing {
			fields["ticks_since_song_started"] = s.TickCount() - jb.started
		}
	}
}

func jukeboxDiscFromNBT(fields map[string]any) ItemStack {
	disc := itemFromNBT(fields)
	components, ok := fields["components"].(map[string]any)
	if !ok || len(components) == 0 {
		return disc
	}
	keys := make([]string, 0, len(components))
	for key := range components {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ominous := jukeboxIsOminous(components)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		if ominous && (key == "minecraft:tooltip_display" || key == "minecraft:item_name" || key == "minecraft:rarity") {
			continue
		}
		if key == "minecraft:map_post_processing" || key == "minecraft:trim" {
			continue
		}
		if key == "minecraft:fireworks" {
			if raw, ok := components[key].(map[string]any); ok {
				value := cloneNBTValue(raw).(map[string]any)
				flight := nbtNumberOr(value["flight_duration"], 0)
				if flight < 0 {
					flight += 256
				}
				value["flight_duration"] = int32(flight)
				if _, ok := value["explosions"]; !ok {
					value["explosions"] = nbt.List{Elem: nbt.TagCompound}
				}
				entries = append(entries, key+"="+componentSNBT(value))
				continue
			}
		}
		if strings.HasPrefix(key, "!") {
			entries = append(entries, key)
			continue
		}
		entries = append(entries, key+"="+componentSNBT(components[key]))
	}
	if patch, _, ok := parseItemPatch(strings.Join(entries, ","), 0); ok {
		patch.ID, patch.Count = disc.ID, disc.Count
		patch.Trim = disc.Trim
		if ominous {
			patch.Ominous = true
			patch.BannerPatterns = ""
		}
		return jukeboxNestedComponents(patch, components)
	}
	return disc
}

// Compare modeled effective values, including native component presence.
// Root count and client bundle selection do not identify the record.
func jukeboxSameDisc(a, b ItemStack) bool {
	return reflect.DeepEqual(jukeboxComparableDisc(a), jukeboxComparableDisc(b))
}

func jukeboxComparableDisc(disc ItemStack) ItemStack {
	disc.Count, disc.BundleSelected = 1, 0
	name, _ := world.ItemName(disc.ID)
	if name == "goat_horn" {
		if disc.Instrument == "" && !disc.InstrumentRemoved {
			disc.Instrument = "ponder_goat_horn"
		}
	} else if disc.Instrument == "" {
		// Removing a component absent from the item's prototype is a no-op.
		disc.InstrumentRemoved = false
	}
	if name == "firework_rocket" && disc.Fireworks == "" {
		disc.Fireworks = "1"
	}
	if (name == "shulker_box" || strings.HasSuffix(name, "_shulker_box")) && disc.Contents == nil {
		disc.Contents = new([shulkerSlots]ItemStack)
	}
	return disc
}

func (s *Server) restoreJukebox(d *dimension, pos world.Pos, fields map[string]any) {
	delete(d.genericBE, pos)
	var disc ItemStack
	if raw, ok := fields["RecordItem"].(map[string]any); ok {
		disc = jukeboxDiscFromNBT(raw)
		if disc.ID != 0 && disc.Count == 0 {
			disc.Count = 1
		}
	}
	var components map[string]any
	if raw, ok := fields["components"].(map[string]any); ok {
		components = cloneNBTValue(raw).(map[string]any)
	}
	old := d.jukeboxes[pos]
	same := old != nil && jukeboxSameDisc(old.disc, disc)
	if old != nil && !same && old.playing {
		s.jukeboxStopSong(d, pos)
	}
	if disc.Empty() && len(components) == 0 {
		delete(d.jukeboxes, pos)
		return
	}
	if d.jukeboxes == nil {
		d.jukeboxes = map[world.Pos]*jukeboxEnt{}
	}
	name, _ := itemNameOf(disc.ID)
	song := jukeboxSongForItem(name)
	jb := &jukeboxEnt{disc: disc, components: components, song: song, songID: jukeboxSongID[song]}
	if same {
		jb.started, jb.playing = old.started, old.playing
	}
	if elapsed, ok := nbtNumber(fields["ticks_since_song_started"]); ok && song != "" && !disc.Empty() {
		jb.started = s.TickCount() - int64(elapsed)
		jb.playing = true
	}
	d.jukeboxes[pos] = jb
}

// Older native saves can hold a jukebox in generic storage. Hydrate once,
// removing the old entry so an ejected record cannot later reappear.
func (s *Server) jukeboxAt(d *dimension, pos world.Pos) *jukeboxEnt {
	if jb := d.jukeboxes[pos]; jb != nil {
		return jb
	}
	if g := d.genericBE[pos]; g != nil && g.id == "minecraft:jukebox" {
		s.restoreJukebox(d, pos, g.fields)
		delete(d.genericBE, pos)
	}
	return d.jukeboxes[pos]
}
