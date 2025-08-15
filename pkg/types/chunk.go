package types

// Chunk represents a 16x256x16 section of the world
type Chunk interface {
	// Position
	GetX() int32
	GetZ() int32
	GetWorld() World
	
	// Blocks
	GetBlock(x, y, z int) Block
	SetBlock(x, y, z int, block Block) error
	GetBlockLight(x, y, z int) byte
	SetBlockLight(x, y, z int, level byte)
	GetSkyLight(x, y, z int) byte
	SetSkyLight(x, y, z int, level byte)
	
	// Biomes
	GetBiome(x, z int) Biome
	SetBiome(x, z int, biome Biome)
	
	// Entities
	GetEntities() []Entity
	AddEntity(entity Entity)
	RemoveEntity(entity Entity)
	
	// Tile Entities
	GetTileEntity(x, y, z int) TileEntity
	SetTileEntity(x, y, z int, tileEntity TileEntity)
	GetTileEntities() []TileEntity
	
	// State
	IsLoaded() bool
	Load() error
	Unload() error
	IsModified() bool
	Save() error
	
	// Generation
	IsGenerated() bool
	Generate() error
	Populate() error
	
	// Serialization
	Serialize() ([]byte, error)
	Deserialize(data []byte) error
}

// Biome represents a biome type
type Biome int

const (
	BiomeOcean Biome = iota
	BiomePlains
	BiomeDesert
	BiomeExtremeHills
	BiomeForest
	BiomeTaiga
	BiomeSwampland
	BiomeRiver
	BiomeNether
	BiomeEnd
	BiomeFrozenOcean
	BiomeFrozenRiver
	BiomeIcePlains
	BiomeIceMountains
	BiomeMushroomIsland
	BiomeMushroomIslandShore
	BiomeBeach
	BiomeDesertHills
	BiomeForestHills
	BiomeTaigaHills
	BiomeExtremeHillsEdge
	BiomeJungle
	BiomeJungleHills
	BiomeJungleEdge
	BiomeDeepOcean
	BiomeStoneBeach
	BiomeColdBeach
	BiomeBirchForest
	BiomeBirchForestHills
	BiomeRoofedForest
	BiomeColdTaiga
	BiomeColdTaigaHills
	BiomeMegaTaiga
	BiomeMegaTaigaHills
	BiomeSavanna
	BiomeSavannaPlateau
	BiomeMesa
	BiomeMesaPlateauF
	BiomeMesaPlateau
)