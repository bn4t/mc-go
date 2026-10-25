package plugin

import (
	"context"
	"sort"
	"sync"
)

type command struct {
	id    uint64
	fn    func(string, []string) string
	ctx   context.Context
	owner *Commands
}
type commandRegistry struct {
	mu   sync.RWMutex
	next uint64
	fn   map[string]command
}

// Commands is a thread-safe command registry. A plugin's scoped registry
// automatically removes its commands when its lifecycle context ends.
type Commands struct {
	root   *commandRegistry
	ctx    context.Context
	parent *Commands
	plugin string
}

func NewCommands() *Commands {
	return &Commands{root: &commandRegistry{fn: make(map[string]command)}, ctx: context.Background()}
}

func (c *Commands) WithContext(ctx context.Context) *Commands {
	scoped, cancel := context.WithCancel(ctx)
	context.AfterFunc(c.ctx, cancel)
	return &Commands{root: c.root, ctx: scoped, parent: c, plugin: c.plugin}
}

func (c *Commands) active() bool { return c.ctx.Err() == nil && (c.parent == nil || c.parent.active()) }

// Register adds or replaces a /name handler. A canceled scope cannot register.
func (c *Commands) Register(name string, fn func(player string, args []string) string) {
	c.RegisterContext(c.ctx, name, fn)
}

// RegisterContext returns an idempotent unsubscribe function. Replacement
// handlers are unaffected by removal of an older registration.
func (c *Commands) RegisterContext(ctx context.Context, name string, fn func(string, []string) string) func() {
	if ctx.Err() != nil || !c.active() {
		return func() {}
	}
	r := c.root
	r.mu.Lock()
	r.next++
	id := r.next
	r.fn[name] = command{id: id, fn: fn, ctx: ctx, owner: c}
	r.mu.Unlock()
	remove := func() {
		r.mu.Lock()
		if r.fn[name].id == id {
			delete(r.fn, name)
		}
		r.mu.Unlock()
	}
	stop := context.AfterFunc(ctx, remove)
	stopScope := context.AfterFunc(c.ctx, remove)
	return func() { stop(); stopScope(); remove() }
}

// Run executes a command, reporting whether it existed. Commands already
// running finish normally; lifecycle cancellation prevents future dispatch.
func (c *Commands) Run(name, player string, args []string) (string, bool) {
	out, found, err := c.RunE(name, player, args)
	if err != nil {
		return "Plugin command failed", found
	}
	return out, found
}

// RunE reports contained callback failures separately from unknown commands.
// A failed command is still found; subsequent commands can execute normally.
func (c *Commands) RunE(name, player string, args []string) (out string, found bool, err error) {
	c.root.mu.RLock()
	cmd, exists := c.root.fn[name]
	c.root.mu.RUnlock()
	if !exists || cmd.fn == nil || cmd.ctx.Err() != nil || !cmd.owner.active() {
		return "", false, nil
	}
	err = callPlugin(cmd.owner.plugin, "command /"+name, func() error {
		out = cmd.fn(player, args)
		return nil
	})
	return out, true, err
}

func (c *Commands) Names() []string {
	c.root.mu.RLock()
	defer c.root.mu.RUnlock()
	out := make([]string, 0, len(c.root.fn))
	for name, cmd := range c.root.fn {
		if cmd.ctx.Err() == nil && cmd.owner.active() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
