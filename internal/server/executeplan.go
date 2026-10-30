package server

import "math"

type executePlanStore struct {
	previous          *executePlanStore
	holder, objective string
	success           bool
}
type executePlanContext struct {
	context executeContext
	stores  *executePlanStore
}

func (s *Server) executePlanBudget(run func() reply) reply {
	if s.functionExecution != nil {
		return run()
	}
	remaining := s.gameRuleInt("minecraft:max_command_sequence_length") - 1
	if remaining < 0 {
		remaining = 0
	}
	q := &functionExecution{remaining: remaining, inLine: true}
	s.functionExecution = q
	defer func() { s.functionExecution = nil }()
	result := run()
	q.inLine = false
	q.push(q.staged)
	q.staged = nil
	q.stagedCost = 0
	s.drainFunctionExecution(q)
	return result
}
func (s *Server) executePlanSource(player string) *commandSourceFrame {
	if source := s.commandFrame(player); source != nil {
		return source
	}
	source := s.functionSource(player)
	source.level = s.permissionLevel(player)
	return source
}
func (s *Server) runExecutePlan(player string, plan *executePlan) reply {
	return s.executePlanBudget(func() reply {
		source := s.executePlanSource(player)
		contexts := []executePlanContext{{context: executeContext{dim: source.dim, pos: source.pos, rotation: source.rotation, anchorEyes: source.anchorEyes, ent: source.executor, alive: true, forked: source.forked}}}
		q := s.functionExecution
		for index, step := range plan.steps {
			if len(contexts) == 0 {
				return reply{}
			}
			if index > 0 {
				if q.remaining <= q.stagedCost {
					return reply{}
				}
				q.remaining--
			}
			next := make([]executePlanContext, 0, len(contexts))
			forked := step.kind == "as" || step.kind == "at" || step.kind == "condition"
			limit := s.gameRuleInt("minecraft:max_command_forks")
			for _, input := range contexts {
				var produced []executePlanContext
				var failure *reply
				var limitExceeded bool
				frame := executeSourceFrame(source, &input.context)
				s.withCommandFrame(frame, func() reply {
					produced, failure, limitExceeded = s.applyExecutePlanStep(player, input, step, limit-len(next))
					return reply{}
				})
				if limitExceeded {
					return executePlanForkLimitReply(input.context.forked || forked, limit)
				}
				if failure != nil {
					if input.context.forked && failure.fail == nil {
						continue
					}
					return *failure
				}
				for _, value := range produced {
					next = append(next, value)
					if len(next) >= limit {
						return executePlanForkLimitReply(input.context.forked || forked, limit)
					}
				}
			}
			contexts = next
		}
		var result reply
		previousDirect := q.directExecute
		q.directExecute = len(plan.steps) == 0
		defer func() { q.directExecute = previousDirect }()
		for _, input := range contexts {
			if len(plan.steps) > 0 && q.remaining <= q.stagedCost {
				break
			}
			frame := executeSourceFrame(source, &input.context)
			r := s.withCommandFrame(frame, func() reply {
				if plan.terminal != nil {
					return s.functionExecuteLeaf(player, nil, func() reply {
						next, err := s.applyExecutePlanCondition(player, input, *plan.terminal, false)
						if err != nil {
							return *err
						}
						r := executePlanConditionalReply(&next.context)
						before := frame.results.completions
						return s.finishSourceCommand(frame, "conditional", before, r)
					})
				}
				return s.execRun(&input.context, player, plan.leaf, plan.leafToken)
			})
			s.applyExecutePlanStores(input.stores, r)
			if input.context.forked {
				mergeReplies(&result, r)
			} else {
				result = r
			}
		}
		return result
	})
}
func (s *Server) applyExecutePlanStep(player string, input executePlanContext, step executePlanStep, available int) ([]executePlanContext, *reply, bool) {
	a := step.args
	ctx := &input.context
	switch step.kind {
	case "as", "at":
		var selected []*dataEntity
		if len(a[0]) > 0 && a[0][0] == '@' {
			var err *reply
			selected, err = s.selectDataEntities(s.findSessionByName(player), a[0], step.token+1, false, ctx.dim)
			if err != nil {
				return nil, err, false
			}
		} else if target := s.findSessionByName(a[0]); target != nil {
			selected = []*dataEntity{{sess: target}}
		}
		if len(selected) >= available {
			return nil, nil, true
		}
		out := make([]executePlanContext, 0, len(selected))
		for _, entity := range selected {
			next := input
			next.context.forked = true
			if step.kind == "as" {
				next.context.ent = entity
			} else {
				next.context.pos = entity.position()
				next.context.dim = entity.dimOf(s)
				next.context.rotation = entityCommandRotation(entity)
			}
			out = append(out, next)
		}
		return out, nil, false
	case "condition":
		next, err := s.applyExecutePlanCondition(player, input, step, true)
		if err != nil {
			return nil, err, false
		}
		if !next.context.alive {
			return nil, nil, false
		}
		return []executePlanContext{next}, nil, false
	case "in":
		ctx.dim = s.dimensionByName(a[0])
	case "positioned":
		ctx.anchorEyes = false
		for i, token := range a {
			ctx.pos[i], _ = parseDouble(token, ctx.pos[i], i != 1)
		}
	case "align":
		for _, axis := range a[0] {
			index := 0
			if axis == 'y' {
				index = 1
			} else if axis == 'z' {
				index = 2
			}
			ctx.pos[index] = math.Floor(ctx.pos[index])
		}
	case "store":
		holder := s.scoreHolderName(player, a[2])
		if holder == "" {
			return nil, nil, false
		}
		input.stores = &executePlanStore{previous: input.stores, holder: holder, objective: a[3], success: a[0] == "success"}
	case "rotated":
		for i, token := range a {
			v, _ := parseDouble(token, float64(ctx.rotation[i]), false)
			ctx.rotation[i] = float32(v)
		}
	case "anchored":
		ctx.anchorEyes = a[0] == "eyes"
	}
	return []executePlanContext{input}, nil, false
}
func (s *Server) applyExecutePlanCondition(player string, input executePlanContext, step executePlanStep, modifier bool) (executePlanContext, *reply) {
	ctx := &input.context
	ctx.condErr = nil
	_, pass, count, counted, err := s.executeCondition(ctx, player, step.args, int(math.Floor(ctx.pos[0])), int(math.Floor(ctx.pos[1])), int(math.Floor(ctx.pos[2])), step.token+1)
	if err != nil {
		return input, err
	}
	if ctx.condErr != nil {
		ctx.hasCond = true
		ctx.alive = false
		if modifier {
			ctx.forked = true
		}
		return input, nil
	}
	if step.negate {
		pass = !pass
	}
	ctx.hasCond = true
	ctx.entityCount, ctx.entityCountd = count, counted
	ctx.alive = pass
	if modifier {
		ctx.forked = true
	}
	return input, nil
}
func executePlanConditionalReply(ctx *executeContext) reply {
	if !ctx.alive {
		if ctx.condErr != nil {
			return *ctx.condErr
		}
		r := componentReply(commandError("commands.execute.conditional.fail"))
		r.result, r.hasResult = 0, true
		return r
	}
	var r reply
	if ctx.entityCountd {
		r = componentReply(translatable("commands.execute.conditional.pass_count", int32(ctx.entityCount)))
		r.result = ctx.entityCount
	} else {
		r = componentReply(translatable("commands.execute.conditional.pass"))
		r.result = 1
	}
	r.hasResult = true
	return r
}
func (s *Server) applyExecutePlanStores(last *executePlanStore, r reply) {
	if last == nil || r.fail != nil {
		return
	}
	failure := r.outcome == commandFailed || r.comp.commandFailure
	known := failure || r.outcome == commandSucceeded || r.hasResult
	if !known {
		return
	}
	var stores []*executePlanStore
	for store := last; store != nil; store = store.previous {
		stores = append(stores, store)
	}
	for index := len(stores) - 1; index >= 0; index-- {
		store := stores[index]
		if !store.success && !r.hasResult && !failure {
			continue
		}
		value := int32(r.result)
		if failure {
			value = 0
		}
		if store.success {
			value = 0
			if !failure {
				value = 1
			}
		}
		s.board.mu.Lock()
		s.setScoreLocked(store.holder, store.objective, value)
		s.board.mu.Unlock()
		s.broadcastScore(store.holder, store.objective, value)
	}
}

func executePlanForkLimitReply(forked bool, limit int) reply {
	if forked {
		return reply{}
	}
	return componentReply(commandError("command.forkLimit", int32(limit)))
}
