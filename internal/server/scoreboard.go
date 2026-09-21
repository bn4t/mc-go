package server

import (
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/protocol"
)

// scoreboard holds objectives (name -> display text) and per-holder
// scores, plus the sidebar display slot and the team table.
type scoreboard struct {
	mu         sync.Mutex
	objectives map[string]string // name -> display text
	scores     map[string]map[string]int32
	sidebar    string
	teams      map[string]*team
}

// objectiveChatComponent renders an objective the way vanilla's
// scoreboard replies do: the name wrapped in square brackets with a
// show_text hover.
func objectiveChatComponent(name string) component {
	return translatable("chat.square_brackets", component{
		text: name,
		hover: map[string]any{
			"action": "show_text",
			"value":  name,
		},
	})
}

// scoreHolderComponent renders a score holder in a reply: a live
// player gets the tell-suggest + show_text name; a fake name is plain.
func scoreHolderComponent(name string, sess *Session) component {
	if sess != nil && sess.getName() == name {
		return component{
			text:      name,
			insertion: name,
			click:     "/tell " + name + " ",
			hover: map[string]any{
				"action": "show_text",
				"value":  name,
			},
		}
	}
	return literal(name)
}

// runScoreboard implements the /scoreboard command subset:
//
//	objectives add <name> [display]
//	objectives remove <name>
//	objectives setdisplay sidebar <name|->
//	players set <target> <objective> <value>
//	players get <target> <objective>
//	players reset <target> <objective>
func (s *Server) runScoreboard(player string, args []string) reply {
	if len(args) < 2 {
		return textReply("Usage: /scoreboard <objectives|players> ...")
	}
	switch args[0] {
	case "objectives":
		return s.runScoreboardObjectives(args[1:])
	case "players":
		return s.runScoreboardPlayers(player, args[1:])
	}
	return textReply("Unknown scoreboard subcommand: " + args[0])
}

func (s *Server) runScoreboardObjectives(args []string) reply {
	if len(args) == 0 {
		return textReply("Usage: /scoreboard objectives <add|remove|setdisplay> ...")
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return textReply("Usage: /scoreboard objectives add <name> [display]")
		}
		name := args[1]
		display := name
		if len(args) > 2 {
			display = strings.Join(args[2:], " ")
		}
		s.board.mu.Lock()
		if s.board.objectives == nil {
			s.board.objectives = map[string]string{}
		}
		s.board.objectives[name] = display
		s.board.mu.Unlock()
		s.broadcastObjectiveCreate(name, display)
		return componentReply(translatable("commands.scoreboard.objectives.add.success",
			objectiveChatComponent(name)))
	case "remove":
		if len(args) < 2 {
			return textReply("Usage: /scoreboard objectives remove <name>")
		}
		name := args[1]
		s.board.mu.Lock()
		delete(s.board.objectives, name)
		s.board.mu.Unlock()
		s.broadcastObjectiveRemove(name)
		return componentReply(translatable("commands.scoreboard.objectives.remove.success",
			objectiveChatComponent(name)))
	case "setdisplay":
		if len(args) < 3 || args[1] != "sidebar" {
			return textReply("Usage: /scoreboard objectives setdisplay sidebar <name|->")
		}
		name := args[2]
		if name == "-" {
			name = ""
		}
		s.board.mu.Lock()
		s.board.sidebar = name
		s.board.mu.Unlock()
		s.broadcastDisplayObjective(1, name)
		return textReply("Set sidebar to " + name)
	}
	return textReply("Unknown objectives subcommand: " + args[0])
}

func (s *Server) runScoreboardPlayers(player string, args []string) reply {
	if len(args) < 2 {
		return textReply("Usage: /scoreboard players <set|get|reset> <target> <objective> [value]")
	}
	op := args[0]
	target := args[1]
	targetSess := s.findSessionByName(target)
	if target == "@s" || target == "@p" || target == "@e" || target == "@r" {
		target = player
		targetSess = s.findSessionByName(player)
	}
	objective := ""
	if len(args) > 2 {
		objective = args[2]
	}
	s.board.mu.Lock()
	if s.board.scores == nil {
		s.board.scores = map[string]map[string]int32{}
	}
	// A write to an objective nobody created misses with
	// arguments.objective.notFound, the token past the objective name.
	objectiveKnown := func() bool {
		_, ok := s.board.objectives[objective]
		return ok
	}
	switch op {
	case "set":
		if len(args) < 4 {
			s.board.mu.Unlock()
			return textReply("Usage: /scoreboard players set <target> <objective> <value>")
		}
		if !objectiveKnown() {
			s.board.mu.Unlock()
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		value, err := strconv.Atoi(args[3])
		if err != nil {
			s.board.mu.Unlock()
			return textReply("Invalid value: " + args[3])
		}
		if s.board.scores[target] == nil {
			s.board.scores[target] = map[string]int32{}
		}
		s.board.scores[target][objective] = int32(value)
		s.board.mu.Unlock()
		s.broadcastScore(target, objective, int32(value))
		return componentReply(translatable("commands.scoreboard.players.set.success.single",
			objectiveChatComponent(objective),
			scoreHolderComponent(target, targetSess),
			nestedCompound{key: "", value: int32(value)}))
	case "get":
		value, ok := s.board.scores[target][objective]
		s.board.mu.Unlock()
		if !ok {
			return textReply(target + " has no score for " + objective)
		}
		// get.success args are [holder, score, objective] — a fake
		// holder serializes as the bare {"":name} arg, a live player as
		// the tell-suggest component.
		var holderArg any = scoreHolderComponent(target, targetSess)
		if targetSess == nil {
			holderArg = nestedCompound{key: "", value: target}
		}
		r := componentReply(translatable("commands.scoreboard.players.get.success",
			holderArg,
			nestedCompound{key: "", value: value},
			objectiveChatComponent(objective)))
		r.result, r.hasResult = int(value), true
		return r
	case "reset":
		if scores := s.board.scores[target]; scores != nil {
			if objective == "" {
				for obj := range scores {
					delete(scores, obj)
					s.broadcastScoreReset(target, &obj)
				}
			} else {
				delete(scores, objective)
				obj := objective
				s.broadcastScoreReset(target, &obj)
			}
		}
		s.board.mu.Unlock()
		return textReply("Reset " + target)
	}
	s.board.mu.Unlock()
	return textReply("Unknown players subcommand: " + op)
}

// broadcastObjectiveCreate sends Update Objectives mode 0.
func (s *Server) broadcastObjectiveCreate(name, display string) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(0) // create
	b.Bool(true)
	b.Raw(textComponentContent(display))
	b.Bool(true)
	b.VarInt(0) // integer
	b.Bool(false)
	s.broadcastToPlay(IDPlayObjectiveCB, b.Bytes())
}

// broadcastObjectiveRemove sends Update Objectives mode 1.
func (s *Server) broadcastObjectiveRemove(name string) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(1) // remove
	s.broadcastToPlay(IDPlayObjectiveCB, b.Bytes())
}

// broadcastDisplayObjective sends Display Objective.
func (s *Server) broadcastDisplayObjective(position int32, name string) {
	var b protocol.Buffer
	b.VarInt(position)
	b.String(name)
	s.broadcastToPlay(IDPlayDisplayObjective, b.Bytes())
}

// broadcastScore sends Update Score.
func (s *Server) broadcastScore(entity, objective string, value int32) {
	var b protocol.Buffer
	b.String(entity)
	b.String(objective)
	b.VarInt(value)
	b.Bool(false) // no display name override
	b.Bool(false) // no number format
	s.broadcastToPlay(IDPlayUpdateScore, b.Bytes())
}

// broadcastScoreReset sends Reset Score.
func (s *Server) broadcastScoreReset(entity string, objective *string) {
	var b protocol.Buffer
	b.String(entity)
	if objective == nil {
		b.Bool(false)
	} else {
		b.Bool(true)
		b.String(*objective)
	}
	s.broadcastToPlay(IDPlayResetScore, b.Bytes())
}

// broadcastToPlay sends a body to every play-stage session.
func (s *Server) broadcastToPlay(packetID int32, body []byte) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(packetID, body)
		return true
	})
}
