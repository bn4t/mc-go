package server

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// All server-data JSON writers share this filesystem fence, including writers
// from separate runtimes. Only detached workers take it; capture and waits under
// simulation ownership must never take it. This preserves write exclusion, not
// a cross-runtime gameplay snapshot or cross-file transaction.
var dataSaveMu sync.Mutex

type serverDataSaveJob struct {
	done chan struct{}
	err  error
}

// serverDataSnapshot retains only private maps, slices and item-content graphs.
// Its writer does not access sessions, dimensions, saved-player stores or locks.
type serverDataSnapshot struct {
	file serverDataFile
}

func (snapshot *serverDataSnapshot) write(path string) error {
	raw, err := json.Marshal(snapshot.file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func (s *Server) pendingServerDataSaveOwned() *serverDataSaveJob {
	job := s.dataSave
	if job == nil {
		return nil
	}
	select {
	case <-job.done:
		return nil
	default:
		return job
	}
}

// startServerDataSaveOwned requires ownership and no pending data job. Public
// callers check shutdown before admission; owned disconnect cleanup deliberately
// remains able to save during shutdown, before connection cleanup is joined.
// One job is reserved before copying, bounding admitted jobs rather than bytes.
func (s *Server) startServerDataSaveOwned() (*serverDataSaveJob, error) {
	path := s.dataPath()
	if path == "" {
		return nil, fmt.Errorf("server: data persistence disabled")
	}
	job := &serverDataSaveJob{done: make(chan struct{})}
	s.dataSave = job
	snapshot := s.captureServerDataOwned()
	write := s.dataSaveWrite
	if write == nil {
		write = func(path string, snapshot *serverDataSnapshot) error {
			return snapshot.write(path)
		}
	}
	// There is at most one unfinished worker per runtime. Even when another
	// runtime owns the global filesystem fence, this worker never needs a
	// simulation owner or live-store lock in order to finish.
	go func() {
		defer close(job.done)
		dataSaveMu.Lock()
		defer dataSaveMu.Unlock()
		job.err = write(path, snapshot)
	}()
	return job, nil
}

func (s *Server) captureServerDataOwned() *serverDataSnapshot {
	f := serverDataFile{Version: serverDataVersion, Players: map[string]playerPersist{}}
	var clone dataSaveCloner
	// Remembered name and UUID keys retain their existing login lookup rules.
	// Seed these before live records so a current incarnation wins by exact name.
	s.savedPlayersMu.Lock()
	for key, p := range s.savedPlayers {
		f.Players[key] = clone.player(p)
	}
	s.savedPlayersMu.Unlock()
	for _, p := range s.activePlayersOwned() {
		sess := p.java
		if sess == nil || !p.admitted || p.left || !p.active.Load() || sess.state.Load() != StatePlay {
			continue
		}
		if name := sess.getName(); name != "" {
			f.Players[name] = clone.player(snapshotPlayer(sess))
		}
	}
	for name, d := range s.dims {
		d.signs.mu.Lock()
		for pos, st := range d.signs.signs {
			f.Signs = append(f.Signs, signPersist{
				Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z,
				Front: st.Front, Back: st.Back,
				FrontColor: st.frontColor, BackColor: st.backColor,
				FrontGlowing: st.frontGlowing, BackGlowing: st.backGlowing,
				Waxed: st.waxed,
			})
		}
		d.signs.mu.Unlock()
		d.chests.mu.Lock()
		for pos, c := range d.chests.chests {
			items := c.slots
			for i, item := range items {
				items[i] = clone.item(item)
			}
			f.Chests = append(f.Chests, chestPersist{
				Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z,
				Items: items, LootTable: c.lootTable, LootSeed: c.lootSeed,
			})
		}
		d.chests.mu.Unlock()
		d.containers.mu.Lock()
		for pos, c := range d.containers.containers {
			f.Containers = append(f.Containers, containerPersist{
				Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z, Block: c.block,
				Items: clone.items(c.slots), BrewTime: c.brewTime, Fuel: c.fuel, Disabled: c.disabled,
			})
		}
		d.containers.mu.Unlock()
	}
	clone.finish()
	return &serverDataSnapshot{file: f}
}

type dataSaveItemCopy struct {
	from, to *[shulkerSlots]ItemStack
}

// A memo plus an iterative worklist copies arbitrary item-content graphs. A
// shared array remains shared within the private graph; cycles are preserved so
// json.Marshal retains its normal error instead of capture recursing forever.
type dataSaveCloner struct {
	contents map[*[shulkerSlots]ItemStack]*[shulkerSlots]ItemStack
	pending  []dataSaveItemCopy
}

func (clone *dataSaveCloner) item(item ItemStack) ItemStack {
	if item.Contents == nil {
		return item
	}
	if to, ok := clone.contents[item.Contents]; ok {
		item.Contents = to
		return item
	}
	if clone.contents == nil {
		clone.contents = make(map[*[shulkerSlots]ItemStack]*[shulkerSlots]ItemStack)
	}
	to := new([shulkerSlots]ItemStack)
	clone.contents[item.Contents] = to
	clone.pending = append(clone.pending, dataSaveItemCopy{from: item.Contents, to: to})
	item.Contents = to
	return item
}

func (clone *dataSaveCloner) items(items []ItemStack) []ItemStack {
	if items == nil {
		return nil
	}
	out := make([]ItemStack, len(items))
	for i, item := range items {
		out[i] = clone.item(item)
	}
	return out
}

func (clone *dataSaveCloner) finish() {
	for i := 0; i < len(clone.pending); i++ {
		pair := clone.pending[i]
		for slot, item := range pair.from {
			pair.to[slot] = clone.item(item)
		}
	}
}

func cloneDataSaveSlice[T any](from []T) []T {
	if from == nil {
		return nil
	}
	to := make([]T, len(from))
	copy(to, from)
	return to
}

func (clone *dataSaveCloner) player(p playerPersist) playerPersist {
	for i, item := range p.Inv {
		p.Inv[i] = clone.item(item)
	}
	p.Carried = clone.item(p.Carried)
	for i, item := range p.Ender {
		p.Ender[i] = clone.item(item)
	}
	p.Body = clone.item(p.Body)
	if from := p.Stats; from != nil {
		p.Stats = make(map[string]int32, len(from))
		for key, value := range from {
			p.Stats[key] = value
		}
	}
	p.StatsOrder = cloneDataSaveSlice(p.StatsOrder)
	if from := p.Advancements; from != nil {
		p.Advancements = make(map[string]savedAdv, len(from))
		for key, adv := range from {
			if fromCriteria := adv.Criteria; fromCriteria != nil {
				adv.Criteria = make(map[string]int64, len(fromCriteria))
				for criterion, value := range fromCriteria {
					adv.Criteria[criterion] = value
				}
			}
			p.Advancements[key] = adv
		}
	}
	p.UnlockedRecipes = cloneDataSaveSlice(p.UnlockedRecipes)
	p.SeenRecipes = cloneDataSaveSlice(p.SeenRecipes)
	return p
}
