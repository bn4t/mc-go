package pipeline

// Packet is the typed client/server packet contract used in the pipeline.
// Concrete packet structs live under internal/packets per protocol version.
type Packet interface {
	ID() int32
}

type Command struct {
	// Placeholder for domain commands to apply on the world thread.
	Kind string
}

type Interceptor interface {
	// S0: inspect/replace raw body (e.g., size/sig checks)
	InboundRaw(ctx PktCtx, f *RawFrame) (Action, *RawFrame)

	// S1: inspect/replace decoded packet (lightweight checks)
	InboundDecoded(ctx PktCtx, p Packet) (Action, Packet)

	// S2: run on world thread before applying commands (heavy checks OK)
	PreApply(ctx PktCtx, p Packet, cmd *Command) (Action, Packet, *Command)

	// S3: observe/modify outbound packets (spoof/bait)
	Outbound(ctx PktCtx, p Packet) (Action, Packet)
}

// NopInterceptor provides no-op defaults; embed to implement only some methods.
type NopInterceptor struct{}

func (NopInterceptor) InboundRaw(ctx PktCtx, f *RawFrame) (Action, *RawFrame) { return Pass, f }
func (NopInterceptor) InboundDecoded(ctx PktCtx, p Packet) (Action, Packet)   { return Pass, p }
func (NopInterceptor) PreApply(ctx PktCtx, p Packet, cmd *Command) (Action, Packet, *Command) {
	return Pass, p, cmd
}
func (NopInterceptor) Outbound(ctx PktCtx, p Packet) (Action, Packet) { return Pass, p }
