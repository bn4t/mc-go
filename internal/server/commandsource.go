package server

// commandSourceFrame is a simulation-owned command source snapshot. Neutral
// actor keys are never admitted as sessions or used as display names.
// Child execute frames retain permissions while rebinding spatial context
// and, for execute-as, the optional entity and display name.
type commandSourceFrame struct {
	// Empty actor retains the original neutral-source binding.
	actor    string
	dim      *dimension
	pos      [3]float64
	rotation [2]float32
	executor *dataEntity
	display  component
	level    int
	output   func(component)
	admin    bool
	results  *commandExecutionResults
	forked   bool
}

type commandExecutionResults struct {
	completions int
	successes   int
	onSuccess   func()
}

const neutralCommandActor = "\x00command-source"

func (s *Server) commandFrame(actor string) *commandSourceFrame {
	frame := s.activeCommandSource
	if frame == nil || frame.actor != actor && !(frame.actor == "" && actor == neutralCommandActor) {
		return nil
	}
	return frame
}

func (s *Server) withCommandFrame(frame *commandSourceFrame, run func() reply) reply {
	if frame != nil && frame.results == nil {
		frame.results = &commandExecutionResults{}
	}
	previous := s.activeCommandSource
	s.activeCommandSource = frame
	defer func() { s.activeCommandSource = previous }()
	return run()
}

func executeSourceFrame(parent *commandSourceFrame, ctx *executeContext) *commandSourceFrame {
	if parent.results == nil {
		parent.results = &commandExecutionResults{}
	}
	child := *parent
	child.dim, child.pos, child.executor = ctx.dim, ctx.pos, ctx.ent
	child.rotation = ctx.rotation
	child.forked = ctx.forked
	if ctx.ent != nil && ctx.ent != parent.executor {
		child.display = ctx.ent.nameComp()
	}
	return &child
}

func (s *Server) commandDisplayName(actor string) component {
	if frame := s.commandFrame(actor); frame != nil {
		return frame.display
	}
	if sess := s.findSessionByName(actor); sess != nil {
		return playerChatName(sess)
	}
	return literal(actor)
}

// finishSourceCommand counts executed leaves rather than their numeric
// results. Nested execute dispatches already delivered their leaf feedback.
func (s *Server) finishSourceCommand(source *commandSourceFrame, name string, before int, r reply) reply {
	if source.results == nil {
		source.results = &commandExecutionResults{}
	}
	if name == "execute" && source.results.completions != before {
		return r
	}
	failure := r.fail != nil || r.outcome == commandFailed || r.comp.commandFailure
	success := !failure && (r.outcome == commandSucceeded || r.hasResult)
	source.results.completions++
	if success {
		source.results.successes++
	}
	emit := func(c component, echo bool) {
		if failure && source.forked && r.fail == nil {
			return
		}
		if source.output != nil && (failure || s.gameRuleBool("minecraft:send_command_feedback")) {
			source.output(c)
		}
		if echo && source.admin && !failure && s.gameRuleBool("minecraft:command_block_output") {
			s.sendOpsEchoNamed(nil, source.display, c)
		}
	}
	if r.styled {
		emit(r.comp, r.opsEcho)
	} else if r.text != "" {
		emit(literal(r.text), false)
	}
	for i, extra := range r.extra {
		emit(extra, i < len(r.extraEcho) && r.extraEcho[i])
	}
	// Commands send feedback before their result callback. Feedback can
	// itself update/reload a command minecart's synced base fields.
	if success && source.results.onSuccess != nil {
		source.results.onSuccess()
	}
	return r
}
