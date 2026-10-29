package blackbox

import "testing"

const toolChestContentsBB = `{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`

var toolChestChecksBB = []string{`execute if data block 0 159 0 {CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`}

func TestCopperChestNativeStorageParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{{name: "copper_chest_native_storage", main: "dirt", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", hand: 1, mainStart: 3, mainEnd: 3, before: toolChestChecksBB, checks: toolChestChecksBB}})
}
