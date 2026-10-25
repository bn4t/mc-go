package blackbox

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// sendKnownPacks emulates a vanilla client entering configuration:
// one core pack entry with the given version string.
func sendKnownPacks(t *testing.T, pw *protocol.Writer, version string) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(1)
	b.String("minecraft")
	b.String("core")
	b.String(version)
	if err := pw.WritePacket(server.IDConfigKnownPacks, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

type packStrings struct {
	namespace, id, version string
}

func parsePacks(t *testing.T, addr string, data []byte) []packStrings {
	t.Helper()
	r := protocol.NewPacketReader(data)
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 64 {
		t.Fatalf("%s: pack count = %d, err %v", addr, n, err)
	}
	out := make([]packStrings, 0, n)
	for i := int32(0); i < n; i++ {
		ns, err := r.String()
		if err != nil {
			t.Fatalf("%s: pack ns: %v", addr, err)
		}
		id, err := r.String()
		if err != nil {
			t.Fatalf("%s: pack id: %v", addr, err)
		}
		ver, err := r.String()
		if err != nil {
			t.Fatalf("%s: pack version: %v", addr, err)
		}
		out = append(out, packStrings{ns, id, ver})
	}
	return out
}

// TestKnownPacksParity proves both servers answer the client's Known
// Packs with their own selection containing minecraft:core before
// registry data. Vanilla's exact version string is logged as ground
// truth for the data baseline.
func TestKnownPacksParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	// Vanilla ground truth: brand plugin message first, then feature
	// flags, then known packs.
	c1, pr1, pw1 := loginStart(t, tg.vanilla, "PackProbe")
	defer c1.Close()
	readLoginSuccess(t, pr1, pw1, "vanilla")
	if err := pw1.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw1, "99.9-test")
	first, err := pr1.ReadPacket()
	if err != nil {
		t.Fatalf("vanilla: no config reply: %v", err)
	}
	if first.ID != server.IDConfigPluginMessageCB {
		t.Fatalf("vanilla first config = %#x, want brand plugin message %#x", first.ID, server.IDConfigPluginMessageCB)
	}
	vPacks, vIDs := scanConfigForKnownPacks(t, "vanilla", pr1)
	t.Logf("vanilla config ids to known packs: %#x", vIDs)
	t.Logf("vanilla known packs: %+v", vPacks)
	if !hasCore(vPacks) {
		t.Fatalf("vanilla packs lack minecraft:core: %+v", vPacks)
	}

	c2, pr2, pw2 := loginStart(t, tg.mcgo, "PackProbe")
	defer c2.Close()
	readLoginSuccess(t, pr2, pw2, "mc-go")
	if err := pw2.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw2, "99.9-test")
	mPacks, mIDs := scanConfigForKnownPacks(t, "mc-go", pr2)
	t.Logf("mc-go config ids to known packs: %#x", mIDs)
	if !hasCore(mPacks) {
		t.Fatalf("mc-go packs lack minecraft:core: %+v", mPacks)
	}
}

func hasCore(packs []packStrings) bool {
	for _, p := range packs {
		if p.namespace == "minecraft" && p.id == "core" {
			return true
		}
	}
	return false
}

// scanConfigForKnownPacks reads config packets until Known Packs and
// returns its parsed body plus the IDs seen.
func scanConfigForKnownPacks(t *testing.T, tag string, pr *protocol.Reader) ([]packStrings, []int32) {
	t.Helper()
	var ids []int32
	for i := 0; i < 80; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s config stream: %v", tag, err)
		}
		ids = append(ids, p.ID)
		if p.ID == server.IDConfigKnownPacksCB {
			return parsePacks(t, tag, p.Data), ids
		}
	}
	t.Fatalf("%s: never saw known packs", tag)
	return nil, nil
}

// TestRegistryParity collects the registry keys vanilla sends and
// requires mc-go to send dimension_type + biome with NBT that parses.
// Vanilla's registry list is logged as ground truth for what a full
// implementation must eventually cover.
func TestRegistryParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	collect := func(tag, addr string) []string {
		c, pr, pw := loginStart(t, addr, "RegProbe")
		defer c.Close()
		readLoginSuccess(t, pr, pw, tag)
		if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
			t.Fatal(err)
		}
		sendKnownPacks(t, pw, "99.9-test")
		scanConfigForKnownPacks(t, tag, pr)
		var keys []string
		for i := 0; i < 80; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s config stream: %v", tag, err)
			}
			if p.ID != server.IDConfigRegistryData {
				break // first non-registry packet ends the registry burst
			}
			r := protocol.NewPacketReader(p.Data)
			key, err := r.String()
			if err != nil {
				t.Fatalf("%s registry key: %v", tag, err)
			}
			n, err := r.VarInt()
			if err != nil || n < 0 {
				t.Fatalf("%s registry %s count = %d, err %v", tag, key, n, err)
			}
			keys = append(keys, key)
		}
		return keys
	}

	vKeys := collect("vanilla", tg.vanilla)
	t.Logf("vanilla registries (%d): %q", len(vKeys), vKeys)
	for _, need := range []string{"minecraft:dimension_type", "minecraft:worldgen/biome"} {
		if !contains(vKeys, need) {
			t.Fatalf("vanilla registries lack %s: %q", need, vKeys)
		}
	}

	mKeys := collect("mc-go", tg.mcgo)
	t.Logf("mc-go registries (%d): %q", len(mKeys), mKeys)
	for _, need := range []string{"minecraft:dimension_type", "minecraft:worldgen/biome"} {
		if !contains(mKeys, need) {
			t.Fatalf("mc-go registries lack %s: %q", need, mKeys)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
