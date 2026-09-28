package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestZZVineProps(t *testing.T) {
	for _, props := range []map[string]string{
		{"east": "true"}, {"north": "true"}, {"south": "true"}, {"west": "true"},
		{"east": "true", "up": "true"},
	} {
		b, ok := world.BlockStateOf("vine", props)
		name, _ := world.StateOwner(b)
		t.Logf("vine %v -> ok=%v owner=%v", props, ok, name)
	}
	for _, props := range []map[string]string{
		{}, {"age": "2", "facing": "north"}, {"age": "0", "facing": "south"},
	} {
		b, ok := world.BlockStateOf("cocoa", props)
		name, _ := world.StateOwner(b)
		t.Logf("cocoa %v -> ok=%v owner=%v", props, ok, name)
	}
}
