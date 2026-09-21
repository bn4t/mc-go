package server

// Versioned protocol frontends.
//
// The server core (world, entities, sessions, broadcast) is
// protocol-agnostic. Each Frontend speaks one wire protocol and owns a
// per-version packet-ID table (ProtoIDs). The client's handshake
// protocol number selects the table; unknown versions are rejected at
// login with an outdated-client message, mirroring vanilla.
//
// Adding a version: append a Version value with its packet IDs (packet
// shapes that differ also need a branch at the encode/decode site, but
// ID-only changes like most minor releases are table-only).
// Adding a protocol (e.g. Bedrock, RCON): implement Frontend in a new
// file and RegisterFrontend it; core needs no changes.

// ProtoIDs is the full packet-ID surface one client version needs.
// Field semantics match the ID* constants in const.go, which pin the
// latest version's values for tests and scripted clients.
type ProtoIDs struct {
	LoginDisconnect     int32
	LoginSuccess        int32
	LoginSetCompression int32

	ConfigClientInfo      int32
	ConfigPluginMessageCB int32
	ConfigKnownPacks      int32
	ConfigKnownPacksCB    int32
	ConfigRegistryData    int32
	ConfigFeatureFlags    int32
	ConfigUpdateTags      int32
	ConfigFinish          int32
	ConfigFinishAck       int32

	PlayJoinGame          int32
	PlaySyncPos           int32
	PlayDisconnect        int32
	PlayChunkData         int32
	PlayUpdateLight       int32
	PlayChunkBatchStart   int32
	PlayChunkBatchDone    int32
	PlayChunkCacheRadius  int32
	PlayChunkBatchRecvSB  int32
	PlaySpawnEntity       int32
	PlayEntityAnimation   int32
	PlayRemoveEntity      int32
	EntityTypePig         int32
	EntityTypePlayer      int32
	PlayEntityTeleportCB  int32
	PlayKeepAliveCB       int32
	PlayKeepAliveSB       int32
	PlayTeleportConfirm   int32
	PlayChatMessageSB     int32
	PlayChatCommandSB     int32
	PlaySystemChatCB      int32
	PlayPlayerChat        int32
	PlayMovePosSB         int32
	PlayMovePosRotSB      int32
	PlayMoveRotSB         int32
	PlayMoveStatusSB      int32
	PlayPlayerActionSB    int32
	PlayUseItemOnSB       int32
	PlayUseItemSB         int32
	PlaySwingArmSB        int32
	PlayClientInfoSB      int32
	PlayClientStatusSB    int32
	PlayAttackSB          int32
	PlayPlayerInputSB     int32
	PlayUpdateSignSB      int32
	PlayPlayerAbilitiesSB int32
	PlayCloseContainerSB  int32
	PlayPlayerLoadedSB    int32
	PlayHeldChangeSB      int32
	PlayCreativeSlotSB    int32
	PlayBlockUpdateCB     int32
	PlayBlockChangedAck   int32
	PlayPlayerInfoUpd     int32
	PlayPlayerInfoRem     int32
	PlayForgetChunk       int32
	PlayDifficulty        int32
	PlayAbilitiesCB       int32
	PlaySetCenterChunk    int32
	PlayGameEvent         int32
	PlaySetHealth         int32
	PlaySetExperience     int32
	PlaySetHeldItemCB     int32
	PlayEntityEvent       int32
	PlayEntityUpdatePos   int32
	PlayEntityUpdateRot   int32
	PlayEntityMetadata    int32
	PlayRespawn           int32
	PlayCombatDeath       int32
	PlayHurtAnimation     int32
	PlayCommands          int32
	PlayUpdateAttributes  int32
	PlayContainerContent  int32
	PlayContainerSlot     int32
	PlayContainerProperty int32
	PlayContainerClose    int32
	PlaySetCarriedItem    int32
	PlayClickContainerSB  int32
	PlayInitializeBorder  int32
	PlayRecipeBookAdd     int32
	PlayRecipeBookSet     int32
	PlayServerData        int32
	PlayDefaultSpawn      int32
	PlayBlockEntityData   int32
	PlayDamageEvent       int32
	PlayObjective         int32
	PlayUpdateScore       int32
	PlayDisplayObjective  int32
	PlayResetScore        int32
	PlayEnterCombat       int32
	PlayEndCombat         int32
	PlaySetEquipment      int32
	PlayOpenSignEditor    int32
	PlayOpenScreen        int32
	PlayTakeItem          int32
	PlayTickingState      int32
	PlayTickingStep       int32
	PlayUpdateAdvance     int32
	PlayUpdateRecipes     int32
	PlayUpdateTags        int32
	PlayUpdateTime        int32

	// Responses and world-state packets used by the serverbound
	// router: tag queries, play-stage ping, command suggestions, the
	// game-rule table, and the configuration round trip.
	PlayTagQueryCB            int32
	PlayPongResponseCB        int32
	PlayCommandSuggestionsCB  int32
	PlayGameRuleValuesCB      int32
	PlayStartConfiguration    int32
	PlayChangeDifficultyCB    int32
	PlaySetCursorItem         int32
	PlaySetPlayerInventory    int32
	PlayMerchantOffers        int32
	PlayCooldown              int32
	PlayAwardStats            int32
	PlaySetPlayerTeam         int32
	PlaySetPassengers         int32
	PlaySetEntityLink         int32
	PlaySetEntityMotion       int32
	PlayUpdateMobEffect       int32
	PlayRemoveMobEffect       int32
	PlaySelectAdvancementsTab int32
	PlayActionBarText         int32
	PlayTabList               int32
	PlayBossEvent             int32
	PlayLevelEvent            int32
	PlayLevelParticles        int32
	PlayExplode               int32
	PlayBlockDestruction      int32
	PlayBlockEvent            int32
	PlayMoveEntityPos         int32
	PlayMoveEntityPosRot      int32
	PlayMoveEntityRot         int32
	PlayRotateHead            int32
	PlayPlayerLookAt          int32
	PlayPlayerRotation        int32
	PlaySectionBlocksUpdate   int32
	PlaySetCamera             int32
	PlaySetSimulationDistance int32
	PlayCustomPayloadCB       int32
	PlayStoreCookie           int32
	PlayTransfer              int32
	PlayCookieRequest         int32
	PlayResourcePackPush      int32
	PlayResourcePackPop       int32
	PlayShowDialog            int32
	PlayClearDialog           int32
	PlayWaypoint              int32
	PlayCustomChatCompletions int32
	PlayDisguisedChat         int32
	PlayDeleteChat            int32
	PlayStopSound             int32
	PlaySound                 int32
	PlaySoundEntity           int32
	PlayLowDiskSpaceWarning   int32
	PlayChunksBiomes          int32
	PlaySetBorderCenter       int32
	PlaySetBorderLerpSize     int32
	PlaySetBorderSize         int32
	PlaySetBorderWarningDelay int32
	PlaySetBorderWarnDistance int32
	PlayMapItemData           int32
	PlayBundleDelimiter       int32
	PlayGameTestHighlightPos  int32
	PlayDebugBlockValue       int32
	PlayDebugChunkValue       int32
	PlayDebugEntityValue      int32
	PlayDebugEvent            int32
	PlayDebugSample           int32
	PlayMountScreenOpen       int32
	PlayMoveMinecart          int32
	PlayMoveVehicleCB         int32
	PlaySetTitlesAnimation    int32
	PlaySetTitleText          int32
	PlaySetSubtitleText       int32
	PlayClearTitles           int32
	PlayCustomReportDetails   int32
	PlayServerLinks           int32
	PlayTestInstanceStatus    int32
	PlayProjectilePower       int32
}

// Version is one supported client version: its display name, protocol
// number, and packet-ID table.
type Version struct {
	Name     string
	Protocol int32
	IDs      ProtoIDs
}

// supportedVersions is the full multi-version table, newest last.
// Today it holds one entry; older/newer client support lands by
// appending entries (plus shape branches where a version's framing
// actually differs).
var supportedVersions = []Version{
	{
		Name:     VersionName,
		Protocol: ProtocolVersion,
		IDs: ProtoIDs{
			LoginDisconnect:       IDLoginDisconnect,
			LoginSuccess:          IDLoginSuccess,
			LoginSetCompression:   IDLoginSetCompression,
			ConfigClientInfo:      IDConfigClientInformation,
			ConfigPluginMessageCB: IDConfigPluginMessageCB,
			ConfigKnownPacks:      IDConfigKnownPacks,
			ConfigKnownPacksCB:    IDConfigKnownPacksCB,
			ConfigRegistryData:    IDConfigRegistryData,
			ConfigFeatureFlags:    IDConfigFeatureFlags,
			ConfigUpdateTags:      IDConfigUpdateTags,
			ConfigFinish:          IDConfigFinish,
			ConfigFinishAck:       IDConfigFinishAck,
			PlayJoinGame:          IDPlayJoinGame,
			PlaySyncPos:           IDPlaySyncPos,
			PlayDisconnect:        IDPlayDisconnect,
			PlayChunkData:         IDPlayChunkData,
			PlayUpdateLight:       IDPlayUpdateLight,
			PlayChunkBatchStart:   IDPlayChunkBatchStart,
			PlayChunkBatchDone:    IDPlayChunkBatchDone,
			PlayChunkCacheRadius:  IDPlayChunkCacheRadius,
			PlayChunkBatchRecvSB:  IDPlayChunkBatchRecvSB,
			PlaySpawnEntity:       IDPlaySpawnEntity,
			PlayEntityAnimation:   IDPlayEntityAnimation,
			PlayRemoveEntity:      IDPlayRemoveEntity,
			EntityTypePig:         EntityTypePig,
			EntityTypePlayer:      EntityTypePlayer,
			PlayEntityTeleportCB:  IDPlayEntityTeleportCB,
			PlayKeepAliveCB:       IDPlayKeepAliveCB,
			PlayKeepAliveSB:       IDPlayKeepAliveSB,
			PlayTeleportConfirm:   IDPlayTeleportConfirmSB,
			PlayChatMessageSB:     IDPlayChatMessageSB,
			PlayChatCommandSB:     IDPlayChatCommandSB,
			PlaySystemChatCB:      IDPlaySystemChatCB,
			PlayPlayerChat:        IDPlayPlayerChatCB,
			PlayMovePosSB:         IDPlayMovePosSB,
			PlayMovePosRotSB:      IDPlayMovePosRotSB,
			PlayMoveRotSB:         IDPlayMoveRotSB,
			PlayMoveStatusSB:      IDPlayMoveStatusSB,
			PlayPlayerActionSB:    IDPlayPlayerActionSB,
			PlayUseItemOnSB:       IDPlayUseItemOnSB,
			PlayUseItemSB:         IDPlayUseItemSB,
			PlaySwingArmSB:        IDPlaySwingArmSB,
			PlayClientInfoSB:      IDPlayClientInfoSB,
			PlayClientStatusSB:    IDPlayClientStatusSB,
			PlayAttackSB:          IDPlayAttackSB,
			PlayPlayerInputSB:     IDPlayPlayerInputSB,
			PlayUpdateSignSB:      IDPlayUpdateSignSB,
			PlayPlayerAbilitiesSB: IDPlayPlayerAbilitiesSB,
			PlayCloseContainerSB:  IDPlayCloseContainerSB,
			PlayPlayerLoadedSB:    IDPlayPlayerLoadedSB,
			PlayHeldChangeSB:      IDPlayHeldChangeSB,
			PlayCreativeSlotSB:    IDPlayCreativeSlotSB,
			PlayBlockUpdateCB:     IDPlayBlockUpdateCB,
			PlayBlockChangedAck:   IDPlayBlockChangedAck,
			PlayPlayerInfoUpd:     IDPlayPlayerInfoUpdate,
			PlayPlayerInfoRem:     IDPlayPlayerInfoRemove,
			PlayForgetChunk:       IDPlayForgetChunk,
			PlayDifficulty:        IDPlayDifficulty,
			PlayAbilitiesCB:       IDPlayAbilitiesCB,
			PlaySetCenterChunk:    IDPlaySetCenterChunk,
			PlayGameEvent:         IDPlayGameEvent,
			PlaySetHealth:         IDPlaySetHealth,
			PlaySetExperience:     IDPlaySetExperience,
			PlaySetHeldItemCB:     IDPlaySetHeldItemCB,
			PlayEntityEvent:       IDPlayEntityEvent,
			PlayEntityUpdatePos:   IDPlayEntityUpdatePos,
			PlayEntityUpdateRot:   IDPlayEntityUpdateRot,
			PlayEntityMetadata:    IDPlayEntityMetadata,
			PlayRespawn:           IDPlayRespawn,
			PlayCombatDeath:       IDPlayCombatDeath,
			PlayHurtAnimation:     IDPlayHurtAnimation,
			PlayCommands:          IDPlayCommands,
			PlayUpdateAttributes:  IDPlayUpdateAttributes,
			PlayContainerContent:  IDPlayContainerContent,
			PlayContainerSlot:     IDPlayContainerSlot,
			PlayContainerProperty: IDPlayContainerProperty,
			PlayContainerClose:    IDPlayContainerClose,
			PlaySetCarriedItem:    IDPlaySetCarriedItem,
			PlayClickContainerSB:  IDPlayClickContainerSB,
			PlayInitializeBorder:  IDPlayInitializeBorder,
			PlayRecipeBookAdd:     IDPlayRecipeBookAdd,
			PlayRecipeBookSet:     IDPlayRecipeBookSet,
			PlayServerData:        IDPlayServerData,
			PlayDefaultSpawn:      IDPlayDefaultSpawn,
			PlayBlockEntityData:   IDPlayBlockEntityData,
			PlayDamageEvent:       IDPlayDamageEvent,
			PlayObjective:         IDPlayObjectiveCB,
			PlayUpdateScore:       IDPlayUpdateScore,
			PlayDisplayObjective:  IDPlayDisplayObjective,
			PlayResetScore:        IDPlayResetScore,
			PlayEnterCombat:       IDPlayEnterCombat,
			PlayEndCombat:         IDPlayEndCombat,
			PlaySetEquipment:      IDPlaySetEquipment,
			PlayOpenSignEditor:    IDPlayOpenSignEditor,
			PlayOpenScreen:        IDPlayOpenScreen,
			PlayTakeItem:          IDPlayTakeItem,
			PlayTickingState:      IDPlayTickingState,
			PlayTickingStep:       IDPlayTickingStep,
			PlayUpdateAdvance:     IDPlayUpdateAdvance,
			PlayUpdateRecipes:     IDPlayUpdateRecipes,
			PlayUpdateTags:        cb("update_tags"),
			PlayUpdateTime:        IDPlayUpdateTime,

			// The rest of the clientbound surface is resolved from
			// the generated official table by name, so an id can
			// never drift from the report.
			PlayTagQueryCB:            cb("tag_query"),
			PlayPongResponseCB:        cb("pong_response"),
			PlayCommandSuggestionsCB:  cb("command_suggestions"),
			PlayGameRuleValuesCB:      cb("game_rule_values"),
			PlayStartConfiguration:    cb("start_configuration"),
			PlayChangeDifficultyCB:    cb("change_difficulty"),
			PlaySetCursorItem:         cb("set_cursor_item"),
			PlaySetPlayerInventory:    cb("set_player_inventory"),
			PlayMerchantOffers:        cb("merchant_offers"),
			PlayCooldown:              cb("cooldown"),
			PlayAwardStats:            cb("award_stats"),
			PlaySetPlayerTeam:         cb("set_player_team"),
			PlaySetPassengers:         cb("set_passengers"),
			PlaySetEntityLink:         cb("set_entity_link"),
			PlaySetEntityMotion:       cb("set_entity_motion"),
			PlayUpdateMobEffect:       cb("update_mob_effect"),
			PlayRemoveMobEffect:       cb("remove_mob_effect"),
			PlaySelectAdvancementsTab: cb("select_advancements_tab"),
			PlayActionBarText:         cb("set_action_bar_text"),
			PlayTabList:               cb("tab_list"),
			PlayBossEvent:             cb("boss_event"),
			PlayLevelEvent:            cb("level_event"),
			PlayLevelParticles:        cb("level_particles"),
			PlayExplode:               cb("explode"),
			PlayBlockDestruction:      cb("block_destruction"),
			PlayBlockEvent:            cb("block_event"),
			PlayMoveEntityPos:         cb("move_entity_pos"),
			PlayMoveEntityPosRot:      cb("move_entity_pos_rot"),
			PlayMoveEntityRot:         cb("move_entity_rot"),
			PlayRotateHead:            cb("rotate_head"),
			PlayPlayerLookAt:          cb("player_look_at"),
			PlayPlayerRotation:        cb("player_rotation"),
			PlaySectionBlocksUpdate:   cb("section_blocks_update"),
			PlaySetCamera:             cb("set_camera"),
			PlaySetSimulationDistance: cb("set_simulation_distance"),
			PlayCustomPayloadCB:       cb("custom_payload"),
			PlayStoreCookie:           cb("store_cookie"),
			PlayTransfer:              cb("transfer"),
			PlayCookieRequest:         cb("cookie_request"),
			PlayResourcePackPush:      cb("resource_pack_push"),
			PlayResourcePackPop:       cb("resource_pack_pop"),
			PlayShowDialog:            cb("show_dialog"),
			PlayClearDialog:           cb("clear_dialog"),
			PlayWaypoint:              cb("waypoint"),
			PlayCustomChatCompletions: cb("custom_chat_completions"),
			PlayDisguisedChat:         cb("disguised_chat"),
			PlayDeleteChat:            cb("delete_chat"),
			PlayStopSound:             cb("stop_sound"),
			PlaySound:                 cb("sound"),
			PlaySoundEntity:           cb("sound_entity"),
			PlayLowDiskSpaceWarning:   cb("low_disk_space_warning"),
			PlayChunksBiomes:          cb("chunks_biomes"),
			PlaySetBorderCenter:       cb("set_border_center"),
			PlaySetBorderLerpSize:     cb("set_border_lerp_size"),
			PlaySetBorderSize:         cb("set_border_size"),
			PlaySetBorderWarningDelay: cb("set_border_warning_delay"),
			PlaySetBorderWarnDistance: cb("set_border_warning_distance"),
			PlayMapItemData:           cb("map_item_data"),
			PlayBundleDelimiter:       cb("bundle_delimiter"),
			PlayGameTestHighlightPos:  cb("game_test_highlight_pos"),
			PlayDebugBlockValue:       cb("debug/block_value"),
			PlayDebugChunkValue:       cb("debug/chunk_value"),
			PlayDebugEntityValue:      cb("debug/entity_value"),
			PlayDebugEvent:            cb("debug/event"),
			PlayDebugSample:           cb("debug_sample"),
			PlayMountScreenOpen:       cb("mount_screen_open"),
			PlayMoveMinecart:          cb("move_minecart_along_track"),
			PlayMoveVehicleCB:         cb("move_vehicle"),
			PlaySetTitlesAnimation:    cb("set_titles_animation"),
			PlaySetTitleText:          cb("set_title_text"),
			PlaySetSubtitleText:       cb("set_subtitle_text"),
			PlayClearTitles:           cb("clear_titles"),
			PlayCustomReportDetails:   cb("custom_report_details"),
			PlayServerLinks:           cb("server_links"),
			PlayTestInstanceStatus:    cb("test_instance_block_status"),
			PlayProjectilePower:       cb("projectile_power"),
		},
	},
}

// cb resolves a clientbound play packet id by its official name.
func cb(name string) int32 { return officialID(PacketStatePlay, PacketDirClientbound, name) }

// sb resolves a serverbound play packet id by its official name.
func sb(name string) int32 { return officialID(PacketStatePlay, PacketDirServerbound, name) }

// SupportedVersions returns the versions this server speaks.
func SupportedVersions() []Version {
	out := make([]Version, len(supportedVersions))
	copy(out, supportedVersions)
	return out
}

// LatestVersion is the newest supported version, advertised in status.
func LatestVersion() Version {
	return supportedVersions[len(supportedVersions)-1]
}

// NegotiateVersion selects the table for a handshake protocol number.
func NegotiateVersion(proto int32) (Version, bool) {
	for _, v := range supportedVersions {
		if v.Protocol == proto {
			return v, true
		}
	}
	return Version{}, false
}
