package server

import (
	"compress/gzip"
	"mc-go/internal/nbt"
)

// structureTemplateStore shares the native repository writer with runtime
// template producers. Disk writes use the repository's confined file opener.
func (s *Server) structureTemplateStore(name string, native map[string]any, toDisk bool) bool {
	if s.structureTemplates == nil {
		s.structureTemplates = map[string]map[string]any{}
	}
	s.structureTemplates[name] = native
	if !toDisk {
		return true
	}
	f, e := s.structureTemplateFile(name, true)
	if e != nil {
		return false
	}
	zw := gzip.NewWriter(f)
	disk := cloneNBTValue(native).(map[string]any)
	delete(disk, "author")
	e = nbt.WriteNamed(zw, "", disk)
	if close := zw.Close(); e == nil {
		e = close
	}
	if close := f.Close(); e == nil {
		e = close
	}
	return e == nil
}
