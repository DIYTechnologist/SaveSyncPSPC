package larian

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"testing"
)

func TestLZ4BlockDecompressLiteralOnly(t *testing.T) {
	// token=0x40 (literal length 4, no match), then "AAAA".
	src := []byte{0x40, 'A', 'A', 'A', 'A'}
	out, err := lz4BlockDecompress(src, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "AAAA" {
		t.Fatalf("out = %q", out)
	}
}

func TestLZ4BlockDecompressWithMatch(t *testing.T) {
	// 4 literal 'A's, then a match of length 6 at offset 1 - expands to
	// "AAAAAAAAAA" (10 'A's) via the classic overlapping-copy RLE trick.
	src := []byte{0x42, 'A', 'A', 'A', 'A', 0x01, 0x00}
	out, err := lz4BlockDecompress(src, 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "AAAAAAAAAA" {
		t.Fatalf("out = %q", out)
	}
}

func TestLZ4BlockDecompressExtendedLiteralLength(t *testing.T) {
	// Literal length 20 requires the token's 15 escape plus a
	// continuation byte (20-15=5).
	payload := bytes.Repeat([]byte{'x'}, 20)
	src := append([]byte{0xF0, 5}, payload...)
	out, err := lz4BlockDecompress(src, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("out = %q", out)
	}
}

type syntheticEntry struct {
	name        string
	content     []byte
	compression CompressionMethod
}

// buildSyntheticArchive hand-assembles a minimal but format-accurate LSPK
// archive: real header layout, a real (literal-only) LZ4-compressed entry
// table, and both compression methods this reader supports. It mirrors
// invariants confirmed against real Baldur's Gate 3 saves (see lspk.go's
// package doc and docs/dev.md): the entry table is LZ4-block compressed
// and sits at the very end of the file.
func buildSyntheticArchive(t *testing.T, entries []syntheticEntry) []byte {
	t.Helper()

	var body []byte
	type placed struct {
		syntheticEntry
		offset     uint64
		sizeOnDisk uint32
	}
	var placedEntries []placed
	for _, e := range entries {
		var onDisk []byte
		switch e.compression {
		case CompressionNone:
			onDisk = e.content
		case CompressionZlib:
			var buf bytes.Buffer
			zw := zlib.NewWriter(&buf)
			if _, err := zw.Write(e.content); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			onDisk = buf.Bytes()
		default:
			t.Fatalf("unsupported test compression method %v", e.compression)
		}
		offset := headerSize + len(body)
		body = append(body, onDisk...)
		placedEntries = append(placedEntries, placed{e, uint64(offset), uint32(len(onDisk))})
	}

	table := make([]byte, 0, len(placedEntries)*entrySize)
	for _, p := range placedEntries {
		row := make([]byte, entrySize)
		copy(row[:entryNameSize], p.name)
		binary.LittleEndian.PutUint32(row[256:260], uint32(p.offset))
		binary.LittleEndian.PutUint16(row[260:262], uint16(p.offset>>32))
		row[262] = 0 // part
		row[263] = byte(p.compression)
		binary.LittleEndian.PutUint32(row[264:268], p.sizeOnDisk)
		binary.LittleEndian.PutUint32(row[268:272], uint32(len(p.content)))
		table = append(table, row...)
	}
	compressedTable := encodeLZ4LiteralOnly(table)

	fileListOffset := headerSize + len(body)
	var listSection []byte
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(placedEntries)))
	listSection = binary.LittleEndian.AppendUint32(listSection, uint32(len(compressedTable)))
	listSection = append(listSection, compressedTable...)

	archive := make([]byte, headerSize)
	putHeader(archive, Header{
		Version:        lspkSupportedVersion,
		FileListOffset: uint64(fileListOffset),
		FileListSize:   uint32(len(listSection)),
		NumParts:       1,
	})
	archive = append(archive, body...)
	archive = append(archive, listSection...)
	return archive
}

func TestParseReadsEntriesAndContent(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "hello.txt", content: []byte("hello world"), compression: CompressionNone},
		{name: "data.json", content: []byte(`{"Platform":"Steam"}`), compression: CompressionZlib},
	})

	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Entries) != 2 {
		t.Fatalf("entries = %#v", archive.Entries)
	}

	hello, ok := archive.Find("hello.txt")
	if !ok {
		t.Fatal("hello.txt not found")
	}
	raw, err := archive.ReadRaw(hello)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "hello world" {
		t.Fatalf("raw = %q", raw)
	}

	data2, ok := archive.Find("data.json")
	if !ok {
		t.Fatal("data.json not found")
	}
	decompressed, err := archive.ReadDecompressed(data2)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != `{"Platform":"Steam"}` {
		t.Fatalf("decompressed = %q", decompressed)
	}
}

func TestParseRejectsUnsupportedVersion(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{{name: "a", content: []byte("x"), compression: CompressionNone}})
	binary.LittleEndian.PutUint32(data[4:8], 99)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestParseRejectsMultiPart(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{{name: "a", content: []byte("x"), compression: CompressionNone}})
	binary.LittleEndian.PutUint16(data[38:40], 2)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for numParts != 1")
	}
}

func TestParseRejectsFileListNotAtEOF(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{{name: "a", content: []byte("x"), compression: CompressionNone}})
	data = append(data, 0, 0, 0) // trailing junk after the file list
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error when file list doesn't end at EOF")
	}
}

// TestRepackIsByteIdenticalWhenUnchanged is the round-trip proof the spec
// requires before any real conversion is attempted: parse a real-shaped
// archive, repack it with no modifications, and assert the result is
// byte-for-byte identical to the input.
func TestRepackIsByteIdenticalWhenUnchanged(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "meta.lsf", content: bytes.Repeat([]byte{0xAB}, 500), compression: CompressionZlib},
		{name: "SaveInfo.json", content: []byte(`{"Platform":"Prospero","Save Name":"AutoSave_0"}`), compression: CompressionZlib},
		{name: "LevelCache/TUT_Avernus_C.lsf", content: []byte("level cache content"), compression: CompressionNone},
	})

	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	repacked := archive.Repack()
	if !bytes.Equal(repacked, data) {
		t.Fatalf("repacked archive (%d bytes) is not byte-identical to the original (%d bytes)", len(repacked), len(data))
	}

	// The repacked bytes must also still parse correctly, not just match
	// by coincidence.
	reparsed, err := Parse(repacked)
	if err != nil {
		t.Fatalf("repacked archive failed to re-parse: %v", err)
	}
	if len(reparsed.Entries) != len(archive.Entries) {
		t.Fatalf("reparsed entries = %d, want %d", len(reparsed.Entries), len(archive.Entries))
	}
}

// TestWithReplacedEntryLeavesOtherEntriesUntouched replaces a middle
// entry (not first, not last, by table order or by physical offset) with
// content of a different size in both directions, and asserts every
// other entry's decompressed content is exactly what it was before -
// only the target entry's content should ever change.
func TestWithReplacedEntryLeavesOtherEntriesUntouched(t *testing.T) {
	original := []syntheticEntry{
		{name: "meta.lsf", content: []byte("meta content unchanged"), compression: CompressionZlib},
		{name: "SaveInfo.json", content: []byte(`{"Platform" : "Prospero","Save Name" : "AutoSave_0"}`), compression: CompressionZlib},
		{name: "StorySave.bin", content: bytes.Repeat([]byte{0x42}, 2000), compression: CompressionZlib},
		{name: "Globals.lsf", content: []byte("globals content unchanged"), compression: CompressionNone},
	}
	data := buildSyntheticArchive(t, original)
	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		newContent []byte
	}{
		{"shrink", []byte(`{"Platform" : "Steam","Save Name" : "AutoSave_0"}`)},
		{"grow", []byte(`{"Platform" : "SomeMuchLongerPlatformNameThanBefore","Save Name" : "AutoSave_0"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rebuilt, err := archive.WithReplacedEntry("SaveInfo.json", tc.newContent, MD5Recompute)
			if err != nil {
				t.Fatal(err)
			}
			reparsed, err := Parse(rebuilt)
			if err != nil {
				t.Fatalf("rebuilt archive failed to parse: %v", err)
			}
			if len(reparsed.Entries) != len(original) {
				t.Fatalf("entries = %d, want %d", len(reparsed.Entries), len(original))
			}
			for _, orig := range original {
				entry, ok := reparsed.Find(orig.name)
				if !ok {
					t.Fatalf("%s missing after rebuild", orig.name)
				}
				got, err := reparsed.ReadDecompressed(entry)
				if err != nil {
					t.Fatalf("%s: %v", orig.name, err)
				}
				want := orig.content
				if orig.name == "SaveInfo.json" {
					want = tc.newContent
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s content = %q, want %q", orig.name, got, want)
				}
			}
		})
	}
}

func TestWithReplacedEntryMD5Strategies(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "SaveInfo.json", content: []byte(`{"Platform" : "Prospero"}`), compression: CompressionZlib},
	})
	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	newContent := []byte(`{"Platform" : "Steam"}`)

	recomputed, err := archive.WithReplacedEntry("SaveInfo.json", newContent, MD5Recompute)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := archive.WithReplacedEntry("SaveInfo.json", newContent, MD5Unchanged)
	if err != nil {
		t.Fatal(err)
	}
	zeroed, err := archive.WithReplacedEntry("SaveInfo.json", newContent, MD5Zero)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(recomputed[22:38], archive.Header.MD5[:]) {
		t.Fatal("MD5Recompute produced the original (stale) md5")
	}
	if !bytes.Equal(unchanged[22:38], archive.Header.MD5[:]) {
		t.Fatal("MD5Unchanged should keep the original md5 bytes")
	}
	for _, b := range zeroed[22:38] {
		if b != 0 {
			t.Fatalf("MD5Zero left a non-zero byte: %v", zeroed[22:38])
		}
	}
	// All three should still be otherwise-valid, parseable archives.
	for _, variant := range [][]byte{recomputed, unchanged, zeroed} {
		if _, err := Parse(variant); err != nil {
			t.Fatalf("variant failed to parse: %v", err)
		}
	}
}

// TestArchiveHashMatchesLarianAlgorithm hand-computes the expected hash
// per Larian's own PackageWriter.ComputeArchiveHash (MD5 over every
// entry's *uncompressed* content, concatenated in table order, then every
// output byte incremented by 1) and checks archiveHash produces exactly
// that - not just "some" hash.
func TestArchiveHashMatchesLarianAlgorithm(t *testing.T) {
	entries := []Entry{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	content := map[string][]byte{
		"a": []byte("first file content"),
		"b": []byte("second"),
		"c": []byte("third file, longer content than the others"),
	}

	h := md5.New()
	h.Write(content["a"])
	h.Write(content["b"])
	h.Write(content["c"])
	want := h.Sum(nil)
	for i := range want {
		want[i]++
	}

	got, err := archiveHash(entries, func(e Entry) ([]byte, error) { return content[e.Name], nil })
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:], want) {
		t.Fatalf("archiveHash = %x, want %x", got, want)
	}
}

// TestEncodeLZ4BlockRoundTrips covers plain data, highly repetitive data
// (matches expected), and data with no viable matches at all (falls back
// to literals only) - decode(encode(x)) must equal x in every case.
func TestEncodeLZ4BlockRoundTrips(t *testing.T) {
	cases := map[string][]byte{
		"empty":                {},
		"tiny":                 []byte("hi"),
		"no repetition":        []byte("the quick brown fox jumps over the lazy dog, abcdefghijklmnop"),
		"highly repetitive":    bytes.Repeat([]byte{0}, 2000),
		"repetitive with tail": append(bytes.Repeat([]byte{0xAB}, 500), []byte("distinct tail content")...),
		"real-shaped entry row": func() []byte {
			row := make([]byte, entrySize)
			copy(row[:entryNameSize], "LevelCache/SomeLevel_A.lsf")
			return bytes.Repeat(row, 7) // mimics 7 mostly-zero-padded table rows
		}(),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			encoded := encodeLZ4Block(data)
			decoded, err := lz4BlockDecompress(encoded, len(data))
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if !bytes.Equal(decoded, data) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(decoded), len(data))
			}
		})
	}
}

// TestEncodeLZ4BlockActuallyCompressesRepetitiveData is the regression
// test for the real bug this encoder replaces encodeLZ4LiteralOnly for:
// a literal-only "encoding" never shrinks its input, and a real game
// silently rejected an otherwise-valid archive whose entry table wasn't
// actually smaller than its uncompressed size (confirmed live - see
// docs/dev.md). The entry table's real shape (fixed 272-byte rows,
// mostly zero-padded names) must compress well below its raw size.
func TestEncodeLZ4BlockActuallyCompressesRepetitiveData(t *testing.T) {
	row := make([]byte, entrySize)
	copy(row[:entryNameSize], "LevelCache/SomeLevel_A.lsf")
	table := bytes.Repeat(row, 7)

	encoded := encodeLZ4Block(table)
	if len(encoded) >= len(table) {
		t.Fatalf("encoded size %d is not smaller than input size %d", len(encoded), len(table))
	}
}

// TestBuildFromArbitraryEntrySet exercises the actual use case Build
// exists for: an entry set that doesn't match any prior archive at all -
// entries added, entries removed, not just one entry's content changed.
func TestBuildFromArbitraryEntrySet(t *testing.T) {
	data, err := Build([]EntrySpec{
		{Name: "meta.lsf", Content: []byte("meta"), Compression: CompressionZlib},
		{Name: "StorySave.bin", Content: bytes.Repeat([]byte{0x11}, 1000), Compression: CompressionZlib},
		{Name: "LevelCache/NewLevel_A.lsf", Content: []byte("new level a"), Compression: CompressionZlib},
		{Name: "LevelCache/NewLevel_B.lsf", Content: []byte("new level b"), Compression: CompressionNone},
	}, MD5Recompute)
	if err != nil {
		t.Fatal(err)
	}

	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Entries) != 4 {
		t.Fatalf("entries = %#v", archive.Entries)
	}
	levelA, ok := archive.Find("LevelCache/NewLevel_A.lsf")
	if !ok {
		t.Fatal("LevelCache/NewLevel_A.lsf missing")
	}
	got, err := archive.ReadDecompressed(levelA)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new level a" {
		t.Fatalf("content = %q", got)
	}
}

func TestBuildRejectsMD5UnchangedWithNoPriorArchive(t *testing.T) {
	_, err := Build([]EntrySpec{{Name: "a", Content: []byte("x"), Compression: CompressionNone}}, MD5Unchanged)
	if err == nil {
		t.Fatal("expected error: MD5Unchanged has no prior archive to copy from")
	}
}

// TestBuildAligns64ByteBoundaries is a regression test for a real bug: an
// earlier Build() laid entries out back-to-back with no padding, which
// parsed fine through this package's own reader but was silently
// rejected by the actual game (confirmed live: a content-identical
// Build() output of a real save vanished from the in-game save list,
// while the equivalent Repack() output - which preserves the original's
// real padding untouched - loaded correctly). Larian's own PackageWriter
// (LSLib) pads every entry to a 64-byte boundary (measured from the end
// of the header) with 0xAD bytes; every real save inspected confirmed
// this exactly. Both Build and WithReplacedEntry must reproduce it.
func TestBuildAligns64ByteBoundaries(t *testing.T) {
	// Content lengths deliberately not multiples of 64, so unpadded
	// layout would misalign every entry after the first.
	data, err := Build([]EntrySpec{
		{Name: "a", Content: bytes.Repeat([]byte{1}, 37), Compression: CompressionNone},
		{Name: "b", Content: bytes.Repeat([]byte{2}, 101), Compression: CompressionNone},
		{Name: "c", Content: bytes.Repeat([]byte{3}, 7), Compression: CompressionNone},
	}, MD5Recompute)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range archive.Entries {
		if (e.Offset-headerSize)%entryAlignment != 0 {
			t.Fatalf("entry %s offset %d is not %d-byte aligned from the header", e.Name, e.Offset, entryAlignment)
		}
	}
	if (archive.Header.FileListOffset-headerSize)%entryAlignment != 0 {
		t.Fatalf("file list offset %d is not %d-byte aligned from the header", archive.Header.FileListOffset, entryAlignment)
	}
}

func TestWithReplacedEntryAligns64ByteBoundaries(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "a", content: bytes.Repeat([]byte{1}, 37), compression: CompressionNone},
		{name: "b", content: bytes.Repeat([]byte{2}, 101), compression: CompressionNone},
		{name: "c", content: bytes.Repeat([]byte{3}, 7), compression: CompressionNone},
	})
	archive, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := archive.WithReplacedEntry("b", bytes.Repeat([]byte{9}, 53), MD5Recompute)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reparsed.Entries {
		if (e.Offset-headerSize)%entryAlignment != 0 {
			t.Fatalf("entry %s offset %d is not %d-byte aligned from the header", e.Name, e.Offset, entryAlignment)
		}
	}
}
