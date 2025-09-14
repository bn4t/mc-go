package pipeline

type Chain struct {
	ic []Interceptor
}

func NewChain(interceptors []Interceptor) *Chain {
	c := &Chain{}
	if len(interceptors) > 0 {
		c.ic = append(c.ic, interceptors...)
	}
	return c
}

func (c *Chain) RunInboundRaw(ctx PktCtx, f *RawFrame) (*RawFrame, bool) {
	for _, ic := range c.ic {
		act, nf := ic.InboundRaw(ctx, f)
		switch act {
		case Drop, Deny:
			return nil, false
		case Replace:
			f = nf
		case Quarantine, Defer, Pass:
			// no-op in this minimal build
		}
	}
	return f, true
}

func (c *Chain) RunInboundDecoded(ctx PktCtx, p Packet) (Packet, bool) {
	for _, ic := range c.ic {
		act, np := ic.InboundDecoded(ctx, p)
		switch act {
		case Drop, Deny:
			return nil, false
		case Replace:
			p = np
		case Quarantine, Defer, Pass:
		}
	}
	return p, true
}

func (c *Chain) RunPreApply(ctx PktCtx, p Packet, cmd *Command) (Packet, *Command, bool) {
	for _, ic := range c.ic {
		act, np, nc := ic.PreApply(ctx, p, cmd)
		switch act {
		case Drop, Deny:
			return nil, nil, false
		case Replace:
			p, cmd = np, nc
		case Quarantine, Defer, Pass:
		}
	}
	return p, cmd, true
}

func (c *Chain) RunOutbound(ctx PktCtx, p Packet) (Packet, bool) {
	for _, ic := range c.ic {
		act, np := ic.Outbound(ctx, p)
		switch act {
		case Drop, Deny:
			return nil, false
		case Replace:
			p = np
		case Quarantine, Defer, Pass:
		}
	}
	return p, true
}
