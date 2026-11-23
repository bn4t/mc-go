package world

import (
	"bytes"
	"compress/zlib"
)

// RegionCodec is the record codec a save writes region files with.
// The ids and names are vanilla's RegionFileVersion constants
// (measured: server-26.2.jar, deflate is the default selection and
// gzip is not selectable through server.properties). The zero value is
// deflate, which keeps every snapshot and server that never picks one
// writing the same records as before.
type RegionCodec int

const (
	RegionDeflate RegionCodec = iota // id 2
	RegionNone                       // id 3
	RegionLZ4                        // id 4
)

// RegionCodecByName resolves one region-file-compression property
// value the way RegionFileVersion.configure does (measured: an exact
// case-sensitive option-name lookup). The second result is false for
// any other value, which the caller reports and then keeps the
// default - the native server logs an error naming the valid options
// and stays on deflate.
func RegionCodecByName(name string) (RegionCodec, bool) {
	switch name {
	case "deflate":
		return RegionDeflate, true
	case "none":
		return RegionNone, true
	case "lz4":
		return RegionLZ4, true
	}
	return RegionDeflate, false
}

// versionByte is the record version id this codec writes.
func (c RegionCodec) versionByte() byte {
	switch c {
	case RegionNone:
		return compressNone
	case RegionLZ4:
		return compressLZ4
	default:
		return compressZlib
	}
}

// compressRecordPayload renders raw record bytes under this codec.
func (c RegionCodec) compressRecordPayload(raw []byte) ([]byte, error) {
	switch c {
	case RegionNone:
		return raw, nil
	case RegionLZ4:
		return encodeLZ4BlockStream(raw), nil
	default:
		var out bytes.Buffer
		zw := zlib.NewWriter(&out)
		if _, err := zw.Write(raw); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
}

// regionExternalChunkThreshold is the sector count at which a record
// moves out of the region file (measured: RegionFile
// EXTERNAL_CHUNK_THRESHOLD - a record of 256 or more sectors has its
// payload written to a c.<x>.<z>.mcc file beside the .mca, and the
// in-file record becomes a 5-byte stub with the external flag set on
// its version byte).
const regionExternalChunkThreshold = 256
