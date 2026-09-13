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

	if out := s.runScoreboard("Alice", []string{"objectives", "add", "test", "Test", "Score"}); out != "Created objective test" {
		t.Fatalf("add = %q", out)
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
	present, _ := r.Bool()
	if !present {
		t.Fatal("objective display missing")
	}
	rest, _ := r.Read(r.Remaining())
	comp, err := nbt.ReadUnnamed(bytes.NewReader(rest[:]))
	if err != nil {
		t.Fatalf("display component: %v", err)
	}
	if comp != "Test Score" {
		t.Fatalf("display = %#v", comp)
	}

	if out := s.runScoreboard("Alice", []string{"players", "set", "Alice", "test", "7"}); out != "Set Alice test to 7" {
		t.Fatalf("set = %q", out)
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

	if out := s.runScoreboard("Alice", []string{"players", "get", "Alice", "test"}); out != "Alice has 7" {
		t.Fatalf("get = %q", out)
	}

	if out := s.runScoreboard("Alice", []string{"objectives", "setdisplay", "sidebar", "test"}); out != "Set sidebar to test" {
		t.Fatalf("setdisplay = %q", out)
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

	if out := s.runScoreboard("Alice", []string{"players", "reset", "Alice", "test"}); out != "Reset Alice" {
		t.Fatalf("reset = %q", out)
	}
	rp, err := waitPacket(t, pr, IDPlayResetScore)
	if err != nil {
		t.Fatal(err)
	}
	rr := protocol.NewPacketReader(rp.Data)
	entity, _ = rr.String()
	present, _ = rr.Bool()
	objective = ""
	if present {
		objective, _ = rr.String()
	}
	if entity != "Alice" || !present || objective != "test" {
		t.Fatalf("reset = %s present=%v %q", entity, present, objective)
	}
}
