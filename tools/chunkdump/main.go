// Command chunkdump decodes a captured 776 Chunk Data and Update Light
// body (see tests/blackbox TestCaptureVanillaWorld) and prints its
// structure. It exists to verify mc-go's chunk encoder against the real
// server byte by byte.
//
// Usage: go run ./tools/chunkdump <body.bin>
package main

import (
	"fmt"
	"os"

	"mc-go/internal/protocol"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: chunkdump <body.bin>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	r := protocol.NewPacketReader(raw)
	x, _ := r.Int()
	z, _ := r.Int()
	fmt.Printf("chunk (%d,%d) body=%d bytes\n", x, z, len(raw))

	hmCount, _ := r.VarInt()
	fmt.Printf("heightmaps: %d\n", hmCount)
	for i := int32(0); i < hmCount; i++ {
		typ, _ := r.VarInt()
		n, _ := r.VarInt()
		longs := make([]int64, n)
		for j := range longs {
			v, err := r.Long()
			if err != nil {
				fatal(fmt.Errorf("heightmap %d long %d: %w", typ, j, err))
			}
			longs[j] = v
		}
		fmt.Printf("  type=%d longs=%d first=%016x\n", typ, n, uint64(longs[0]))
	}

	dataLen, _ := r.VarInt()
	data, err := r.Read(int(dataLen))
	if err != nil {
		fatal(err)
	}
	dr := protocol.NewPacketReader(data)
	fmt.Printf("data: %d bytes\n", dataLen)
	for si := 0; si < 24; si++ {
		bc, err := dr.Short()
		if err != nil {
			fatal(fmt.Errorf("section %d block count: %w", si, err))
		}
		fc, _ := dr.Short()
		blk := readContainer(dr, 4096, 4)
		bio := readContainer(dr, 64, 1)
		if bc != 0 || fc != 0 || si == 0 {
			fmt.Printf("  s%02d blockCount=%d fluidCount=%d block=%s biome=%s\n", si, bc, fc, blk, bio)
		}
	}
	if dr.Remaining() != 0 {
		fmt.Printf("  data trailing: %d bytes\n", dr.Remaining())
	}

	beCount, _ := r.VarInt()
	fmt.Printf("block entities: %d\n", beCount)
	for i := int32(0); i < beCount; i++ {
		packed, err := r.Long()
		if err != nil {
			fatal(err)
		}
		y, _ := r.Short()
		typ, _ := r.VarInt()
		fmt.Printf("  be %d: packed=%016x y=%d type=%d (nbt follows)\n", i, uint64(packed), y, typ)
		fatal(fmt.Errorf("block entity NBT skip not implemented"))
	}

	skyMask := readBitSet(r)
	blockMask := readBitSet(r)
	emptySky := readBitSet(r)
	emptyBlock := readBitSet(r)
	fmt.Printf("light: sky=%x block=%x emptySky=%x emptyBlock=%x remaining=%d\n",
		skyMask, blockMask, emptySky, emptyBlock, r.Remaining())
	for i := 0; i < len(skyMask)*64; i++ {
		if skyMask[i/64]&(1<<(i%64)) == 0 {
			continue
		}
		n, _ := r.VarInt()
		arr, _ := r.Read(int(n))
		sum := 0
		for _, b := range arr {
			sum += int(b)
		}
		fmt.Printf("  sky[%d] %d bytes value-sum=%d first=%x last=%x\n", i, n, sum, arr[:1], arr[n-1:])
	}
	for i := 0; i < len(blockMask)*64; i++ {
		if blockMask[i/64]&(1<<(i%64)) == 0 {
			continue
		}
		n, _ := r.VarInt()
		_, _ = r.Read(int(n))
		fmt.Printf("  block[%d] %d bytes\n", i, n)
	}
	fmt.Printf("light trailing: %d bytes\n", r.Remaining())
}

func readContainer(r *protocol.PacketReader, entries int, indirectMin int) string {
	bpe, err := r.Byte()
	if err != nil {
		fatal(err)
	}
	if bpe == 0 {
		v, _ := r.VarInt()
		return fmt.Sprintf("single(%d)", v)
	}
	typ := "indirect"
	var pal []int32
	if int(bpe) > 8 || (entries == 64 && int(bpe) > 3) {
		typ = "direct"
	} else {
		n, _ := r.VarInt()
		pal = make([]int32, n)
		for i := range pal {
			pal[i], _ = r.VarInt()
		}
	}
	perLong := 64 / int(bpe)
	longs := (entries + perLong - 1) / perLong
	_, _ = r.Read(longs * 8)
	return fmt.Sprintf("%s(bpe=%d palette=%v longs=%d)", typ, bpe, pal, longs)
}

func readBitSet(r *protocol.PacketReader) []uint64 {
	n, err := r.VarInt()
	if err != nil || n < 0 {
		fatal(fmt.Errorf("bitset: %v", err))
	}
	out := make([]uint64, n)
	for i := range out {
		v, _ := r.Long()
		out[i] = uint64(v)
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "chunkdump:", err)
	os.Exit(1)
}
