package server

import (
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mc-go/internal/world"
)

// addListener tracks a serving listener for Shutdown.
func (s *Server) addListener(ln net.Listener) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	s.listeners[ln] = struct{}{}
}

// removeListener untracks a closed listener.
func (s *Server) removeListener(ln net.Listener) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	delete(s.listeners, ln)
}

// ShuttingDown reports whether Shutdown has begun. main uses it to
// treat the resulting Accept error as a clean exit, not a crash.
func (s *Server) ShuttingDown() bool { return s.shuttingDown.Load() }

// SaveWorld persists every dimension's block overrides. WorldDir, when
// set, saves in the real server's own world format so the world can be
// handed to vanilla and back; otherwise it writes the compact JSON
// save file. Compact JSON captures a coherent cut under simulation ownership,
// then sorts, serializes and writes it outside ownership. This call waits for
// the actual write result. Native saves likewise detach before writing. It
// reports an error when persistence is disabled so /save-all answers honestly.
func (s *Server) SaveWorld() error {
	for {
		var job *compactWorldSaveJob
		var native *nativeWorldSaveJob
		var admitted bool
		var err error
		s.simulationAction(func() {
			if s.ShuttingDown() {
				err = net.ErrClosed
				return
			}
			if s.WorldDir != "" {
				if native = s.pendingNativeWorldSaveOwned(); native != nil {
					return
				}
				native, err = s.startNativeWorldSaveOwned()
				admitted = err == nil
				return
			}
			if job = s.pendingCompactWorldSaveOwned(); job != nil {
				return
			}
			job, err = s.startCompactWorldSaveOwned()
			admitted = err == nil
		})
		if err != nil {
			return err
		}
		if native != nil {
			<-native.done
			if admitted {
				s.simulationAction(func() { s.recordNativeSaveOwned(native) })
				return native.err
			}
			continue
		}
		if job == nil {
			return err
		}
		// A busy caller waits without ownership or a reserved snapshot. Retry
		// admission afterward, including the shutdown check, to capture its own
		// coherent state rather than reporting an earlier save as its result.
		<-job.done
		if admitted {
			return job.err
		}
	}
}

// saveWorldOwned preserves synchronous command completion. Commands join any
// earlier submitted write before capturing their own ordered save.
// Waiting here still pauses gameplay; workers must never need the owner to
// complete. Public SaveWorld waits outside ownership instead.
func (s *Server) saveWorldOwned() error {
	if s.WorldDir != "" {
		return s.saveWorldDir()
	}
	if job := s.pendingCompactWorldSaveOwned(); job != nil {
		<-job.done
	}
	job, err := s.startCompactWorldSaveOwned()
	if err != nil {
		return err
	}
	<-job.done
	return job.err
}

// LoadWorld restores block overrides at boot, per dimension. Missing
// files mean a fresh world, not an error, mirroring vanilla's
// first-boot generate.
//
// It is idempotent, and Serve calls it: a server configured with a
// world directory serves that world whether or not its caller
// remembered to load it, and a caller that did (main does) pays nothing
// for the second call.
func (s *Server) LoadWorld() error {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	if s.worldLoaded {
		return nil
	}
	if err := s.loadWorld(); err != nil {
		return err
	}
	s.worldLoaded = true
	return nil
}

// loadWorld is LoadWorld's body, run once under loadMu.
func (s *Server) loadWorld() error {
	if s.WorldDir != "" {
		return s.loadWorldDir()
	}
	if err := s.applyLevelType(); err != nil {
		return err
	}
	if s.WorldPath == "" {
		return nil
	}
	for name, d := range s.dims {
		path := s.WorldPath
		if name != dimOverworld {
			path += "." + strings.TrimPrefix(name, "minecraft:")
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := d.w.Load(path); err != nil {
			return err
		}
	}
	return nil
}

// saveWorldDir is the owned native-save fence for commands. It joins an
// earlier detached writer before capturing its own cut and waits for completion
// without releasing gameplay ownership. Public SaveWorld uses the same writer
// but waits outside ownership.
func (s *Server) saveWorldDir() error {
	s.waitNativeWorldSaveOwned()
	job, err := s.startNativeWorldSaveOwned()
	if err != nil {
		return err
	}
	<-job.done
	s.recordNativeSaveOwned(job)
	return job.err
}

// loadWorldDir reads a world in the real server's format: the level
// metadata first (time of day, spawn, game rules), then each
// dimension's chunks. A world directory that does not exist is a fresh
// world, not an error.
func (s *Server) loadWorldDir() error {
	if _, err := os.Stat(filepath.Join(s.WorldDir, "level.dat")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		lv, err := world.ReadLevel(s.WorldDir)
		if err != nil {
			return err
		}
		if lv.LevelName != "" && s.LevelName == "" {
			s.LevelName = lv.LevelName
		}
		if lv.Seed != 0 && s.WorldSeed == 0 {
			s.WorldSeed = lv.Seed
		}
		if s.LevelType == "" && lv.Generator != "" {
			s.LevelType = lv.Generator
		}
		if s.FlatPreset == nil && lv.Flat != nil {
			s.FlatPreset = lv.Flat
		}
		if lv.Spawn != [3]int{} {
			s.setWorldSpawn(lv.Spawn[0], lv.Spawn[1], lv.Spawn[2])
		}
		if lv.Difficulty != "" {
			s.setDifficultyQuiet(difficultyByte(lv.Difficulty))
		}
		if lv.DayTime != 0 {
			s.setDayTime(lv.DayTime)
		}
		s.setGameTimeBase(lv.GameTime)
		if len(lv.GameRules) > 0 {
			s.rulesMu.Lock()
			for k, v := range lv.GameRules {
				if _, ok := s.gameRules[k]; ok {
					s.gameRules[k] = v
				}
			}
			s.rulesMu.Unlock()
		}
	}
	if err := s.applyLevelType(); err != nil {
		return err
	}
	for name, d := range s.dims {
		if err := d.w.LoadDimension(s.WorldDir, name, d.minY, d.height); err != nil {
			return err
		}
	}
	// The chunks brought their block entities with them; without this a
	// loaded chest would stand there open and empty.
	s.restoreBlockEntities()
	for _, d := range s.dims {
		s.restoreEntities(d)
	}
	if err := s.loadPlayerFiles(); err != nil {
		return err
	}
	s.loadPlayerMetaFiles()
	s.loadFunctions()
	s.loadAdvancementPacks()
	if err := s.loadTestInstancePacks(); err != nil {
		return err
	}
	return nil
}

// levelName is the name level.dat carries: the configured one, or the
// world directory's own name, which is how vanilla names a world.
func (s *Server) levelName() string {
	if s.LevelName != "" {
		return s.LevelName
	}
	return filepath.Base(filepath.Clean(s.WorldDir))
}

// difficultyName renders a difficulty byte the way level.dat holds it.
func difficultyName(v byte) string {
	switch v {
	case DifficultyPeaceful:
		return "peaceful"
	case DifficultyEasy:
		return "easy"
	case DifficultyHard:
		return "hard"
	default:
		return "normal"
	}
}

// difficultyByte parses level.dat's difficulty name.
func difficultyByte(name string) byte {
	switch name {
	case "peaceful":
		return DifficultyPeaceful
	case "easy":
		return DifficultyEasy
	case "hard":
		return DifficultyHard
	default:
		return DifficultyNormal
	}
}

// floor is int(math.Floor) for the whole-number spawn coordinates the
// level file holds.
func floor(v float64) int { return int(math.Floor(v)) }

// WorldSpawn is the world's spawn point. A world loaded from disk can
// have moved it; a fresh one uses the generator's own spawn, which for
// the flat world is the block the real server writes into level.dat.
// The caller gets the player's position on that block, not the block
// itself, because that is where a joining player stands.
func (s *Server) WorldSpawn() (x, y, z float64) {
	s.spawnMu.Lock()
	defer s.spawnMu.Unlock()
	if s.haveSpawn {
		return float64(s.spawnPos[0]) + 0.5, float64(s.spawnPos[1]), float64(s.spawnPos[2]) + 0.5
	}
	return s.world.Spawn()
}

// setWorldSpawn records the spawn block a saved world carried.
func (s *Server) setWorldSpawn(px, py, pz int) {
	s.spawnMu.Lock()
	s.spawnPos = [3]int{px, py, pz}
	s.haveSpawn = true
	s.spawnMu.Unlock()
}

// setWorldSpawnAngles stores the spawn point and angle /setworldspawn
// reports, returning whether anything changed — the real server skips
// the broadcast when it didn't.
func (s *Server) setWorldSpawnAngles(pos [3]int, yaw, pitch float32) bool {
	s.spawnMu.Lock()
	defer s.spawnMu.Unlock()
	changed := !s.haveSpawn || s.spawnPos != pos || s.spawnYaw != yaw || s.spawnPitch != pitch
	s.spawnPos = pos
	s.spawnYaw, s.spawnPitch = yaw, pitch
	s.haveSpawn = true
	return changed
}

// disconnectSoon kicks one session and closes the socket after the
// writer flushes the kick.
func (s *Server) disconnectSoon(sess *Session, reason string) {
	s.SendPlayDisconnect(sess, reason)
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = sess.conn.Close()
	}()
}

// Shutdown stops the server like vanilla /stop: listeners close (Serve
// unblocks), every player is kicked with reason, and the world halts.
// Calls join runtime work and are idempotent across concurrent callers.
// A synchronous gameplay hook or command must request shutdown asynchronously
// (go s.Shutdown(reason)); it cannot join its own simulation action.
func (s *Server) Shutdown(reason string) {
	if reason == "" {
		reason = "Server closed"
	}
	if !s.shuttingDown.CompareAndSwap(false, true) {
		if s.runtimeDone != nil {
			<-s.runtimeDone
		}
		return
	}
	if s.runtimeDone != nil {
		defer close(s.runtimeDone)
	}
	s.discardSimulationActions()
	// Exclude startup/attachment before taking ownership of the singleton
	// cleanup. No owner action is held while joining runtime goroutines.
	s.runtimeMu.Lock()
	stop := s.runtimeStop
	s.runtimeStop = nil
	if s.runtimeCancel != nil {
		s.runtimeCancel()
	}
	s.listenersMu.Lock()
	for ln := range s.listeners {
		_ = ln.Close()
	}
	s.listenersMu.Unlock()
	s.runtimeMu.Unlock()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay {
			s.disconnectSoon(sess, reason)
		} else {
			_ = sess.conn.Close()
		}
		return true
	})
	if stop != nil {
		stop()
	}
	s.stopJoinPreparation()
	s.connections.Wait()
	s.stopPlugins()
	// Drain an in-flight external action before retiring shard workers. Public
	// action ingress rejects shutdown, so no new world mutation can follow.
	s.simulationAction(func() {
		s.closeAllWorldviewsOwned()
		s.retireAvatarsOwned("Server closed")
	})
	// Admitted saves own detached values and must finish even after cancellation.
	// Disconnect cleanup may have submitted its last data save during shutdown;
	// connections have now joined. Read the latest receipts under ownership and
	// join outside it before retiring world workers.
	var save *compactWorldSaveJob
	var dataSave *serverDataSaveJob
	var nativeSave *nativeWorldSaveJob
	s.simulationAction(func() {
		save = s.compactSave
		dataSave = s.dataSave
		nativeSave = s.nativeSave
	})
	if save != nil {
		<-save.done
	}
	if dataSave != nil {
		<-dataSave.done
	}
	if nativeSave != nil {
		<-nativeSave.done
		s.simulationAction(func() { s.recordNativeSaveOwned(nativeSave) })
	}
	s.stopChunkEncoder()
	for _, d := range s.dims {
		d.w.Stop()
	}
}
