package server

import (
	"strings"
	"time"
)

// performCommandBase is BaseCommandBlock's common execution state machine.
// The block and minecart supply their distinct spatial/entity source and
// onUpdated callback; successful leaves count even when their result is zero.
func (s *Server) performCommandBase(currentFields func() map[string]any, frame commandSourceFrame, updated func()) bool {
	now := s.TickCount()
	if commandLong(currentFields()["LastExecution"], -1) == now {
		return false
	}
	command, _ := currentFields()["Command"].(string)
	if strings.EqualFold(command, "Searge") {
		currentFields()["SuccessCount"] = int32(1)
		currentFields()["LastOutput"] = "#itzlipofutzli"
		return true
	}
	currentFields()["SuccessCount"] = int32(0)
	if s.gameRuleBool("minecraft:command_blocks_work") && command != "" {
		delete(currentFields(), "LastOutput")
		results := &commandExecutionResults{onSuccess: func() {
			fields := currentFields()
			fields["SuccessCount"] = int32(nbtNumberOr(fields["SuccessCount"], 0)) + 1
		}}
		frame.results = results
		frame.output = nil
		frame.admin = false
		if nbtTruth(currentFields()["TrackOutput"]) {
			frame.admin = true
			frame.output = func(message component) {
				output := component{text: "[" + time.Now().Format("15:04:05") + "] ", extra: []component{message}}
				currentFields()["LastOutput"] = output.nested()
				if updated != nil {
					updated()
				}
			}
		}
		s.withCommandFrame(&frame, func() reply {
			words := splitCommandArgs(strings.TrimPrefix(command, "/"))
			if len(words) != 0 {
				if r, known := s.runCommand(neutralCommandActor, words[0], words[1:]); known {
					return r
				}
			}
			r := reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
			return s.finishSourceCommand(&frame, "", results.completions, r)
		})
	}
	if nbtTruth(currentFields()["UpdateLastExecution"]) {
		currentFields()["LastExecution"] = now
	} else {
		delete(currentFields(), "LastExecution")
	}
	return true
}
