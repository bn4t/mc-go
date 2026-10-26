package server

import (
	"errors"
	"net"
	"strings"

	"mc-go/internal/world"
)

// compactWorldSaveJob is a write-completion receipt. The owner publishes it
// before capture; closing done publishes err without reentering the owner.
type compactWorldSaveJob struct {
	done chan struct{}
	err  error
}

type compactDimensionSave struct {
	path     string
	snapshot *world.SaveSnapshot
}

// pendingCompactWorldSaveOwned returns only a still-running, already submitted
// job. No caller reserves a future slot while waiting for ownership.
func (s *Server) pendingCompactWorldSaveOwned() *compactWorldSaveJob {
	job := s.compactSave
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

// startCompactWorldSaveOwned requires ownership and no pending compact job.
// Reservation precedes capture, keeping admitted snapshot/write concurrency at
// one. Capture still costs O(total overrides) time and storage; there is no
// payload-byte, world-size or owner-hold bound.
func (s *Server) startCompactWorldSaveOwned() (*compactWorldSaveJob, error) {
	if s.ShuttingDown() {
		return nil, net.ErrClosed
	}
	if s.WorldPath == "" {
		return nil, errors.New("world saving is disabled (no --world)")
	}
	job := &compactWorldSaveJob{done: make(chan struct{})}
	s.compactSave = job
	saves := make([]compactDimensionSave, 0, len(s.dims))
	for name, d := range s.dims {
		path := s.WorldPath
		if name != dimOverworld {
			path += "." + strings.TrimPrefix(name, "minecraft:")
		}
		saves = append(saves, compactDimensionSave{path: path, snapshot: d.w.CaptureSave()})
	}
	write := s.compactSaveWrite
	if write == nil {
		write = func(path string, snapshot *world.SaveSnapshot) error {
			return snapshot.Write(path)
		}
	}
	// The worker receives no live world, dimension or session. In particular it
	// needs neither simulation ownership nor shard workers to finish. There is
	// one worker per admitted job and at most one unfinished job per runtime.
	go func() {
		defer close(job.done)
		for _, save := range saves {
			if err := write(save.path, save.snapshot); err != nil {
				job.err = err
				return
			}
		}
	}()
	return job, nil
}
