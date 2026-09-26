package server

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// errUnknownComponent reports a data component type the slot parser
// does not know the payload shape of. Failing closed keeps the stream
// aligned by rejecting the click; extend the switch in readSlot (by
// measurement against vanilla) when a new type shows up in the wild.
func errUnknownComponent(typ, id int32) error {
	return fmt.Errorf("server: unknown data component type %d on item %d", typ, id)
}

// ItemStack is one inventory slot value. Count<=0 means empty. Data
// components are mostly not modeled yet; every stack is sent with an
// empty component patch, which is what vanilla does for plain items.
// The exception is a shulker box's contents (vanilla's
// minecraft:container component), which has to survive the box being
// broken, dropped, and picked up.
type ItemStack struct {
	ID    int32
	Count int32
	// Damage is the minecraft:damage component value (the max wears
	// the item to nothing), absent when zero: vanilla omits
	// damage:0, so a fresh tool reads back bare. RepairCost is the
	// minecraft:repair_cost component, likewise absent when zero.
	// Both are plain values so ItemStack stays comparable with ==,
	// which the inventory tests and the change-detection paths rely
	// on.
	Damage     int32 `json:"damage,omitempty"`
	RepairCost int32 `json:"repair_cost,omitempty"`
	// Name is the minecraft:custom_name component's literal text (the
	// anvil's rename product), absent when empty: an empty rename field
	// clears the component, so no custom_name is ever the empty string.
	// A plain string keeps ItemStack comparable with ==.
	Name string `json:"name,omitempty"`
	// Contents is a shulker box's stored items. It is a pointer so
	// ItemStack stays comparable with ==, which the inventory tests and
	// the change-detection paths rely on; a box holding anything is
	// therefore also not stackable with a plain box, exactly as vanilla
	// refuses to merge stacks with different components.
	Contents *[shulkerSlots]ItemStack `json:"contents,omitempty"`
	// Ench is the minecraft:enchantments component as a canonical
	// "name:level,name:level" string sorted by name, names stripped of
	// the minecraft: prefix. A plain string keeps ItemStack comparable
	// with ==.
	Ench string `json:"ench,omitempty"`
	// StoredEnch is the minecraft:stored_enchantments component — the
	// list an enchanted_book carries — same canonical encoding as Ench.
	StoredEnch string `json:"stored_ench,omitempty"`
	// Trim is the minecraft:trim component: "<material>/<pattern>" with
	// bare registry names (a lapis bolt trim reads "lapis/bolt"),
	// absent when empty. One plain string keeps ItemStack comparable.
	Trim string `json:"trim,omitempty"`
	// Bees is the minecraft:bees component's occupant count — a
	// bee_nest/beehive item remembers how many bees it carries into
	// placement. Only the count matters for the triggers we model;
	// the entity payloads are never inspected.
	Bees int32 `json:"bees,omitempty"`
	// BlockState is the minecraft:block_state component — the flat
	// property map a silk-touched block item carries (a bee nest's
	// honey_level) so placement restores it. Canonical "key=val"
	// pairs sorted by key keeps ItemStack comparable with ==.
	BlockState string `json:"block_state,omitempty"`
	// Potion is the minecraft:potion_contents component's potion id,
	// minecraft: prefix stripped; empty means the holder is absent —
	// a bare potion item ships the component EMPTY (the uncraftable
	// potion), not water. The component's optional color/effects/
	// custom-name fields are not modeled — brewed potions never set
	// them.
	Potion string `json:"potion,omitempty"`
	// Ominous marks the raid-captain ominous banner: a white_banner
	// carrying getBannerComponentPatch — banner_patterns (8 layers),
	// tooltip_display hiding them, item_name block.minecraft.ominous_banner,
	// and rarity uncommon.
	Ominous bool `json:"ominous,omitempty"`
	// BannerPatterns is the minecraft:banner_patterns component's
	// canonical "pattern:color,..." layer list in application order
	// (the loom appends one layer per take) — absent when empty, which
	// is also a fresh banner's default so only loomed banners carry it.
	BannerPatterns string `json:"banner_patterns,omitempty"`
	// BottleAmpSet/BottleAmp carry minecraft:ominous_bottle_amplifier —
	// set_ominous_bottle_amplifier rolls uniform 0-4 into the bottle a
	// raid captain drops. Amplifier 0 is a legal value, so presence
	// needs its own flag.
	BottleAmpSet bool  `json:"bottle_amp_set,omitempty"`
	BottleAmp    int32 `json:"bottle_amp,omitempty"`
	// LootTable/LootSeed are minecraft:container_loot — the ungenerated
	// loot reference a broken chest-type block keeps so placing it
	// re-arms the same table. Persistent-only in vanilla (no
	// networkSynchronized codec), so nothing wire-emits them.
	LootTable string `json:"loot_table,omitempty"`
	LootSeed  int64  `json:"loot_seed,omitempty"`
	// BookWritable / BookWritten are the minecraft:writable_book_content
	// and minecraft:written_book_content components' canonical SNBT —
	// {pages:[...]} and {author,generation,pages,resolved,title}. A
	// component patch applies to any item, so a stack may carry both;
	// empty means the holder lacks that component.
	BookWritable string `json:"book_writable,omitempty"`
	BookWritten  string `json:"book_written,omitempty"`
	// MapID is the minecraft:map_id component's MapId — stored as the
	// map's data id +1 so the zero value means absent (vanilla's first
	// map id is itself 0). MapPost is minecraft:map_post_processing
	// stored likewise: 0 absent, mapPostLock / mapPostScale the
	// internal encodings — the wire writes MapPost-1 (lock 0, scale 1).
	MapID   int32 `json:"map_id,omitempty"`
	MapPost int32 `json:"map_post,omitempty"`
}

// Empty reports whether the slot holds nothing.
func (it ItemStack) Empty() bool { return it.Count <= 0 || it.ID == 0 }

// Stackable reports whether two stacks merge into one slot: same item
// and no component difference we model. Vanilla treats any component
// difference as unstackable, and a box with contents carries one.
func (it ItemStack) Stackable(other ItemStack) bool {
	if it.Empty() || other.Empty() || it.ID != other.ID {
		return false
	}
	return it.Contents == nil && other.Contents == nil &&
		it.Damage == other.Damage && it.RepairCost == other.RepairCost &&
		it.Name == other.Name && it.Ench == other.Ench &&
		it.StoredEnch == other.StoredEnch && it.Bees == other.Bees &&
		it.BlockState == other.BlockState &&
		it.Potion == other.Potion && it.Ominous == other.Ominous &&
		it.BannerPatterns == other.BannerPatterns &&
		it.BottleAmpSet == other.BottleAmpSet && it.BottleAmp == other.BottleAmp &&
		it.LootTable == other.LootTable && it.LootSeed == other.LootSeed &&
		it.BookWritable == other.BookWritable && it.BookWritten == other.BookWritten &&
		it.Trim == other.Trim &&
		it.MapID == other.MapID && it.MapPost == other.MapPost
}

// enchLevel reports the stack's level of one enchantment, or 0 when
// absent. Ench is stored canonical — "silk_touch:1" — so a scan is the
// whole lookup and names match with or without the minecraft: prefix.
func (it ItemStack) enchLevel(name string) int32 {
	want := strings.TrimPrefix(name, "minecraft:")
	for _, e := range strings.Split(it.Ench, ",") {
		if n, lv, ok := strings.Cut(e, ":"); ok && n == want {
			v, err := strconv.Atoi(lv)
			if err == nil {
				return int32(v)
			}
		}
	}
	return 0
}

// withCount returns the stack with another count, keeping every
// component: splitting a damaged tool must not repair it.
func (it ItemStack) withCount(n int32) ItemStack {
	it.Count = n
	return it
}

// Player inventory window layout (menu slots):
//
//	0      crafting result
//	1-4    crafting grid
//	5-8    armor
//	9-35   main inventory (27)
//	36-44  hotbar (9)
//	45     offhand
const (
	slotCraftResult = 0
	slotCraftStart  = 1
	slotArmorStart  = 5
	slotMainStart   = 9
	slotHotbarStart = 36
	slotOffhand     = 45
	slotCount       = 46
)

// invSlotForHotbar maps a selected hotbar index (0-8) to its menu slot.
func invSlotForHotbar(held int32) int16 { return int16(slotHotbarStart + held) }

// Component type ids on the Slot wire format, measured on 26.2: a
// half-worn pick reads damage (3) with a bare VarInt payload, and a
// repaired one carries repair_cost (19) right after it, also a bare
// VarInt. The add list is written in ascending type order.
const (
	compDamage       = 3
	compCustomName   = 6
	compEnchantments = 13
	compRepairCost   = 19
	compStoredEnch   = 42
	compTrim         = 56
	// writable_book_content / written_book_content carry the book
	// payload a lectern (or book-and-quill editor) reads; payloads are
	// lists of Filterable<String>/<Component> wrappers (measured 26.2).
	compWritableBook = 54
	compWrittenBook  = 55
	// potion_contents, measured on 26.2: presence bool, potion
	// registry id (raw, not holder +1), then three absent-option
	// bytes — custom color, custom effects list (VarInt 0), custom
	// name.
	compPotionContents = 51
	// The ominous-banner patch components: banner_patterns carries a
	// layer list of (pattern holder, dye ordinal) pairs; tooltip_display
	// is a hide bool plus a HolderSet of hidden component types;
	// item_name is a text component (NBT); rarity an enum VarInt
	// (common=0, uncommon=1).
	compBannerPatterns = 72
	compTooltipDisplay = 18
	compItemName       = 9
	compRarity         = 12
	rarityUncommon     = 1
	// ominous_bottle_amplifier is a bare VarInt, registered 64th in the
	// data_component_type registry (measured on 26.2).
	compOminousBottleAmplifier = 63
	// map_id is a bare VarInt (MapId wraps the data id); the map
	// post-processing enum is a bare VarInt too — lock 0, scale 1
	// (measured on 26.2: MapPostProcessing { LOCK, SCALE }).
	compMapID   = 46
	compMapPost = 48
	// block_state is BlockItemStateProperties' flat map — VarInt
	// pairs of UTF8 property name to value (measured on 26.2:
	// PROPERTIES_STREAM_CODEC is ByteBufCodecs.map of STRING_UTF8).
	// container is ItemContainerContents: a VarInt length and that many
	// optional ItemStackTemplate slots (bool + id + count + patch).
	compContainer  = 75
	compBlockState = 76
	// bees is a VarInt-counted list of BeehiveBlockEntity.Occupant:
	// TypedEntityData (entity-type holder VarInt, then the entity
	// compound tag) plus ticks_in_hive and min_ticks_in_hive VarInts.
	compBees = 77
)

// potionNamesByID inverts data.PotionIDs for the decode path; the
// potion registry is static (never synced), so the generated table is
// authoritative both ways.
var potionNamesByID = func() map[int32]string {
	m := make(map[int32]string, len(data.PotionIDs))
	for n, id := range data.PotionIDs {
		m[id] = n
	}
	return m
}()

// writeSlot writes one Slot value: VarInt count, then (when non-empty)
// item id and empty component add/remove counts.
func writeSlot(b *protocol.Buffer, it ItemStack) {
	if it.Empty() {
		b.VarInt(0)
		return
	}
	b.VarInt(it.Count)
	b.VarInt(it.ID)
	writeSlotPatch(b, it)
}

// writeSlotPatch writes the add/remove component patch of a slot (and,
// recursively, of each ItemStackTemplate inside a container component —
// the nested template is id + count + this same patch shape).
func writeSlotPatch(b *protocol.Buffer, it ItemStack) {
	var add int32
	if it.Damage != 0 {
		add++
	}
	if it.RepairCost != 0 {
		add++
	}
	if it.Name != "" {
		add++
	}
	if it.Ench != "" {
		add++
	}
	if it.StoredEnch != "" {
		add++
	}
	if it.Potion != "" {
		add++
	}
	if it.Ominous {
		add += 4
	}
	if it.BottleAmpSet {
		add++
	}
	if it.BookWritable != "" {
		add++
	}
	if it.BookWritten != "" {
		add++
	}
	if it.BlockState != "" {
		add++
	}
	if it.Bees > 0 {
		add++
	}
	if it.Trim != "" {
		add++
	}
	if it.Contents != nil {
		add++
	}
	if it.BannerPatterns != "" && !it.Ominous {
		add++
	}
	if it.MapID != 0 {
		add++
	}
	if it.MapPost != 0 {
		add++
	}
	b.VarInt(add)
	b.VarInt(0) // components to remove
	if it.Damage != 0 {
		b.VarInt(compDamage)
		b.VarInt(it.Damage)
	}
	if it.Name != "" {
		// custom_name's payload is an NBT string tag: type byte 0x08,
		// then a u16 length and the UTF-8 text (measured on 26.2).
		b.VarInt(compCustomName)
		b.Byte(0x08)
		b.UShort(uint16(len(it.Name)))
		b.Raw([]byte(it.Name))
	}
	if it.Ench != "" {
		// enchantments' payload is a VarInt-counted map of VarInt
		// enchantment registry id to VarInt level (measured on 26.2;
		// ItemEnchantments.STREAM_CODEC is the flat pair map).
		b.VarInt(compEnchantments)
		parts := strings.Split(it.Ench, ",")
		b.VarInt(int32(len(parts)))
		for _, e := range parts {
			n, lv, _ := strings.Cut(e, ":")
			v, _ := strconv.Atoi(lv)
			b.VarInt(registryEntryID("minecraft:enchantment", "minecraft:"+n))
			b.VarInt(int32(v))
		}
	}
	if it.StoredEnch != "" {
		b.VarInt(compStoredEnch)
		parts := strings.Split(it.StoredEnch, ",")
		b.VarInt(int32(len(parts)))
		for _, e := range parts {
			n, lv, _ := strings.Cut(e, ":")
			v, _ := strconv.Atoi(lv)
			b.VarInt(registryEntryID("minecraft:enchantment", "minecraft:"+n))
			b.VarInt(int32(v))
		}
	}
	if it.Potion != "" {
		if pid, ok := data.PotionIDs[it.Potion]; ok {
			b.VarInt(compPotionContents)
			b.Byte(1) // potion holder present: the RAW registry id
			b.VarInt(pid)
			b.Byte(0)   // custom_color absent
			b.VarInt(0) // custom_effects empty
			b.Byte(0)   // custom_name absent
		}
	}
	if it.RepairCost != 0 {
		b.VarInt(compRepairCost)
		b.VarInt(it.RepairCost)
	}
	if it.Ominous {
		// getBannerComponentPatch's insertion order: banner_patterns,
		// tooltip_display, item_name, rarity. Holder encoding is the
		// registry id +1 (0 is the named-tag form); pattern ids are the
		// sorted datapack order, dye ids the DyeColor ordinals
		// (light_gray 8, cyan 9, gray 7, black 15).
		b.VarInt(compBannerPatterns)
		b.VarInt(8)
		for _, l := range ominousBannerLayers {
			b.VarInt(l.pattern + 1)
			b.VarInt(l.color)
		}
		b.VarInt(compTooltipDisplay)
		b.Byte(0)   // hide_tooltip false
		b.VarInt(1) // hidden_components count
		b.VarInt(compBannerPatterns)
		b.VarInt(compItemName)
		// A translatable component writes as an NBT compound:
		// {translate:"block.minecraft.ominous_banner"}.
		b.Byte(0x0A)
		b.Byte(0x08)
		b.UShort(9)
		b.Raw([]byte("translate"))
		b.UShort(uint16(len("block.minecraft.ominous_banner")))
		b.Raw([]byte("block.minecraft.ominous_banner"))
		b.Byte(0)
		b.VarInt(compRarity)
		b.VarInt(rarityUncommon)
	}
	if it.BannerPatterns != "" && !it.Ominous {
		// BannerPatternLayers STREAM_CODEC: a VarInt layer count and
		// (pattern holder, dye ordinal) pairs — the holder writes its
		// registry id +1, the dye its DyeColor ordinal.
		layers := strings.Split(it.BannerPatterns, ",")
		b.VarInt(compBannerPatterns)
		b.VarInt(int32(len(layers)))
		for _, l := range layers {
			pat, col, _ := strings.Cut(l, ":")
			pid, _ := loomPatternID(pat)
			b.VarInt(pid + 1)
			var ord int32
			for i, d := range loomDyeNames {
				if d == col {
					ord = int32(i)
					break
				}
			}
			b.VarInt(ord)
		}
	}
	if it.BottleAmpSet {
		// OminousBottleAmplifier.STREAM_CODEC is a bare VarInt.
		b.VarInt(compOminousBottleAmplifier)
		b.VarInt(it.BottleAmp)
	}
	if it.MapID != 0 {
		b.VarInt(compMapID)
		b.VarInt(it.MapID - 1)
	}
	if it.MapPost != 0 {
		b.VarInt(compMapPost)
		b.VarInt(it.MapPost - 1)
	}
	if it.BookWritable != "" || it.BookWritten != "" {
		writeBookContentPatch(b, it)
	}
	if it.Contents != nil {
		b.VarInt(compContainer)
		b.VarInt(shulkerSlots)
		for i := 0; i < shulkerSlots; i++ {
			inner := it.Contents[i]
			if inner.Empty() {
				b.Bool(false)
				continue
			}
			b.Bool(true)
			b.VarInt(inner.ID)
			b.VarInt(inner.Count)
			writeSlotPatch(b, inner)
		}
	}
	if it.BlockState != "" {
		b.VarInt(compBlockState)
		parts := strings.Split(it.BlockState, ",")
		b.VarInt(int32(len(parts)))
		for _, kv := range parts {
			k, v, _ := strings.Cut(kv, "=")
			b.String(k)
			b.String(v)
		}
	}
	if it.Bees > 0 {
		b.VarInt(compBees)
		b.VarInt(it.Bees)
		for i := int32(0); i < it.Bees; i++ {
			// TypedEntityData: entity_type holder (raw id +1), then
			// the occupant's entity compound with id stripped — the
			// count-only model ships an empty tag.
			if bid, ok := EntityTypeID("bee"); ok {
				b.VarInt(bid + 1)
			} else {
				b.VarInt(0)
			}
			_ = nbt.WriteNamed(b, "", map[string]any{})
			b.VarInt(0)   // ticks_in_hive
			b.VarInt(600) // min_ticks_in_hive
		}
	}
	if it.Trim != "" {
		// ItemTrim's holders write material first, then pattern —
		// each a registry id +1 (measured on 26.2: a lapis bolt
		// trim emits 07 01, lapis=6 and bolt=0). SmithingTrimRecipe
		// sets TRIM on a copy of the base, so it lands last in the
		// patch after whatever components the base carried.
		mat, pat, _ := strings.Cut(it.Trim, "/")
		b.VarInt(compTrim)
		b.VarInt(registryEntryID("minecraft:trim_material", "minecraft:"+mat) + 1)
		b.VarInt(registryEntryID("minecraft:trim_pattern", "minecraft:"+pat) + 1)
	}
}

// ominousBannerLayers is the ominous banner's 8-layer pattern list
// (Raid.getOminousBannerInstance / getBannerComponentPatch): pattern
// registry ids are the sorted datapack file order, colors the DyeColor
// ordinals.
var ominousBannerLayers = [...]struct {
	pattern int32
	color   int32
}{
	{23, 9},  // rhombus cyan
	{31, 8},  // stripe_bottom light_gray
	{32, 7},  // stripe_center gray
	{1, 8},   // border light_gray
	{36, 15}, // stripe_middle black
	{17, 8},  // half_horizontal light_gray
	{3, 8},   // circle light_gray
	{1, 15},  // border black
}

// readSlot reads one Slot value, decoding the minecraft:damage and
// minecraft:repair_cost components (both bare VarInts, measured on
// vanilla 26.2) into the stack so the stream stays aligned and clicks
// carrying a worn tool parse. Any other component fails the parse and
// the click is rejected, instead of silently desyncing every field
// after it.
func readSlot(r *protocol.PacketReader) (ItemStack, error) {
	count, err := r.VarInt()
	if err != nil || count <= 0 {
		return ItemStack{}, err
	}
	id, err := r.VarInt()
	if err != nil {
		return ItemStack{}, err
	}
	nadd, err := r.VarInt() // components to add
	if err != nil {
		return ItemStack{}, err
	}
	nrem, err := r.VarInt() // components to remove (types only)
	if err != nil {
		return ItemStack{}, err
	}
	it := ItemStack{ID: id, Count: count}
	for ; nadd > 0; nadd-- {
		typ, err := r.VarInt()
		if err != nil {
			return ItemStack{}, err
		}
		if err := readSlotComponent(r, &it, typ); err != nil {
			return ItemStack{}, err
		}
	}

	for ; nrem > 0; nrem-- {
		if _, err := r.VarInt(); err != nil { // removed types carry no payload
			return ItemStack{}, err
		}
	}
	return it, nil
}

// readSlotComponent decodes one component-add entry's payload into
// *it by type id; the container case recurses for inner stacks.
func readSlotComponent(r *protocol.PacketReader, it *ItemStack, typ int32) error {
	var err error
	switch typ {
	case compDamage: // minecraft:damage, VarInt payload (measured 26.2)
		if it.Damage, err = r.VarInt(); err != nil {
			return err
		}
	case compCustomName: // minecraft:custom_name, NBT string tag
		var tag byte
		if tag, err = r.Byte(); err != nil {
			return err
		}
		if tag != 0x08 {
			return fmt.Errorf("server: custom_name tag %d is not a string", tag)
		}
		var n uint16
		if n, err = r.UShort(); err != nil {
			return err
		}
		var raw []byte
		if raw, err = r.Read(int(n)); err != nil {
			return err
		}
		it.Name = string(raw)
	case compEnchantments: // minecraft:enchantments, VarInt id->level map
		n, err := r.VarInt()
		if err != nil {
			return err
		}
		parts := make([]string, 0, n)
		for ; n > 0; n-- {
			eid, err := r.VarInt()
			if err != nil {
				return err
			}
			lv, err := r.VarInt()
			if err != nil {
				return err
			}
			if name := registryEntryName("minecraft:enchantment", eid); name != "" {
				parts = append(parts, strings.TrimPrefix(name, "minecraft:")+":"+strconv.Itoa(int(lv)))
			}
		}
		sort.Strings(parts)
		it.Ench = strings.Join(parts, ",")
	case compStoredEnch: // minecraft:stored_enchantments, same map payload
		n, err := r.VarInt()
		if err != nil {
			return err
		}
		parts := make([]string, 0, n)
		for ; n > 0; n-- {
			eid, err := r.VarInt()
			if err != nil {
				return err
			}
			lv, err := r.VarInt()
			if err != nil {
				return err
			}
			if name := registryEntryName("minecraft:enchantment", eid); name != "" {
				parts = append(parts, strings.TrimPrefix(name, "minecraft:")+":"+strconv.Itoa(int(lv)))
			}
		}
		sort.Strings(parts)
		it.StoredEnch = strings.Join(parts, ",")
	case compPotionContents: // minecraft:potion_contents
		if tag, err := r.Byte(); err != nil {
			return err
		} else if tag != 0 {
			pid, err := r.VarInt()
			if err != nil {
				return err
			}
			if name, ok := potionNamesByID[pid]; ok {
				it.Potion = name
			}
		}
		if tag, err := r.Byte(); err != nil {
			return err
		} else if tag != 0 {
			if _, err := r.VarInt(); err != nil { // custom_color
				return err
			}
		}
		n, err := r.VarInt() // custom_effects
		if err != nil {
			return err
		}
		for ; n > 0; n-- {
			// MobEffectInstance: id, amplifier, duration, ambient,
			// showParticles, showIcon, hiddenEntry — read as far as
			// the fixed fields go; brewed potions never carry this.
			if _, err := r.VarInt(); err != nil {
				return err
			}
			if _, err := r.VarInt(); err != nil {
				return err
			}
			if _, err := r.VarInt(); err != nil {
				return err
			}
			if _, err := r.Byte(); err != nil {
				return err
			}
			if _, err := r.Byte(); err != nil {
				return err
			}
			if _, err := r.Byte(); err != nil {
				return err
			}
			if b, err := r.Byte(); err != nil {
				return err
			} else if b != 0 {
				return errUnknownComponent(typ, -1)
			}
		}
		if tag, err := r.Byte(); err != nil {
			return err
		} else if tag != 0 {
			// custom_name: NBT string tag
			if t2, err := r.Byte(); err != nil {
				return err
			} else if t2 != 0x08 {
				return errUnknownComponent(typ, -1)
			}
			var nn uint16
			if nn, err = r.UShort(); err != nil {
				return err
			}
			if _, err := r.Read(int(nn)); err != nil {
				return err
			}
		}
	case compRepairCost: // minecraft:repair_cost, VarInt payload
		if it.RepairCost, err = r.VarInt(); err != nil {
			return err
		}
	case compBlockState: // minecraft:block_state, UTF8 pair map
		n, err := r.VarInt()
		if err != nil {
			return err
		}
		parts := make([]string, 0, n)
		for ; n > 0; n-- {
			k, err := r.String()
			if err != nil {
				return err
			}
			v, err := r.String()
			if err != nil {
				return err
			}
			parts = append(parts, k+"="+v)
		}
		sort.Strings(parts)
		it.BlockState = strings.Join(parts, ",")
	case compTrim: // minecraft:trim — material holder, then pattern holder
		mid, err := r.VarInt()
		if err != nil {
			return err
		}
		pid, err := r.VarInt()
		if err != nil {
			return err
		}
		mat := strings.TrimPrefix(registryEntryName("minecraft:trim_material", mid-1), "minecraft:")
		pat := strings.TrimPrefix(registryEntryName("minecraft:trim_pattern", pid-1), "minecraft:")
		if mat == "" || pat == "" {
			return fmt.Errorf("server: trim material/pattern ids %d/%d unknown", mid, pid)
		}
		it.Trim = mat + "/" + pat
	case compBees: // minecraft:bees, occupant list — count only
		n, err := r.VarInt()
		if err != nil {
			return err
		}
		for ; n > 0; n-- {
			if _, err := r.VarInt(); err != nil { // entity-type holder
				return err
			}
			if _, _, err := r.NamedTag(); err != nil { // entity compound
				return err
			}
			if _, err := r.VarInt(); err != nil { // ticks_in_hive
				return err
			}
			if _, err := r.VarInt(); err != nil { // min_ticks_in_hive
				return err
			}
			it.Bees++
		}
	case compContainer: // minecraft:container, optional-template list
		n, err := r.VarInt()
		if err != nil || n < 0 || n > shulkerSlots {
			return errUnknownComponent(typ, -1)
		}
		box := new([shulkerSlots]ItemStack)
		for i := int32(0); i < n; i++ {
			present, err := r.Byte()
			if err != nil {
				return err
			}
			if present == 0 {
				continue
			}
			iid, err := r.VarInt()
			if err != nil {
				return err
			}
			icount, err := r.VarInt()
			if err != nil {
				return err
			}
			inner := ItemStack{ID: iid, Count: icount}
			iadd, err := r.VarInt()
			if err != nil {
				return err
			}
			for ; iadd > 0; iadd-- {
				t2, err := r.VarInt()
				if err != nil {
					return err
				}
				if err := readSlotComponent(r, &inner, t2); err != nil {
					return err
				}
			}
			irem, err := r.VarInt()
			if err != nil {
				return err
			}
			for ; irem > 0; irem-- {
				if _, err := r.VarInt(); err != nil {
					return err
				}
			}
			box[i] = inner
		}
		it.Contents = box

	default:
		return errUnknownComponent(typ, -1)
	}
	return nil
}

// setSlot records one inventory slot; count<=0 clears it.
func (sess *Session) setSlot(slot int16, item int32, count int32) {
	if slot < 0 || slot >= slotCount {
		return
	}
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	if count <= 0 {
		sess.inv[slot] = ItemStack{}
		return
	}
	sess.inv[slot] = ItemStack{ID: item, Count: count}
}

// setStack stores one stack value.
func (sess *Session) setStack(slot int16, it ItemStack) {
	if slot < 0 || slot >= slotCount {
		return
	}
	sess.invMu.Lock()
	sess.inv[slot] = it
	sess.invMu.Unlock()
}

// stackAt reads one slot value.
func (sess *Session) stackAt(slot int16) ItemStack {
	if slot < 0 || slot >= slotCount {
		return ItemStack{}
	}
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	return sess.inv[slot]
}

// heldStack returns the selected hotbar slot's stack.
func (sess *Session) heldStack() ItemStack {
	return sess.stackAt(invSlotForHotbar(sess.held.Load()))
}

// stackForHand resolves the InteractionHand a packet named: 0 is the
// selected hotbar slot, 1 the offhand. Out-of-range hands fall back to
// the main hand. Returns the stack and its menu slot so callers can
// write back to the same hand.
func (sess *Session) stackForHand(hand int32) (ItemStack, int16) {
	slot := invSlotForHotbar(sess.held.Load())
	if hand == 1 {
		slot = slotOffhand
	}
	return sess.stackAt(slot), slot
}

// consumeSlot shrinks the stack in one menu slot by n.
func (sess *Session) consumeSlot(slot int16, n int32) {
	if slot < 0 || slot >= slotCount {
		return
	}
	sess.invMu.Lock()
	it := sess.inv[slot]
	if it.Empty() {
		sess.invMu.Unlock()
		return
	}
	it.Count -= n
	if it.Count <= 0 {
		it = ItemStack{}
	}
	sess.inv[slot] = it
	sess.invMu.Unlock()
	_ = sess.server.sendContainerSlot(sess, slot, it)
}

// firstEmptySlot returns the first empty storage slot, searching the
// main inventory then the hotbar — vanilla's ordering for a creative
// pickup that lands in a fresh stack.
func (sess *Session) firstEmptySlot() int16 {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	for slot := int16(slotMainStart); slot < slotOffhand; slot++ {
		if sess.inv[slot].Empty() {
			return slot
		}
	}
	return -1
}

// heldItem keeps the old (id, ok) shape used by placement code.
func (sess *Session) heldItem() (int32, bool) {
	it := sess.heldStack()
	if it.Empty() {
		return 0, false
	}
	return it.ID, true
}

// placeBackInInventory is vanilla's
// Inventory.placeItemBackInInventory: the stack merges into the first
// slot with remaining space — the flat items-list scan, hotbar then
// main, offhand never — else the first free slot. The touched slot
// goes out as a Set Player Inventory packet (sendUpdates=true); what
// still does not fit drops at the player's feet.
func (s *Server) placeBackInInventory(sess *Session, it ItemStack) {
	if it.Empty() {
		return
	}
	flat := make([]int16, 0, slotCount)
	for i := int16(slotHotbarStart); i < slotOffhand; i++ {
		flat = append(flat, i)
	}
	for i := int16(slotMainStart); i < slotHotbarStart; i++ {
		flat = append(flat, i)
	}
	sess.invMu.Lock()
	slot := int16(-1)
	for _, sl := range flat {
		cur := sess.inv[sl]
		if !cur.Empty() && cur.Stackable(it) && cur.Count < maxStackOfID(cur.ID) {
			slot = sl
			break
		}
	}
	if slot < 0 {
		for _, sl := range flat {
			if sess.inv[sl].Empty() {
				slot = sl
				break
			}
		}
	}
	var stored ItemStack
	if slot >= 0 {
		cur := sess.inv[slot]
		if cur.Empty() {
			cur = it.withCount(0)
		}
		room := maxStackOfID(it.ID) - cur.Count
		move := min(room, it.Count)
		cur.Count += move
		it.Count -= move
		sess.inv[slot] = cur
		stored = cur
	}
	sess.invMu.Unlock()
	if slot < 0 {
		s.dropItemLocked(sess, it)
		return
	}
	s.sendPlayerInventorySlot(sess, playerContainerIndex(slot), stored)
	if !it.Empty() {
		s.dropItemLocked(sess, it)
	}
}

// sendContainerSlot syncs one inventory slot to the client.
func (s *Server) sendContainerSlot(sess *Session, slot int16, it ItemStack) error {
	var b protocol.Buffer
	b.VarInt(0) // window 0: player inventory
	b.VarInt(sess.invState.Load())
	b.Short(slot)
	writeSlot(&b, it)
	return sess.write(sess.ids().PlayContainerSlot, b.Bytes())
}

// sendCarriedItem syncs the click cursor stack.
func (s *Server) sendCarriedItem(sess *Session, it ItemStack) error {
	var b protocol.Buffer
	writeSlot(&b, it)
	return sess.write(sess.ids().PlaySetCarriedItem, b.Bytes())
}

// sendContainerContent syncs the full player inventory window.
func (s *Server) sendContainerContent(sess *Session) error {
	var b protocol.Buffer
	b.VarInt(0) // window 0: player inventory
	b.VarInt(sess.invState.Load())
	b.VarInt(slotCount)
	sess.invMu.RLock()
	for i := 0; i < slotCount; i++ {
		writeSlot(&b, sess.inv[i])
	}
	sess.invMu.RUnlock()
	writeSlot(&b, sess.carried)
	return sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// maxStack is the vanilla stack limit for unstackable-unaware items.
const maxStack = 64

// maxStackOfID is the most of one item a slot holds: the measured table
// in maxstack_gen.go by item id, defaulting to 64 for anything outside
// it. Air stacks to 1 - the real server refuses `air 5` naming a cap of
// one - and as id 0 is outside the table it is pinned here.
func maxStackOfID(id int32) int32 {
	if id == 0 {
		return 1
	}
	if name, ok := world.ItemName(id); ok {
		return maxStackOfItem(name)
	}
	return maxStack
}

// creativeClone answers a middle-click (mode 3) the way vanilla 26.2
// does, measured: only a creative player clones, only with the middle
// button, and only from a slot holding something - anything else is
// ignored. The clone is a full stack that keeps the slot's components,
// and the slot itself is unchanged.
func creativeClone(sess *Session, it ItemStack, button byte) (ItemStack, bool) {
	if sess.mode() != GameModeCreative || button != 2 || it.Empty() {
		return ItemStack{}, false
	}
	out := it
	out.Count = maxStackOfID(it.ID)
	return out, true
}

// handleClickContainer implements the core click modes on the player
// inventory. Click modes: 0 pickup, 1 quick-move, 2 swap, 3 creative
// clone, 4 throw. Exotic modes are acknowledged with a state bump and
// a full resync rather than silently diverging. The return value
// reports whether an equipment slot changed, so the caller can
// broadcast it after the inventory lock is released.
func (s *Server) handleClickContainer(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return false
	}
	if win != 0 {
		// A container window is open: route to its menu.
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w != nil && w.craft != nil {
			s.handleCraftTableClickPacket(sess, p)
			return false
		}
		if w != nil && w.hopper != nil {
			s.handleHopperClickPacket(sess, p)
			return false
		}
		if w != nil && w.container != nil {
			s.handleContainerClickPacket(sess, p)
			return false
		}
		s.handleChestClickPacket(sess, p)
		return false
	}
	sess.windowMu.Lock()
	menuOpen := sess.window != nil
	sess.windowMu.Unlock()
	if menuOpen {
		// Clicks on window 0 while a menu is open are ignored on
		// vanilla 26.2 (measured): the menu owns the cursor until it
		// closes.
		return false
	}
	st, err := r.VarInt() // state id
	if err != nil {
		return false
	}
	// Vanilla answers a stale click with a full resync of window 0,
	// ignoring the click instead of applying it.
	if st != sess.invState.Load() {
		_ = s.sendContainerContent(sess)
		return false
	}
	slot, err := r.Short()
	if err != nil {
		return false
	}
	button, err := r.Byte()
	if err != nil {
		return false
	}
	mode, err := r.VarInt()
	if err != nil {
		return false
	}
	changed, ok := readChangedSlots(r)
	if !ok {
		return false
	}
	if _, err := readSlot(r); err != nil { // carried item
		// Tolerate a truncated tail only for empty carried items.
		return false
	}

	// A click outside the 46 player slots (a slot from another menu,
	// a stale client view) touches nothing instead of crashing: it
	// falls through with no state change, like any other bad input.
	if slot < 0 || slot >= slotCount {
		return false
	}

	// The crafting result is take-only and drives crafting.
	if slot == slotCraftResult {
		s.handleCraftResultClick(sess, button, mode)
		return false
	}

	sess.invMu.Lock()
	defer sess.invMu.Unlock()

	// Remember whether the click touched the 2x2 grid so the result can
	// refresh after the normal handling, and whether it touched any
	// equipment slot so armor changes can be broadcast.
	gridTouched := slot >= slotCraftStart && slot < slotCraftStart+4
	equipTouched := (slot >= slotArmorStart && slot < slotArmorStart+4) || slot == slotOffhand

	var touched []int16
	touch := func(sl int16) { touched = append(touched, sl) }

	switch mode {
	case 0: // pickup / place with cursor
		cur := sess.carried
		target := sess.inv[slot]
		if button == 0 { // left click: whole stack
			if cur.Empty() {
				sess.carried = target
				sess.inv[slot] = ItemStack{}
			} else if target.Empty() {
				sess.inv[slot] = cur
				sess.carried = ItemStack{}
			} else if target.Stackable(cur) {
				room := maxStackOfID(target.ID) - target.Count
				move := cur.Count
				if move > room {
					move = room
				}
				sess.inv[slot].Count += move
				sess.carried.Count -= move
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			} else {
				sess.inv[slot] = cur
				sess.carried = target
			}
		} else { // right click: split / place one
			if cur.Empty() {
				if !target.Empty() {
					half := (target.Count + 1) / 2
					sess.carried = target.withCount(half)
					sess.inv[slot].Count -= half
					if sess.inv[slot].Count <= 0 {
						sess.inv[slot] = ItemStack{}
					}
				}
			} else if target.Empty() {
				sess.inv[slot] = cur.withCount(1)
				sess.carried.Count--
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			} else if target.Stackable(cur) && target.Count < maxStackOfID(target.ID) {
				sess.inv[slot].Count++
				sess.carried.Count--
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			}
		}
		touch(slot)
	case 1: // quick move: merge into the other half, else swap half
		target := sess.inv[slot]
		if target.Empty() {
			break
		}
		if slot >= slotHotbarStart && slot <= slotOffhand {
			s.quickMoveLocked(sess, slot, slotMainStart, slotHotbarStart-1, &touched)
		} else {
			s.quickMoveLocked(sess, slot, slotHotbarStart, slotOffhand, &touched)
			if !sess.inv[slot].Empty() {
				s.quickMoveLocked(sess, slot, slotMainStart, slotHotbarStart-1, &touched)
			}
		}
	case 2: // swap with hotbar button
		swap := int16(slotHotbarStart) + int16(button)
		if swap >= slotHotbarStart && swap <= slotOffhand {
			sess.inv[slot], sess.inv[swap] = sess.inv[swap], sess.inv[slot]
			touch(slot)
			touch(swap)
		}
	case 3: // creative clone
		if out, ok := creativeClone(sess, sess.inv[slot], button); ok {
			sess.carried = out
		}
		touch(slot)
	case 4: // throw
		if !sess.inv[slot].Empty() {
			dropped := sess.inv[slot]
			if button == 0 {
				dropped = dropped.withCount(1)
			}
			sess.inv[slot].Count -= dropped.Count
			if sess.inv[slot].Count <= 0 {
				sess.inv[slot] = ItemStack{}
			}
			s.dropItemLocked(sess, dropped)
			touch(slot)
		}
	default:
		// Unsupported mode: fall through to a full resync.
	}

	if gridTouched {
		// Recompute the 2x2 result while still holding the lock.
		grid := craftGrid{
			sess.inv[slotCraftStart],
			sess.inv[slotCraftStart+1], {},
			sess.inv[slotCraftStart+2],
			sess.inv[slotCraftStart+3], {}, {},
			{}, {},
		}
		want, _, built := craftResult(grid)
		if !built {
			want = ItemStack{}
		}
		if prev := sess.inv[slotCraftResult]; prev != want {
			sess.inv[slotCraftResult] = want
			touched = append(touched, slotCraftResult)
		}
	}

	sess.invState.Add(1)
	state := sess.invState.Load()
	_ = changed
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(touched))
	for _, sl := range touched {
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	carried := sess.carried

	for _, u := range updates {
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(state)
		b.Short(u.slot)
		writeSlot(&b, u.it)
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	var cb protocol.Buffer
	writeSlot(&cb, carried)
	_ = sess.write(sess.ids().PlaySetCarriedItem, cb.Bytes())
	return equipTouched
}

// handleCraftResultClick implements taking from the 2x2 result slot.
func (s *Server) handleCraftResultClick(sess *Session, button byte, mode int32) {
	switch mode {
	case 0: // one craft to the cursor
		carried, touched, leftovers, ok := sess.takeCraftResult(1)
		if !ok {
			return
		}
		s.sendCraftUpdates(sess, touched, carried)
		s.placeCraftLeftovers(sess, leftovers)
	case 1: // shift-click: craft everything into the inventory
		for i := 0; i < maxStack; i++ {
			sess.invMu.Lock()
			empty := sess.inv[slotCraftResult].Empty()
			sess.invMu.Unlock()
			if empty {
				break
			}
			carried, _, leftovers, ok := sess.takeCraftResult(1)
			if !ok {
				break
			}
			s.placeCraftLeftovers(sess, leftovers)
			remainder, _ := sess.addStack(carried)
			sess.invMu.Lock()
			sess.carried = remainder
			sess.invMu.Unlock()
		}
		sess.invMu.Lock()
		carried := sess.carried
		sess.invMu.Unlock()
		_ = s.sendContainerContent(sess)
		_ = s.sendCarriedItem(sess, carried)
		s.refreshCraftResult(sess)
	}
}

// placeCraftLeftovers is ResultSlot.onTake's fallback for a crafting
// remainder that could not land in or merge onto its grid slot:
// Inventory.add, dropping whatever the inventory cannot take.
func (s *Server) placeCraftLeftovers(sess *Session, leftovers []ItemStack) {
	for _, rem := range leftovers {
		if rest, _ := sess.addStack(rem); !rest.Empty() {
			s.dropItemLocked(sess, rest)
		}
	}
}

// sendCraftUpdates syncs the grid slots, cursor, and result after a
// craft.
func (s *Server) sendCraftUpdates(sess *Session, slots []int16, carried ItemStack) {
	sess.invMu.Lock()
	state := sess.invState.Add(1)
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(slots)+2)
	seen := map[int16]bool{}
	add := func(sl int16) {
		if seen[sl] {
			return
		}
		seen[sl] = true
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	for _, sl := range slots {
		add(sl)
	}
	// Always sync the result slot so the client drops the crafted
	// stack even when recomputation lands on empty.
	add(slotCraftResult)
	sess.invMu.Unlock()
	for _, u := range updates {
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(state)
		b.Short(u.slot)
		writeSlot(&b, u.it)
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	_ = s.sendCarriedItem(sess, carried)
	s.refreshCraftResult(sess)
}

// quickMoveLocked moves from slot into the first stackable/empty slots
// of [from,to]. Caller holds invMu.
func (s *Server) quickMoveLocked(sess *Session, slot int16, from, to int16, touched *[]int16) {
	moving := sess.inv[slot]
	if moving.Empty() {
		return
	}
	touch := func(sl int16) {
		for _, t := range *touched {
			if t == sl {
				return
			}
		}
		*touched = append(*touched, sl)
	}
	// Merge into existing stacks first.
	for i := from; i <= to && !moving.Empty(); i++ {
		if !sess.inv[i].Stackable(moving) {
			continue
		}
		room := maxStackOfID(sess.inv[i].ID) - sess.inv[i].Count
		move := moving.Count
		if move > room {
			move = room
		}
		sess.inv[i].Count += move
		moving.Count -= move
		touch(i)
	}
	// Then fill empty slots, one capped stack at a time.
	for i := from; i <= to && !moving.Empty(); i++ {
		if !sess.inv[i].Empty() {
			continue
		}
		put := moving.Count
		if limit := maxStackOfID(moving.ID); put > limit {
			put = limit
		}
		sess.inv[i] = moving.withCount(put)
		moving.Count -= put
		if moving.Count <= 0 {
			moving = ItemStack{}
		}
		touch(i)
	}
	sess.inv[slot] = moving
	touch(slot)
}

// addStack merges a stack into the inventory (hotbar first), returning
// the remainder and whether anything was picked up. Slot updates are
// sent for every slot the stack touched.
func (sess *Session) addStack(it ItemStack) (ItemStack, bool) {
	if it.Empty() {
		return ItemStack{}, false
	}
	sess.invMu.Lock()
	order := make([]int16, 0, slotCount)
	for i := int16(slotHotbarStart); i <= slotOffhand; i++ {
		order = append(order, i)
	}
	for i := int16(slotMainStart); i < slotHotbarStart; i++ {
		order = append(order, i)
	}
	var touched []int16
	picked := false
	for _, sl := range order {
		if it.Empty() {
			break
		}
		cur := sess.inv[sl]
		if !cur.Stackable(it) {
			continue
		}
		room := maxStackOfID(cur.ID) - cur.Count
		if room <= 0 {
			continue
		}
		move := it.Count
		if move > room {
			move = room
		}
		sess.inv[sl].Count += move
		it.Count -= move
		touched = append(touched, sl)
		picked = true
	}
	for _, sl := range order {
		if it.Empty() {
			break
		}
		if !sess.inv[sl].Empty() {
			continue
		}
		put := it.Count
		if limit := maxStackOfID(it.ID); put > limit {
			put = limit
		}
		sess.inv[sl] = it.withCount(put)
		it.Count -= put
		if it.Count <= 0 {
			it = ItemStack{}
		}
		touched = append(touched, sl)
		picked = true
	}
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(touched))
	for _, sl := range touched {
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	sess.invMu.Unlock()
	for _, u := range updates {
		_ = sess.server.sendContainerSlot(sess, u.slot, u.it)
	}
	if picked {
		// vanilla's container.changed() listener evaluates the
		// inventory_changed advancement criteria after each mutation
		sess.server.fireInventoryChanged(sess)
	}
	return it, picked
}

// readChangedSlots consumes the Changed Slots array from a click
// packet (Prefixed Array of (Short slot, Hashed Slot)).
func readChangedSlots(r *protocol.PacketReader) (int, bool) {
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 128 {
		return 0, false
	}
	for i := int32(0); i < n; i++ {
		if _, err := r.Short(); err != nil {
			return 0, false
		}
		if _, ok := readHashedSlot(r); !ok {
			return 0, false
		}
	}
	return int(n), true
}

// readHashedSlot consumes one Hashed Slot: Bool has item, then when
// true item id, count, component hashes.
func readHashedSlot(r *protocol.PacketReader) (ItemStack, bool) {
	has, err := r.Bool()
	if err != nil {
		return ItemStack{}, false
	}
	if !has {
		return ItemStack{}, true
	}
	id, err := r.VarInt()
	if err != nil {
		return ItemStack{}, false
	}
	count, err := r.VarInt()
	if err != nil {
		return ItemStack{}, false
	}
	add, err := r.VarInt()
	if err != nil || add < 0 || add > 256 {
		return ItemStack{}, false
	}
	for i := int32(0); i < add; i++ {
		if _, err := r.VarInt(); err != nil { // component type
			return ItemStack{}, false
		}
		if _, err := r.Int(); err != nil { // hash
			return ItemStack{}, false
		}
	}
	rem, err := r.VarInt()
	if err != nil || rem < 0 || rem > 256 {
		return ItemStack{}, false
	}
	for i := int32(0); i < rem; i++ {
		if _, err := r.VarInt(); err != nil {
			return ItemStack{}, false
		}
	}
	return ItemStack{ID: id, Count: count}, true
}

// dropItemLocked spawns the thrown stack as an item entity above the
// player and counts the drop statistics — one custom "drop" per throw
// and the item count under dropped[item].
func (s *Server) dropItemLocked(sess *Session, it ItemStack) *Entity {
	if it.Empty() {
		return nil
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	itemType, ok := EntityTypeID("item")
	if !ok {
		return nil
	}
	// Vanilla's Player.drop tosses the stack forward along the look
	// vector at ~0.3 blocks/tick with a small upward pop.
	lx, _, lz := lookVector(yaw, pitch)
	e := s.entities.Spawn(&Entity{
		Type:        itemType,
		Dim:         sess.dimName(),
		X:           x + lx*0.3,
		Y:           y + 1.32, // eye height minus vanilla's 0.3 offset
		Z:           z + lz*0.3,
		MotionX:     lx * 0.3,
		MotionZ:     lz * 0.3,
		MotionY:     0.2,
		Item:        it,
		PickupDelay: 40,
		Thrower:     sess.entityID.Load(),
	})
	e.Yaw = yaw
	s.broadcastEntitySpawn(nil, *e)
	sess.addCustomStat("drop", 1)
	sess.addItemStat(statDropped, it.ID, it.Count)
	return e
}
