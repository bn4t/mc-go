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
// save file. It reports an error when persistence is disabled so
// /save-all answers honestly.
func (s *Server) SaveWorld() error {
	if s.WorldDir != "" {
		return s.saveWorldDir()
	}
	if s.WorldPath == "" {
		return errors.New("world saving is disabled (no --world)")
	}
	for name, d := range s.dims {
		path := s.WorldPath
		if name != dimOverworld {
			path += "." + strings.TrimPrefix(name, "minecraft:")
		}
		if err := d.w.Save(path); err != nil {
			return err
		}
	}
	return nil
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

// saveWorldDir writes a vanilla-format world: level.dat and the
// SavedData files that describe it, then each dimension's chunks under
// dimensions/<namespace>/<path>/region, each chunk carrying the block
// entities that sit in it - a chest's contents, a sign's text, a
// furnace's burn - so a saved chest comes back full.
func (s *Server) saveWorldDir() error {
	sx, sy, sz := s.WorldSpawn()
	rules := map[string]string{}
	s.rulesMu.RLock()
	for k, v := range s.gameRules {
		rules[k] = v
	}
	difficulty := s.difficulty
	s.rulesMu.RUnlock()
	if err := world.WriteLevel(world.LevelData{
		Dir:                s.WorldDir,
		LevelName:          s.levelName(),
		GameType:           int32(s.GameMode),
		Difficulty:         difficultyName(difficulty),
		Seed:               s.WorldSeed,
		GenerateStructures: s.GenerateStructures,
		GameTime:           s.gameTime(),
		DayTime:            s.dayTime(),
		Spawn:              [3]int{floor(sx), floor(sy), floor(sz)},
		GameRules:          rules,
	}); err != nil {
		return err
	}
	for name, d := range s.dims {
		d := d
		if err := d.w.SaveDimension(s.WorldDir, name, d.minY, d.height, func(cx, cz int) []map[string]any {
			return s.chunkBlockEntities(d, cx, cz)
		}); err != nil {
			return err
		}
	}
	return nil
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
	for name, d := range s.dims {
		if err := d.w.LoadDimension(s.WorldDir, name, d.minY, d.height); err != nil {
			return err
		}
	}
	// The chunks brought their block entities with them; without this a
	// loaded chest would stand there open and empty.
	s.restoreBlockEntities()
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
// Safe to call multiple times and from any goroutine, including a
// command handler.
func (s *Server) Shutdown(reason string) {
	if reason == "" {
		reason = "Server closed"
	}
	s.shuttingDown.Store(true)
	s.listenersMu.Lock()
	for ln := range s.listeners {
		_ = ln.Close()
	}
	s.listenersMu.Unlock()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		s.disconnectSoon(sess, reason)
		return true
	})
	for _, d := range s.dims {
		d.w.Stop()
	}
}
