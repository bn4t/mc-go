package frontend

import (
	"context"

	"mc-go/api/worldview"
)

// Blocks is an optional attachment-scoped capability. Type-assert the Core
// supplied to Serve; existing Core implementations need not provide it.
// Frontends must authorize privileged edits. At most one watch setup or edit
// per attachment may wait for ownership; other calls return worldview.ErrBusy.
// That mutex wait is not interruptible, but cancellation and attachment lifetime
// are checked before committing. Bounds remain fixed until the watch is closed.
type Blocks interface {
	WatchBlocks(context.Context, worldview.Request) (worldview.Subscription, error)
	EditBlock(context.Context, worldview.Edit) (worldview.EditResult, error)
}
