package blackbox

import "testing"

// Command selectors omit a dying corpse before its actual removal. The
// runner proves spawn/death/removal through the paired entity's wire packets.
func TestPlacementDyingBodyParity(t *testing.T) {
	cleanup := []string{`tp @e[type=cow] 1000 160 1000`, `kill @e[type=cow]`}
	summon := []string{`summon cow 0.5 160 0.5 {Tags:["body_fixture"],NoAI:1b}`}
	checkHandUseCases(t, []handUseCase{
		{name: "living_body", bodyPhase: "alive", main: "stone", mainStart: 3, mainEnd: 3, clicked: "stone", wantBlock: "air", pre: cleanup, setup: summon, before: []string{`execute if entity @e[tag=body_fixture,nbt={Health:10.0f}]`}},
		{name: "dying_body", bodyPhase: "dying", main: "stone", mainStart: 3, mainEnd: 3, clicked: "stone", wantBlock: "air", pre: cleanup, setup: summon, before: []string{`execute unless entity @e[tag=body_fixture]`}},
		{name: "retired_body", bodyPhase: "retired", main: "stone", mainStart: 3, mainEnd: 2, clicked: "stone", wantBlock: "stone", pre: cleanup, setup: summon, before: []string{`execute unless entity @e[tag=body_fixture]`}},
	})
}
