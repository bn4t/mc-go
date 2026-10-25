package data

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
)

//go:embed advwire.dat.gz
var advWireGZ []byte

// AdvDefWire returns the advancement's definition entry bytes exactly
// as vanilla 26.2 writes them inside an update_advancements packet:
// the id string, parent flag + id, display flag + payload, requirements
// groups, and the telemetry flag. The table covers all 1688
// advancements in the data pack: entries were sliced off the wire of a
// real 26.2 server (grant-all plus gameplay captures), and the few
// display-less definitions vanilla never sends that way are
// synthesized from the data pack by tools/advslice.
var AdvDefWire = func() map[string][]byte {
	zr, err := gzip.NewReader(bytes.NewReader(advWireGZ))
	if err != nil {
		panic(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	n := binary.BigEndian.Uint32(raw[:4])
	m := make(map[string][]byte, n)
	o := 4
	for i := uint32(0); i < n; i++ {
		nl := int(binary.BigEndian.Uint16(raw[o:]))
		o += 2
		id := string(raw[o : o+nl])
		o += nl
		l := int(binary.BigEndian.Uint32(raw[o:]))
		o += 4
		m[id] = raw[o : o+l]
		o += l
	}
	return m
}()

// the file is parsed eagerly at init — the map literal above does it.
