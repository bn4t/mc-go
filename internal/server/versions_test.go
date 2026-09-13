package server

import (
	"reflect"
	"testing"

	"mc-go/internal/protocol"
)

// TestVersionTableMatchesConstants pins every ProtoIDs field to its
// ID* constant. The constants serve tests and scripted clients; the
// table serves negotiated sessions. If they drift, multi-version
// dispatch silently speaks the wrong protocol, so drift must fail
// loudly here instead.
func TestVersionTableMatchesConstants(t *testing.T) {
	ids := LatestVersion().IDs
	pinned := map[string]int32{
		"LoginDisconnect": IDLoginDisconnect, "LoginSuccess": IDLoginSuccess,
		"LoginSetCompression":   IDLoginSetCompression,
		"ConfigClientInfo":      IDConfigClientInformation,
		"ConfigPluginMessageCB": IDConfigPluginMessageCB,
		"ConfigKnownPacks":      IDConfigKnownPacks, "ConfigKnownPacksCB": IDConfigKnownPacksCB,
		"ConfigRegistryData": IDConfigRegistryData, "ConfigFeatureFlags": IDConfigFeatureFlags,
		"ConfigUpdateTags": IDConfigUpdateTags, "ConfigFinish": IDConfigFinish,
		"ConfigFinishAck": IDConfigFinishAck,
		"PlayJoinGame":    IDPlayJoinGame, "PlaySyncPos": IDPlaySyncPos,
		"PlayDisconnect": IDPlayDisconnect, "PlayChunkData": IDPlayChunkData,
		"PlayUpdateLight":     IDPlayUpdateLight,
		"PlayChunkBatchStart": IDPlayChunkBatchStart, "PlayChunkBatchDone": IDPlayChunkBatchDone,
		"PlayChunkCacheRadius": IDPlayChunkCacheRadius, "PlayChunkBatchRecvSB": IDPlayChunkBatchRecvSB,
		"PlaySpawnEntity":     IDPlaySpawnEntity,
		"PlayEntityAnimation": IDPlayEntityAnimation,
		"EntityTypePlayer":    EntityTypePlayer, "PlayEntityTeleportCB": IDPlayEntityTeleportCB,
		"PlayRemoveEntity": IDPlayRemoveEntity, "EntityTypePig": EntityTypePig,
		"PlayKeepAliveCB": IDPlayKeepAliveCB, "PlayKeepAliveSB": IDPlayKeepAliveSB,
		"PlayTeleportConfirm": IDPlayTeleportConfirmSB,
		"PlayChatMessageSB":   IDPlayChatMessageSB, "PlayChatCommandSB": IDPlayChatCommandSB,
		"PlaySystemChatCB": IDPlaySystemChatCB, "PlayPlayerChat": IDPlayPlayerChatCB,
		"PlayMovePosSB":    IDPlayMovePosSB,
		"PlayMovePosRotSB": IDPlayMovePosRotSB, "PlayMoveRotSB": IDPlayMoveRotSB,
		"PlayMoveStatusSB": IDPlayMoveStatusSB, "PlayPlayerActionSB": IDPlayPlayerActionSB,
		"PlayUseItemOnSB": IDPlayUseItemOnSB, "PlayUseItemSB": IDPlayUseItemSB,
		"PlaySwingArmSB": IDPlaySwingArmSB, "PlayClientInfoSB": IDPlayClientInfoSB,
		"PlayClientStatusSB": IDPlayClientStatusSB, "PlayAttackSB": IDPlayAttackSB,
		"PlayPlayerInputSB":  IDPlayPlayerInputSB,
		"PlayPlayerLoadedSB": IDPlayPlayerLoadedSB,
		"PlayHeldChangeSB":   IDPlayHeldChangeSB,
		"PlayCreativeSlotSB": IDPlayCreativeSlotSB,
		"PlayBlockUpdateCB":  IDPlayBlockUpdateCB, "PlayBlockChangedAck": IDPlayBlockChangedAck,
		"PlayPlayerInfoUpd": IDPlayPlayerInfoUpdate, "PlayPlayerInfoRem": IDPlayPlayerInfoRemove,
		"PlayForgetChunk": IDPlayForgetChunk, "PlayDifficulty": IDPlayDifficulty,
		"PlayAbilitiesCB": IDPlayAbilitiesCB, "PlaySetCenterChunk": IDPlaySetCenterChunk,
		"PlayGameEvent": IDPlayGameEvent, "PlaySetHealth": IDPlaySetHealth,
		"PlaySetExperience": IDPlaySetExperience, "PlaySetHeldItemCB": IDPlaySetHeldItemCB,
		"PlayEntityEvent": IDPlayEntityEvent, "PlayEntityUpdatePos": IDPlayEntityUpdatePos,
		"PlayEntityUpdateRot": IDPlayEntityUpdateRot, "PlayEntityMetadata": IDPlayEntityMetadata,
		"PlayRespawn": IDPlayRespawn, "PlayCombatDeath": IDPlayCombatDeath,
		"PlayHurtAnimation":    IDPlayHurtAnimation,
		"PlayCommands":         IDPlayCommands,
		"PlayUpdateAttributes": IDPlayUpdateAttributes, "PlayContainerContent": IDPlayContainerContent,
		"PlayContainerSlot": IDPlayContainerSlot, "PlayContainerClose": IDPlayContainerClose,
		"PlayContainerProperty": IDPlayContainerProperty,
		"PlaySetCarriedItem":    IDPlaySetCarriedItem, "PlayClickContainerSB": IDPlayClickContainerSB,
		"PlayInitializeBorder": IDPlayInitializeBorder, "PlayRecipeBookAdd": IDPlayRecipeBookAdd,
		"PlayRecipeBookSet": IDPlayRecipeBookSet, "PlayServerData": IDPlayServerData,
		"PlayDefaultSpawn": IDPlayDefaultSpawn, "PlayTickingState": IDPlayTickingState,
		"PlayTickingStep": IDPlayTickingStep, "PlayUpdateAdvance": IDPlayUpdateAdvance,
		"PlayTakeItem":          IDPlayTakeItem,
		"PlayBlockEntityData":   IDPlayBlockEntityData,
		"PlayDamageEvent":       IDPlayDamageEvent,
		"PlayObjective":         IDPlayObjectiveCB,
		"PlayUpdateScore":       IDPlayUpdateScore,
		"PlayDisplayObjective":  IDPlayDisplayObjective,
		"PlayResetScore":        IDPlayResetScore,
		"PlayEnterCombat":       IDPlayEnterCombat,
		"PlayEndCombat":         IDPlayEndCombat,
		"PlaySetEquipment":      IDPlaySetEquipment,
		"PlayOpenSignEditor":    IDPlayOpenSignEditor,
		"PlayOpenScreen":        IDPlayOpenScreen,
		"PlayUpdateSignSB":      IDPlayUpdateSignSB,
		"PlayPlayerAbilitiesSB": IDPlayPlayerAbilitiesSB,
		"PlayCloseContainerSB":  IDPlayCloseContainerSB,
		"PlayUpdateRecipes":     IDPlayUpdateRecipes, "PlayUpdateTime": IDPlayUpdateTime,
	}
	// reportPinned maps the fields whose value comes from the generated
	// official table to the packet name they must resolve to. The name
	// is the assertion: officialID panics on a name the report does not
	// contain, so a typo fails here rather than shipping a wrong id.
	reportPinned := map[string]string{
		"PlayTagQueryCB": "tag_query", "PlayPongResponseCB": "pong_response",
		"PlayCommandSuggestionsCB": "command_suggestions", "PlayGameRuleValuesCB": "game_rule_values",
		"PlayStartConfiguration": "start_configuration", "PlayChangeDifficultyCB": "change_difficulty",
		"PlaySetCursorItem": "set_cursor_item", "PlaySetPlayerInventory": "set_player_inventory",
		"PlayMerchantOffers": "merchant_offers", "PlayCooldown": "cooldown",
		"PlayAwardStats": "award_stats", "PlaySetPlayerTeam": "set_player_team",
		"PlaySetPassengers": "set_passengers", "PlaySetEntityLink": "set_entity_link",
		"PlaySetEntityMotion": "set_entity_motion", "PlayUpdateMobEffect": "update_mob_effect",
		"PlayRemoveMobEffect": "remove_mob_effect", "PlaySelectAdvancementsTab": "select_advancements_tab",
		"PlayActionBarText": "set_action_bar_text", "PlayTabList": "tab_list",
		"PlayBossEvent": "boss_event", "PlayLevelEvent": "level_event",
		"PlayLevelParticles": "level_particles", "PlayExplode": "explode",
		"PlayBlockDestruction": "block_destruction", "PlayBlockEvent": "block_event",
		"PlayMoveEntityPos": "move_entity_pos", "PlayMoveEntityPosRot": "move_entity_pos_rot",
		"PlayMoveEntityRot": "move_entity_rot", "PlayRotateHead": "rotate_head",
		"PlayPlayerLookAt": "player_look_at", "PlayPlayerRotation": "player_rotation",
		"PlaySectionBlocksUpdate": "section_blocks_update", "PlaySetCamera": "set_camera",
		"PlaySetSimulationDistance": "set_simulation_distance", "PlayCustomPayloadCB": "custom_payload",
		"PlayStoreCookie": "store_cookie", "PlayTransfer": "transfer",
		"PlayCookieRequest": "cookie_request", "PlayResourcePackPush": "resource_pack_push",
		"PlayResourcePackPop": "resource_pack_pop", "PlayShowDialog": "show_dialog",
		"PlayClearDialog": "clear_dialog", "PlayWaypoint": "waypoint",
		"PlayCustomChatCompletions": "custom_chat_completions", "PlayDisguisedChat": "disguised_chat",
		"PlayDeleteChat": "delete_chat", "PlayStopSound": "stop_sound",
		"PlaySound": "sound", "PlaySoundEntity": "sound_entity",
		"PlayLowDiskSpaceWarning": "low_disk_space_warning", "PlayChunksBiomes": "chunks_biomes",
		"PlaySetBorderCenter": "set_border_center", "PlaySetBorderLerpSize": "set_border_lerp_size",
		"PlaySetBorderSize": "set_border_size", "PlaySetBorderWarningDelay": "set_border_warning_delay",
		"PlaySetBorderWarnDistance": "set_border_warning_distance", "PlayMapItemData": "map_item_data",
		"PlayBundleDelimiter": "bundle_delimiter", "PlayGameTestHighlightPos": "game_test_highlight_pos",
		"PlayDebugBlockValue": "debug/block_value", "PlayDebugChunkValue": "debug/chunk_value",
		"PlayDebugEntityValue": "debug/entity_value", "PlayDebugEvent": "debug/event",
		"PlayDebugSample": "debug_sample", "PlayMountScreenOpen": "mount_screen_open",
		"PlayMoveMinecart": "move_minecart_along_track", "PlayMoveVehicleCB": "move_vehicle",
		"PlaySetTitlesAnimation": "set_titles_animation", "PlaySetTitleText": "set_title_text",
		"PlaySetSubtitleText": "set_subtitle_text", "PlayClearTitles": "clear_titles",
		"PlayCustomReportDetails": "custom_report_details", "PlayServerLinks": "server_links",
		"PlayTestInstanceStatus": "test_instance_block_status", "PlayProjectilePower": "projectile_power",
	}
	rv := reflect.ValueOf(ids)
	rt := rv.Type()
	if want := len(pinned) + len(reportPinned); rt.NumField() != want {
		t.Fatalf("ProtoIDs has %d fields, %d are pinned", rt.NumField(), want)
	}
	// Every clientbound play field must name a distinct packet: two
	// fields on one id means a copy-paste wired a handler to the wrong
	// packet, which no value comparison alone would catch.
	seen := map[string]string{}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		got := int32(rv.Field(i).Int())
		if want, ok := pinned[name]; ok {
			if got != want {
				t.Fatalf("ProtoIDs.%s = %#x, const %#x", name, got, want)
			}
			continue
		}
		pkt, ok := reportPinned[name]
		if !ok {
			t.Fatalf("ProtoIDs.%s is neither pinned to a constant nor to an official packet name", name)
		}
		if want := officialID(PacketStatePlay, PacketDirClientbound, pkt); got != want {
			t.Fatalf("ProtoIDs.%s = %#x, official %s is %#x", name, got, pkt, want)
		}
		if prev, dup := seen[pkt]; dup {
			t.Fatalf("ProtoIDs.%s and ProtoIDs.%s both resolve to %s", prev, name, pkt)
		}
		seen[pkt] = name
	}
}

func TestNegotiateVersion(t *testing.T) {
	v, ok := NegotiateVersion(ProtocolVersion)
	if !ok || v.Name != VersionName {
		t.Fatalf("negotiate current = %+v,%v", v, ok)
	}
	if _, ok := NegotiateVersion(0); ok {
		t.Fatal("negotiated protocol 0")
	}
	if _, ok := NegotiateVersion(-1); ok {
		t.Fatal("negotiated protocol -1")
	}
	if LatestVersion().Protocol != ProtocolVersion {
		t.Fatal("latest is not the pinned protocol")
	}
}

func TestFrontendRegistry(t *testing.T) {
	found := false
	for _, n := range FrontendNames() {
		if n == "java" {
			found = true
		}
	}
	if !found {
		t.Fatalf("java missing from %v", FrontendNames())
	}
	fe, ok := NewFrontend("java", New("unused"))
	if !ok {
		t.Fatal("NewFrontend(java) failed")
	}
	if fe.Name() != "java" || len(fe.Versions()) == 0 {
		t.Fatalf("bad java frontend %+v", fe)
	}
	if _, ok := NewFrontend("bedrock", New("unused")); ok {
		t.Fatal("NewFrontend(bedrock) unexpectedly succeeded")
	}
}

// TestOutdatedClientRejected proves the negotiated table is enforced:
// a handshake protocol nobody speaks gets a login disconnect, not a
// session on the latest table.
func TestOutdatedClientRejected(t *testing.T) {
	s := New("unused")
	c, pr, pw := pipePair(t, s)
	defer c.Close()

	if err := sendHandshake(pw, 9999, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String("OldClient")
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	resp, err := pr.ReadPacket()
	if err != nil {
		t.Fatalf("outdated login read: %v", err)
	}
	if resp.ID != IDLoginDisconnect {
		t.Fatalf("outdated login resp = %#x, want disconnect %#x", resp.ID, IDLoginDisconnect)
	}
	if s.OnlineCount() != 0 {
		t.Fatalf("rejected client counted online: %d", s.OnlineCount())
	}
}
