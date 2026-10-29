package blackbox

import "testing"

func TestJukeboxEjectOrderParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{{name: "record_ejection_order", main: "dirt", mainStart: 3, mainEnd: 3,
		clicked: `jukebox[has_record=true]{RecordItem:{id:"minecraft:music_disc_cat",count:1},ticks_since_song_started:7L}`, wantBlock: "air", order: true,
		pre:    []string{`summon item 1.5 161.25 0.5 {Item:{id:"minecraft:stick",count:1},PickupDelay:32767s}`},
		checks: []string{`execute if block 0 159 0 jukebox[has_record=false]`, `execute unless data block 0 159 0 RecordItem`, `execute if entity @e[type=item,nbt={Item:{id:"minecraft:music_disc_cat",count:1}}]`},
	}})
}
