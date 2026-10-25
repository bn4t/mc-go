// Package plugin preserves internal imports of the public plugin API.
package plugin

import public "mc-go/api/plugin"

type API = public.API
type LifecycleEnforcement = public.LifecycleEnforcement
type PlayerControl = public.PlayerControl
type LifecyclePlayerControl = public.LifecyclePlayerControl
type PlayerState = public.PlayerState
type Violation = public.Violation
type Info = public.Info
type Plugin = public.Plugin
type Metadata = public.Metadata
type Described = public.Described
type Closer = public.Closer
type Manager = public.Manager
type Hooks = public.Hooks
type Move = public.Move
type Position = public.Position
type MoveHandler = public.MoveHandler
type AdmissionHandler = public.AdmissionHandler
type AdmissionDecision = public.AdmissionDecision
type CallbackError = public.CallbackError

const APIVersion = public.APIVersion

var Register = public.Register
var All = public.All
var InitAll = public.InitAll
var NewManager = public.NewManager
var NewHooks = public.NewHooks
