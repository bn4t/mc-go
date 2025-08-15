package types

// Inventory represents an inventory container
type Inventory interface {
	// Size and Type
	GetSize() int
	GetType() InventoryType
	GetTitle() string
	
	// Items
	GetItem(slot int) ItemStack
	SetItem(slot int, item ItemStack) error
	AddItem(items ...ItemStack) []ItemStack
	RemoveItem(items ...ItemStack) []ItemStack
	Contains(item ItemStack) bool
	ContainsAtLeast(item ItemStack, amount int) bool
	Clear()
	
	// Searching
	First(item ItemStack) int
	FirstEmpty() int
	IsEmpty() bool
	IsFull() bool
	
	// Viewers
	GetViewers() []Player
	OpenFor(player Player)
	CloseFor(player Player)
}

// ItemStack represents a stack of items
type ItemStack interface {
	// Identity
	GetType() ItemType
	GetAmount() int16
	GetData() int16
	GetDurability() int16
	
	// Modification
	SetAmount(amount int16)
	SetData(data int16)
	SetDurability(durability int16)
	
	// Properties
	GetMaxStackSize() int16
	GetDisplayName() string
	SetDisplayName(name string)
	GetLore() []string
	SetLore(lore []string)
	
	// Enchantments
	GetEnchantments() map[EnchantmentType]int
	AddEnchantment(enchantment EnchantmentType, level int)
	RemoveEnchantment(enchantment EnchantmentType)
	
	// Comparison
	IsSimilar(other ItemStack) bool
	Equals(other ItemStack) bool
	Clone() ItemStack
}

// ItemType represents the type of item
type ItemType uint16

// InventoryType represents the type of inventory
type InventoryType int

const (
	InventoryTypePlayer InventoryType = iota
	InventoryTypeChest
	InventoryTypeDoubleChest
	InventoryTypeFurnace
	InventoryTypeCrafting
	InventoryTypeEnchanting
	InventoryTypeBrewingStand
	InventoryTypeAnvil
	InventoryTypeHopper
	InventoryTypeDropper
	InventoryTypeDispenser
)

// EnchantmentType represents enchantment types
type EnchantmentType int

const (
	EnchantmentProtection EnchantmentType = iota
	EnchantmentFireProtection
	EnchantmentFeatherFalling
	EnchantmentBlastProtection
	EnchantmentProjectileProtection
	EnchantmentRespiration
	EnchantmentAquaAffinity
	EnchantmentThorns
	EnchantmentSharpness
	EnchantmentSmite
	EnchantmentBaneOfArthropods
	EnchantmentKnockback
	EnchantmentFireAspect
	EnchantmentLooting
	EnchantmentEfficiency
	EnchantmentSilkTouch
	EnchantmentUnbreaking
	EnchantmentFortune
	EnchantmentPower
	EnchantmentPunch
	EnchantmentFlame
	EnchantmentInfinity
)