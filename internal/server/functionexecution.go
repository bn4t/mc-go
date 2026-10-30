package server

import "strings"

// Function work is simulation-owned. Calls expand onto an explicit stack so a
// recursive resource consumes the native command budget without Go recursion.
type functionAction struct {
	actor  string
	source *commandSourceFrame
	lines  []string
	names  []string
	index  int
	call   bool
	cost   int
}

type functionExecution struct {
	remaining                     int
	head, pendingCost, stagedCost int
	stack, staged                 []functionAction
	inLine                        bool
	inExecuteLeaf                 bool
	directExecute                 bool
}

func (s *Server) functionSource(actor string) *commandSourceFrame {
	var source commandSourceFrame
	if parent := s.commandFrame(actor); parent != nil {
		source = *parent
	} else {
		source.dim = s.dimOf(actor)
		source.pos = s.execOrigin(actor)
		source.display = s.commandDisplayName(actor)
		if sess := s.findSessionByName(actor); sess != nil {
			_, _, _, yaw, pitch, _ := sess.Pos()
			source.rotation = [2]float32{yaw, pitch}
			if sess.execPos != nil {
				source.pos = *sess.execPos
			}
			source.executor = sess.execEnt
			if source.executor == nil {
				source.executor = &dataEntity{sess: sess}
			} else {
				source.display = source.executor.nameComp()
			}
		}
	}
	source.actor = actor
	source.level = functionPermLevel
	source.output, source.admin = nil, false
	source.results = &commandExecutionResults{}
	return &source
}

// A direct body /function line already paid its call unit. An execute leaf
// and each additional tag member need their own call unit instead.
func (s *Server) executeFunctionNames(actor string, names []string, command bool) {
	if len(names) == 0 {
		return
	}
	q := s.functionExecution
	firstCost := 1
	if command && q != nil && q.inLine && !q.inExecuteLeaf {
		firstCost = 0
	}
	if q != nil && firstCost > q.remaining-q.stagedCost {
		return
	}
	actions := []functionAction{{actor: actor, source: s.functionSource(actor), names: names, call: true, cost: firstCost}}
	if q != nil {
		q.staged = append(q.staged, actions...)
		q.stagedCost += firstCost
		return
	}
	q = &functionExecution{remaining: s.gameRuleInt("minecraft:max_command_sequence_length")}
	s.functionExecution = q
	defer func() { s.functionExecution = nil }()
	q.push(actions)
	s.drainFunctionExecution(q)
}

func (s *Server) drainFunctionExecution(q *functionExecution) {
	for len(q.stack) > q.head {
		last := len(q.stack) - 1
		action := q.stack[last]
		q.stack[last] = functionAction{}
		q.stack = q.stack[:last]
		q.pendingCost -= action.cost
		if action.cost > q.remaining {
			break
		}
		q.remaining -= action.cost
		if action.call {
			name := action.names[action.index]
			if action.index+1 < len(action.names) {
				next := action
				next.index++
				next.cost = 1
				q.appendAction(next)
			}
			if lines := s.functions[name]; len(lines) != 0 {
				member := *action.source
				member.results = &commandExecutionResults{}
				q.appendAction(functionAction{actor: action.actor, source: &member, lines: lines, cost: 1})
			}
			q.trimBudget()
			continue
		}
		line := action.lines[action.index]
		if action.index+1 < len(action.lines) {
			next := action
			next.index++
			q.appendAction(next)
		}
		fields := splitCommandArgs(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		q.inLine = true
		q.directExecute = functionDirectExecute(fields)
		s.runFunctionLine(action, fields)
		q.inLine = false
		q.push(q.staged)
		q.staged = nil
		q.stagedCost = 0
	}
}

func (q *functionExecution) push(actions []functionAction) {
	end, cost := len(actions), 0
	for i, action := range actions {
		if cost+action.cost > q.remaining {
			end = i
			break
		}
		cost += action.cost
	}
	for i := end - 1; i >= 0; i-- {
		q.appendAction(actions[i])
	}
	q.trimBudget()
}

func (q *functionExecution) appendAction(action functionAction) {
	if len(q.stack) == cap(q.stack) && q.head != 0 {
		length := len(q.stack) - q.head
		copy(q.stack, q.stack[q.head:])
		clear(q.stack[length:])
		q.stack = q.stack[:length]
		q.head = 0
	}
	q.stack = append(q.stack, action)
	q.pendingCost += action.cost
}

// Every pending action has a minimum charge. Work below a prefix that
// already exhausts the remaining budget cannot run, including after a
// recursive fork inserts more work. A moving head avoids repeated copies.
func (q *functionExecution) trimBudget() {
	for q.pendingCost > q.remaining && q.head < len(q.stack) {
		q.pendingCost -= q.stack[q.head].cost
		q.stack[q.head] = functionAction{}
		q.head++
	}
	if q.head == len(q.stack) {
		q.stack = q.stack[:0]
		q.head = 0
	}
}

// Execute modifiers consume a body action, and their resulting commands
// consume leaf actions. Function leaves enqueue their calls so a child's body
// runs before the next fork's call; ordinary leaves finish synchronously.
func (s *Server) functionExecuteLeaf(actor string, sub []string, run func() reply) reply {
	q := s.functionExecution
	if q == nil || !q.inLine {
		return run()
	}
	// Pure `execute run` redirects collapse to the body's command action.
	// Nested execute syntax is still the same compiled command chain.
	if q.directExecute || len(sub) != 0 && sub[0] == "execute" {
		return run()
	}
	if s.functionLeafQueues(sub) {
		previous := q.inExecuteLeaf
		q.inExecuteLeaf = true
		defer func() { q.inExecuteLeaf = previous }()
		return run()
	}
	if q.remaining <= 0 {
		return reply{}
	}
	q.remaining--
	return run()
}

func (s *Server) functionLeafQueues(sub []string) bool {
	if len(sub) != 2 || sub[0] != "function" {
		return false
	}
	if strings.HasPrefix(sub[1], "#") {
		id, ok := jigsawIdentifier(sub[1][1:])
		return ok && len(s.functionTags[id]) != 0
	}
	_, ok := s.functions[normalizeID(sub[1])]
	return ok
}

// Existing player helpers also consult these session-scoped fields. Bind them
// only for the action; actual position/dimension changes remain persistent.
func (s *Server) runFunctionLine(action functionAction, fields []string) reply {
	if sess := s.findSessionByName(action.actor); sess != nil {
		previousEntity, previousPosition := sess.execEnt, sess.execPos
		sess.execEnt, sess.execPos = action.source.executor, &action.source.pos
		defer func() { sess.execEnt, sess.execPos = previousEntity, previousPosition }()
	}
	return s.withCommandFrame(action.source, func() reply {
		r, _ := s.runCommandAt(action.actor, functionPermLevel, fields[0], fields[1:])
		return r
	})
}

func functionDirectExecute(fields []string) bool {
	if len(fields) < 2 || fields[0] != "execute" || fields[1] != "run" {
		return false
	}
	for len(fields) >= 2 && fields[0] == "execute" && fields[1] == "run" {
		fields = fields[2:]
	}
	return len(fields) != 0 && fields[0] != "execute"
}
