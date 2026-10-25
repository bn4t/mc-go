package plugin

import "fmt"

// CallbackError reports a panic contained at a trusted plugin callback boundary.
// Containment cannot stop a callback that blocks or a plugin-owned goroutine.
type CallbackError struct {
	Plugin string
	Phase  string
	Value  any
}

func (e *CallbackError) Error() string {
	return fmt.Sprintf("plugin %q %s panicked: %v", e.Plugin, e.Phase, e.Value)
}

// Unwrap preserves a panic's error value for errors.Is/errors.As callers.
func (e *CallbackError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

func callPlugin(name, phase string, fn func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &CallbackError{Plugin: name, Phase: phase, Value: value}
		}
	}()
	return fn()
}
