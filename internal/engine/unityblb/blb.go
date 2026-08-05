// Package unityblb implements engine.Engine for Subnautica's save
// container format: no encryption, no proprietary class/versioned object
// serialization (unlike RE Engine's DSSS or Unreal's GVAS) - just a
// gzip-wrapped flat sequence of length-prefixed entries. Confirmed
// against a real PS5 Subnautica save (title PPSA02453, save name
// "sdimg_slot0000", payload "slot0000.blb") compared byte-for-byte
// against the equivalent Steam PC save at
// SNAppData/SavedGames/slot0000/.
package unityblb

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Entry is one file inside a decoded save container.
type Entry struct {
	Name string
	Data []byte
}

// Decode gunzips and parses a Subnautica ".blb" save container. Beneath
// the gzip wrapper, entries sit back-to-back with no directory index or
// footer: [1-byte name length][name][4-byte little-endian size][data],
// repeated until EOF - the only terminator. A real slot0000.blb parses to
// exactly gameinfo.json, screenshot.jpg, scene-objects.bin,
// global-objects.bin, and one CellsCache/baked-batch-cells-<batch>-<x>-<y>.bin
// entry per world cell the player has modified.
func Decode(data []byte) ([]Entry, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a gzip-wrapped save container: %w", err)
	}
	defer gr.Close()
	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("decompressing save container: %w", err)
	}

	var entries []Entry
	pos := 0
	for pos < len(raw) {
		if pos+1 > len(raw) {
			return nil, errors.New("truncated entry: missing name length")
		}
		nameLen := int(raw[pos])
		pos++
		if pos+nameLen > len(raw) {
			return nil, errors.New("truncated entry: name runs past end of file")
		}
		name := string(raw[pos : pos+nameLen])
		pos += nameLen
		if pos+4 > len(raw) {
			return nil, fmt.Errorf("truncated entry %q: missing size field", name)
		}
		size := binary.LittleEndian.Uint32(raw[pos : pos+4])
		pos += 4
		if pos+int(size) > len(raw) {
			return nil, fmt.Errorf("truncated entry %q: data runs past end of file", name)
		}
		content := append([]byte(nil), raw[pos:pos+int(size)]...)
		pos += int(size)
		entries = append(entries, Entry{Name: name, Data: content})
	}
	return entries, nil
}

// Encode builds a ".blb" save container from entries, in order, and
// gzips it.
func Encode(entries []Entry) ([]byte, error) {
	var raw bytes.Buffer
	for _, e := range entries {
		if len(e.Name) > 255 {
			return nil, fmt.Errorf("entry name %q longer than 255 bytes", e.Name)
		}
		raw.WriteByte(byte(len(e.Name)))
		raw.WriteString(e.Name)
		var sizeField [4]byte
		binary.LittleEndian.PutUint32(sizeField[:], uint32(len(e.Data)))
		raw.Write(sizeField[:])
		raw.Write(e.Data)
	}

	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	gw.OS = 3 // matches the OS byte observed in a real PS5 slot0000.blb; purely informational
	if _, err := gw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Find returns the first entry with the given name.
func Find(entries []Entry, name string) ([]byte, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e.Data, true
		}
	}
	return nil, false
}
