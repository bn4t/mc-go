package server

// A leaf command describes token cursors relative to its own command name.
// Execute redirects keep that leaf inside the original input for diagnostics.
func rebaseExecuteLeaf(r reply, commandToken int) reply {
	if r.fail != nil && !r.fail.hasAbs {
		mark := *r.fail
		mark.token += commandToken
		r.fail = &mark
	}
	return r
}
