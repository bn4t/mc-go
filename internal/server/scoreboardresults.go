package server

// Scoreboard commands return a signed integer even for successful zero results.
// A runtime failure delivers zero; syntax failures deliver no callback.
func scoreboardCommandResult(r reply, value int32) reply {
	if r.fail == nil && !r.hasResult && (r.outcome == commandSucceeded || r.outcome == commandFailed) {
		if r.outcome == commandFailed {
			value = 0
		}
		r.result, r.hasResult = int(value), true
	}
	return r
}
