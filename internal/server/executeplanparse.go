package server

import (
	"mc-go/internal/world"
	"strconv"
	"strings"
)

// Execute plans validate their supported syntax before evaluating sources.
// Other command grammars keep the existing dispatcher until they have a
// side-effect-free parser; validation never speculatively runs a command.
type executePlanStep struct {
	kind   string
	args   []string
	token  int
	negate bool
}
type executePlan struct {
	steps     []executePlanStep
	terminal  *executePlanStep
	leaf      []string
	leafToken int
}

func (s *Server) compileExecutePlan(player string, args []string, base int) (*executePlan, *reply, bool) {
	p := new(executePlan)
	for i := 0; i < len(args); {
		step := executePlanStep{kind: args[i], token: base + i}
		need := 0
		switch args[i] {
		case "run":
			if i+1 >= len(args) {
				r := errTokenEnd("command.unknown.command", base+i)
				return nil, &r, true
			}
			sub := args[i+1:]
			if sub[0] == "execute" {
				next, r, ok := s.compileExecutePlan(player, sub[1:], base+i+2)
				if !ok || r != nil {
					return nil, r, ok
				}
				p.steps = append(p.steps, next.steps...)
				p.terminal, p.leaf, p.leafToken = next.terminal, next.leaf, next.leafToken
				return p, nil, true
			}
			r, ok := s.validateExecutePlanLeaf(player, sub, base+i+1)
			if !ok || r != nil {
				return nil, r, ok
			}
			p.leaf, p.leafToken = sub, base+i+1
			return p, nil, true
		case "as", "at":
			need = 2
		case "in":
			need = 2
		case "positioned":
			if i+1 < len(args) && (args[i+1] == "as" || args[i+1] == "over") {
				return nil, nil, false
			}
			need = 4
		case "rotated":
			if i+1 < len(args) && args[i+1] == "as" {
				return nil, nil, false
			}
			need = 3
		case "align", "anchored":
			need = 2
		case "store":
			if i+2 >= len(args) {
				r := errTokenEnd("command.unknown.command", base+len(args)-1)
				return nil, &r, true
			}
			if args[i+2] != "score" {
				return nil, nil, false
			}
			need = 5
		case "if", "unless":
			if i+1 >= len(args) {
				r := errTokenEnd("command.unknown.command", base+i)
				return nil, &r, true
			}
			step.negate = args[i] == "unless"
			step.kind = "condition"
			switch args[i+1] {
			case "entity":
				need = 3
			case "score":
				if i+4 >= len(args) {
					r := errTokenEnd("command.unknown.command", base+len(args)-1)
					return nil, &r, true
				}
				need = 7
				if args[i+4] == "matches" {
					need = 6
				}
			default:
				return nil, nil, false
			}
		default:
			return nil, nil, false
		}
		if len(args)-i < need {
			r := errTokenEnd("command.unknown.command", base+len(args)-1)
			return nil, &r, true
		}
		step.args = args[i+1 : i+need]
		if r, ok := s.validateExecutePlanStep(step); !ok || r != nil {
			return nil, r, ok
		}
		i += need
		if step.kind == "condition" && i == len(args) {
			p.terminal = &step
			return p, nil, true
		}
		p.steps = append(p.steps, step)
	}
	return nil, nil, false
}

func executePlanSelector(token string, at int, single bool) *reply {
	if !strings.HasPrefix(token, "@") {
		return nil
	}
	spec, r := parseSelector(token, at)
	if r != nil {
		return r
	}
	if single && spec.singleMaxRes() > 1 {
		r := errTokenEnd("argument.entity.toomany", at)
		return &r
	}
	return nil
}
func (s *Server) validateExecutePlanStep(step executePlanStep) (*reply, bool) {
	at, a := step.token+1, step.args
	switch step.kind {
	case "as", "at":
		return executePlanSelector(a[0], at, false), true
	case "in":
		if s.dimensionByName(a[0]) == nil {
			return nil, false
		}
	case "positioned", "rotated":
		for i, v := range a {
			if _, e := parseDouble(v, 0, step.kind == "positioned" && i != 1); e != nil {
				r := errToken("parsing.double.expected", at+i)
				return &r, true
			}
		}
	case "align":
		for _, v := range a[0] {
			if v != 'x' && v != 'y' && v != 'z' {
				r := errToken("command.unknown.argument", at)
				return &r, true
			}
		}
	case "anchored":
		if a[0] != "eyes" && a[0] != "feet" {
			r := errToken("argument.anchor.invalid", at, a[0])
			return &r, true
		}
	case "store":
		if a[0] != "result" && a[0] != "success" {
			r := errToken("command.unknown.argument", at)
			return &r, true
		}
		if a[2] == "*" {
			return nil, false
		}
		if strings.HasPrefix(a[2], "@") {
			spec, r := parseSelector(a[2], at+2)
			if r != nil {
				return r, true
			}
			if spec.singleMaxRes() > 1 {
				return nil, false
			}
		}
		s.board.mu.Lock()
		_, exists := s.board.objectives[a[3]]
		s.board.mu.Unlock()
		if !exists {
			return nil, false
		}
	case "condition":
		if a[0] == "entity" {
			return executePlanSelector(a[1], at+1, false), true
		}
		if r := executePlanSelector(a[1], at+1, true); r != nil {
			return r, true
		}
		if a[3] == "matches" {
			if _, _, ok := parseIntRange(a[4]); !ok {
				r := errToken("command.unknown.argument", at+4)
				return &r, true
			}
		} else {
			switch a[3] {
			case "=", "<", "<=", ">", ">=":
			default:
				r := errToken("command.unknown.argument", at+3)
				return &r, true
			}
			if r := executePlanSelector(a[4], at+4, true); r != nil {
				return r, true
			}
		}
	}
	return nil, true
}

func (s *Server) validateExecutePlanLeaf(player string, sub []string, at int) (*reply, bool) {
	if !s.commandExists(sub[0]) || s.permissionLevel(player) < commandLevel(sub[0]) {
		r := errToken("command.unknown.argument", at)
		return &r, true
	}
	missing := func() *reply { r := errTokenEnd("command.unknown.command", at+len(sub)-1); return &r }
	extra := func(index int) *reply { r := errToken("command.unknown.argument", at+index); return &r }
	switch sub[0] {
	case "function":
		if len(sub) < 2 {
			return missing(), true
		}
		if len(sub) > 2 {
			return nil, false
		}
		id := strings.TrimPrefix(sub[1], "#")
		if _, ok := jigsawIdentifier(id); !ok {
			r := errToken("argument.id.invalid", at+1)
			return &r, true
		}
	case "scoreboard":
		if len(sub) < 3 {
			return nil, false
		}
		if sub[1] != "players" {
			return nil, false
		}
		need := 6
		single := false
		switch sub[2] {
		case "get":
			need = 5
			single = true
		case "set", "add", "remove":
		default:
			return nil, false
		}
		if len(sub) < need {
			return missing(), true
		}
		if len(sub) > need {
			return extra(need), true
		}
		if r := executePlanSelector(sub[3], at+3, single); r != nil {
			return r, true
		}
		if need == 6 {
			n, r := sbParseInt(sub[5], at+5)
			if r != nil {
				return r, true
			}
			if sub[2] != "set" && n < 0 {
				r := errTokenEnd("argument.integer.low", at+5, int32(0), int32(n))
				return &r, true
			}
		}
	case "clear":
		if len(sub) > 1 && strings.HasPrefix(sub[1], "@e") {
			return nil, false
		}
		if len(sub) > 4 {
			return extra(4), true
		}
		if len(sub) > 1 {
			if r := executePlanSelector(sub[1], at+1, false); r != nil {
				return r, true
			}
		}
		if len(sub) > 2 {
			name := strings.TrimPrefix(sub[2], "minecraft:")
			if _, ok := world.ItemIDs[name]; !ok {
				r := errTokenEnd("argument.item.id.invalid", at+2, sub[2])
				return &r, true
			}
		}
		if len(sub) > 3 {
			n, e := strconv.ParseInt(sub[3], 10, 32)
			if e != nil {
				r := errToken("parsing.int.expected", at+3)
				return &r, true
			}
			if n < 0 {
				r := errToken("argument.integer.low", at+3, int32(0), int32(n))
				return &r, true
			}
		}
	default:
		return nil, false
	}
	return nil, true
}
