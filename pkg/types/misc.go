package types

// Weather represents weather conditions
type Weather interface {
	IsRaining() bool
	IsThundering() bool
	SetRaining(raining bool)
	SetThundering(thundering bool)
	GetDuration() int
	SetDuration(duration int)
}

// TileEntity represents a tile entity (chest, furnace, etc)
type TileEntity interface {
	GetType() TileEntityType
	GetLocation() BlockLocation
	GetData() map[string]interface{}
	Update()
	Save() map[string]interface{}
	Load(data map[string]interface{})
}

// TileEntityType represents the type of tile entity
type TileEntityType int

const (
	TileEntityTypeChest TileEntityType = iota
	TileEntityTypeFurnace
	TileEntityTypeSign
	TileEntityTypeHopper
	TileEntityTypeBrewingStand
	TileEntityTypeEnchantingTable
	TileEntityTypeBeacon
	TileEntityTypeSkull
	TileEntityTypeBanner
	TileEntityTypeComparator
	TileEntityTypeFlowerPot
)

// Effect represents a potion effect
type Effect interface {
	GetType() EffectType
	GetAmplifier() int
	GetDuration() int
	IsAmbient() bool
	HasParticles() bool
}

// EffectType represents types of effects
type EffectType int

const (
	EffectTypeSpeed EffectType = iota
	EffectTypeSlowness
	EffectTypeHaste
	EffectTypeMiningFatigue
	EffectTypeStrength
	EffectTypeInstantHealth
	EffectTypeInstantDamage
	EffectTypeJumpBoost
	EffectTypeNausea
	EffectTypeRegeneration
	EffectTypeResistance
	EffectTypeFireResistance
	EffectTypeWaterBreathing
	EffectTypeInvisibility
	EffectTypeBlindness
	EffectTypeNightVision
	EffectTypeHunger
	EffectTypeWeakness
	EffectTypePoison
	EffectTypeWither
	EffectTypeHealthBoost
	EffectTypeAbsorption
	EffectTypeSaturation
)

// GameMode represents player game modes
type GameMode int

const (
	GameModeSurvival  GameMode = 0
	GameModeCreative  GameMode = 1
	GameModeAdventure GameMode = 2
	GameModeSpectator GameMode = 3
)

// Difficulty represents world difficulty
type Difficulty int

const (
	DifficultyPeaceful Difficulty = iota
	DifficultyEasy
	DifficultyNormal
	DifficultyHard
)

// ChatColor represents Minecraft chat colors
type ChatColor string

const (
	ChatColorBlack       ChatColor = "§0"
	ChatColorDarkBlue    ChatColor = "§1"
	ChatColorDarkGreen   ChatColor = "§2"
	ChatColorDarkAqua    ChatColor = "§3"
	ChatColorDarkRed     ChatColor = "§4"
	ChatColorDarkPurple  ChatColor = "§5"
	ChatColorGold        ChatColor = "§6"
	ChatColorGray        ChatColor = "§7"
	ChatColorDarkGray    ChatColor = "§8"
	ChatColorBlue        ChatColor = "§9"
	ChatColorGreen       ChatColor = "§a"
	ChatColorAqua        ChatColor = "§b"
	ChatColorRed         ChatColor = "§c"
	ChatColorLightPurple ChatColor = "§d"
	ChatColorYellow      ChatColor = "§e"
	ChatColorWhite       ChatColor = "§f"
	ChatColorReset       ChatColor = "§r"
	ChatColorBold        ChatColor = "§l"
	ChatColorItalic      ChatColor = "§o"
	ChatColorUnderline   ChatColor = "§n"
	ChatColorStrike      ChatColor = "§m"
	ChatColorObfuscated  ChatColor = "§k"
)