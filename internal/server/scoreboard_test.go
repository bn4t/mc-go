package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

func TestScoreboardCommands(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Alice")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	if out := s.runScoreboard("Alice", []string{"objectives", "add", "test", "dummy", `{"text":"Test Score"}`}); out.comp.translate != "commands.scoreboard.objectives.add.success" {
		t.Fatalf("add = %v", out.comp)
	}
	p, err := waitPacket(t, pr, IDPlayObjectiveCB)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	name, _ := r.String()
	mode, _ := r.Byte()
	if name != "test" || mode != 0 {
		t.Fatalf("objective = %q mode %d", name, mode)
	}
	// add-mode body: display component NBT, render-type VarInt,
	// optional number format.
	rest, _ := r.Read(r.Remaining())
	comp, err := nbt.ReadUnnamed(bytes.NewReader(rest[:]))
	if err != nil {
		t.Fatalf("display component: %v", err)
	}
	if m, ok := comp.(map[string]any); !ok || m["text"] != "Test Score" {
		t.Fatalf("display = %#v", comp)
	}

	if out := s.runScoreboard("Alice", []string{"players", "set", "Alice", "test", "7"}); out.comp.translate != "commands.scoreboard.players.set.success.single" {
		t.Fatalf("set = %v", out.comp)
	}
	sp, err := waitPacket(t, pr, IDPlayUpdateScore)
	if err != nil {
		t.Fatal(err)
	}
	sr := protocol.NewPacketReader(sp.Data)
	entity, _ := sr.String()
	objective, _ := sr.String()
	value, _ := sr.VarInt()
	if entity != "Alice" || objective != "test" || value != 7 {
		t.Fatalf("score = %s/%s/%d", entity, objective, value)
	}

	if out := s.runScoreboard("Alice", []string{"players", "get", "Alice", "test"}); out.comp.translate != "commands.scoreboard.players.get.success" {
		t.Fatalf("get = %v", out.comp)
	}

	if out := s.runScoreboard("Alice", []string{"objectives", "setdisplay", "sidebar", "test"}); out.comp.translate != "commands.scoreboard.objectives.display.set" {
		t.Fatalf("setdisplay = %v", out.comp)
	}
	dp, err := waitPacket(t, pr, IDPlayDisplayObjective)
	if err != nil {
		t.Fatal(err)
	}
	dr := protocol.NewPacketReader(dp.Data)
	position, _ := dr.VarInt()
	sideName, _ := dr.String()
	if position != 1 || sideName != "test" {
		t.Fatalf("display = %d/%q", position, sideName)
	}

	if out := s.runScoreboard("Alice", []string{"players", "reset", "Alice", "test"}); out.comp.translate != "commands.scoreboard.players.reset.specific.single" {
		t.Fatalf("reset = %v", out.comp)
	}
	rp, err := waitPacket(t, pr, IDPlayResetScore)
	if err != nil {
		t.Fatal(err)
	}
	rr := protocol.NewPacketReader(rp.Data)
	entity, _ = rr.String()
	present, _ := rr.Bool()
	objective = ""
	if present {
		objective, _ = rr.String()
	}
	if entity != "Alice" || !present || objective != "test" {
		t.Fatalf("reset = %s present=%v %q", entity, present, objective)
	}
}
