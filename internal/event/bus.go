// Package event preserves internal imports of the public observation API.
package event

import public "mc-go/api/event"

type Bus = public.Bus
type Event = public.Event
type PlayerMove = public.PlayerMove
type PlayerTeleport = public.PlayerTeleport
type PlayerJoined = public.PlayerJoined
type PlayerLeft = public.PlayerLeft
type Block = public.Block
type BlockChange = public.BlockChange
type ChatMessage = public.ChatMessage

var New = public.New

const (
	TopicPlayerLogin    = public.TopicPlayerLogin
	TopicPlayerJoin     = public.TopicPlayerJoin
	TopicPlayerLeave    = public.TopicPlayerLeave
	TopicPlayerJoined   = public.TopicPlayerJoined
	TopicPlayerLeft     = public.TopicPlayerLeft
	TopicPlayerMove     = public.TopicPlayerMove
	TopicPlayerTeleport = public.TopicPlayerTeleport
	TopicTick           = public.TopicTick
	TopicChat           = public.TopicChat
	TopicBlockChange    = public.TopicBlockChange
	TopicViolation      = public.TopicViolation
)
