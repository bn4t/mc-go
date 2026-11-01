//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"testing"
	"time"
)

type seedProbeObservation struct {
	join, respawn int64
	seedReply     []any
}

func seedObserve(t *testing.T, addr, tag string) seedProbeObservation {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "SeedProbe")
	defer c.Close()
	p, err := pr.ReadPacket()
	if err != nil || p.ID != server.IDPlayJoinGame {
		t.Fatalf("%s join packet %x %v", tag, p.ID, err)
	}
	r := protocol.NewPacketReader(p.Data)
	r.Int()
	r.Bool()
	n, _ := r.VarInt()
	for i := int32(0); i < n; i++ {
		r.String()
	}
	r.VarInt()
	r.VarInt()
	r.VarInt()
	r.Bool()
	r.Bool()
	r.Bool()
	r.VarInt()
	dim, _ := r.String()
	hash, err := r.Long()
	if err != nil {
		t.Fatal(err)
	}
	awaitPlayReadyBlackbox(t, tag, c, pr, pw)
	target := "minecraft:the_nether"
	if dim == target {
		target = "minecraft:overworld"
	}
	sendCommand(t, pw, "execute in "+target+" run tp @s 100 40 100")
	resp := readRespawn(t, tag, pr, pw)
	if resp.dim != target {
		t.Fatalf("wrong dimension %s", resp.dim)
	}
	sink := newChatSinkResolvedTP(pr, pw)
	sendCommand(t, pw, "seed")
	sendCommand(t, pw, "say SEED_PROBE_END")
	lines := waitForExactSayBB(t, sink, "SEED_PROBE_END", 30*time.Second)
	var reply []any
	for _, line := range lines {
		key, args, ok := componentArgs(line)
		if ok && key == "commands.seed.success" {
			reply = args
			break
		}
	}
	if reply == nil {
		t.Fatalf("%s missing seed reply", tag)
	}
	t.Logf("%s join=%d respawn=%d seed=%v", tag, hash, resp.seed, reply)
	return seedProbeObservation{hash, resp.seed, reply}
}
func TestSeedConfiguredParity(t *testing.T) {
	for _, kind := range []string{"flat", "normal"} {
		for _, seed := range []int64{0, 1} {
			t.Run(fmt.Sprintf("%s_%d", kind, seed), func(t *testing.T) {
				targets := requireBlackboxFiles(t, map[string]string{"level-type": "minecraft:" + kind, "level-seed": fmt.Sprint(seed), "view-distance": "2", "simulation-distance": "2", "generate-structures": "false"}, map[string]string{"ops.json": opsJSONFor("SeedProbe")}, func(s *server.Server) {
					s.LevelType = kind
					s.WorldSeed = seed
					s.ViewDistance = 2
					s.SimulationDistance = 2
					s.GenerateStructures = false
				})
				defer targets.cleanup()
				native := seedObserve(t, targets.vanilla, "native")
				actual := seedObserve(t, targets.mcgo, "go")
				if !reflect.DeepEqual(native, actual) {
					t.Errorf("seed%d native=%+v go=%+v", seed, native, actual)
				}
			})
		}
	}
}

func TestSeedDefaultParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("SeedProbe")}, nil)
	defer targets.cleanup()
	native := seedObserve(t, targets.vanilla, "native-default")
	actual := seedObserve(t, targets.mcgo, "go-default")
	if !reflect.DeepEqual(native, actual) {
		t.Errorf("default native=%+v go=%+v", native, actual)
	}
}
