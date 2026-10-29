package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestCommandSourceFrameIsolation(t *testing.T) {
	s, overworld := randSim(t)
	nether := s.dims[dimNether]
	frame := commandSourceFrame{dim: nether, pos: [3]float64{12.5, 5.5, 20.5}, display: literal("Logic"), level: 2}
	s.withCommandFrame(&frame, func() reply {
		if frame.results == nil {
			t.Fatal("source frame has no result state")
		}
		if _, ok := s.runCommand(neutralCommandActor, "setblock", []string{"~", "~", "~", "gold_block"}); !ok {
			t.Fatal("neutral setblock was unavailable")
		}
		if !isBlockNamed(nether.w.GetBlock(world.Pos{X: 12, Y: 5, Z: 20}), "gold_block") {
			t.Fatal("neutral origin/dimension was not applied")
		}
		before := frame.results.successes
		if _, ok := s.runCommandAt("RCON", 4, "setblock", []string{"40", "5", "40", "emerald_block"}); !ok {
			t.Fatal("ordinary source setblock was unavailable")
		}
		if !isBlockNamed(overworld.w.GetBlock(world.Pos{X: 40, Y: 5, Z: 40}), "emerald_block") || isBlockNamed(nether.w.GetBlock(world.Pos{X: 40, Y: 5, Z: 40}), "emerald_block") {
			t.Fatal("ordinary command inherited neutral dimension")
		}
		if s.activeCommandSource != &frame || frame.results.successes != before {
			t.Fatal("ordinary dispatch leaked context or results into neutral source")
		}
		if _, ok := s.runCommand(neutralCommandActor, "tick", []string{"query"}); ok {
			t.Fatal("neutral source inherited the open server's level-four permission")
		}
		s.runCommand(neutralCommandActor, "execute", []string{"positioned", "20", "5", "30", "run", "setblock", "~", "~", "~", "diamond_block"})
		if !isBlockNamed(nether.w.GetBlock(world.Pos{X: 20, Y: 5, Z: 30}), "diamond_block") || frame.pos != [3]float64{12.5, 5.5, 20.5} {
			t.Fatal("execute child position was lost or leaked")
		}
		if entities, err := s.selectDataEntities(nil, "@s", 0, false, nil); err != nil || len(entities) != 0 {
			t.Fatal("block source acquired a synthetic executing entity")
		}
		for _, x := range []float64{13.5, 14.5} {
			if _, ok := s.summonMobRaw(nether, "armor_stand", x, 5, 20.5, false, true); !ok {
				t.Fatal("could not create selector fixture")
			}
		}
		before = frame.results.successes
		s.runCommand(neutralCommandActor, "execute", []string{"as", "@e[type=minecraft:armor_stand]", "run", "data", "get", "entity", "@s", "Pos[0]"})
		if frame.results.successes != before+2 {
			t.Fatalf("two entity leaves counted %d successes", frame.results.successes-before)
		}
		return reply{}
	})
	if s.activeCommandSource != nil {
		t.Fatal("neutral command frame survived its scope")
	}
}

func TestCommandSourceTypedOutcomes(t *testing.T) {
	s, d := randSim(t)
	s.registerComponentCommand("redsuccess", func(string, []string) reply {
		return componentReply(component{text: "Usage: a valid red success", color: "red"})
	})
	s.registerComponentCommand("greenfailure", func(string, []string) reply {
		failure := commandError("commands.setblock.failed")
		failure.color = "green"
		return componentReply(failure)
	})
	s.registerComponentCommand("legacytext", func(string, []string) reply { return textReply("untyped result") })
	s.runCommandAt("RCON", 4, "scoreboard", []string{"objectives", "add", "zero", "dummy"})
	s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "set", "holder", "zero", "0"})
	for _, x := range []float64{30, 31} {
		s.summonMobRaw(d, "armor_stand", x, 1, 30, false, true)
	}
	var output []component
	frame := commandSourceFrame{dim: d, pos: [3]float64{30, 1, 30}, display: literal("Logic"), level: 2, output: func(c component) { output = append(output, c) }}
	s.withCommandFrame(&frame, func() reply {
		s.runCommand(neutralCommandActor, "redsuccess", nil)
		if frame.results.successes != 1 {
			t.Fatal("valid red styling or text prefix changed execution outcome")
		}
		s.runCommand(neutralCommandActor, "greenfailure", nil)
		if frame.results.successes != 1 {
			t.Fatal("failure styling changed execution outcome")
		}
		r, _ := s.runCommand(neutralCommandActor, "scoreboard", []string{"players", "get", "holder", "zero"})
		if !r.hasResult || r.result != 0 || frame.results.successes != 2 {
			t.Fatal("successful result zero was treated as failure")
		}
		s.runCommand(neutralCommandActor, "legacytext", nil)
		if frame.results.successes != 2 {
			t.Fatal("untyped legacy result was guessed to be successful")
		}
		beforeOutput := len(output)
		s.runCommand(neutralCommandActor, "execute", []string{"as", "@e[type=minecraft:armor_stand]", "run", "redsuccess"})
		if frame.results.successes != 4 || len(output) != beforeOutput+2 {
			t.Fatal("execute fork counted or emitted its outer reply twice")
		}
		beforeOutput = len(output)
		s.runCommand(neutralCommandActor, "execute", []string{"as", "@e[type=minecraft:armor_stand]", "run", "greenfailure"})
		if frame.results.successes != 4 || len(output) != beforeOutput {
			t.Fatal("forked runtime failure was miscounted or emitted")
		}
		s.runCommand(neutralCommandActor, "execute", []string{"store", "success", "score", "stored", "zero", "run", "scoreboard", "players", "get", "holder", "zero"})
		stored, _ := s.runCommand(neutralCommandActor, "scoreboard", []string{"players", "get", "stored", "zero"})
		if !stored.hasResult || stored.result != 1 || frame.results.successes != 6 {
			t.Fatal("execute store success confused numeric zero with failure")
		}
		return reply{}
	})
}

func TestCommandBlockSourceRotation(t *testing.T) {
	for facing, yaw := range map[string]float32{"south": 0, "west": 90, "north": 180, "east": 270, "up": 270, "down": 270} {
		if got := commandBlockRotation(facing); got != [2]float32{yaw, 0} {
			t.Errorf("%s source rotation=%v", facing, got)
		}
	}
}
