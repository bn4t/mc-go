package types

// Block represents a block in the world
type Block interface {
	// Identity
	GetType() BlockType
	GetData() byte
	GetID() int16
	
	// Position
	GetX() int32
	GetY() int32
	GetZ() int32
	GetLocation() BlockLocation
	
	// Properties
	IsAir() bool
	IsSolid() bool
	IsLiquid() bool
	IsTransparent() bool
	GetHardness() float32
	GetLightLevel() byte
	
	// Interaction
	CanBreak() bool
	CanPlace() bool
	GetDrops() []ItemStack
}

// BlockType represents the type of block
type BlockType uint16

// Common block types
const (
	BlockTypeAir       BlockType = 0
	BlockTypeStone     BlockType = 1
	BlockTypeGrass     BlockType = 2
	BlockTypeDirt      BlockType = 3
	BlockTypeCobblestone BlockType = 4
	BlockTypeWoodPlanks BlockType = 5
	BlockTypeBedrock   BlockType = 7
	BlockTypeWater     BlockType = 8
	BlockTypeLava      BlockType = 10
	BlockTypeSand      BlockType = 12
	BlockTypeGravel    BlockType = 13
	BlockTypeGoldOre   BlockType = 14
	BlockTypeIronOre   BlockType = 15
	BlockTypeCoalOre   BlockType = 16
	BlockTypeWood      BlockType = 17
	BlockTypeLeaves    BlockType = 18
	BlockTypeGlass     BlockType = 20
)

// BlockLocation represents a block position
type BlockLocation struct {
	X, Y, Z int32
	World   string
}

// BlockState represents a block with its state
type BlockState interface {
	Block
	GetStateData() map[string]interface{}
	SetStateData(key string, value interface{})
}