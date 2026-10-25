package server

import (
	"context"
	"log"
	"net"

	"mc-go/api/frontend"
)

// serveFrontend contains only the trusted external Serve callback boundary.
// Host attachment defers still own cancellation and player retirement. A
// callback must cooperate with cancellation; other goroutine panics and blocked
// callbacks cannot be contained by this wrapper.
func serveFrontend(ctx context.Context, core frontend.Core, ln net.Listener, fe frontend.Frontend) (err error) {
	defer func() {
		if value := recover(); value != nil {
			log.Printf("frontend %T Serve failed: %v", fe, value)
			err = frontend.ErrCallbackFailed
		}
	}()
	return fe.Serve(ctx, core, ln)
}
