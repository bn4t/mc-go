package plugin

import "sync"

// Commands is a thread-safe chat-command registry shared with plugins.
type Commands struct {
	mu sync.RWMutex
	fn map[string]func(player string, args []string) string
}

// NewCommands builds an empty registry.
func NewCommands() *Commands {
	return &Commands{fn: make(map[string]func(string, []string) string)}
}

// Register adds or replaces a "/name" handler.
func (c *Commands) Register(name string, fn func(player string, args []string) string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fn[name] = fn
}

// Run executes a command, reporting whether it existed.
func (c *Commands) Run(name, player string, args []string) (string, bool) {
	c.mu.RLock()
	fn := c.fn[name]
	c.mu.RUnlock()
	if fn == nil {
		return "", false
	}
	return fn(player, args), true
}

// Names lists registered commands.
func (c *Commands) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.fn))
	for n := range c.fn {
		out = append(out, n)
	}
	return out
}
