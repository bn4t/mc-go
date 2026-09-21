package server

import (
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/protocol"
)

// scoreboard holds objectives, per-entity scores, and the sidebar
// display objective.
type scoreboard struct {
	mu         sync.Mutex
	objectives map[string]string // name -> display text
	scores     map[string]map[string]int32
	sidebar    string
	teams      map[string]*team
}

// runScoreboard implements the /scoreboard command subset:
//
//	objectives add <name> [display]
//	objectives remove <name>
//	objectives setdisplay sidebar <name|->
//	players set <target> <objective> <value>
//	players get <target> <objective>
//	players reset <target> <objective>
func (s *Server) runScoreboard(player string, args []string) string {
	if len(args) < 2 {
		return "Usage: /scoreboard <objectives|players> ..."
	}
	switch args[0] {
	case "objectives":
		return s.runScoreboardObjectives(args[1:])
	case "players":
		return s.runScoreboardPlayers(player, args[1:])
	}
	return "Unknown scoreboard subcommand: " + args[0]
}

func (s *Server) runScoreboardObjectives(args []string) string {
	if len(args) == 0 {
		return "Usage: /scoreboard objectives <add|remove|setdisplay> ..."
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return "Usage: /scoreboard objectives add <name> [display]"
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
		return "Created objective " + name
	case "remove":
		if len(args) < 2 {
			return "Usage: /scoreboard objectives remove <name>"
		}
		name := args[1]
		s.board.mu.Lock()
		delete(s.board.objectives, name)
		s.board.mu.Unlock()
		s.broadcastObjectiveRemove(name)
		return "Removed objective " + name
	case "setdisplay":
		if len(args) < 3 || args[1] != "sidebar" {
			return "Usage: /scoreboard objectives setdisplay sidebar <name|->"
		}
		name := args[2]
		if name == "-" {
			name = ""
		}
		s.board.mu.Lock()
		s.board.sidebar = name
		s.board.mu.Unlock()
		s.broadcastDisplayObjective(1, name)
		return "Set sidebar to " + name
	}
	return "Unknown objectives subcommand: " + args[0]
}

func (s *Server) runScoreboardPlayers(player string, args []string) string {
	if len(args) < 2 {
		return "Usage: /scoreboard players <set|get|reset> <target> <objective> [value]"
	}
	op := args[0]
	target := args[1]
	if target == "@s" || target == "@p" {
		target = player
	}
	objective := ""
	if len(args) > 2 {
		objective = args[2]
	}
	s.board.mu.Lock()
	if s.board.scores == nil {
		s.board.scores = map[string]map[string]int32{}
	}
	switch op {
	case "set":
		if len(args) < 4 {
			s.board.mu.Unlock()
			return "Usage: /scoreboard players set <target> <objective> <value>"
		}
		value, err := strconv.Atoi(args[3])
		if err != nil {
			s.board.mu.Unlock()
			return "Invalid value: " + args[3]
		}
		if s.board.scores[target] == nil {
			s.board.scores[target] = map[string]int32{}
		}
		s.board.scores[target][objective] = int32(value)
		s.board.mu.Unlock()
		s.broadcastScore(target, objective, int32(value))
		return "Set " + target + " " + objective + " to " + args[3]
	case "get":
		value, ok := s.board.scores[target][objective]
		s.board.mu.Unlock()
		if !ok {
			return target + " has no score for " + objective
		}
		return target + " has " + strconv.Itoa(int(value))
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
		return "Reset " + target
	}
	s.board.mu.Unlock()
	return "Unknown players subcommand: " + op
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
