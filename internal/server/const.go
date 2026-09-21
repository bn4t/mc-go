package server

// Vanilla baseline we implement against, from vanilla/server-26.2.jar version.json.
const (
	ProtocolVersion = 776
	VersionName     = "26.2"
	MOTD            = "mc-go from scratch"
	MaxPlayers      = 20
)

// Connection states.
const (
	StateHandshaking = iota
	StateStatus
	StateLogin
	StateConfiguration
	StatePlay
)

// Next-state values sent in Handshake packet.
const (
	NextStatus = 1
	NextLogin  = 2
)

// Packet IDs for protocol 776 (Minecraft 26.2), from the public
// protocol documentation (https://minecraft.wiki/w/Java_Edition_protocol).
// The full 776 ID tables live in testdata/protocol776-packet-ids.tsv and
// are asserted by the blackbox suite against the real server.
const (
	// Handshaking serverbound
	IDHandshake = 0x00
	// Status serverbound
	IDStatusRequest = 0x00
	IDPingRequest   = 0x01
	// Status clientbound
	IDStatusResponse = 0x00
	IDPongResponse   = 0x01
	// Login serverbound
	IDLoginStart        = 0x00
	IDLoginAcknowledged = 0x03
	// Login clientbound
	IDLoginDisconnect     = 0x00
	IDLoginSuccess        = 0x02
	IDLoginSetCompression = 0x03
	// Compression threshold, matching the vanilla default
	// (network-compression-threshold=256).
	CompressionThreshold = 256
	// Configuration serverbound
	IDConfigClientInformation = 0x00
	IDConfigFinishAck         = 0x03
	IDConfigKeepAliveSB       = 0x04
	IDConfigPongSB            = 0x05
	IDConfigKnownPacks        = 0x07
	// Configuration clientbound
	IDConfigPluginMessageCB = 0x01
	IDConfigFinish          = 0x03
	IDConfigKeepAliveCB     = 0x04
	IDConfigRegistryData    = 0x07
	IDConfigFeatureFlags    = 0x0C
	IDConfigUpdateTags      = 0x0D
	IDConfigKnownPacksCB    = 0x0E
	// Play clientbound (join + core sync set)
	IDPlayJoinGame         = 0x31
	IDPlaySyncPos          = 0x48
	IDPlayDisconnect       = 0x20
	IDPlayChunkData        = 0x2D
	IDPlayUpdateLight      = 0x30
	IDPlayChunkBatchStart  = 0x0C
	IDPlayChunkBatchDone   = 0x0B
	IDPlayChunkCacheRadius = 0x5F
	IDPlayChunkBatchRecvSB = 0x0B
	IDPlaySpawnEntity      = 0x01
	IDPlayEntityAnimation  = 0x02
	IDPlayRemoveEntity     = 0x4D
	IDPlayEntityTeleportCB = 0x23
	IDPlayKeepAliveCB      = 0x2C
	IDPlaySystemChatCB     = 0x79
	IDPlayPlayerChatCB     = 0x41
	// Play clientbound stateless sync / world state.
	IDPlayBlockUpdateCB     = 0x08
	IDPlayBlockChangedAck   = 0x04
	IDPlayPlayerInfoUpdate  = 0x46
	IDPlayPlayerInfoRemove  = 0x45
	IDPlayForgetChunk       = 0x25
	IDPlayDifficulty        = 0x0A
	IDPlayAbilitiesCB       = 0x40
	IDPlaySetCenterChunk    = 0x5E
	IDPlayGameEvent         = 0x26
	IDPlaySetHealth         = 0x68
	IDPlaySetExperience     = 0x67
	IDPlaySetHeldItemCB     = 0x69
	IDPlayEntityEvent       = 0x22
	IDPlayEntityUpdatePos   = 0x35
	IDPlayEntityUpdateRot   = 0x36
	IDPlayEntityMetadata    = 0x63
	IDPlayRespawn           = 0x52
	IDPlayCombatDeath       = 0x44
	IDPlayHurtAnimation     = 0x2A
	IDPlayCommands          = 0x10
	IDPlayUpdateAttributes  = 0x83
	IDPlayContainerContent  = 0x12
	IDPlayMerchantOffers    = 0x34
	IDPlayContainerSlot     = 0x14
	IDPlayContainerProperty = 0x13
	IDPlayContainerClose    = 0x11
	IDPlaySetCarriedItem    = 0x60
	IDPlayLevelEvent        = 0x2E
	IDPlayInitializeBorder  = 0x2B
	IDPlayRecipeBookAdd     = 0x4A
	IDPlayRecipeBookRemove  = 0x4B
	IDPlayRecipeBookSet     = 0x4C
	IDPlayServerData        = 0x56
	IDPlayDefaultSpawn      = 0x61
	IDPlayBlockEntityData   = 0x06
	IDPlayDamageEvent       = 0x19
	IDPlayObjectiveCB       = 0x6A
	IDPlayUpdateScore       = 0x6E
	IDPlayDisplayObjective  = 0x62
	IDPlayResetScore        = 0x4F
	IDPlayEnterCombat       = 0x43
	IDPlayEndCombat         = 0x42
	IDPlayEntityMotion      = 0x65
	IDPlaySetEquipment      = 0x66
	IDPlayOpenSignEditor    = 0x3C
	IDPlayOpenScreen        = 0x3B
	IDPlayTakeItem          = 0x7C
	IDPlayTickingState      = 0x7F
	IDPlayTickingStep       = 0x80
	IDPlayUpdateAdvance     = 0x82
	IDPlayRemoveMobEffect   = 0x4E
	IDPlayUpdateMobEffect   = 0x84
	IDPlayUpdateRecipes     = 0x85
	IDPlayUpdateTime        = 0x71
	// Play serverbound
	IDPlayTeleportConfirmSB = 0x00
	IDPlayAttackSB          = 0x01
	IDPlayChatCommandSB     = 0x07
	IDPlayChatMessageSB     = 0x09
	IDPlayClientStatusSB    = 0x0C
	IDPlayClientInfoSB      = 0x0E
	IDPlayClickContainerSB  = 0x12
	IDPlayCloseContainerSB  = 0x13
	IDPlayKeepAliveSB       = 0x1C
	IDPlayMovePosSB         = 0x1E
	IDPlayMovePosRotSB      = 0x1F
	IDPlayMoveRotSB         = 0x20
	IDPlayMoveStatusSB      = 0x21
	IDPlayPlayerAbilitiesSB = 0x28
	IDPlayPlayerActionSB    = 0x29
	IDPlayPlayerCommandSB   = 0x2A
	IDPlayPlayerInputSB     = 0x2B
	IDPlayUpdateSignSB      = 0x3D
	IDPlayPlayerLoadedSB    = 0x2C
	IDPlayInteractSB        = 0x1A
	IDPlayHeldChangeSB      = 0x35
	IDPlayCreativeSlotSB    = 0x38
	IDPlaySwingArmSB        = 0x3F
	IDPlayUseItemOnSB       = 0x42
	IDPlayUseItemSB         = 0x43
	// The remaining serverbound play packets. The router in routes.go
	// covers all 69; these are the ones without a named handler above.
	IDPlayBlockEntityTagQuerySB       = 0x02
	IDPlayBundleItemSelectedSB        = 0x03
	IDPlayChangeDifficultySB          = 0x04
	IDPlayChangeGameModeSB            = 0x05
	IDPlayChatAckSB                   = 0x06
	IDPlayChatCommandSignedSB         = 0x08
	IDPlayChatSessionUpdateSB         = 0x0A
	IDPlayClientTickEndSB             = 0x0D
	IDPlayCommandSuggestionSB         = 0x0F
	IDPlayConfigurationAcknowledgedSB = 0x10
	IDPlayContainerButtonClickSB      = 0x11
	IDPlayContainerSlotStateChangedSB = 0x14
	IDPlayCookieResponseSB            = 0x15
	IDPlayCustomPayloadSB             = 0x16
	IDPlayDebugSubscriptionRequestSB  = 0x17
	IDPlayEditBookSB                  = 0x18
	IDPlayEntityTagQuerySB            = 0x19
	IDPlayJigsawGenerateSB            = 0x1B
	IDPlayLockDifficultySB            = 0x1D
	IDPlayMoveVehicleSB               = 0x22
	IDPlayPaddleBoatSB                = 0x23
	IDPlayPickItemFromBlockSB         = 0x24
	IDPlayPickItemFromEntitySB        = 0x25
	IDPlayPingRequestSB               = 0x26
	IDPlayPlaceRecipeSB               = 0x27
	IDPlayPongSB                      = 0x2D
	IDPlayRecipeBookChangeSettingsSB  = 0x2E
	IDPlayRecipeBookSeenRecipeSB      = 0x2F
	IDPlayRenameItemSB                = 0x30
	IDPlayResourcePackSB              = 0x31
	IDPlaySeenAdvancementsSB          = 0x32
	IDPlaySelectTradeSB               = 0x33
	IDPlaySetBeaconSB                 = 0x34
	IDPlaySetCommandBlockSB           = 0x36
	IDPlaySetCommandMinecartSB        = 0x37
	IDPlaySetGameRuleSB               = 0x39
	IDPlaySetJigsawBlockSB            = 0x3A
	IDPlaySetStructureBlockSB         = 0x3B
	IDPlaySetTestBlockSB              = 0x3C
	IDPlaySpectatorActionSB           = 0x3E
	IDPlayTeleportToEntitySB          = 0x40
	IDPlayTestInstanceBlockActionSB   = 0x41
	IDPlayCustomClickActionSB         = 0x44

	// Login serverbound, beyond the handshake.
	IDLoginHello             = 0x00
	IDLoginKey               = 0x01
	IDLoginCustomQueryAnswer = 0x02
	IDLoginCookieResponse    = 0x04

	// Configuration serverbound, beyond the handshake.
	IDConfigCookieResponse      = 0x01
	IDConfigCustomPayload       = 0x02
	IDConfigResourcePack        = 0x06
	IDConfigCustomClickAction   = 0x08
	IDConfigAcceptCodeOfConduct = 0x09

	// Play clientbound packets the router above never sends yet but
	// which handlers reference by name.
	IDPlayTagQueryCB           = 0x7B
	IDPlayPongResponseCB       = 0x3E
	IDPlayCommandSuggestionsCB = 0x0F
	IDPlayGameRuleValuesCB     = 0x27
	IDPlayStartConfiguration   = 0x76
	IDPlayChangeDifficultyCB   = 0x0A
	IDPlaySetCursorItem        = 0x60
	IDPlayOpenBook             = 0x3A
	IDPlaySetPlayerInventory   = 0x6C

	// Entity type registry ids (776), from the official entity_type
	// report; TestEntityTypeConstantsMatchOfficialReport pins them.
	EntityTypePig    = 100
	EntityTypePlayer = 156
)
