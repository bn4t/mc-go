package conformance_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
)

// These tiny, independently authored packets describe a two-section custom
// dimension. Passing this peer must not depend on mc-go's world, bootstrap
// length, default dimension shape, terrain contents or opening batch size.
func chunkWindowPeer(t *testing.T, protocol int32, fault string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var clients []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			clients = append(clients, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				serveChunkWindowPeer(conn, protocol, fault)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, conn := range clients {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}

func serveChunkWindowPeer(c net.Conn, protocol int32, fault string) {
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	for range 2 { // handshake and Login Start
		if _, err := receive(c, false); err != nil {
			return
		}
	}
	id, _ := hex.DecodeString("eb0450abdedd37c9a7c9fbd5b33bd860")
	login := append(id, text("Conformance")...)
	login = append(login, 0)
	if protocol == 776 {
		login = append(login, make([]byte, 16)...)
	}
	reply(c, 2, login, -1)
	if _, err := receive(c, false); err != nil {
		return
	}
	reply(c, 0x0c, append([]byte{1}, text("minecraft:vanilla")...), -1)
	reply(c, 0x0e, []byte{0}, -1)
	clientInfoSeen := false
	for {
		frame, err := receive(c, false)
		if err != nil || len(frame) == 0 {
			return
		}
		if frame[0] == 7 { // known-packs selection
			if !clientInfoSeen {
				return
			}
			break
		}
		if frame[0] != 0 { // Configuration Client Information
			return
		}
		want := append([]byte{0}, text("en_us")...)
		want = append(want, 2, 0, 1, 0x7f, 1, 0, 1, 0)
		if clientInfoSeen || !bytes.Equal(frame, want) {
			return
		}
		clientInfoSeen = true
	}
	for _, name := range []string{"minecraft:dimension_type", "minecraft:worldgen/biome"} {
		registry := append(text(name), 1)
		registry = append(registry, text("example:compact")...)
		registry = append(registry, 1, 10, 0) // full empty compound NBT
		reply(c, 7, registry, -1)
	}
	reply(c, 0x0d, []byte{0}, -1)
	reply(c, 3, nil, -1)
	for {
		frame, err := receive(c, false)
		if err != nil || len(frame) == 0 {
			return
		}
		if frame[0] == 3 { // Finish Configuration acknowledgement
			break
		}
		if frame[0] != 0 { // optional Configuration Client Information
			return
		}
	}
	join := binary.BigEndian.AppendUint32(nil, 42)
	join = append(join, 0, 1)
	join = append(join, text("example:compact")...)
	join = append(join, 20, 2, 2, 0, 1, 0, 0)
	join = append(join, text("example:compact")...)
	join = append(join, make([]byte, 8)...)
	join = append(join, 1, 255, 0, 0, 0, 0, 0)
	if protocol == 776 {
		join = append(join, 0)
	}
	join = append(join, 0)
	reply(c, 0x31, join, -1)

	// Drain control replies independently: no correctness assertion relies on
	// waiting for socket silence or on the client's scheduler winning a race.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			if _, err := receive(c, false); err != nil {
				return
			}
		}
	}()
	defer func() { _ = c.Close(); <-drained }()
	if fault == "missing-readiness" {
		<-drained
		return
	}
	if fault == "packet-bound" {
		for range 4097 {
			reply(c, 0x7f, nil, -1) // unrelated packets cannot postpone readiness forever
		}
		<-drained
		return
	}
	if fault == "truncated-frame" {
		_, _ = c.Write([]byte{100, 0x48, 7})
		return
	}

	const cx, cz = -7, 11
	position := []byte{7}
	for _, value := range []float64{cx*16 + 0.5, 1, cz*16 + 0.5, 0, 0, 0} {
		position = binary.BigEndian.AppendUint64(position, math.Float64bits(value))
	}
	position = append(position, make([]byte, 12)...)
	if fault == "relative-position" {
		position[len(position)-1] = 1
	}
	if fault == "unknown-position-flags" {
		position[len(position)-2] = 2 // flag bit 9 is undefined
	}
	if fault == "nonfinite-position" {
		binary.BigEndian.PutUint64(position[1:], math.Float64bits(math.Inf(1)))
	}
	if fault == "truncated-position" {
		position = position[:len(position)-1]
	}
	if fault != "missing-position" {
		reply(c, 0x48, position, -1)
	}
	center := append(signedWindowVarInt(cx), signedWindowVarInt(cz)...)
	if fault == "wrong-center" {
		center = append(signedWindowVarInt(cx+1), signedWindowVarInt(cz)...)
	}
	if fault == "trailing-center" {
		center = append(center, 0)
	}
	if fault != "missing-center" {
		reply(c, 0x5e, center, -1)
	}
	if fault == "attributes-before-batch" {
		reply(c, 0x83, windowPeerAttributes(42), -1)
	}
	if fault == "done-without-start" {
		reply(c, 0x0b, []byte{1}, -1)
	}
	if fault != "chunk-without-start" {
		body := []byte(nil)
		if fault == "start-body" {
			body = []byte{0}
		}
		reply(c, 0x0c, body, -1)
	}
	if fault == "nested-start" {
		reply(c, 0x0c, nil, -1)
	}
	x := int32(cx)
	if fault == "opening-misses-own-column" {
		x--
	}
	reply(c, 0x2d, windowPeerChunk(x, cz, fault), -1)
	count := []byte{1}
	if fault == "batch-count" {
		count[0] = 2
	}
	if fault == "trailing-done" {
		count = append(count, 0)
	}
	reply(c, 0x0b, count, -1)
	if fault == "other-attributes-before-self" {
		reply(c, 0x83, []byte{43, 0}, -1)
	}
	if fault != "missing-attributes" && fault != "delayed-attributes" {
		entity := byte(42)
		if fault == "other-entity-attributes" {
			entity = 43
		}
		attrs := windowPeerAttributes(entity)
		if fault == "empty-attributes" {
			attrs = []byte{entity, 0}
		}
		if fault == "truncated-attributes" {
			attrs = attrs[:len(attrs)-1]
		}
		reply(c, 0x83, attrs, -1)
	}
	if fault == "forget-during-window" {
		forgot := binary.BigEndian.AppendUint32(nil, uint32(cz))
		forgotX := int32(cx)
		forgot = binary.BigEndian.AppendUint32(forgot, uint32(forgotX))
		reply(c, 0x25, forgot, -1)
	}
	if fault == "respawn-during-window" {
		reply(c, 0x52, nil, -1)
	}
	if fault == "incomplete-window" {
		<-drained
		return
	}
	reply(c, 0x0c, nil, -1)
	first := true
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			x, z := int32(cx+dx), int32(cz+dz)
			if first && fault == "duplicate-column" {
				x, z = cx, cz
			}
			if first && fault == "outside-window" {
				x = cx - 4
			}
			first = false
			reply(c, 0x2d, windowPeerChunk(x, z, ""), -1)
		}
	}
	if fault != "missing-final-done" {
		reply(c, 0x0b, []byte{48}, -1)
	}
	if fault == "delayed-attributes" {
		reply(c, 0x83, windowPeerAttributes(42), -1)
	}
	<-drained
}

func windowPeerAttributes(entity byte) []byte {
	body := []byte{entity, 1, 0} // entity, one attribute, registry attribute zero
	body = binary.BigEndian.AppendUint64(body, math.Float64bits(20))
	return append(body, 0) // no modifiers
}

// vint in the existing fixture intentionally accepts positive counts only.
// Coordinates need Minecraft's signed two's-complement VarInt encoding.
func signedWindowVarInt(value int32) []byte {
	u := uint32(value)
	var out []byte
	for u >= 128 {
		out = append(out, byte(u)|128)
		u >>= 7
	}
	return append(out, byte(u))
}

func windowPeerChunk(x, z int32, fault string) []byte {
	body := binary.BigEndian.AppendUint32(nil, uint32(x))
	body = binary.BigEndian.AppendUint32(body, uint32(z))
	body = append(body, 0) // no heightmaps
	var sections []byte
	n := 2
	if fault == "wrong-section-shape" {
		n++
	}
	for i := 0; i < n; i++ {
		section := []byte{0, 0, 0, 0, 0, 7, 0, 0}
		if fault == "block-registry-bound" {
			section[5] = 8
		}
		if fault == "biome-registry-bound" {
			section[7] = 1
		}
		if fault == "section-count" {
			binary.BigEndian.PutUint16(section, 4097)
		}
		sections = append(sections, section...)
	}
	body = append(body, vint(len(sections))...)
	body = append(body, sections...)
	body = append(body, 0) // block entities
	var data uint64
	if fault == "high-mask-bit" {
		data = 1 << 63
	}
	if fault == "overlapping-masks" || fault == "array-mask-count" || fault == "light-array-size" {
		data = 1
	}
	empty := uint64(15) &^ data
	if fault == "overlapping-masks" {
		empty = 15
	}
	for _, mask := range []uint64{data, 0, empty, 15} {
		body = append(body, 1)
		body = binary.BigEndian.AppendUint64(body, mask)
	}
	arrays := 0
	if data != 0 && fault != "array-mask-count" {
		arrays = 1
	}
	body = append(body, byte(arrays))
	if arrays != 0 {
		size := 2048
		if fault == "light-array-size" {
			size--
		}
		body = append(body, vint(size)...)
		body = append(body, make([]byte, size)...)
	}
	body = append(body, 0)
	if fault == "truncated-chunk" {
		body = body[:len(body)-1]
	}
	if fault == "trailing-chunk" {
		body = append(body, 0)
	}
	return body
}

func windowPeerConfig(addr string, protocol int32) conformance.Config {
	c := config(addr, "play-chunks")
	c.Protocol, c.Version = protocol, ""
	c.Timeout = 250 * time.Millisecond
	c.Play = &conformance.PlayConfig{Shape: conformance.ChunkShape{Sections: 2, BlockStates: 8, Biomes: 1}}
	return c
}

func TestPlayChunkWindowIndependentPeers(t *testing.T) {
	for _, protocol := range []int32{775, 776} {
		for _, mode := range []string{"", "delayed-attributes", "other-attributes-before-self"} {
			t.Run(fmt.Sprintf("%d/%s", protocol, mode), func(t *testing.T) {
				c := windowPeerConfig(chunkWindowPeer(t, protocol, mode), protocol)
				report := conformance.Run(context.Background(), c)
				result := find(t, report, "play.chunk-window")
				if result.Status != conformance.Pass || report.ExitCode() != 0 || len(result.Transcript) == 0 {
					t.Fatalf("independent custom-dimension peer rejected: %+v", report.Results)
				}
			})
		}
	}
}

func TestPlayChunkWindowAdversarialPeers(t *testing.T) {
	for _, fault := range []string{
		"missing-readiness", "missing-position", "truncated-position", "relative-position", "unknown-position-flags", "nonfinite-position", "missing-center", "wrong-center", "trailing-center",
		"attributes-before-batch", "missing-attributes", "other-entity-attributes", "empty-attributes", "truncated-attributes", "incomplete-window", "missing-final-done",
		"done-without-start", "chunk-without-start", "start-body", "nested-start", "batch-count", "trailing-done",
		"opening-misses-own-column", "duplicate-column", "outside-window", "wrong-section-shape", "block-registry-bound", "biome-registry-bound", "section-count",
		"high-mask-bit", "overlapping-masks", "array-mask-count", "light-array-size", "truncated-chunk", "trailing-chunk",
		"truncated-frame", "packet-bound", "forget-during-window", "respawn-during-window",
	} {
		t.Run(fault, func(t *testing.T) {
			c := windowPeerConfig(chunkWindowPeer(t, 776, fault), 776)
			report := conformance.Run(context.Background(), c)
			result := find(t, report, "play.chunk-window")
			if result.Status != conformance.Fail || report.ExitCode() != 1 || !result.Declared || len(result.Transcript) == 0 {
				t.Fatalf("bad peer did not fail its declared wire contract: %+v", report.Results)
			}
			if fault == "packet-bound" && !strings.Contains(result.Detail, "4096") {
				t.Fatalf("unrelated packet flood hit timeout instead of packet limit: %s", result.Detail)
			}
		})
	}
}

func TestPlayChunkWindowRequiresDeclaredBoundedShape(t *testing.T) {
	for _, shape := range []*conformance.PlayConfig{
		nil, {}, {Shape: conformance.ChunkShape{Sections: 257, BlockStates: 8, Biomes: 1}},
		{Shape: conformance.ChunkShape{Sections: 2, BlockStates: 0, Biomes: 1}},
		{Shape: conformance.ChunkShape{Sections: 2, BlockStates: 8, Biomes: -1}},
		{Shape: conformance.ChunkShape{Sections: 2, BlockStates: 1<<24 + 1, Biomes: 1}},
		{Shape: conformance.ChunkShape{Sections: 2, BlockStates: 8, Biomes: 1<<20 + 1}},
	} {
		c := windowPeerConfig("example.invalid:25565", 776)
		c.Play = shape
		report := conformance.Run(context.Background(), c)
		if report.ExitCode() != 2 || len(report.Results) != 1 || report.Results[0].Status != conformance.Infrastructure || strings.Contains(report.Results[0].Detail, "dial:") {
			t.Fatalf("invalid manifest reached the network: %+v", report)
		}
	}
	c := windowPeerConfig("example.invalid:25565", 774)
	report := conformance.Run(context.Background(), c)
	result := find(t, report, "play.chunk-window")
	if result.Status != conformance.Unsupported || !result.Declared || report.ExitCode() != 2 {
		t.Fatalf("unsupported declared profile passed: %+v", report)
	}
	c = config(endpoint(t, behavior{compression: -1}), "status")
	c.Play = &conformance.PlayConfig{} // irrelevant when play-chunks is omitted
	report = conformance.Run(context.Background(), c)
	result = find(t, report, "play.chunk-window")
	if result.Status != conformance.Unsupported || result.Declared || report.ExitCode() != 0 {
		t.Fatalf("omitted play contract changed a passing run: %+v", report)
	}
}
