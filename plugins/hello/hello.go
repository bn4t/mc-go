// Package hello is an example compiled-in plugin. It shows the whole
// pattern: blank-import for registration, Init for wiring, event bus
// for observation, Commands for chat commands (invocable once the
// signed-chat milestone lands).
package hello

import (
	"sync/atomic"

	"mc-go/api/event"
	"mc-go/api/plugin"
)

func init() { plugin.Register(&hello{}) }

type hello struct{ joins atomic.Int64 }

func (h *hello) Name() string { return "hello" }

func (h *hello) Init(api plugin.API) error {
	api.Commands().Register("ping", func(player string, args []string) string {
		return "pong"
	})
	ch, unsubscribe := api.Bus().SubscribeContext(api.Context(), event.TopicPlayerJoin, 16)
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-api.Context().Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				h.joins.Add(1)
			}
		}
	}()
	return nil
}
