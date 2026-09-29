package server

import (
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// Jukebox block entity measured from vanilla 26.2
// (JukeboxBlock/JukeboxBlockEntity/JukeboxSongPlayer):
//   - useItemOn: has_record already set answers TRY_WITH_EMPTY_HAND
//     regardless of the held item → the click falls through to
//     useWithoutItem's popOutTheItem — any click on a loaded jukebox
//     ejects (a held disc never places). An empty jukebox takes a
//     JUKEBOX_PLAYABLE item (a music disc) via setTheItem; anything
//     else passes both paths → the item places.
//   - setTheItem → notifyItemChangedInJukebox writes has_record with
//     setBlock flag 2 (client update, no neighbours) + a BLOCK_CHANGE
//     game event; the song player then emits level_event 1010 carrying
//     the song's jukebox_song registry id.
//   - JukeboxSongPlayer.tick re-emits GameEvent.JUKEBOX_PLAY every 20
//     ticks (a note particle rides it) until length_in_seconds*20,
//     then stop(): GameEvent.JUKEBOX_STOP_PLAY + level_event 1011.
//   - popOutTheItem drops the disc at pos+(0.5,1.01,0.5) scattered
//     ±0.7 on xz with the default 10-tick pickup delay, then the same
//     has_record=false/BLOCK_CHANGE pair and the stop events.
type jukeboxEnt struct {
	disc    ItemStack
	song    string
	songID  int32
	started int64 // s.TickCount() at play start
	playing bool
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
	cur := d.w.GetBlock(pos)
	if n, _ := world.StateOwner(cur); n != "jukebox" {
		return false
	}
	if rec, _ := world.StateProperty(cur, "has_record"); rec == "true" {
		s.jukeboxEject(d, pos, cur)
		return true
	}
	held, slot := sess.stackForHand(0)
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
	d.jukeboxes[pos] = &jukeboxEnt{
		disc:    disc,
		song:    song,
		songID:  jukeboxSongID[song],
		started: s.TickCount(),
		playing: true,
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

// jukeboxEject is JukeboxBlockEntity.popOutTheItem: spawn the held
// disc at pos+(0.5,1.01,0.5) scattered ±0.7 xz with the default
// pickup delay, then has_record=false flag-2 + BLOCK_CHANGE and the
// song stop (JUKEBOX_STOP_PLAY + level_event 1011).
func (s *Server) jukeboxEject(d *dimension, pos world.Pos, cur world.Block) {
	jb := d.jukeboxes[pos]
	if jb == nil || jb.disc.Empty() {
		return
	}
	disc := jb.disc
	delete(d.jukeboxes, pos)
	itemType, ok := EntityTypeID("item")
	if ok {
		e := s.entities.Spawn(&Entity{
			Type:        itemType,
			Dim:         d.name,
			X:           float64(pos.X) + 0.5 + (rand.Float64()*2-1)*0.7,
			Y:           float64(pos.Y) + 1.01,
			Z:           float64(pos.Z) + 0.5 + (rand.Float64()*2-1)*0.7,
			Item:        disc,
			PickupDelay: 10,
		})
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
	if nb, ok := world.WithProperty(cur, "has_record", "false"); ok {
		s.rsWriteBlockFlag2(d, pos, nb)
	}
	s.emitGameEvent(d, "block_change",
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, nil, "jukebox")
	// onSongChanged → songPlayer.stop, which self-guards on song != null:
	// a natural finish already emitted STOP_PLAY/1011, so eject only
	// re-emits when the disc is still playing.
	if jb.playing {
		s.jukeboxStopSong(d, pos)
	}
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
	jb := d.jukeboxes[pos]
	if jb == nil {
		return
	}
	delete(d.jukeboxes, pos)
	if !jb.disc.Empty() {
		s.spawnItemDrop(d.name, pos, jb.disc)
	}
	if jb.playing {
		s.jukeboxStopSong(d, pos)
	}
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
