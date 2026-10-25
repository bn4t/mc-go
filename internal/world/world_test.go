package world

import (
	"sync"
	"testing"
	"time"

	"mc-go/internal/event"
)

func TestFlatGenerator(t *testing.T) {
	w := New(nil, 4)
	w.Start()
	defer w.Stop()
	bedrock, _ := BlockState("bedrock")
	cases := map[int]Block{
		-65: Air, -64: bedrock, -63: Dirt, -62: Dirt,
		-61: Grass, -60: Air, 0: Air, 200: Air,
	}
	for y, want := range cases {
		if got := w.GetBlock(Pos{X: 3, Y: y, Z: -7}); got != want {
			t.Fatalf("y=%d: got %d want %d", y, got, want)
		}
	}
}

func TestSetGetOverride(t *testing.T) {
	w := New(nil, 4)
	w.Start()
	defer w.Stop()
	p := Pos{X: 10, Y: 65, Z: 10}
	if got := w.GetBlock(p); got != Air {
		t.Fatalf("before set = %d, want air", got)
	}
	w.SetBlock(p, Stone)
	if got := w.GetBlock(p); got != Stone {
		t.Fatalf("after set = %d, want stone", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	w := New(event.New(), 8)
	w.Start()
	defer w.Stop()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				p := Pos{X: i, Y: 60 + j%10, Z: j}
				w.SetBlock(p, Grass)
				_ = w.GetBlock(p)
			}
		}(i)
	}
	wg.Wait()
}

func TestTickAdvances(t *testing.T) {
	w := New(event.New(), 2)
	w.Start()
	defer w.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for w.TickCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if w.TickCount() < 2 {
		t.Fatalf("ticks = %d, want >= 2", w.TickCount())
	}
}

func TestFillBoxMatchesGet(t *testing.T) {
	w := New(nil, 4)
	w.Start()
	defer w.Stop()
	w.SetBlock(Pos{X: 1, Y: 65, Z: -2}, Stone)
	w.SetBlock(Pos{X: -30, Y: 62, Z: 30}, Grass) // generator says dirt here
	min, max := Pos{X: -32, Y: 60, Z: -32}, Pos{X: 31, Y: 70, Z: 31}
	dx, dy, dz := max.X-min.X+1, max.Y-min.Y+1, max.Z-min.Z+1
	out := make([]Block, dx*dy*dz)
	w.FillBox(min, max, out)
	at := func(x, y, z int) Block {
		return out[((y-min.Y)*dz+(z-min.Z))*dx+(x-min.X)]
	}
	for y := min.Y; y <= max.Y; y += 5 {
		for x := min.X; x <= max.X; x += 7 {
			for z := min.Z; z <= max.Z; z += 11 {
				if got, want := at(x, y, z), w.GetBlock(Pos{X: x, Y: y, Z: z}); got != want {
					t.Fatalf("(%d,%d,%d): fill=%d get=%d", x, y, z, got, want)
				}
			}
		}
	}
	if at(1, 65, -2) != Stone || at(-30, 62, 30) != Grass {
		t.Fatal("overrides missing from fill")
	}
}
