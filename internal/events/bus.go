package events

// Simple in-process event bus placeholder. Domain events will be added later.
type Bus struct{}

func NewBus() *Bus { return &Bus{} }
