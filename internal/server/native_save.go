package server

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// nativeWorldSaveJob is published before capture. Closing done publishes both
// error fields; its worker never reads live gameplay state or acquires ownership.
type nativeWorldSaveJob struct {
	done                    chan struct{}
	err                     error
	playerErr               error
	entityOwnership         map[string]nativeEntityOwnership
	entityOwnershipRecorded bool
}

type nativeDimensionSave struct {
	name     string
	chunks   *world.DimensionSaveSnapshot
	entities map[[2]int32][]map[string]any
	// fight is the End's ender_dragon_fight saved data (dragonfight.go).
	fight map[string]any
}

// All payloads belong privately to this snapshot. There are no callbacks, worlds,
// dimensions or sessions retained by its writer. Capture remains unbounded in
// time and bytes; one unfinished job bounds concurrency, not heap usage.
type nativeWorldSaveSnapshot struct {
	scheduledEvents map[string]any
	level           world.LevelData
	dimensions      []nativeDimensionSave
	players         map[[16]byte]playerPersist
	playerErr       error
	captureErr      error
	entityOwnership map[string]nativeEntityOwnership
}

func (snapshot *nativeWorldSaveSnapshot) write() error {
	if snapshot.captureErr != nil {
		return snapshot.captureErr
	}
	if err := world.WriteLevel(snapshot.level); err != nil {
		return err
	}
	if snapshot.scheduledEvents != nil {
		if err := writeFunctionSchedule(snapshot.level.Dir, snapshot.scheduledEvents); err != nil {
			return err
		}
	}
	for _, d := range snapshot.dimensions {
		if err := d.chunks.Write(snapshot.level.Dir, d.name); err != nil {
			return err
		}
		if err := world.MergeEntityRegions(world.DimensionDir(snapshot.level.Dir, d.name), d.entities); err != nil {
			return err
		}
		if d.fight != nil {
			path := filepath.Join(world.DimensionDir(snapshot.level.Dir, d.name), "data", "minecraft", "ender_dragon_fight.dat")
			if err := writeFightData(path, d.fight); err != nil {
				return err
			}
		}
	}
	// Preserve legacy native SaveWorld behavior: player-file failures are recorded
	// separately, while level/chunk/entity write failures are returned to callers.
	for _, p := range snapshot.players {
		if err := writeNativePlayerFile(snapshot.level.Dir, p); err != nil && snapshot.playerErr == nil {
			snapshot.playerErr = err
		}
	}
	for _, p := range snapshot.players {
		if err := writeNativePlayerMetaFile(snapshot.level.Dir, p); err != nil && snapshot.playerErr == nil {
			snapshot.playerErr = err
		}
	}
	return nil
}

func (s *Server) recordNativeSaveOwned(job *nativeWorldSaveJob) {
	if job != nil && !job.entityOwnershipRecorded {
		if job.err == nil {
			s.completeNativeEntityWritesOwned(job.entityOwnership)
		}
		job.entityOwnershipRecorded = true
	}
	if job != nil && job.playerErr != nil && s.dataSaveErr == nil {
		s.dataSaveErr = job.playerErr
	}
}

func (s *Server) pendingNativeWorldSaveOwned() *nativeWorldSaveJob {
	job := s.nativeSave
	if job == nil {
		return nil
	}
	select {
	case <-job.done:
		s.recordNativeSaveOwned(job)
		return nil
	default:
		return job
	}
}

// Owned native player writes must follow any earlier submitted world snapshot,
// including during disconnect cleanup. The detached worker never needs this
// owner, so waiting preserves the existing disconnect fence without deadlock.
func (s *Server) waitNativeWorldSaveOwned() {
	if tail := s.nativeWriteTail; tail != nil {
		<-tail
	}
	if job := s.nativeSave; job != nil {
		<-job.done
		s.recordNativeSaveOwned(job)
	}
	s.scheduledRetirementOwned(false)
}

func (s *Server) startNativeWorldSaveOwned() (*nativeWorldSaveJob, error) {
	if s.ShuttingDown() {
		return nil, net.ErrClosed
	}
	job := &nativeWorldSaveJob{done: make(chan struct{})}
	s.nativeSave = job
	snapshot := s.captureNativeWorldSaveOwned()
	job.entityOwnership = snapshot.entityOwnership
	if snapshot.captureErr == nil {
		s.beginNativeEntityWritesOwned(job.entityOwnership)
	}
	write := s.nativeSaveWrite
	if write == nil {
		write = func(snapshot *nativeWorldSaveSnapshot) error { return snapshot.write() }
	}
	s.submitNativeWriteOwned(job.done, func() {
		job.err = write(snapshot)
		job.playerErr = snapshot.playerErr
	})
	return job, nil
}

func (s *Server) captureNativeWorldSaveOwned() *nativeWorldSaveSnapshot {
	// Publish any completed retirement before packing a later full save.
	// Otherwise a completed cold write could be overwritten by aged resident rows.
	s.scheduledRetirementOwned(false)
	// Complete admitted generation before capturing piece flags and entities.
	// This does not generate chunks or consume natural-spawn/End queues.
	s.structureGenEntityStep()
	sx, sy, sz := s.WorldSpawn()
	rules := map[string]string{}
	s.rulesMu.RLock()
	for k, v := range s.gameRules {
		rules[k] = v
	}
	difficulty := s.difficulty
	s.rulesMu.RUnlock()
	snapshot := &nativeWorldSaveSnapshot{level: world.LevelData{
		Dir: s.WorldDir, LevelName: s.levelName(), GameType: int32(s.GameMode),
		Difficulty: difficultyName(difficulty), Seed: s.WorldSeed,
		GenerateStructures: s.GenerateStructures, Generator: s.LevelType, Flat: s.FlatPreset, GameTime: s.gameTime(), DayTime: s.dayTime(),
		Spawn: [3]int{floor(sx), floor(sy), floor(sz)}, GameRules: rules,
	}}
	snapshot.entityOwnership = map[string]nativeEntityOwnership{}
	scheduledTicks := s.captureScheduledChunkTicksOwned()
	for name, d := range s.dims {
		selection := s.captureNativeEntitySelectionOwned(d)
		if selection.err != nil && snapshot.captureErr == nil {
			snapshot.captureErr = fmt.Errorf("entity regions %s: %w", name, selection.err)
		}
		snapshot.entityOwnership[name] = selection.ownership
		chunks := d.w.CaptureDimensionSaveWithTicks(d.minY, d.height,
			func(cx, cz int) []map[string]any { return s.chunkBlockEntities(d, cx, cz) },
			func(cx, cz int) *world.ChunkLight { return d.savedLight(cx, cz) },
			scheduledTicks[name])
		var fight map[string]any
		if d.fight != nil {
			fight = d.fight.saveNBT()
		}
		snapshot.dimensions = append(snapshot.dimensions, nativeDimensionSave{
			name: name, chunks: chunks, entities: selection.rows, fight: fight,
		})
	}
	snapshot.scheduledEvents = s.captureFunctionSchedule()
	snapshot.level.ClockStates = s.captureWorldClocks()["data"].(map[string]any)
	snapshot.players = s.captureNativePlayersOwned()
	var clone dataSaveCloner
	for uuid, p := range snapshot.players {
		snapshot.players[uuid] = clone.player(p)
	}
	clone.finish()
	return snapshot
}

// Remembered keys provide offline players; only the current admitted Java
// incarnation can overlay them. Configuring/retired/replaced sessions cannot
// supply an authoritative native snapshot.
func (s *Server) captureNativePlayersOwned() map[[16]byte]playerPersist {
	players := make(map[[16]byte]playerPersist)
	s.savedPlayersMu.Lock()
	for name, p := range s.savedPlayers {
		if p.UUID == ([16]byte{}) {
			if strings.Count(name, "-") == 4 {
				continue
			}
			p.UUID = offlineUUID(name)
		}
		players[p.UUID] = p
	}
	s.savedPlayersMu.Unlock()
	for _, player := range s.activePlayersOwned() {
		sess := player.java
		if sess == nil || !player.admitted || player.left || !player.active.Load() || sess.state.Load() != StatePlay || sess.getName() == "" {
			continue
		}
		p := snapshotPlayer(sess)
		players[p.UUID] = p
	}
	return players
}

// NBT rendering can include ExtraNBT maps and primitive arrays owned by a live
// entity. Copy the NBT value domain before handing any compound to the worker.
func cloneNativeNBT(value any) any {
	switch v := value.(type) {
	case int8, int16, int32, int64, float32, float64, string:
		return value
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, value := range v {
			out[k] = cloneNativeNBT(value)
		}
		return out
	case nbt.List:
		out := nbt.List{Elem: v.Elem, Items: make([]any, len(v.Items))}
		for i, value := range v.Items {
			out.Items[i] = cloneNativeNBT(value)
		}
		return out
	case []byte:
		return cloneDataSaveSlice(v)
	case []int32:
		return cloneDataSaveSlice(v)
	case []int64:
		return cloneDataSaveSlice(v)
	default:
		// Preserve the codec's ErrUnknownTag without retaining an arbitrary
		// mutable object that the codec cannot represent.
		return struct{}{}
	}
}
