// Package larian's lspk.go implements the LSPK save-container format used
// by Baldur's Gate 3. This is the read path: parse a real .lsv, list its
// entries, read a named entry's content (raw on-disk bytes, or
// zlib-decompressed), and reconstruct an unmodified archive byte-for-byte
// (Repack). The format facts here (header layout, LZ4-compressed entry
// table, per-entry zlib compression, the entry table always spanning to
// EOF) were independently confirmed against real PS5 and PC Baldur's Gate
// 3 saves, not ported from any other tool - see docs/dev.md.
package larian

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

const (
	lspkMagic            = "LSPK"
	lspkSupportedVersion = 18
	headerSize           = 40
	entrySize            = 272
	entryNameSize        = 256
	// entryAlignment and entryPadByte match Larian's own PackageWriter
	// (LSLib, Norbyte/lslib - format facts only): every packed entry is
	// padded with 0xAD bytes to a 64-byte boundary measured from the end
	// of the header, unless a "Solid" package flag is set (not used by
	// any real save observed - every sample's header Flags byte was 0).
	// Confirmed against real saves: every entry offset minus headerSize,
	// and the file list's own offset minus headerSize, was an exact
	// multiple of 64. A game that enforces this (or just expects it)
	// will very likely reject an archive missing it, even though it
	// parses fine as far as this reader itself checks.
	entryAlignment = 64
	entryPadByte   = 0xAD
)

// CompressionMethod is the low nibble of an Entry's Flags byte.
type CompressionMethod byte

const (
	CompressionNone CompressionMethod = 0
	CompressionZlib CompressionMethod = 1
	CompressionLZ4  CompressionMethod = 2
)

func (m CompressionMethod) String() string {
	switch m {
	case CompressionNone:
		return "none"
	case CompressionZlib:
		return "zlib"
	case CompressionLZ4:
		return "lz4"
	default:
		return fmt.Sprintf("unknown(%d)", byte(m))
	}
}

// Header is the fixed 40-byte LSPK archive header.
type Header struct {
	Version        uint32
	FileListOffset uint64
	FileListSize   uint32
	Flags          uint8
	Priority       uint8
	MD5            [16]byte
	NumParts       uint16
}

// Entry describes one packed file's location and size within an Archive.
type Entry struct {
	Name             string
	Offset           uint64
	Part             uint8
	Flags            uint8
	SizeOnDisk       uint32
	UncompressedSize uint32
}

func (e Entry) Compression() CompressionMethod {
	return CompressionMethod(e.Flags & 0x0F)
}

// Archive is a parsed LSPK save container. It keeps the original bytes it
// was parsed from, so entry content can be read on demand rather than
// eagerly decompressing everything up front.
type Archive struct {
	Header  Header
	Entries []Entry
	data    []byte
}

// Parse reads an LSPK archive's header and entry table. It does not
// decompress any entry content.
func Parse(data []byte) (*Archive, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("not an LSPK archive: file too short (%d bytes)", len(data))
	}
	if string(data[0:4]) != lspkMagic {
		return nil, fmt.Errorf("not an LSPK archive: bad magic %q", data[0:4])
	}
	header := Header{
		Version:        binary.LittleEndian.Uint32(data[4:8]),
		FileListOffset: binary.LittleEndian.Uint64(data[8:16]),
		FileListSize:   binary.LittleEndian.Uint32(data[16:20]),
		Flags:          data[20],
		Priority:       data[21],
		NumParts:       binary.LittleEndian.Uint16(data[38:40]),
	}
	copy(header.MD5[:], data[22:38])

	if header.Version != lspkSupportedVersion {
		return nil, fmt.Errorf("unsupported LSPK version %d (only %d is supported)", header.Version, lspkSupportedVersion)
	}
	if header.NumParts != 1 {
		// Refuse rather than mis-parse: every observed save is single-part,
		// and a multi-part archive splits file content across sibling
		// files this reader has never seen, let alone tested against.
		return nil, fmt.Errorf("multi-part LSPK archives (numParts=%d) are not supported", header.NumParts)
	}
	end := header.FileListOffset + uint64(header.FileListSize)
	if header.FileListOffset > uint64(len(data)) || end > uint64(len(data)) {
		return nil, fmt.Errorf("file list [%d, %d) is out of bounds for a %d-byte archive", header.FileListOffset, end, len(data))
	}
	if end != uint64(len(data)) {
		// Confirmed invariant on every real save inspected: the file list
		// is the last thing in the archive. A mismatch here means either
		// a format variant this reader doesn't understand, or corruption.
		return nil, fmt.Errorf("file list ends at %d, not at end of archive (%d) - refusing to guess", end, len(data))
	}

	listSection := data[header.FileListOffset:end]
	if len(listSection) < 8 {
		return nil, fmt.Errorf("file list section too short (%d bytes)", len(listSection))
	}
	numFiles := binary.LittleEndian.Uint32(listSection[0:4])
	compressedSize := binary.LittleEndian.Uint32(listSection[4:8])
	if uint64(8+compressedSize) > uint64(len(listSection)) {
		return nil, fmt.Errorf("file list compressed table (%d bytes) exceeds its section (%d bytes)", compressedSize, len(listSection)-8)
	}
	compressed := listSection[8 : 8+compressedSize]
	wantSize := int(numFiles) * entrySize
	table, err := lz4BlockDecompress(compressed, wantSize)
	if err != nil {
		return nil, fmt.Errorf("decompressing file list table: %w", err)
	}

	entries := make([]Entry, numFiles)
	for i := range entries {
		raw := table[i*entrySize : (i+1)*entrySize]
		nameEnd := bytes.IndexByte(raw[:entryNameSize], 0)
		if nameEnd < 0 {
			nameEnd = entryNameSize
		}
		offsetLo := binary.LittleEndian.Uint32(raw[256:260])
		offsetHi := binary.LittleEndian.Uint16(raw[260:262])
		entries[i] = Entry{
			Name:             string(raw[:nameEnd]),
			Offset:           uint64(offsetHi)<<32 | uint64(offsetLo),
			Part:             raw[262],
			Flags:            raw[263],
			SizeOnDisk:       binary.LittleEndian.Uint32(raw[264:268]),
			UncompressedSize: binary.LittleEndian.Uint32(raw[268:272]),
		}
	}

	return &Archive{Header: header, Entries: entries, data: data}, nil
}

// Find returns the Entry named name, if present.
func (a *Archive) Find(name string) (Entry, bool) {
	for _, e := range a.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// ReadRaw returns an entry's exact on-disk bytes, uninterpreted.
func (a *Archive) ReadRaw(e Entry) ([]byte, error) {
	if e.Part != 0 {
		return nil, fmt.Errorf("entry %q lives in part %d, but only single-part archives are supported", e.Name, e.Part)
	}
	end := e.Offset + uint64(e.SizeOnDisk)
	if end > uint64(len(a.data)) {
		return nil, fmt.Errorf("entry %q [%d, %d) is out of bounds for a %d-byte archive", e.Name, e.Offset, end, len(a.data))
	}
	return a.data[e.Offset:end], nil
}

// ReadDecompressed returns an entry's decompressed content.
func (a *Archive) ReadDecompressed(e Entry) ([]byte, error) {
	raw, err := a.ReadRaw(e)
	if err != nil {
		return nil, err
	}
	switch e.Compression() {
	case CompressionNone:
		return raw, nil
	case CompressionZlib:
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", e.Name, err)
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", e.Name, err)
		}
		if uint32(len(out)) != e.UncompressedSize {
			return nil, fmt.Errorf("entry %q: decompressed to %d bytes, expected %d", e.Name, len(out), e.UncompressedSize)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("entry %q uses unsupported compression method %s", e.Name, e.Compression())
	}
}

// Repack reconstructs the archive's bytes. With no modifications (this is
// all Phase 3 needs - see docs/dev.md), it re-serializes the header from
// its parsed fields and copies everything else through verbatim, which is
// byte-identical to the input by construction as long as the header
// serialization is correct: nothing else was touched.
func (a *Archive) Repack() []byte {
	out := make([]byte, len(a.data))
	putHeader(out[:headerSize], a.Header)
	copy(out[headerSize:], a.data[headerSize:])
	return out
}

// MD5Strategy controls what WithReplacedEntry writes into the rebuilt
// header's md5[16] field: MD5 over every file's *uncompressed* content
// concatenated in PHYSICAL (on-disk layout) order, with every output byte
// then incremented by 1. The +1-per-byte and uncompressed-content parts
// come from Larian's own PackageWriter.cs (LSLib, Norbyte/lslib - format
// facts only, not ported code); the physical-order part was determined
// empirically against a real game-written PS5 save, using its stored
// header MD5 as the oracle: of twelve algorithm variants tried
// (table/alphabetical/physical order x raw/uncompressed x +1/no-+1),
// exactly one - uncompressed, physical order, +1 - reproduced the stored
// value. (LSLib hashes in Build.Files order, which for its writer equals
// physical order; table order differs from physical order in real
// game-written saves, so "table order" was the wrong reading.)
type MD5Strategy int

const (
	// MD5Recompute uses the game's own algorithm, described above.
	MD5Recompute MD5Strategy = iota
	// MD5Unchanged copies the original archive's md5 bytes through even
	// though the content changed - a deliberately-stale value, useful
	// only for empirically testing whether the field is checked at all.
	MD5Unchanged
	// MD5Zero writes all-zero bytes.
	MD5Zero
)

// archiveHash computes the header md5 described on MD5Strategy. The
// caller must pass entries in the PHYSICAL layout order of the archive
// the hash will be stored in (Build lays entries out in spec order, so
// spec order is correct there; WithReplacedEntry must sort by offset).
func archiveHash(entries []Entry, content func(Entry) ([]byte, error)) ([16]byte, error) {
	h := md5.New()
	for _, e := range entries {
		data, err := content(e)
		if err != nil {
			return [16]byte{}, err
		}
		h.Write(data)
	}
	var sum [16]byte
	copy(sum[:], h.Sum(nil))
	for i := range sum {
		sum[i]++
	}
	return sum, nil
}

// WithReplacedEntry rebuilds the archive with the named entry's
// decompressed content replaced by newContent, re-compressed with that
// entry's existing compression method. Every other entry's raw on-disk
// bytes are reused unchanged; entries physically positioned after the
// modified one shift to accommodate its new size, and the entry table is
// rebuilt (freshly LZ4-encoded) to reflect the new layout.
//
// This does not touch meta.lsf, StorySave.bin, or any other entry's
// content - only the named entry's bytes and the bookkeeping (offsets,
// sizes, header fileListOffset/fileListSize) needed to keep the archive
// structurally valid. It has not been confirmed that a game will accept
// output from this function; see the md5 caveat on MD5Strategy.
func (a *Archive) WithReplacedEntry(name string, newContent []byte, md5Strategy MD5Strategy) ([]byte, error) {
	target, ok := a.Find(name)
	if !ok {
		return nil, fmt.Errorf("entry %q not found", name)
	}
	if target.Part != 0 {
		return nil, fmt.Errorf("entry %q lives in part %d, but only single-part archives are supported", name, target.Part)
	}

	var newRaw []byte
	switch target.Compression() {
	case CompressionNone:
		newRaw = newContent
	case CompressionZlib:
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write(newContent); err != nil {
			return nil, fmt.Errorf("compressing replacement for %q: %w", name, err)
		}
		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("compressing replacement for %q: %w", name, err)
		}
		newRaw = buf.Bytes()
	default:
		return nil, fmt.Errorf("entry %q uses unsupported compression method %s", name, target.Compression())
	}

	// Walk entries in physical (on-disk offset) order - table order and
	// physical order aren't the same thing (confirmed against real
	// saves) - assigning fresh offsets as sizes shift.
	ordered := append([]Entry(nil), a.Entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Offset < ordered[j].Offset })

	body := make([]byte, 0, len(a.data))
	newOffsets := map[string]uint64{}
	newSizes := map[string]uint32{}
	for _, e := range ordered {
		offset := uint64(headerSize) + uint64(len(body))
		newOffsets[e.Name] = offset
		if e.Name == name {
			newSizes[e.Name] = uint32(len(newRaw))
			body = append(body, newRaw...)
			body = append(body, padding(len(body))...)
			continue
		}
		raw, err := a.ReadRaw(e)
		if err != nil {
			return nil, err
		}
		newSizes[e.Name] = e.SizeOnDisk
		body = append(body, raw...)
		body = append(body, padding(len(body))...)
	}

	table := make([]byte, 0, len(a.Entries)*entrySize)
	for _, e := range a.Entries {
		row := make([]byte, entrySize)
		copy(row[:entryNameSize], e.Name)
		offset := newOffsets[e.Name]
		binary.LittleEndian.PutUint32(row[256:260], uint32(offset))
		binary.LittleEndian.PutUint16(row[260:262], uint16(offset>>32))
		row[262] = e.Part
		row[263] = e.Flags
		binary.LittleEndian.PutUint32(row[264:268], newSizes[e.Name])
		uncompressedSize := e.UncompressedSize
		if e.Name == name {
			uncompressedSize = uint32(len(newContent))
		}
		binary.LittleEndian.PutUint32(row[268:272], uncompressedSize)
		table = append(table, row...)
	}
	compressedTable := encodeLZ4Block(table)

	var listSection []byte
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(a.Entries)))
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(compressedTable)))
	listSection = append(listSection, compressedTable...)

	header := a.Header
	header.FileListOffset = uint64(headerSize) + uint64(len(body))
	header.FileListSize = uint32(len(listSection))

	out := make([]byte, headerSize, headerSize+len(body)+len(listSection))
	putHeader(out, header)
	out = append(out, body...)
	out = append(out, listSection...)

	switch md5Strategy {
	case MD5Recompute:
		// ordered is the rebuilt archive's physical layout order, which is
		// what archiveHash requires.
		sum, err := archiveHash(ordered, func(e Entry) ([]byte, error) {
			if e.Name == name {
				return newContent, nil
			}
			return a.ReadDecompressed(e)
		})
		if err != nil {
			return nil, fmt.Errorf("computing archive hash: %w", err)
		}
		copy(out[22:38], sum[:])
	case MD5Unchanged:
		copy(out[22:38], a.Header.MD5[:])
	case MD5Zero:
		for i := 22; i < 38; i++ {
			out[i] = 0
		}
	}

	return out, nil
}

// padding returns the 0xAD filler bytes needed to bring a body of the
// given length (measured from the start of the body, i.e. right after
// the header) up to the next entryAlignment boundary - see the
// entryAlignment doc comment for why this matters.
func padding(bodyLen int) []byte {
	need := (entryAlignment - bodyLen%entryAlignment) % entryAlignment
	pad := make([]byte, need)
	for i := range pad {
		pad[i] = entryPadByte
	}
	return pad
}

// lz4MinMatch and lz4LastLiterals follow the raw LZ4 block format: a
// match must be at least 4 bytes, and the final lz4LastLiterals bytes of
// a block are always literals (no match may start there), matching the
// safety margin real LZ4 encoders/decoders rely on.
const (
	lz4MinMatch     = 4
	lz4LastLiterals = 5
)

// encodeLZ4Block is a real (if simple, greedy, single-pass) raw LZ4
// block encoder: it finds 4-byte match candidates via a hash map of
// "last position this 4-byte sequence was seen," extends matches
// forward, and emits token/literal/offset/match sequences per the LZ4
// block spec. This exists because encodeLZ4LiteralOnly - valid LZ4, but
// never actually smaller than its input - turned out to matter: a real
// game silently rejected an otherwise-correct, content-identical
// archive whose only difference from a working one was a literal-only
// (i.e. non-shrinking) entry table, strongly suggesting its reader
// checks that compressed size is actually smaller. This encoder is not
// optimal (single hash slot per 4-byte key, so it can miss shorter-range
// matches once overwritten), but for the entry table's use case - a
// small, highly repetitive, zero-padded structure - it comfortably
// achieves real compression.
func encodeLZ4Block(data []byte) []byte {
	n := len(data)
	if n < lz4MinMatch+lz4LastLiterals {
		return encodeLZ4LiteralOnly(data)
	}

	var out []byte
	lastPos := make(map[uint32]int)
	literalStart := 0
	pos := 0
	matchLimit := n - lz4MinMatch - lz4LastLiterals

	for pos <= matchLimit {
		key := binary.LittleEndian.Uint32(data[pos : pos+4])
		candidate, found := lastPos[key]
		lastPos[key] = pos
		if found && pos-candidate <= 65535 {
			matchLen := lz4MinMatch
			maxLen := n - lz4LastLiterals - pos
			for matchLen < maxLen && data[candidate+matchLen] == data[pos+matchLen] {
				matchLen++
			}
			emitLZ4Sequence(&out, data[literalStart:pos], pos-candidate, matchLen)
			pos += matchLen
			literalStart = pos
			continue
		}
		pos++
	}
	emitLZ4FinalLiterals(&out, data[literalStart:])
	return out
}

func appendLZ4Length(out *[]byte, length int) {
	for length >= 255 {
		*out = append(*out, 255)
		length -= 255
	}
	*out = append(*out, byte(length))
}

func emitLZ4Sequence(out *[]byte, literals []byte, offset, matchLen int) {
	litLen := len(literals)
	tokenLit := litLen
	if tokenLit > 15 {
		tokenLit = 15
	}
	encodedMatchLen := matchLen - lz4MinMatch
	tokenMatch := encodedMatchLen
	if tokenMatch > 15 {
		tokenMatch = 15
	}
	*out = append(*out, byte(tokenLit<<4|tokenMatch))
	if litLen >= 15 {
		appendLZ4Length(out, litLen-15)
	}
	*out = append(*out, literals...)
	*out = append(*out, byte(offset), byte(offset>>8))
	if encodedMatchLen >= 15 {
		appendLZ4Length(out, encodedMatchLen-15)
	}
}

func emitLZ4FinalLiterals(out *[]byte, literals []byte) {
	litLen := len(literals)
	tokenLit := litLen
	if tokenLit > 15 {
		tokenLit = 15
	}
	*out = append(*out, byte(tokenLit<<4)) // match nibble 0: no match follows
	if litLen >= 15 {
		appendLZ4Length(out, litLen-15)
	}
	*out = append(*out, literals...)
}

// encodeLZ4LiteralOnly builds a valid (if suboptimal) raw LZ4 block that's
// entirely literals - always correct regardless of content, used only for
// small test fixtures (see buildSyntheticArchive in lspk_test.go) and as
// encodeLZ4Block's fallback for inputs too small to safely match against.
func encodeLZ4LiteralOnly(data []byte) []byte {
	var out []byte
	n := len(data)
	if n < 15 {
		out = append(out, byte(n<<4))
	} else {
		out = append(out, 0xF0)
		remaining := n - 15
		for remaining >= 255 {
			out = append(out, 255)
			remaining -= 255
		}
		out = append(out, byte(remaining))
	}
	out = append(out, data...)
	return out
}

func putHeader(dst []byte, h Header) {
	copy(dst[0:4], lspkMagic)
	binary.LittleEndian.PutUint32(dst[4:8], h.Version)
	binary.LittleEndian.PutUint64(dst[8:16], h.FileListOffset)
	binary.LittleEndian.PutUint32(dst[16:20], h.FileListSize)
	dst[20] = h.Flags
	dst[21] = h.Priority
	copy(dst[22:38], h.MD5[:])
	binary.LittleEndian.PutUint16(dst[38:40], h.NumParts)
}

// lz4BlockDecompress decodes a raw LZ4 block (not the LZ4 frame format -
// there's no frame header/footer here, just the token/literal/match
// sequence loop) into exactly uncompressedSize bytes.
func lz4BlockDecompress(src []byte, uncompressedSize int) ([]byte, error) {
	out := make([]byte, 0, uncompressedSize)
	pos := 0
	for pos < len(src) {
		token := src[pos]
		pos++

		litLen := int(token >> 4)
		if litLen == 15 {
			for {
				if pos >= len(src) {
					return nil, fmt.Errorf("truncated literal-length extension")
				}
				b := src[pos]
				pos++
				litLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		if pos+litLen > len(src) {
			return nil, fmt.Errorf("literal run of %d bytes exceeds remaining input", litLen)
		}
		out = append(out, src[pos:pos+litLen]...)
		pos += litLen

		if pos >= len(src) {
			break // final sequence in a block is literals-only
		}
		if pos+2 > len(src) {
			return nil, fmt.Errorf("truncated match offset")
		}
		offset := int(binary.LittleEndian.Uint16(src[pos : pos+2]))
		pos += 2
		if offset == 0 {
			return nil, fmt.Errorf("match offset of 0 is invalid")
		}

		matchLen := int(token & 0x0F)
		if matchLen == 15 {
			for {
				if pos >= len(src) {
					return nil, fmt.Errorf("truncated match-length extension")
				}
				b := src[pos]
				pos++
				matchLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		matchLen += 4 // minimum encodable match length

		matchPos := len(out) - offset
		if matchPos < 0 {
			return nil, fmt.Errorf("match offset %d points before the start of output", offset)
		}
		for i := 0; i < matchLen; i++ {
			out = append(out, out[matchPos+i])
		}
	}
	if len(out) != uncompressedSize {
		return nil, fmt.Errorf("decompressed to %d bytes, expected %d", len(out), uncompressedSize)
	}
	return out, nil
}

// EntrySpec is one file to include when building a fresh archive with
// Build: its name, its decompressed content, and which compression
// method to store it with.
type EntrySpec struct {
	Name        string
	Content     []byte
	Compression CompressionMethod
}

// Build assembles a brand-new LSPK archive from an explicit entry set,
// laid out in the given order. Unlike WithReplacedEntry (which patches
// one entry in an existing archive, preserving everything else exactly),
// Build doesn't require the entry set to match any prior archive at all -
// entries can be added or removed freely, which WithReplacedEntry can't
// do. This is the right tool for grafting a different save's file set
// onto a container (e.g. swapping in another save's LevelCache files
// wholesale, where the source and target don't share the same level and
// so don't have the same LevelCache filenames at all), as opposed to
// patching a single known field in an otherwise-unchanged file.
func Build(entries []EntrySpec, md5Strategy MD5Strategy) ([]byte, error) {
	body := make([]byte, 0)
	type placed struct {
		EntrySpec
		offset     uint64
		sizeOnDisk uint32
	}
	placedEntries := make([]placed, 0, len(entries))
	for _, e := range entries {
		var onDisk []byte
		switch e.Compression {
		case CompressionNone:
			onDisk = e.Content
		case CompressionZlib:
			var buf bytes.Buffer
			zw := zlib.NewWriter(&buf)
			if _, err := zw.Write(e.Content); err != nil {
				return nil, fmt.Errorf("compressing %q: %w", e.Name, err)
			}
			if err := zw.Close(); err != nil {
				return nil, fmt.Errorf("compressing %q: %w", e.Name, err)
			}
			onDisk = buf.Bytes()
		default:
			return nil, fmt.Errorf("entry %q uses unsupported compression method %s", e.Name, e.Compression)
		}
		offset := uint64(headerSize) + uint64(len(body))
		body = append(body, onDisk...)
		body = append(body, padding(len(body))...)
		placedEntries = append(placedEntries, placed{e, offset, uint32(len(onDisk))})
	}

	table := make([]byte, 0, len(placedEntries)*entrySize)
	for _, p := range placedEntries {
		if len(p.Name) >= entryNameSize {
			return nil, fmt.Errorf("entry name %q is too long (max %d bytes)", p.Name, entryNameSize-1)
		}
		row := make([]byte, entrySize)
		copy(row[:entryNameSize], p.Name)
		binary.LittleEndian.PutUint32(row[256:260], uint32(p.offset))
		binary.LittleEndian.PutUint16(row[260:262], uint16(p.offset>>32))
		row[262] = 0 // part
		row[263] = byte(p.Compression)
		binary.LittleEndian.PutUint32(row[264:268], p.sizeOnDisk)
		binary.LittleEndian.PutUint32(row[268:272], uint32(len(p.Content)))
		table = append(table, row...)
	}
	compressedTable := encodeLZ4Block(table)

	var listSection []byte
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(placedEntries)))
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(compressedTable)))
	listSection = append(listSection, compressedTable...)

	header := Header{
		Version:        lspkSupportedVersion,
		FileListOffset: uint64(headerSize) + uint64(len(body)),
		FileListSize:   uint32(len(listSection)),
		NumParts:       1,
	}

	out := make([]byte, headerSize, headerSize+len(body)+len(listSection))
	putHeader(out, header)
	out = append(out, body...)
	out = append(out, listSection...)

	switch md5Strategy {
	case MD5Recompute:
		entryList := make([]Entry, len(entries))
		for i, e := range entries {
			entryList[i] = Entry{Name: e.Name}
		}
		contentByName := make(map[string][]byte, len(entries))
		for _, e := range entries {
			contentByName[e.Name] = e.Content
		}
		sum, err := archiveHash(entryList, func(e Entry) ([]byte, error) { return contentByName[e.Name], nil })
		if err != nil {
			return nil, fmt.Errorf("computing archive hash: %w", err)
		}
		copy(out[22:38], sum[:])
	case MD5Zero:
		for i := 22; i < 38; i++ {
			out[i] = 0
		}
	case MD5Unchanged:
		return nil, fmt.Errorf("MD5Unchanged has no meaning for Build: there is no prior archive to copy a stale value from")
	}

	return out, nil
}
