package protocol

import (
	"bytes"
	"compress/zlib"
)

// CreateChunkDataPacket creates a Chunk Data packet (0x21) for Minecraft 1.8.9
func CreateChunkDataPacket(chunkX, chunkZ int32, groundUpContinuous bool, primaryBitMask uint16, data []byte) *Packet {
	packet := NewPacket(0x21)
	
	// Chunk coordinates
	packet.WriteInt(chunkX)
	packet.WriteInt(chunkZ)
	
	// Ground-up continuous (true for new chunks)
	packet.WriteBoolean(groundUpContinuous)
	
	// Primary bit mask (which sections are included)
	packet.WriteShort(int16(primaryBitMask))
	
	// Data array
	packet.WriteVarInt(int32(len(data)))
	packet.WriteBytes(data)
	
	return packet
}

// CreateEmptyChunkData creates chunk data for a flat world with ground
func CreateEmptyChunkData() (uint16, []byte) {
	var buffer bytes.Buffer
	
	// Send sections 0-4 (y=0 to y=79) for a simple flat world
	primaryBitMask := uint16(0x001F) // Sections 0,1,2,3,4
	
	// For each section (16x16x16 blocks)
	for section := 0; section < 5; section++ {
		// In 1.8.9, blocks are stored as shorts (2 bytes each)
		// Format: 4096 shorts, each containing block ID (12 bits) and metadata (4 bits)
		for y := 0; y < 16; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					worldY := section*16 + y
					var blockID uint16 = 0 // Air by default
					var metadata uint16 = 0
					
					if worldY == 0 {
						// Bedrock at y=0
						blockID = 7
					} else if worldY > 0 && worldY < 60 {
						// Stone layers
						blockID = 1
					} else if worldY >= 60 && worldY < 63 {
						// Dirt layers
						blockID = 3
					} else if worldY == 63 {
						// Grass block
						blockID = 2
					}
					// Above y=63 is air (blockID = 0)
					
					// Combine block ID and metadata into a short
					blockData := (blockID << 4) | (metadata & 0x0F)
					
					// Write as little-endian short
					buffer.Write([]byte{byte(blockData & 0xFF), byte((blockData >> 8) & 0xFF)})
				}
			}
		}
	}
	
	// Block light (half byte per block)
	for section := 0; section < 5; section++ {
		lightData := make([]byte, 2048)
		buffer.Write(lightData)
	}
	
	// Sky light (half byte per block)
	for section := 0; section < 5; section++ {
		skyLightData := make([]byte, 2048)
		// Set to full brightness for above-ground sections
		if section >= 3 {
			for i := range skyLightData {
				skyLightData[i] = 0xFF
			}
		}
		buffer.Write(skyLightData)
	}
	
	// Biome data (256 bytes, one per column)
	biomeData := make([]byte, 256)
	for i := range biomeData {
		biomeData[i] = 1 // Plains biome
	}
	buffer.Write(biomeData)
	
	return primaryBitMask, buffer.Bytes()
}

// CompressChunkData compresses chunk data using zlib
func CompressChunkData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}