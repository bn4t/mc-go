package frontend

import "errors"

// ErrCallbackFailed reports a contained panic in a trusted external Serve
// callback. The host logs its diagnostic; the public error omits panic values.
var ErrCallbackFailed = errors.New("frontend: Serve callback failed")
