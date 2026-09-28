package server

import (
	"fmt"
	"math"
	"mc-go/internal/world"
	"sort"
	"sync"
	"sync/atomic"

	"mc-go/internal/protocol"
)

// Entity is one spawned world entity. Dim is the dimension registry
// name it lives in; empty means the overworld.
type Entity struct {
	ID                  int32
	UUID                [16]byte
	Type                int32
	Dim                 string
	X, Y, Z             float64
	Yaw, Pitch, HeadYaw float32
	Data                int32
	VX, VY, VZ          int16
	// lastSect is the 16-block section the pairing eval last saw the
	// entity in — vanilla's TrackedEntity.lastSectionPos.
	lastSect [3]int
	// Item is set on item entities (Type minecraft:item): the stack
	// the entity represents, with a vanilla-like pickup delay and
	// float vertical motion for drop physics.
	Item        ItemStack
	PickupDelay int
	MotionY     float64
	// Sheared is the Shearable readyForShearing complement — sheep
	// sheared by shears/dispensers regrow wool on grass-eat ticks.
	Sheared bool
	// Pumpkin is SnowGolem's DATA_PUMPKIN_ID flag — a sheared snow
	// golem loses its carved-pumpkin head (default true at spawn).
	Pumpkin bool
	// EatAnim is EatBlockGoal.eatAnimationTick — a sheep grazes for
	// 40 ticks (entity event 10) before the eat lands.
	EatAnim int32
	// ChickenJockey is Chicken.isChickenJockey — a ridden-as-mount
	// flag that suppresses egg laying and marks the chicken as a
	// zombie mount (NBT IsChickenJockey).
	ChickenJockey bool
	// EggLayTime is Chicken.eggTime — counts down from a
	// 6000+rand(6000) roll toward the next egg lay.
	EggLayTime int32
	// ItemAge is ItemEntity.age — drives the 6000-tick despawn and
	// the merge cadence (every 2 ticks after a block crossing, else
	// 40), and a merge takes the survivor's minimum.
	ItemAge int32
	// ItemHP is ItemEntity.health — 5 from spawn, drained 4/tick by
	// lava (items burn in one tick then die). Lazily seeded on the
	// first lava touch since nothing else wounds item entities.
	ItemHP int32
	// OrbCount is ExperienceOrb.count — orbs start at 1, merges add the
	// neighbour's count, and each player touch grants OrbValue xp and
	// decrements it, discarding at zero.
	OrbCount int32
	// OrbFollowing is ExperienceOrb.followingPlayer — the entity id of
	// the player being pulled toward, retargeted each tick.
	OrbFollowing int32
	// OrbValue is ExperienceOrb's xp value (metadata index 8). Kept
	// out of Data: vanilla sends data=0 on an orb's spawn packet —
	// the value only rides the set_entity_data payload.
	OrbValue int32
	// Thrower is the entity id of the player who dropped/threw the
	// item entity (vanilla's ItemEntity owner) — mob pickups fire
	// thrown_item_picked_up_by_entity for that player. Zero when the
	// item came from a block drop or world spawn.
	Thrower int32
	// Owner restricts pickup to that player (ItemEntity's
	// setTarget — /give overflow). HasOwner distinguishes "no owner"
	// from a zero uuid.
	Owner    [16]byte
	HasOwner bool
	// ProjOwner is Projectile.owner — the shooter or latest
	// deflector's UUID (vanilla's EntityReference). Set by
	// AIM_DEFLECT; a live owner shields the ownerless
	// hurting-projectile unload discard. HasProjOwner distinguishes
	// "no owner" from a zero uuid.
	ProjOwner    [16]byte
	HasProjOwner bool
	// LastDeflectedBy is Projectile.lastDeflectedBy — an entity may
	// deflect the same projectile only once, later hits land.
	LastDeflectedBy int32
	// LeftOwner is Projectile.leftOwner — latches the first tick the
	// projectile's expanded box clears the owner's (ownerless
	// projectiles latch immediately). HasBeenShot is the same
	// unconditional latch the first Projectile.tick runs.
	LeftOwner   bool
	HasBeenShot bool
	// FakeItem is vanilla's ItemEntity.makeFakeItem: the /give visual
	// toss — never pickuppable, despawns on the next item tick.
	FakeItem bool
	// HeldItem is the allay's carried stack; LikedPlayer is the
	// LIKED_PLAYER brain memory (the hand-off's giver and the
	// trigger's subject); HeardNote/HeardNoteAt are the
	// LIKED_NOTEBLOCK_POSITION memory and when it was heard.
	HeldItem ItemStack
	// InvItem is the allay's single-slot SimpleContainer — only
	// inventory items (picked up, not hand-given) are thrown to a
	// deposit target.
	InvItem        ItemStack
	LikedPlayer    [16]byte
	HasLikedPlayer bool
	HeardNote      world.Pos
	HeardNoteAt    int64
	HasHeardNote   bool
	// LastThrow is the tick the allay last threw an item; vanilla's
	// GoAndGiveItemsToTarget waits TIME_BETWEEN_ITEM_THROWS (20 ticks)
	// between throws.
	LastThrow int64
	// Converting/ConversionTime/ConversionStarter model
	// ZombieVillager's cure countdown: ConversionTime ticks down by
	// cureProgress each game tick, and at zero finishCure replaces
	// the entity with a villager and fires cured_zombie_villager for
	// the starter. Silent suppresses the cure level event.
	Converting     bool
	ConversionTime int32
	// WaterConv/FreezeConv carry the drowned/stray conversion
	// countdowns' on-the-wire flag (Converting stays the zombie
	// villager CURE flag — different accessors in vanilla).
	WaterConv  bool
	FreezeConv bool
	// inWaterTime/inPowderSnowTime count the submerged/frozen ticks
	// toward the zombie->drowned (600+300) and skeleton->stray
	// (140+300) conversions; Converting/ConversionTime carry the
	// countdown itself.
	inWaterTime       int32
	inPowderSnowTime  int32
	ConversionStarter [16]byte
	HasConvStarter    bool
	Silent            bool
	// NoGravity mirrors Entity's NoGravity — summon NBT; gravity
	// never pulls the entity while set. Saved as a byte only when
	// true, matching Entity.saveWithoutId.
	NoGravity bool
	// CustomNameVisible is Entity's DATA_CUSTOM_NAME_VISIBLE flag —
	// summon NBT; the save writes the byte only while true.
	CustomNameVisible bool
	// CanBreakDoors is Zombie's door-breaking flag — summon NBT on
	// the zombie family, carried through ConversionType.convert.
	CanBreakDoors bool
	// Invulnerable is Entity's invulnerable flag — summon NBT;
	// saved always as a byte on mobs.
	Invulnerable bool
	// Effects is the mob's active status effects, mob_effect registry
	// id -> instance (summon active_effects / cure strength). Expiry
	// is checked lazily against the server clock, the way sessEffect
	// treats an expired-but-unflushed entry as gone.
	Effects map[int32]mobEffectInst
	// Offers is the merchant recipe list on traders (villager,
	// wandering_trader) from summon NBT Offers.Recipes. Profession is
	// VillagerData.profession (names the open screen's title). Baby is
	// Age<0 — babies never trade.
	Offers     []merchantOffer
	Profession string
	Baby       bool
	// Age is AgeableMob.age: babies count up toward 0 (grown at 0),
	// adults count a breeding cooldown down. Zombie-family babies use
	// the IsBaby flag instead and never age.
	Age int32
	// ForcedAge/AgeLocked/InLove are AgeableMob's remaining save
	// triple: forcedAge is the breeder's staged age, ageLocked pins
	// baby state (a negative Age that never counts up), and inLove
	// counts Animal's breeding window. All three write
	// unconditionally on save, InLove only on Animal subclasses.
	ForcedAge int32
	AgeLocked bool
	InLove    int32
	// AngryAt/AngryUntil are NeutralMob's persistent anger: AngryAt is
	// the provoker's uuid (zero when calm), AngryUntil the game time
	// the anger lapses — 0 means the open-ended HurtByTargetGoal
	// retaliation golems and spiders run.
	AngryAt     [16]byte
	AngryUntil  int64
	nextAlertAt int64
	// RangedBowAttackGoal state for the AbstractSkeleton family:
	// seeTime walks up/down on line of sight, attackTime is the
	// draw-restart cooldown, drawTicks counts the 20-tick pull.
	rangedSee    int32
	rangedStrafe int32
	rangedAttack int32
	rangedDraw   int32
	rangedCW     bool
	rangedBack   bool
	// RangedCrossbowAttackGoal / CrossbowAttack behavior state for
	// crossbow-armed mobs (pillager, piglin): cbState walks
	// UNCHARGED→CHARGING→CHARGED→READY_TO_ATTACK, cbTime counts the
	// 25-tick charge (CrossbowItem.getChargeDuration unenchanted),
	// cbDelay is the 20+rand(20) post-charge hold.
	cbState uint8
	cbTime  int32
	cbDelay int32
	// NearestHealableRaiderTargetGoal state for the witch:
	// witchHealCd is the 200-tick cooldown the heal goal's start()
	// sets — Witch.aiStep decrements it and bars player targeting
	// while it runs (setCanAttack(false)); witchHealTgt is the
	// locked raider's entity id, cleared by the throw's
	// setTarget(null).
	witchHealCd  int32
	witchHealTgt int32
	// GuardianAttackGoal state: beamTarget is the locked session
	// entity id (0 = idle), beamTime walks -10 up to the beam
	// duration (80 ticks, elder 60), beamMeta marks whether the
	// index-17 DATA_ID_ATTACK_TARGET sync is live on the wire.
	beamTarget int32
	beamTime   int32
	beamMeta   bool
	// Moving mirrors Guardian's DATA_ID_MOVING (metadata index 16,
	// boolean): GuardianMoveControl latches it while a swim drive is
	// live — it gates travelInWater's idle -0.005 sink and the client
	// tail/spikes animation.
	Moving bool
	// AvoidEntityGoal state: fleeUntil is the game-time deadline the
	// goal stays armed to — vanilla's posAvoider paths to a point
	// ~16 blocks off, so the fleer overshoots its trigger distance —
	// and fleeX/Z pin that running threat position plus its sprint.
	fleeUntil int64
	fleeX     float64
	// FleeSunGoal state: hideX/Y/Z latch the picked shelter cell —
	// PathNavigation keeps walking there until "done" (inside 1 block),
	// matching canContinueToUse = !navigation.isDone.
	hideLatched bool
	hideX       float64
	hideY       float64
	hideZ       float64
	fleeZ       float64
	fleeSprint  float64
	// Creeper swell state: Swell counts toward MaxSwell (NBT Fuse,
	// default 30), SwellDir is ±1 (metadata idx 18), Powered doubles
	// explosionRadius in explodeCreeper (NBT powered), Ignited is the
	// flint/NBT fuse-light that forces swellDir +1.
	Swell      int32
	SwellDir   int32
	MaxSwell   int32
	BlastPower int32
	Powered    bool
	Ignited    bool
	// DroppedSkulls latches after the powered creeper's blast
	// produces its first skull — killedEntity's CHARGED_CREEPER
	// loot-table drop runs once per creeper.
	DroppedSkulls bool
	// Bee sting state: Stung is vanilla's hasStung — latched by
	// doHurtTarget's landed sting, which also clears anger and starts
	// the post-sting death clock (customServerAiStep: every 5th tick a
	// random.nextInt(clamp(1200-timeSinceSting,1,1200))==0 roll deals
	// the bee its own health). CannotEnterHive is
	// Bee.stayOutOfHiveCountdown (NBT CannotEnterHiveTicks) — the
	// sedated branch of emptyAllLivingFromHive's anger sweep.
	Stung           bool
	CannotEnterHive int32
	timeSinceSting  int32
	// ExplosionPower is LargeFireball.explosionPower — the MOB
	// explosion radius onHit (ghast shoots 1, the summon NBT
	// ExplosionPower reads anything). Zero is treated as 1, the
	// vanilla default, for types that carry the field.
	ExplosionPower float32
	// AccelPower is AbstractHurtingProjectile.accelerationPower —
	// the power a hurting projectile's motion sync rides on (NBT
	// acceleration_power, constructor + read default 0.1). Zero is
	// treated as 0.1 for types that carry the field.
	AccelPower float64
	// Dangerous is WitherSkull's DATA_DANGEROUS — the black skull
	// variant the wither shoots. Reads from the "dangerous" NBT key,
	// drives getInertia 0.73 (vs the 0.95 base) and ships as entity
	// metadata index 8 in the pairing data.
	Dangerous bool
	// CloudOwnerID is AreaEffectCloud.owner — the LivingEntity that
	// spawned the cloud (0 when none). The dose's indirect_magic
	// damage source wires it as the cause; the cloud itself is the
	// direct entity.
	CloudOwnerID int32
	// ShulkerBullet wander state: flightSteps counts down to the next
	// selectNextMoveDirection roll; the target-delta triple is the
	// picked Direction's step*0.15 the velocity lerps toward;
	// ShulkerAxis latches the picked direction's axis (excluding it on
	// the re-roll). -1 ShulkerAxis means no direction picked yet.
	ShulkerSteps                       int32
	ShulkerAxis                        int32
	ShulkerTdx, ShulkerTdy, ShulkerTdz float64
	// AreaEffectCloud state: CloudWait is the 10-tick pre-activation
	// pause (setWaitTime), CloudDuration the 300-tick lifetime to
	// discard (setDuration), CloudAge the tick counter both compare
	// against. Radius shrinks by RadiusPerTick each active tick and
	// RadiusOnUse per victim dosed; CloudVictims is the
	// reapplication-delay (20t) cooldown keyed by entity id. The
	// copied potion contents ride in Effects.
	CloudWait          int32
	CloudDuration      int32
	CloudWaiting       bool
	CloudAge           int32
	CloudRadius        float32
	CloudRadiusPerTick float32
	CloudRadiusOnUse   float32
	CloudVictims       map[int32]int32
	// Ephemeral entity state for the non-living summons that run
	// their own per-tick end conditions: FangWarmup is
	// EvokerFangs.warmupDelayTicks (constructor 0), FangLife its
	// lifeTicks (22); EphemeralAge counts up for eye_of_ender's
	// life>80 discard, ominous_item_spawner's tickCount, and the
	// hanging entities' checkInterval-100 survives pass;
	// OminousSpawnAfter is spawnItemAfterTicks (60-120 roll);
	// FallingBlockName is FallingBlockEntity.blockState's name
	// (summon NBT BlockState.Name, default sand).
	FangWarmup        int32
	FangLife          int32
	EphemeralAge      int32
	OminousSpawnAfter int64
	FallingBlockName  string
	// Passengers holds the entity ids riding this entity (vanilla's
	// passengers list); Riding is the vehicle id on the passenger.
	Passengers []int32
	Riding     int32
	// WeldedPos marks the seat a passenger was last welded to: vanilla
	// anchors a passenger's bounding box centered on the seat point,
	// not at the feet, and keeps that box until the entity moves
	// elsewhere — a NoAI rider's box stays welded forever.
	WeldedPos *[3]float64
	// VehAnchor is the listener's vehicleFirstGood anchor: the last
	// vehicle position the driver's move_vehicle was validated
	// against. Set at mount; updated on each accepted move.
	VehAnchor [3]float64
	// PaddleL/PaddleR are AbstractBoat's DATA_ID_PADDLE_LEFT/RIGHT —
	// the driver's paddle input as last reported by paddle_boat.
	PaddleL, PaddleR bool
	// VehDamage/VehHurtTime/VehHurtDir are VehicleEntity's
	// DATA_ID_DAMAGE(10)/DATA_ID_HURT(8)/DATA_ID_HURTDIR(9): damage
	// accumulates amount*10 per hit and destroys past 40; hurtTime is
	// set to 10 per hit and hurtDir flips sign each hit.
	VehDamage   float32
	VehHurtTime int32
	VehHurtDir  int32
	// CartFlipped/CartYawO are OldMinecartBehavior's yaw-chase state:
	// isFlipped adds 180 to the atan2'd travel direction (toggled when
	// the wrapped yaw delta crosses ±170°), and CartYawO is last
	// tick's yaw the delta is measured against.
	CartFlipped bool
	CartYawO    float32
	// Saddle is the equipment.saddle slot on saddleable mounts
	// (strider, pig, horse family): vanilla gates mounting on it.
	Saddle bool
	// Tamed is AbstractHorse's tamed flag (NBT Tame) — a tamed mount's
	// sneak-interact opens the mount inventory screen.
	Tamed bool
	// HasChest is AbstractChestedHorse's ChestedHorse flag — a chested
	// donkey/mule/llama's mount inventory gains 15 slots (5 columns).
	HasChest bool
	// MountInv is the chested mount's cell contents — mount window
	// slots 2..2+3*columns, sized at window open.
	MountInv []ItemStack
	// Strength is the llama family's NBT key (1-5) — a chested
	// llama's inventory columns equal it (donkey/mule are 5).
	Strength int32
	// LlamaVariant is Llama's Variant enum (0-3, LEGACY_CODEC id) —
	// written for the llama family only.
	LlamaVariant int32
	// Small/ShowArms/DisabledSlots are the armor stand's
	// ArmorStand.CLIENT_FLAG bits and NBT; Equips is the entity
	// equipment map keyed by vanilla equipment index
	// (equipMainHand..equipHelmet) — armor stands read/write it in
	// interact; other mobs still use the named fields.
	Small         bool
	ShowArms      bool
	DisabledSlots int32
	Equips        map[int32]ItemStack
	// EquipDirty is vanilla's equipment dirty flag: summon NBT marks
	// every loaded slot, and the next mob tick's
	// detectEquipmentUpdates resends the set to trackers.
	EquipDirty bool
	// EquipDelta is the per-slot dirty set detectEquipmentUpdates
	// actually ships: /item writes and wolf-armor wear mark the slot
	// that changed, and the next tick's set_equipment packet carries
	// only those slots — not the whole set.
	EquipDelta map[byte]bool
	// MetaDirty/AttrDirty/PosDirty model ServerEntity's post-attach
	// dirty sync: pairing leaves every loaded entry dirty, so the next
	// mob tick resends the full metadata set (now including the
	// freshly-computed effect particles) and the syncable attributes;
	// PosDirty maps to vanilla's needsSync forcing a sendChanges run.
	// SyncCount counts sync ticks so the zero-delta keepalive lands
	// every 60 like vanilla's tickCount%60.
	MetaDirty bool
	AttrDirty bool
	PosDirty  bool
	// FallFlying is LivingEntity.isFallFlying — shared-flag 7. No mob
	// models elytra flight yet, so it only wires the motion-emit
	// bypass in ServerEntity.sendChanges' velocity branch.
	FallFlying bool
	SyncCount  int64
	// PortalCooldown is Entity.portalCooldown — the ticks before the
	// entity may use a portal again. AbsorptionAmount is
	// LivingEntity.absorptionAmount — absorption hearts above the
	// health pool. Both persist through the entity save and
	// ConversionType.convertCommon.
	PortalCooldown   int32
	AbsorptionAmount float32
	// Jumping is LivingEntity.jumping — the flag FloatGoal's
	// JumpControl latches while a mob is deep in fluid; the aiStep
	// jump branch consumes it. NoJumpDelay is the 10-tick
	// jumpFromGround cooldown. HColl records the last move's
	// horizontalCollision for the fluid wall-climb branch.
	Jumping     bool
	NoJumpDelay int
	HColl       bool
	// ServerEntity position/rotation send state: the fixed-point
	// (1/4096) base the last position packet was computed from, the
	// last packed rotation bytes, ticks since the last
	// entity_position_sync, and the previous onGround flag.
	syncInit                         bool
	syncPX, syncPY, syncPZ           int64
	lastYRotB, lastXRotB, lastYHeadB byte
	teleportDelay                    int
	wasOnGround                      bool
	// ParticlesSent is the last-broadcast DATA_EFFECT_PARTICLES value
	// bytes — dirty packs emit index 10 whenever it changes.
	ParticlesSent []byte
	// metaSent is the metadata set as the client last received it —
	// vanilla's SynchedEntityData dirty bookkeeping: a packDirty resend
	// carries only the entries whose backing value changed, so every
	// metadata emit (attach pack, surgical data-merge pack, health or
	// dying pack, MetaDirty flush) records its sent values here.
	// metaInited latches the defined-default baseline on first use —
	// entries start clean at their defined value (air 300, health 1.0,
	// pumpkin on, per-entity villagerdata), so a field sitting at its
	// default produces no dirty entry.
	metaSent   livingMeta
	metaInited bool
	// VillagerTypeID/VillagerProfID/VillagerLevel are the entity's
	// villagerdata as registry protocol ids (zombie_villager metadata
	// index 20, villager index 19). VillagerProfDefault is the
	// defined-default profession id — a zombie villager's is random
	// per vanilla's define(), a villager's is fixed none — and the
	// villagerdata entry packs only when the value differs from
	// {plains, VillagerProfDefault, 1}. VillagerFinalized packs the
	// finalized bool (loaded from NBT or set by conversion).
	VillagerTypeID      int32
	VillagerProfID      int32
	VillagerLevel       int32
	VillagerProfDefault int32
	VillagerFinalized   bool
	// MotionX/MotionZ carry a thrown item's horizontal drift, applied
	// each tick with vanilla's 0.98 air / ~0.55 grounded decay.
	MotionX, MotionZ float64
	// CustomName is the entity's custom_name exactly as the summon NBT
	// carried it: vanilla stores the component's serialized string and
	// uses that string as the display name's text.
	CustomName string
	// ExtraNBT holds save-map fields /data entity writes that the
	// entity model does not track individually — they round-trip back
	// into the save map verbatim like vanilla's loaded NBT.
	ExtraNBT map[string]any
	// dataSync is a pending dirty-entries entity_metadata payload and
	// dataSyncAttrs the attribute resend — /data mutations emit both on
	// the next mob sync tick, after the command's reply, like vanilla's
	// sendChanges running at tick end.
	dataSync      []byte
	dataSyncAttrs bool
	// dataSyncTick is the game tick the pending pack may flush from:
	// command-path writes stamp the next boundary so the command's
	// reply reaches the client first, like vanilla's queue order
	// (sendChanges runs after command dispatch inside the tick).
	dataSyncTick int64
	// Fuse is a PrimedTnt's remaining fuse ticks — DATA_FUSE_ID,
	// counting down one per tick until the entity discards and explodes.
	Fuse int32
	// KnotCheckTicks is BlockAttachedEntity.checkInterval on a
	// leash_knot — counts to 100 between survives() (fence) checks.
	KnotCheckTicks int32
	// Living mobs carry health and take damage from attacks.
	Living bool
	// Waypoint marks an entity that broadcasts a locator-bar waypoint;
	// WaypointColor / WaypointStyle are the /waypoint-modify overrides
	// (empty = the vanilla defaults).
	Waypoint      bool
	WaypointColor string
	WaypointStyle string
	// firstTick is Entity.firstTick — set at Spawn, cleared at the end
	// of the next level tick; waypoint transmitters emit nothing to
	// receivers while it holds (makeWaypointConnectionWith's gate).
	firstTick bool
	// AttrBase holds /attribute base-value overrides by attribute name;
	// AttrMods holds modifiers as attr -> modifier id -> modifier.
	AttrBase map[string]float64
	AttrMods map[string]map[string]attrMod
	// AttrTouched is the lazy AttributeMap membership: vanilla only
	// packs attribute instances that were getInstance'd at least once
	// — summon reads movement_speed for every living mob, setTame
	// re-asserts tamable max_health, and a non-NoAI Mob.finalizeSpawn
	// rolls the spawn bonuses. AttrsDirty is the subset pending resend
	// (vanilla's attributesToSync): instances are dirty on creation.
	AttrTouched map[string]bool
	AttrsDirty  map[string]bool
	// NoAI (from the summon NBT) freezes gravity and wander like
	// vanilla's Mob.NoAi flag — the entity still takes knockback.
	NoAI      bool
	Health    float32
	MaxHealth float32
	// invulnTicks/lastHurt are the same twenty-tick post-hit grace
	// players have: a hit inside it only lands for the amount it
	// exceeds the last hit by.
	invulnTicks int32
	lastHurt    float32
	// FireTicks is vanilla's remainingFireTicks (NBT "Fire"): it
	// decrements every tick and burns for one on_fire damage at each
	// multiple of twenty. Zero is unlit.
	FireTicks int32
	// HurtTime is vanilla's LivingEntity.hurtTime: set to 10 on every
	// landed hit (hurt flash duration), counted down in the living
	// baseTick, and saved as the HurtTime NBT key.
	HurtTime int16
	// HurtMarked is vanilla's Entity.hurtMarked: LivingEntity.hurt
	// marks it on every landed hit outside the no_impact tag (only
	// drown), and ServerEntity.sendChanges' tail clears it after
	// emitting set_entity_motion unconditionally — the drift gate is
	// bypassed so even a no-knockback hurt flushes the delta to
	// trackers and, for a player victim, to the victim itself.
	HurtMarked bool
	// BoardingCooldown is vanilla's Entity.boardingCooldown:
	// removePassenger sets it to 60 on the departing rider, the
	// entity tick counts it down, and canRide refuses a startRiding
	// while it's still positive — an entity that just dismounted
	// can't be re-scooped for three seconds.
	BoardingCooldown int32
	// TicksFrozen is vanilla's DATA_TICKS_FROZEN (NBT "TicksFrozen"):
	// powder snow accrues one per tick up to 140
	// (getTicksRequiredToFreeze), out-of-snow entities thaw at two
	// per tick, and a fully-frozen living entity takes freeze damage
	// every forty ticks.
	TicksFrozen int32
	// AirSupply is vanilla's Entity.airSupply (NBT "Air", metadata
	// index 1): entities construct at getMaxAirSupply (300); an
	// eye-level water block drains it one per tick and reaching -20
	// resets it to zero with the drown damage + event-67 pass.
	AirSupply int16
	// IsInPowderSnow is vanilla's isInPowderSnow: reset every base
	// tick and re-set by the powder snow FREEZE inside effect; the
	// freeze lifecycle reads the flag from the same tick's inside
	// pass.
	IsInPowderSnow bool
	// LastHurtByPlayerTicks is vanilla's lastHurtByPlayerMemoryTime:
	// 100 on a player-caused hurt, counting down in mobTickStep — the
	// killed_by_player window for loot gating and XP.
	LastHurtByPlayerTicks int32
	// LastHurtByPlayerUUID is the player behind that hurt — vanilla's
	// lastHurtByPlayer reference, which getKillCredit resolves so the
	// scoreboard kill criteria bump whoever hurt last, not whoever
	// landed the final blow.
	LastHurtByPlayerUUID [16]byte
	// Dying mobs lie as a corpse for twenty ticks (vanilla's
	// deathTime window) before the death-poof event and removal.
	Dying      bool
	deathTicks int32
	// Dormant marks an entity parked the way vanilla parks one whose
	// chunk never loads: a mob teleported or summoned into a
	// dimension holding no players. It exists, keeps its fields, and
	// persists — but it is invisible to @e selectors, never spawns
	// to a session, and never ticks. It wakes when a session enters
	// its dimension, the way vanilla's entity activates once a player
	// tickets its chunk.
	Dormant bool
	// WanderTicks counts AI steps; the wander angle derives from it so
	// movement is deterministic for tests. Strolling/StrollTX/StrollTZ
	// model vanilla's RandomStrollGoal: idle ~interval ticks between
	// strolls, then walk to a picked target until arrival or give-up.
	// SentMotionX/Y/Z are ServerEntity.lastSentMovement — the delta last
	// shipped as set_entity_motion; the emit fires when the current delta
	// drifts past 1e-7 squared, or just turned exactly zero.
	WanderTicks  int64
	WanderTicks2 int64
	LookYaw      float32
	Strolling    bool
	StrollTX     float64
	StrollTZ     float64
	// StrollTY is the wander target's y component — only aquatic
	// movers pick targets in 3D (generateRandomPosForAquatic); land
	// strolls leave it zero.
	StrollTY      float64
	StrollSteps   int32
	SentMotionX   float64
	SentMotionY   float64
	SentMotionZ   float64
	SentMotionSet bool
	// NeedsSync is Entity.needsSync — the flag vanilla's push() and
	// snapTo() latch (LivingEntity.aiStep pushes its walk input every
	// tick, so living entities carry it almost constantly). Inside
	// ServerEntity.sendChanges it both forces the pass to run early
	// and opens the set_entity_motion emit branch; cleared at the end
	// of each sendChanges like vanilla's `needsSync = false`.
	NeedsSync bool
	// WalkSpeed and WalkZZA are LivingEntity's speed field and zza
	// input drive: a MOVE_TO tick sets both to the goal's speed
	// (speedModifier·movement_speed), applyInput decays WalkZZA ×0.98
	// per tick once the drive lapses, and travel multiplies zza by the
	// friction-influenced speed for its input.
	WalkSpeed float64
	WalkZZA   float64
	// WalkXXA is the lateral half of the input pair — MoveControl's
	// STRAFE tick lands xxa here and it decays alongside WalkZZA.
	WalkXXA float64
	// PanicActive + PanicTX/TZ model PanicGoal (priority 1): a
	// panic-capable mob whose last damage type is in PANIC_CAUSES runs
	// to a DefaultRandomPos target within ±5 on x/z at
	// speedModifier × movement_speed until the navigation finishes.
	// PanicSteps is the stall cap standing in for an unreachable path.
	PanicActive bool
	PanicTX     float64
	PanicTZ     float64
	PanicSteps  int32
	// TemptActive/TemptTarget model TemptGoal: an animal whose
	// tempting item is in a nearby player's hand walks to the holder
	// at speedModifier × movement_speed and stops at 2.5 blocks;
	// TemptCalm is the post-stop cool-down (vanilla
	// reducedTickDelay(100)).
	TemptActive bool
	TemptTarget int32
	TemptCalm   int32
	// attackCooldown is the hostile-melee swing timer — vanilla's
	// twenty-tick reset after each landed blow.
	attackCooldown int32
	// loveTicks/loveCause are the breeding state: vanilla's Animal
	// inLove countdown plus the player who fed it last.
	loveTicks int32
	loveCause string
	// BreedTime is BreedGoal's loveTime — ticks the pair has spent
	// walking toward each other; the goal breeds at 60 when the gap
	// drops under 3 (9.0 dist²).
	BreedTime int32
	// ambientSoundTime is Mob.ambientSoundTime — climbs each living
	// tick, fires the ambient voice when random.nextInt(1000) is under
	// it, then resets to -getAmbientSoundInterval.
	ambientSoundTime int32
	// tamed/owner are the TamableAnimal state the tame_animal trigger
	// consumes.
	tamed bool
	owner string
	// WitchDrink/WitchDrinkTime are Witch.aiStep's using-item state:
	// while drinking the mainhand shows the potion and the timer
	// counts down from the 32-tick use duration; at zero the potion's
	// effects apply, the slot empties, and the drinking speed
	// modifier comes off.
	WitchDrink     bool
	WitchDrinkTime int32
	// LastDamageType is the minecraft:damage_type name of the most
	// recent landed hurt — vanilla's lastDamageSource, which the
	// witch's fire-resistance roll reads through DamageTypeTags.
	LastDamageType string
	// CanPickUpLoot mirrors vanilla's Mob.CanPickUpLoot flag — the
	// /summon NBT that arms the aiStep item pickup scan. Sulfur
	// cubes ignore it (their override is "body slot empty").
	CanPickUpLoot bool
	// PersistenceRequired mirrors Mob.persistenceRequired — the
	// summon NBT (and trade/breed/name paths) set it; checkDespawn
	// leaves such mobs alone.
	PersistenceRequired bool
	// NoActionTime is Mob.noActionTime — ticks since checkDespawn
	// last saw a player inside the 32-block no-despawn radius; the
	// random despawn roll needs it above 600.
	NoActionTime int32
	// PatrolLeader mirrors PatrollingMonster.patrolLeader — a
	// finalizeSpawn roll on non-patrol/event/structure spawns puts
	// the ominous banner on the leader's head with a 2.0 drop.
	PatrolLeader bool
	// Patrolling mirrors PatrollingMonster.patrolling — set when the
	// spawn reason is PATROL or findPatrolTarget ran.
	Patrolling bool
	// PatrolTarget is PatrollingMonster.patrolTarget — the leader's
	// wander anchor, picked ±500 blocks out when the patrol spawns.
	PatrolTarget world.Pos
	// HasPatrolTarget mirrors hasPatrolTarget() (patrolTarget != null):
	// the leader has one from spawn; companions only get theirs when
	// the leader's LongDistancePatrolGoal pushes its waypoint over.
	HasPatrolTarget bool
	// HomePos / HomeRadius / HasHome mirror Mob's homePosition /
	// homeRadius pair — hasHome() is homeRadius != -1. The elder
	// guardian's first customServerAiStep plants its home at its
	// block position with radius 16; the pair saves as
	// home_pos / home_radius.
	HomePos    world.Pos
	HomeRadius int32
	HasHome    bool
	// TurtleHome is Turtle.homePos — the turtle's own home field, not
	// the Mob restriction pair: every turtle writes it in its save
	// (default the entity's block position at read, ZERO before), and
	// hatched babies take the egg's position. HasEgg mirrors the
	// entity-data HAS_EGG flag into the has_egg save key.
	TurtleHome world.Pos
	HasEgg     bool
	// LeftHanded is Mob's 5% finalizeSpawn roll — a left-handed mob
	// renders its mainhand item in the other hand.
	LeftHanded bool
	// DropChances is Mob.dropChances: per-equipment-slot drop chance
	// overrides keyed by slot name (mainhand..saddle). Absent slots
	// default to 0.085; a chance over 1.0 preserves the item (it
	// drops unconditionally, undamaged). Parsed from the summon
	// drop_chances compound and re-emitted whole when any slot
	// differs from the default.
	DropChances map[string]float64
	// ImmuneToZombification / TimeInOverworld are the
	// AbstractPiglin+Hoglin zombification pair: a converting mob
	// (overworld, not NoAI) ticks TimeInOverworld to 300 then
	// converts — piglins to zombified_piglin, hoglin to zoglin.
	ImmuneToZombification bool
	TimeInOverworld       int32
	// BodyItem is the sulfur_cube BODY slot: one swallowed stack.
	BodyItem ItemStack
	// LeashHolder is Leashable.LeashData.leashHolder — the entity id
	// the leash points at (a player's entity id, a leash_knot's, or
	// another mob's for a leash chain). Zero is unleashed.
	LeashHolder int32
	// arrowShooter is the entity id of the player who loosed an arrow;
	// zero when unattributed.
	arrowShooter int32
	// arrowWeapon names the item that fired this arrow ("bow" is
	// vanilla's default; "crossbow" when a crossbow loosed it) —
	// killed_by_arrow's fired_from_weapon predicate reads it.
	arrowWeapon string
	// AttachedTo is FireworkRocketEntity.DATA_ATTACHED_TO_TARGET —
	// the living entity id a boost-use rocket rides (0 = free flight).
	AttachedTo int32
	// ShotAtAngle is DATA_SHOT_AT_ANGLE — dispenser-fired rockets
	// skip the free-flight horizontal acceleration.
	ShotAtAngle bool
	// RocketLife is FireworkRocketEntity.life — ticks since launch;
	// the rocket explodes when it exceeds Fuse (its lifetime).
	RocketLife int32
	// rocketHColl is Entity.horizontalCollision for a free rocket —
	// latched by the box sweep each tick; a horizontal collision
	// switches the draft factor from 1.15 to 1.0.
	rocketHColl bool
	// tridentStuck marks a thrown trident embedded in a block; the
	// owner walks over it to get the stack back.
	tridentStuck bool
	// tridentDamage is the minecraft:damage the pickup stack carries
	// (the trident wears one point when thrown).
	tridentDamage int32
	// tridentCreativeOnly marks a creative-thrown trident — vanilla's
	// Pickup.CREATIVE_ONLY: it can't be picked up.
	tridentCreativeOnly bool
	// tridentEnch is the thrown stack's enchantments component in the
	// canonical "name:level" encoding — the channeling check reads it
	// on hit, matching vanilla's copy of the ItemStack into the entity.
	tridentEnch string
	// arrowEffects is Arrow's customEffects list — the stray/bogged
	// potion shots each addEffect on the arrow; on a hit they apply
	// at full duration (a mob-made arrow stack carries no
	// potion_duration_scale component, so forEachEffect runs at 1.0).
	arrowEffects []arrowEffect
	// splashPotion is the potion id inside a thrown splash_potion's
	// potion_contents — the witch's performRangedAttack pick
	// (harming/slowness/poison/weakness) rides the projectile until
	// impact.
	splashPotion string
	// arrowStuck marks an arrow embedded in a block: it stops
	// moving and colliding until it despawns, like vanilla's
	// inGround state.
	arrowStuck bool
	// arrowLife counts the stuck ticks — AbstractArrow.tickDespawn
	// discards at 1200. arrowShake is the 7-tick embed wobble;
	// arrowStuckBlock is lastState, the block the tip embedded in —
	// break it and the arrow starts falling again.
	arrowLife       int32
	arrowShake      int32
	arrowStuckBlock string
	// arrowInWater is AbstractArrow's isInWater — latched one tick
	// behind the fluid scan like vanilla's Entity.tick water update.
	arrowInWater bool
	// The AbstractArrow NBT tail: arrowPickup is Pickup's ordinal
	// (0 disallow / 1 allowed / 2 creative-only), arrowDamage the
	// baseDamage double (2.0 default), arrowCrit the ID_FLAGS crit
	// bit, arrowPierce the PierceLevel byte, arrowSound the
	// hit-ground SoundEvent key, arrowItem the pickup stack's id.
	arrowPickup int32
	arrowDamage float64
	arrowCrit   bool
	arrowPierce int32
	arrowSound  string
	arrowItem   string
	// fishing is non-nil on a fishing_bobber: the per-hook catch state
	// machine (owner link, timers, parked surface).
	fishing  *fishBobber
	Grounded bool
	// FallDist is Entity.fallDistance for mobs: the accumulated
	// downward travel since the last landing, consumed by
	// mobFallDamage.
	FallDist float64
	// Raid / Wave mirror Raider's currentRaid + wave: raiders flag in
	// through Raid.joinRaid, drop on death or 112 blocks past center.
	Raid *raid
	Wave int32
	// CanJoinRaid is Raider.canJoinRaid: false by default, set true
	// by finalizeSpawn for non-NATURAL spawns (bare /summon is a
	// COMMAND spawn, so a tagged summon leaves it false) and by
	// Raid.joinRaid itself. Gates the every-20-ticks auto-join.
	CanJoinRaid bool
	// POIPos is a villager's claimed bed/bell position; POITicks
	// throttles the brain's re-scan, and HasPOI keeps the claim out of
	// the village's available set until release.
	POIPos   world.Pos
	HasPOI   bool
	POITicks int32
	// GlowUntil is the entity flag's glowing bit expiry — /raid glow
	// applies GLOWING for 1000 ticks.
	GlowUntil int64
}

// Manager owns entity ids and the live set. All methods are safe for
// concurrent use; entity AI ticks read snapshots.
type Manager struct {
	mu     sync.RWMutex
	byID   map[int32]*Entity
	nextID atomic.Int32
}

// NewManager starts ids at 1 — vanilla's global entity counter hands
// the first joiner (or first spawned entity) id 1 and increments.
func NewManager() *Manager {
	m := &Manager{byID: make(map[int32]*Entity)}
	m.nextID.Store(1)
	return m
}

// Spawn registers an entity and returns it. A preset UUID (players
// carry their profile id) is kept; zero UUIDs get a synthetic one.
// An empty Dim defaults to the overworld.
func (m *Manager) Spawn(e *Entity) *Entity {
	e.ID = m.nextID.Add(1) - 1
	if e.UUID == ([16]byte{}) {
		e.UUID = offlineUUID(fmt.Sprintf("entity-%d", e.ID))
	}
	// Entity's constructor sets airSupply to getMaxAirSupply (300).
	e.AirSupply = 300
	// AbstractHurtingProjectile constructs accelerationPower to
	// INITAL_ACCELERATION_POWER (0.1); readAdditionalSaveData's
	// getDoubleOr falls back to the same.
	if e.AccelPower == 0 {
		if name, ok := EntityTypeName(e.Type); ok && hurtingProjectileTypes[name] {
			e.AccelPower = 0.1
		}
	}
	// Entity.load latches needsSync unconditionally, so a summoned
	// hurting projectile's gate opens on its first sendChanges even
	// with no Motion tag in the SNBT (PosDirty is the outer-gate arm,
	// NeedsSync the inner drift gate).
	if name, ok := EntityTypeName(e.Type); ok && hurtingProjectileTypes[name] {
		e.PosDirty = true
		e.NeedsSync = true
	}
	if e.Dim == "" {
		e.Dim = dimOverworld
	}
	e.lastSect = sectOf(e.X, e.Y, e.Z)
	e.firstTick = true
	m.mu.Lock()
	m.byID[e.ID] = e
	m.mu.Unlock()
	return e
}

// ClearWaypointFirstTicks ends Entity.firstTick for every entity the
// level ticks — the flag suppresses waypoint connections only through
// the spawn tick.
func (m *Manager) ClearWaypointFirstTicks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.byID {
		e.firstTick = false
	}
}

// SetDimension moves an entity between dimensions.
func (m *Manager) SetDimension(id int32, dim string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byID[id]; ok {
		e.Dim = dim
	}
}

// Update moves an entity, reporting whether it existed. Movers publish
// the change; the manager only stores it.
func (m *Manager) Update(id int32, x, y, z float64, yaw, pitch float32, onGround bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.X, e.Y, e.Z = x, y, z
	e.Yaw, e.Pitch, e.HeadYaw = yaw, pitch, yaw
	_ = onGround
	return true
}

// SetMotion stores a fresh entity velocity (knockback writes here; the
// next tracker sync or explicit motion packet ships it).
func (m *Manager) SetMotion(id int32, x, y, z float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.MotionX, e.MotionY, e.MotionZ = x, y, z
	return true
}

// Damage applies damage to a living entity: the hit lands fully
// outside the post-hit grace window; inside it only the excess over
// the last hit applies (vanilla's invulnerableTime rule). Armor
// absorbs what the grace delta left standing — vanilla's absorb runs
// on the post-delta amount inside hurt. It reports the remaining
// health, the amount dealt, whether the entity exists, and whether
// the hit ran inside the grace window — vanilla skips the damage
// event and knockback on that excess path.
type DamageResult struct {
	Health float32 // post-hit health
	Dealt  float32 // health the hit removed (post-armor, health-capped)
	Exists bool    // the entity was found and not dying
	// Graced marks the invulnerableTime tail hit that returned
	// false in vanilla (orig <= lastHurt): no damage event, no
	// knockback, no hurt sound.
	Graced bool
	// BodyBroke fires when a wolf/horse body item hit max damage —
	// callers broadcast equipment-break event 65.
	BodyBroke bool
	// Absorbed marks the wolf_armor full-absorb branch: the hit was
	// accepted (hurtServer true — hurt sound, retaliation still run)
	// but the armor ate all of it.
	Absorbed bool
	// CrackChanged fires when the absorb wear crossed a wolf-armor
	// crackiness level — vanilla plays the crack sound + scute
	// particles on the transition only.
	CrackChanged bool
}

func (m *Manager) Damage(id int32, amount float32, typeName string, breachLevel float32) DamageResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok || e.Dying {
		return DamageResult{}
	}
	res := DamageResult{Exists: true}
	// Entity.isInvulnerableToBase: a fireImmune entity is invulnerable
	// to every is_fire-tagged source — the hit never lands (no
	// damage event, knockback, or sound).
	if name, _ := EntityTypeName(e.Type); mobFireImmune[name] && isFireDamageType[typeName] {
		res.Health = e.Health
		res.Graced = true
		return res
	}
	orig := amount
	res.Graced = e.invulnTicks > 10
	if res.Graced {
		if orig <= e.lastHurt {
			res.Health = e.Health
			return res
		}
		amount = orig - e.lastHurt
	}
	e.lastHurt = orig
	// The graced branch does not touch invulnerableTime — only a
	// full hit restarts the twenty-tick window.
	if !res.Graced {
		e.invulnTicks = 20
	}
	mobName, _ := EntityTypeName(e.Type)
	var armor, toughness float32
	armored := !bypassesArmor(typeName)
	if armored {
		armor = mobArmor(e)
		toughness = mobToughness(e)
	}
	if mobName == "wolf" && !bypassesWolfArmor(typeName) {
		// Wolf.actuallyHurt's armor-absorb branch: while the body
		// slot holds wolf_armor and the damage type doesn't bypass
		// wolf armor, the armor eats the whole hit — the wolf's
		// health never moves — wearing ceil(amount) durability.
		if iname, iok := itemNameOf(e.BodyItem.ID); iok && iname == "wolf_armor" {
			it := e.BodyItem
			maxD := damageableItems[iname]
			before := wolfArmorCrackLevel(it.Damage, maxD)
			it.Damage += unbreakingReduce(it, iname, int32(math.Ceil(float64(amount))))
			if it.Damage >= maxD {
				e.BodyItem = ItemStack{}
				res.BodyBroke = true
				// byDamage on the empty slot reports NONE — a break
				// never counts as a crack transition.
				res.CrackChanged = wolfArmorCrackLevel(0, maxD) != before
			} else {
				e.BodyItem = it
				res.CrackChanged = wolfArmorCrackLevel(it.Damage, maxD) != before
			}
			e.markEquipDelta(6)
			res.Absorbed = true
			res.Health = e.Health
			return res
		}
	}
	if armored && (mobName == "wolf" || mobName == "horse") && !e.BodyItem.Empty() {
		// Wolf.hurtArmor / Horse.hurtArmor — the only non-player
		// overrides: the body slot wears max(1, rawDamage/4) per hit
		// (unbreaking rolls apply); equippable.damage_on_hurt=false
		// pieces (every horse armor) never wear.
		if iname, iok := itemNameOf(e.BodyItem.ID); iok {
			if maxD, dok := damageableItems[iname]; dok && itemHurtWearExempt[iname] == 0 {
				it := e.BodyItem
				it.Damage += unbreakingReduce(it, iname, max(int32(amount/4), 1))
				if it.Damage >= maxD {
					e.BodyItem = ItemStack{}
					res.BodyBroke = true
				} else {
					e.BodyItem = it
				}
				e.markEquipDelta(6)
			}
		}
	}
	amount = reduceDamage(amount, armor, toughness, breachLevel)
	// Vanilla's damage_dealt is the health the target actually lost:
	// the hit's amount clamped to the pre-hit health.
	if amount > e.Health {
		amount = e.Health
	}
	e.Health -= amount
	if e.Health < 0 {
		e.Health = 0
	}
	// LivingEntity.damageApplied sets the hurt-flash countdown on
	// every landed hit — including the graced-excess branch.
	e.HurtTime = 10
	res.Health = e.Health
	res.Dealt = amount
	return res
}

// Heal raises a mob's health up to its vanilla max, reporting the
// amount actually applied — LivingEntity.heal's clamp. A dying or
// missing entity reports 0.
func (m *Manager) Heal(id int32, amount float32) float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok || e.Dying {
		return 0
	}
	maxHealth := e.MaxHealth
	if maxHealth <= 0 {
		if name, ok := EntityTypeName(e.Type); ok {
			maxHealth = mobHealth[name]
		}
	}
	if e.Health >= maxHealth {
		return 0
	}
	e.Health = min(e.Health+amount, maxHealth)
	e.MetaDirty = true
	return amount
}

// SetFireTicks stores a mob's burning countdown.
func (m *Manager) SetFireTicks(id int32, ticks int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byID[id]; ok {
		e.FireTicks = ticks
	}
}

// SetDying marks a living entity as dying (health exhausted).
func (m *Manager) SetDying(id int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byID[id]; ok {
		e.Dying = true
	}
}

// Remove deletes an entity, reporting whether it existed.
// markAttrTouched adds an attribute instance to the lazy map — the
// summon touches and every /attribute or attribute-list write go
// through here so `data get attributes` mirrors vanilla's pack.
func (e *Entity) markAttrTouched(name string) {
	if e.AttrTouched == nil {
		e.AttrTouched = map[string]bool{}
	}
	if e.AttrTouched[name] {
		return
	}
	e.AttrTouched[name] = true
	// A freshly-created attribute instance is dirty (vanilla marks
	// instances dirty whenever the attribute is written, and instance
	// creation is a write). Re-reads of an existing instance do not
	// re-dirty it.
	if attrSyncable[name] {
		if e.AttrsDirty == nil {
			e.AttrsDirty = map[string]bool{}
		}
		e.AttrsDirty[name] = true
		e.AttrDirty = true
	}
}

// markAttrDirty is AttributeInstance.setDirty — a write to an
// already-materialized instance re-arms the resend even though the
// touch itself is a no-op.
func (e *Entity) markAttrDirty(name string) {
	e.markAttrTouched(name)
	if attrSyncable[name] {
		if e.AttrsDirty == nil {
			e.AttrsDirty = map[string]bool{}
		}
		e.AttrsDirty[name] = true
		e.AttrDirty = true
	}
}

// markEquipDelta flags one wire equipment slot for the next
// detectEquipmentUpdates flush.
func (e *Entity) markEquipDelta(slot byte) {
	if e.EquipDelta == nil {
		e.EquipDelta = map[byte]bool{}
	}
	e.EquipDelta[slot] = true
}

// mobSlotStack reads the entity's current stack at a wire equipment
// slot: 0-5 the shared table, 6 body, 7 saddle.
func mobSlotStack(e *Entity, slot byte) ItemStack {
	switch slot {
	case 6:
		return e.BodyItem
	case 7:
		if e.Saddle {
			if sid, ok := world.ItemID("saddle"); ok {
				return ItemStack{ID: sid, Count: 1}
			}
		}
		return ItemStack{}
	}
	return e.Equips[int32(slot)]
}

func (m *Manager) Remove(id int32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return false
	}
	delete(m.byID, id)
	return true
}

// Get returns a copy of an entity.
func (m *Manager) Get(id int32) (Entity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byID[id]
	if !ok {
		return Entity{}, false
	}
	return *e, true
}

// GetPtr returns the live entity pointer — for callers that mutate
// fields the manager does not guard (the bobber's catch state).
func (m *Manager) GetPtr(id int32) (*Entity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byID[id]
	return e, ok
}

// All snapshots the live entity set — entity copies, so mutating a
// returned Entity does not write back (use SetMotion/Damage for that).
func (m *Manager) All() []Entity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entity, 0, len(m.byID))
	for _, e := range m.byID {
		out = append(out, *e)
	}
	return out
}

// TickDeath advances a dying mob's corpse countdown, reporting the
// tick at which vanilla fires the death-poof event and removes the
// entity (deathTime == 20).
func (m *Manager) TickDeath(id int32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.deathTicks++
	return e.deathTicks >= 20
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

// encodeSpawn builds a Spawn Entity body: VarInt id, UUID, VarInt
// type, position, LpVec3 velocity, rotation, head yaw, data. Field
// order and types follow the 776 protocol docs.
func encodeSpawn(e *Entity) []byte {
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.UUID(e.UUID)
	b.VarInt(e.Type)
	b.Double(e.X)
	b.Double(e.Y)
	b.Double(e.Z)
	b.Raw(lpVec3(float64(e.VX)/8000, float64(e.VY)/8000, float64(e.VZ)/8000))
	b.Byte(angleByte(e.Pitch))
	b.Byte(angleByte(e.Yaw))
	b.Byte(angleByte(e.HeadYaw))
	b.VarInt(e.Data)
	return b.Bytes()
}

// lpVec3 encodes the 26.2 low-precision Vec3 wire format (LpVec3):
// a zero/tiny vector (< 3.051944088384301e-5 max abs) is one 0x00
// byte; otherwise each axis is quantized to 15 bits over [-1,1] times
// a ceil-rounded shared scale in the low bits. Bits: scale low-2 at
// 0-1, continuation at 2, x/y/z at 3/18/33 — the packed word goes out
// as two low bytes then a big-endian int of the upper 32, plus a
// VarInt scale-extension when the scale needs more than 2 bits.
func lpVec3(x, y, z float64) []byte {
	san := func(v float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(-1.7179869183e10, math.Min(1.7179869183e10, v))
	}
	x, y, z = san(x), san(y), san(z)
	max := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if max < 3.051944088384301e-5 {
		return []byte{0}
	}
	scale := int64(math.Ceil(max))
	var scaleBits uint64
	cont := scale > 3
	if cont {
		scaleBits = uint64(scale&3) | 4
	} else {
		scaleBits = uint64(scale)
	}
	pack := func(v float64) uint64 {
		return uint64(math.Round((v/float64(scale)*0.5 + 0.5) * 32766))
	}
	word := scaleBits | pack(x)<<3 | pack(y)<<18 | pack(z)<<33
	hi := uint32(word >> 16)
	out := []byte{byte(word), byte(word >> 8), byte(hi >> 24), byte(hi >> 16), byte(hi >> 8), byte(hi)}
	if cont {
		var vb protocol.Buffer
		vb.VarInt(int32(scale >> 2))
		out = append(out, vb.Bytes()...)
	}
	return out
}

// encodeEntityTeleport builds a 776 Teleport Entity (Entity Position
// Sync) body: VarInt id, position, velocity, rotation, on-ground flag.
func encodeEntityTeleport(id int32, x, y, z float64, yaw, pitch float32, onGround bool) []byte {
	var b protocol.Buffer
	b.VarInt(id)
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Double(0) // velocity x
	b.Double(0) // velocity y
	b.Double(0) // velocity z
	b.Float(yaw)
	b.Float(pitch)
	b.Bool(onGround)
	return b.Bytes()
}

// playerEntity snapshots a live player as a spawnable entity: profile
// UUID, player type, current position and look.
func playerEntity(sess *Session) (Entity, bool) {
	eid := sess.entityID.Load()
	m := sess.server.entities
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byID[eid]
	if !ok {
		return Entity{}, false
	}
	return *e, true
}

// sendEntitySpawnsTo sends every other spawned player in the session's
// dimension to it (join burst); failures only fail the join.
func (s *Server) sendEntitySpawnsTo(sess *Session) error {
	var first error
	dim := sess.dimName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sess || other.state.Load() != StatePlay || !other.spawned.Load() || other.getName() == "" {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		e, ok := playerEntity(other)
		if !ok {
			return true
		}
		// Other players pair like any entity (player tracking range 32
		// chunks, clipped by view distance) — the tick eval maintains
		// it, the join burst seeds it.
		x, y, z, yaw, pitch, _ := other.Pos()
		e.X, e.Y, e.Z, e.Yaw, e.Pitch = x, y, z, yaw, pitch
		if s.pairable(sess, &e) {
			sess.setTracked(e.ID, true)
			if err := s.sendEntityTo(sess, &e); err != nil && first == nil {
				first = err
			}
		}
		return true
	})
	// World entities spawned before this player joined must be sent
	// too — vanilla tracks every entity in range, not just players.
	// Ridden vehicles follow with their passenger list so the client
	// attaches the riders.
	playerType := sess.ids().EntityTypePlayer
	s.entities.mu.RLock()
	ents := make([]Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		if e.Dim != dim || e.Dying || e.Dormant || e.Type == playerType {
			continue
		}
		ents = append(ents, *e)
	}
	s.entities.mu.RUnlock()
	for i := range ents {
		e := &ents[i]
		if !s.pairable(sess, e) {
			continue
		}
		// Pair on the live entity — the snapshot copy's latch would
		// leave the real send baseline unset for entities nobody
		// ticks.
		if live, ok := s.entities.GetPtr(e.ID); ok {
			e = live
		}
		sess.setTracked(e.ID, true)
		if err := s.sendEntityTo(sess, e); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// encodePlayerAttachMeta packs the player entity's attach-burst
// entries: health (index 9, float — the player's real health rides the
// record-less player entity) and DATA_PLAYER_MODE_CUSTOMISATION (index
// 16, byte) — vanilla marks skin parts dirty when client information
// applies, so the pairing stream ships 0x7f (all parts shown).
// DATA_PLAYER_MAIN_HAND (index 15) sits at its right-hand default and
// stays off the wire.
func encodePlayerAttachMeta(health float32) []byte {
	var b protocol.Buffer
	b.Byte(9) // index: DATA_HEALTH_ID
	b.Byte(3) // type: float
	b.Float(health)
	b.Byte(16) // index: skin parts
	b.Byte(0)  // type: byte
	b.Byte(0x7f)
	b.Byte(0xFF)
	return b.Bytes()
}

// playerAttachHealth resolves the health a player entity's attach meta
// carries: the owning session's live value, falling back to full
// health when the session isn't visible (record-only entities).
func (s *Server) playerAttachHealth(e *Entity) float32 {
	health := float32(20)
	s.sessions.Range(func(_, v any) bool {
		os := v.(*Session)
		if os.entityID.Load() == e.ID {
			health = os.health
			return false
		}
		return true
	})
	return health
}

// encodeBoatMeta packs the non-default AbstractBoat entries for the
// attach burst: indices 11/12 (bool) ride only while a paddle is down
// — vanilla's sendPairingData ships non-default entries, and a boat
// paired mid-stroke shows the driver paddling immediately.
func encodeBoatMeta(e *Entity) []byte {
	var b protocol.Buffer
	if e.PaddleL {
		b.Byte(11)
		b.Byte(8)
		b.Bool(true)
	}
	if e.PaddleR {
		b.Byte(12)
		b.Byte(8)
		b.Bool(true)
	}
	if b.Len() == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// sendEntityTo writes one entity's pairing stream to a session — the
// add_entity plus the pairing data vanilla's sendPairingData bundles
// into ClientboundBundlePacket: entity data (item stacks, living
// health) and the passenger list. Syncable attributes, velocity,
// leash and equipment ride the bundle too; mc-go ships those on their
// own packets where implemented. Used by the join burst and the
// tracked-entity pairing step. Vanilla's addPairing collects the whole
// stream into one ClientboundBundlePacket — bundle_delimiter bookends.
func (s *Server) sendEntityTo(sess *Session, e *Entity) error {
	var first error
	if err := sess.write(sess.ids().PlayBundleDelimiter, nil); err != nil {
		first = err
	}
	if err := sess.write(sess.ids().PlaySpawnEntity, encodeSpawn(e)); err != nil && first == nil {
		first = err
	}
	if !e.syncInit {
		// Pairing latches the send baseline (ServerEntity.broadcast):
		// position packets for unticked entities measure from the
		// spawned spot, not wherever the entity happens to sit when a
		// first flush finally runs.
		s.latchSyncBaseline(e)
	}
	var meta []byte
	switch {
	case e.Type == mustEntityType("firework_rocket"):
		// A firework rocket pairs its own data — fireworks item,
		// attached target, shot-at-angle — not the item-entity slot.
		meta = encodeRocketMetadata(e)
	case e.Item.ID != 0:
		meta = encodeItemMetadata(e.Item)
	case e.Type == sess.ids().EntityTypePlayer:
		meta = encodePlayerAttachMeta(s.playerAttachHealth(e))
	case e.Living:
		meta = encodeLivingMetadata(e, nil)
	default:
		if name, ok := EntityTypeName(e.Type); ok && isBoatEntityName(name) {
			meta = encodeBoatMeta(e)
		} else if ok && name == "wither_skull" && e.Dangerous {
			// DATA_DANGEROUS sits at index 8 behind the shared
			// Entity chain — the pairing data carries the dirty
			// BOOLEAN entry; a plain skull's pack is empty.
			meta = []byte{8, 8, 1, 0xFF}
		}
	}
	if meta != nil {
		var mb protocol.Buffer
		mb.VarInt(e.ID)
		mb.Raw(meta)
		if err := sess.write(sess.ids().PlayEntityMetadata, mb.Bytes()); err != nil && first == nil {
			first = err
		}
	}
	// sendPairingData continues with the attribute table for living
	// entities — vanilla streams every syncable attribute at pairing,
	// before equipment and passengers.
	if e.Living {
		if attrs := pairingAttrs(e, s.clock.count); attrs != nil {
			if err := sess.write(sess.ids().PlayUpdateAttributes, attrs); err != nil && first == nil {
				first = err
			}
		}
		// sendPairingData's non-empty equipment list rides the bundle
		// between attributes and passengers — the EquipDirty resend
		// that broadcasts fresh mob equipment after spawn is a
		// separate (loose) packet on vanilla too.
		if slots := armorStandEquipAll(e); len(slots) > 0 {
			if err := sess.write(sess.ids().PlaySetEquipment, encodeEquipment(e.ID, slots)); err != nil && first == nil {
				first = err
			}
		}
	}
	if len(e.Passengers) > 0 {
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.VarInt(int32(len(e.Passengers)))
		for _, pid := range e.Passengers {
			b.VarInt(pid)
		}
		if err := sess.write(sess.ids().PlaySetPassengers, b.Bytes()); err != nil && first == nil {
			first = err
		}
	}
	// sendPairingData ends with the leash holder — SetEntityLinkPacket
	// (source, dest) whenever the entity isLeashed.
	if e.LeashHolder != 0 {
		var b protocol.Buffer
		b.Int(e.ID)
		b.Int(e.LeashHolder)
		if err := sess.write(sess.ids().PlaySetEntityLink, b.Bytes()); err != nil && first == nil {
			first = err
		}
	}
	if err := sess.write(sess.ids().PlayBundleDelimiter, nil); err != nil && first == nil {
		first = err
	}
	return first
}

// pairingAttrs builds the pairing-stream update_attributes body: the
// entity's materialized attributes filtered to client-syncable, each
// at its current base, with the speed effect's multiplier folded into
// movement_speed like mobSyncAttrs. Vanilla's getSyncableAttributes
// only returns AttributeInstances that exist — created lazily on first
// access — so the set is AttrTouched ∩ syncable, not the supplier.
func pairingAttrs(e *Entity, now int64) []byte {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return nil
	}
	bases := entityAttrBases[name]
	if len(bases) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.AttrTouched))
	for a := range e.AttrTouched {
		if attrSyncable[a] {
			if _, ok := attrIDByName[a]; ok {
				names = append(names, a)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.VarInt(int32(len(names)))
	for _, a := range names {
		var mods []attrModifier
		if a == "movement_speed" {
			if inst, ok := e.Effects[effectSpeed]; ok && (inst.expiry < 0 || inst.expiry > now) {
				mods = append(mods, attrModifier{
					key:    "minecraft:effect.speed",
					amount: 0.2 * float64(inst.amplifier+1),
					op:     2, // add_multiplied_total
				})
			}
		}
		base, has := e.AttrBase[a]
		if !has {
			base = bases[a]
		}
		writeAttrEntry(&b, attrIDByName[a], base, mods...)
	}
	return b.Bytes()
}

// broadcastEntitySpawn announces one entity to every other spawned
// play-stage session in the entity's dimension.
func (s *Server) broadcastEntitySpawn(except *Session, e Entity) {
	s.broadcastEntitySpawnIn(e.Dim, except, e)
}

// broadcastEntitySpawnIn announces one entity to spawned sessions in
// dimension dim — vanilla's addEntity path, which pairs the new entity
// with every player in range (TrackedEntity.updatePlayers).
// The snapshot copy only carries identity; pairing runs against the
// live entity so sendEntityTo's send-baseline latch lands on the
// object later flushes measure.
func (s *Server) broadcastEntitySpawnIn(dim string, except *Session, e Entity) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except {
			return true
		}
		if live, ok := s.entities.GetPtr(e.ID); ok {
			s.evalPair(sess, live)
		} else {
			s.evalPair(sess, &e)
		}
		return true
	})
}

// broadcastSetPassengers announces a vehicle's passenger list to every
// spawned play-stage session in its dimension — vanilla's
// ServerEntity sends set_passengers to trackers whenever the list
// changes (mount, dismount, passenger removal).
func (s *Server) broadcastSetPassengers(veh *Entity) {
	var b protocol.Buffer
	b.VarInt(veh.ID)
	b.VarInt(int32(len(veh.Passengers)))
	for _, pid := range veh.Passengers {
		b.VarInt(pid)
	}
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != veh.Dim || !sess.tracksEntity(veh.ID) {
			return true
		}
		_ = sess.write(sess.ids().PlaySetPassengers, body)
		return true
	})
}

// broadcastEntityTeleport fans one entity movement to every other
// spawned play-stage session sharing the entity's dimension.
func (s *Server) broadcastEntityTeleport(except *Session, id int32, x, y, z float64, yaw, pitch float32, onGround bool) {
	dim := dimOverworld
	if except != nil {
		dim = except.dimName()
	} else if e, ok := s.entities.Get(id); ok {
		dim = e.Dim
	}
	body := encodeEntityTeleport(id, x, y, z, yaw, pitch, onGround)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim || !sess.tracksEntity(id) {
			return true
		}
		if e, ok := s.entities.Get(id); ok && !s.isPlayerEntity(id) && !s.pairable(sess, &e) {
			return true
		}
		_ = sess.write(sess.ids().PlayEntityTeleportCB, body)
		return true
	})
}

// broadcastEntityRemove drops entity ids from every spawned play-stage
// session's tracker in the entity's dimension. Unknown ids are ignored
// client-side, so a missed spawn (early join failure) cannot corrupt
// anyone.
func (s *Server) broadcastEntityRemove(ids []int32) {
	s.broadcastEntityRemoveIn(dimOverworld, nil, ids)
}

// broadcastEntityRemoveIn drops entity ids for sessions in dimension
// dim, optionally skipping one session.
func (s *Server) broadcastEntityRemoveIn(dim string, except *Session, ids []int32) {
	if len(ids) == 0 {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		for _, id := range ids {
			if !sess.tracksEntity(id) {
				continue
			}
			sess.setTracked(id, false)
			_ = sess.write(sess.ids().PlayRemoveEntity, encodeRemove(id))
		}
		return true
	})
}

// encodeRemove builds a Remove Entities body for one id.
func encodeRemove(id int32) []byte {
	var b protocol.Buffer
	b.VarInt(1)
	b.VarInt(id)
	return b.Bytes()
}

// encodeTeleportEntity builds a teleport_entity body (packet id 125):
// VarInt entity id, PositionMoveRotation (pos vec3 + delta vec3 +
// yRot/xRot floats), the int32 relatives bitmask, and onGround — the
// exact field order of ClientboundTeleportEntityPacket's stream codec.
func encodeTeleportEntity(id int32, x, y, z, dx, dy, dz float64, yaw, pitch float32, relatives int32, onGround bool) []byte {
	var b protocol.Buffer
	b.VarInt(id)
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Double(dx)
	b.Double(dy)
	b.Double(dz)
	b.Float(yaw)
	b.Float(pitch)
	b.Int(relatives)
	b.Bool(onGround)
	return b.Bytes()
}

// angleByte compresses degrees into a protocol angle byte,
// wrapping full turns and negatives into [0, 256).
func angleByte(deg float32) byte {
	v := int(float64(deg)*256.0/360.0) % 256
	if v < 0 {
		v += 256
	}
	return byte(v)
}

// arrowEffect is one custom potion instance on an arrow —
// (mob_effect id, tick duration).
type arrowEffect struct {
	id  int32
	dur int32
}
