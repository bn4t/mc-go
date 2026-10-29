package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/worldgen"
)

// An unreadable generated root compound does not mask bundled resources.
// Complete template schema/data-version validation is a separate concern.
func (s *Server) structureTemplateRead(name string) map[string]any {
	decode := func(input io.Reader) map[string]any {
		_, value, err := nbt.ReadNamed(input)
		if err != nil {
			return nil
		}
		template, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		template["author"] = "?" // Native template files do not serialize author.
		return template
	}
	if f, err := s.structureTemplateFile(name, false); err == nil {
		reader, err := gzip.NewReader(f)
		if err == nil {
			// 26.2 reads the root without requiring a gzip trailer: a
			// complete compound survives bad CRC or a missing footer.
			template := decode(io.LimitReader(reader, 64<<20))
			_ = reader.Close()
			if template != nil {
				_ = f.Close()
				return template
			}
		}
		_ = f.Close()
	}
	if strings.HasPrefix(name, "minecraft:") {
		if raw, ok := worldgen.BuiltinTemplateNBT(strings.TrimPrefix(name, "minecraft:")); ok {
			return decode(bytes.NewReader(raw))
		}
	}
	return nil
}
