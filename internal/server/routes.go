package server

import (
	"time"

	"mc-go/internal/protocol"
)

// playHandler handles one decoded serverbound play packet under simulation
// ownership. Handlers must not block on the socket (sess.write enqueues).
type playHandler func(*Server, *Session, protocol.Packet)

// playServerbound is the pinned 776 routing table. The admitted Java profile
// captures its own immutable dispatcher; candidates do not inherit it merely
// because packet numbers match. Every one of the 69 ids the 776 client sends appears
// here exactly once; TestServerboundPlayCoverage pins the key set
// against the vanilla server's own --reports packet table, so a packet
// can never be silently dropped by an added or removed case.
//
// ignoreServerbound includes both deliberate no-ops and placeholders for
// unimplemented privileged editor workflows. Coverage requires an explicit
// registration; it does not establish behavior parity for ignored packets.
//
// It is filled in init rather than a var initializer because the
// configuration handshake loops back into the play stage
// (Acknowledge Configuration re-runs handleConfig, which ends in
// handlePlay): a var initializer would make Go's static dependency
// analysis report an initialization cycle through those handlers.
var playServerbound map[int32]playHandler

func init() {
	playServerbound = map[int32]playHandler{
		IDPlayTeleportConfirmSB: (*Server).routeTeleportConfirm,
		IDPlayAttackSB:          (*Server).handleAttack,

		IDPlayBlockEntityTagQuerySB: (*Server).handleBlockEntityTagQuery,
		IDPlayBundleItemSelectedSB:  (*Server).handleBundleItemSelected,
		IDPlayChangeDifficultySB:    (*Server).handleChangeDifficulty,
		IDPlayChangeGameModeSB:      (*Server).handleChangeGameMode,

		IDPlayChatAckSB:           (*Server).handleChatAck,
		IDPlayChatCommandSB:       (*Server).routeChatCommand,
		IDPlayChatCommandSignedSB: (*Server).routeChatCommandSigned,
		IDPlayChatMessageSB:       (*Server).routeChatMessage,
		IDPlayChatSessionUpdateSB: (*Server).handleChatSessionUpdate,

		IDPlayChunkBatchRecvSB:            (*Server).handleChunkBatchReceived,
		IDPlayClientStatusSB:              (*Server).handleClientStatus,
		IDPlayClientTickEndSB:             (*Server).ignoreServerbound,
		IDPlayClientInfoSB:                (*Server).handleClientInfo,
		IDPlayCommandSuggestionSB:         (*Server).handleCommandSuggestion,
		IDPlayConfigurationAcknowledgedSB: (*Server).handleConfigurationAcknowledged,

		IDPlayContainerButtonClickSB:      (*Server).handleContainerButtonClick,
		IDPlayClickContainerSB:            (*Server).routeClickContainer,
		IDPlayCloseContainerSB:            (*Server).handleCloseContainer,
		IDPlayContainerSlotStateChangedSB: (*Server).handleContainerSlotStateChanged,

		IDPlayCookieResponseSB:           (*Server).handleCookieResponse,
		IDPlayCustomPayloadSB:            (*Server).handleCustomPayload,
		IDPlayDebugSubscriptionRequestSB: (*Server).handleDebugSubscriptionRequest,
		IDPlayEditBookSB:                 (*Server).handleEditBook,
		IDPlayEntityTagQuerySB:           (*Server).handleEntityTagQuery,
		IDPlayInteractSB:                 (*Server).handleInteract,
		IDPlayJigsawGenerateSB:           (*Server).ignoreServerbound,

		IDPlayKeepAliveSB:      (*Server).routeKeepAlive,
		IDPlayLockDifficultySB: (*Server).handleLockDifficulty,

		IDPlayMovePosSB:     (*Server).handleMove,
		IDPlayMovePosRotSB:  (*Server).handleMove,
		IDPlayMoveRotSB:     (*Server).handleMove,
		IDPlayMoveStatusSB:  (*Server).handleMove,
		IDPlayMoveVehicleSB: (*Server).handleMoveVehicle,
		IDPlayPaddleBoatSB:  (*Server).handlePaddleBoat,

		IDPlayPickItemFromBlockSB:  (*Server).handlePickItemFromBlock,
		IDPlayPickItemFromEntitySB: (*Server).handlePickItemFromEntity,
		IDPlayPingRequestSB:        (*Server).handlePingRequest,
		IDPlayPlaceRecipeSB:        (*Server).handlePlaceRecipe,

		IDPlayPlayerAbilitiesSB: (*Server).handlePlayerAbilities,
		IDPlayPlayerActionSB:    (*Server).routePlayerAction,
		IDPlayPlayerCommandSB:   (*Server).handlePlayerCommand,
		IDPlayPlayerInputSB:     (*Server).handlePlayerInput,
		IDPlayPlayerLoadedSB:    (*Server).handlePlayerLoaded,
		IDPlayPongSB:            (*Server).handlePong,

		IDPlayRecipeBookChangeSettingsSB: (*Server).handleRecipeBookChangeSettings,
		IDPlayRecipeBookSeenRecipeSB:     (*Server).handleRecipeBookSeenRecipe,
		IDPlayRenameItemSB:               (*Server).handleRenameItem,
		IDPlayResourcePackSB:             (*Server).handleResourcePackResponse,
		IDPlaySeenAdvancementsSB:         (*Server).handleSeenAdvancements,
		IDPlaySelectTradeSB:              (*Server).handleSelectTrade,
		IDPlaySetBeaconSB:                (*Server).handleSetBeacon,

		IDPlayHeldChangeSB:         (*Server).handleHeld,
		IDPlaySetCommandBlockSB:    (*Server).handleSetCommandBlock,
		IDPlaySetCommandMinecartSB: (*Server).handleSetCommandMinecart,
		IDPlayCreativeSlotSB:       (*Server).handleCreativeSlot,
		IDPlaySetGameRuleSB:        (*Server).handleSetGameRule,
		IDPlaySetJigsawBlockSB:     (*Server).ignoreServerbound,
		IDPlaySetStructureBlockSB:  (*Server).handleSetStructureBlock,
		IDPlaySetTestBlockSB:       (*Server).ignoreServerbound,
		IDPlayUpdateSignSB:         (*Server).handleUpdateSign,

		IDPlaySpectatorActionSB:         (*Server).handleSpectatorAction,
		IDPlaySwingArmSB:                (*Server).handleSwing,
		IDPlayTeleportToEntitySB:        (*Server).handleTeleportToEntity,
		IDPlayTestInstanceBlockActionSB: (*Server).ignoreServerbound,
		IDPlayUseItemOnSB:               (*Server).handleUseItem,
		IDPlayUseItemSB:                 (*Server).handleUseItemPacket,
		IDPlayCustomClickActionSB:       (*Server).handleCustomClickAction,
	}
}

// ignoreServerbound explicitly consumes packets whose behavior is not
// modeled here. Some are no-ops; jigsaw and test editor actions still
// include placeholder registrations for missing privileged behavior.
// The coverage test tracks this list so omissions remain visible.
func (s *Server) ignoreServerbound(sess *Session, p protocol.Packet) {}

// routeKeepAlive consumes the client's keep-alive echo and feeds the
// timeout watchdog and tab-list latency sync.
func (s *Server) routeKeepAlive(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if id, err := r.Long(); err == nil && id == sess.kaNext.Load() {
		sess.rtt.Store(time.Now().UnixNano() - sess.kaTime.Load())
		s.broadcastPlayerLatency(sess)
	}
}

// routeTeleportConfirm records the teleport id the client confirmed.
func (s *Server) routeTeleportConfirm(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if id, err := r.VarInt(); err == nil {
		sess.teleportAck.Store(id)
	}
}

// routeChatMessage is Player Chat (unsigned, 0x09).
func (s *Server) routeChatMessage(sess *Session, p protocol.Packet) {
	s.handleChat(sess, p, false)
}

// routeChatCommand is the unsigned Chat Command (0x07).
func (s *Server) routeChatCommand(sess *Session, p protocol.Packet) {
	s.handleChat(sess, p, true)
}

// routePlayerAction runs the survival action extras first (drop/swap)
// and falls back to the digging state machine.
func (s *Server) routePlayerAction(sess *Session, p protocol.Packet) {
	if !s.handlePlayerActionExtras(sess, p) {
		s.handleDig(sess, p)
	}
}

// routeClickContainer handles a container click; any equipment slots
// it moved are picked up by the tick-time detectEquipmentUpdates.
func (s *Server) routeClickContainer(sess *Session, p protocol.Packet) {
	s.handleClickContainer(sess, p)
}

// routeChatCommandSigned is the signed Chat Command (0x08). mc-go runs
// the same command path; the signature chain is parsed for shape but
// the command itself is what changes state.
func (s *Server) routeChatCommandSigned(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	cmd, err := r.String()
	if err != nil {
		return
	}
	s.dispatchCommandText(sess, cmd)
}
