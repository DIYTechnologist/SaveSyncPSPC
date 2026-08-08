package unityblb

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitCellsCacheEntries(t *testing.T) {
	entries := []Entry{
		{Name: "gameinfo.json", Data: []byte("info")},
		{Name: "CellsCache/baked-batch-cells-11-17-11.bin", Data: []byte("a")},
		{Name: "CellsCache/baked-batch-cells-12-18-11.bin", Data: []byte("b")},
	}
	plain, cellsCache, err := splitCellsCacheEntries(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0].Name != "gameinfo.json" {
		t.Fatalf("plain = %+v", plain)
	}
	if len(cellsCache) != 2 {
		t.Fatalf("got %d CellsCache entries, want 2", len(cellsCache))
	}
	if cellsCache[0].Name != "baked-batch-cells-11-17-11.bin" {
		t.Fatalf("cellsCache[0].Name = %q, want prefix stripped", cellsCache[0].Name)
	}
}

func TestSplitCellsCacheEntriesRejectsUnrecognizedName(t *testing.T) {
	entries := []Entry{{Name: "CellsCache/something-else.bin", Data: []byte("x")}}
	if _, _, err := splitCellsCacheEntries(entries); err == nil {
		t.Fatal("expected an error for an unrecognized CellsCache entry name")
	}
}

func TestGroupCellsCacheIntoZipsGroupsByBatchID(t *testing.T) {
	cellsCache := []Entry{
		{Name: "baked-batch-cells-11-17-11.bin", Data: []byte("a")},
		{Name: "baked-batch-cells-11-18-12.bin", Data: []byte("b")},
		{Name: "baked-batch-cells-12-17-11.bin", Data: []byte("c")},
	}
	zips, err := groupCellsCacheIntoZips(cellsCache)
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 2 {
		t.Fatalf("got %d zips, want 2 (one per batch id)", len(zips))
	}
	names := map[string]bool{}
	for _, z := range zips {
		names[z.Name] = true
	}
	if !names["CellsCache/baked-batch-cells-11-grp0.zip"] || !names["CellsCache/baked-batch-cells-12-grp0.zip"] {
		t.Fatalf("unexpected zip names: %+v", zips)
	}
}

// TestCellsCacheRoundTrip verifies flatten(group(entries)) reproduces the
// original per-cell entries - the actual PC<->PS5 conversion path for
// CellsCache.
func TestCellsCacheRoundTrip(t *testing.T) {
	original := []Entry{
		{Name: "baked-batch-cells-11-17-11.bin", Data: []byte("cell-a-data")},
		{Name: "baked-batch-cells-11-18-12.bin", Data: []byte("cell-b-data")},
		{Name: "baked-batch-cells-12-17-11.bin", Data: []byte("cell-c-data")},
	}
	zips, err := groupCellsCacheIntoZips(original)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "CellsCache"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, z := range zips {
		if err := os.WriteFile(filepath.Join(dir, z.Name), z.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gameinfo.json"), []byte(`{"protoBufVersion":13}`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := readPCDirEntries(dir)
	if err != nil {
		t.Fatal(err)
	}

	var flattenedCells []Entry
	var sawGameInfo bool
	for _, e := range entries {
		if e.Name == "gameinfo.json" {
			sawGameInfo = true
			continue
		}
		flattenedCells = append(flattenedCells, e)
	}
	if !sawGameInfo {
		t.Fatal("expected gameinfo.json to survive readPCDirEntries")
	}
	if len(flattenedCells) != len(original) {
		t.Fatalf("got %d flattened cell entries, want %d", len(flattenedCells), len(original))
	}

	got := map[string]string{}
	for _, e := range flattenedCells {
		got[e.Name] = string(e.Data)
	}
	for _, e := range original {
		want := string(e.Data)
		gotData, ok := got[cellsCacheDir+e.Name]
		if !ok {
			t.Fatalf("missing flattened entry for %s", e.Name)
		}
		if gotData != want {
			t.Fatalf("flattened entry %s = %q, want %q", e.Name, gotData, want)
		}
	}
}

func TestFlattenCellsCacheZipIgnoresDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("subdir/"); err != nil {
		t.Fatal(err)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "baked-batch-cells-5-1-1.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("payload"))
	zw.Close()
	f.Close()

	entries, err := flattenCellsCacheZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (directory entry should be skipped)", len(entries))
	}
	if entries[0].Name != "CellsCache/baked-batch-cells-5-1-1.bin" {
		t.Fatalf("entry name = %q", entries[0].Name)
	}
}

// TestFlattenCellsCacheZipDataRejectsOversizedEntry is a regression test
// for a zip-bomb guard: a compressed entry that decompresses past the
// configured limit must be refused rather than fully read into memory.
// Uses a tiny injected limit so the test doesn't need to actually
// decompress hundreds of megabytes to exercise the real 256MB default.
func TestFlattenCellsCacheZipDataRejectsOversizedEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "baked-batch-cells-5-1-1.bin", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	// Highly compressible content that's still bigger than the tiny test
	// limit once decompressed.
	if _, err := w.Write(bytes.Repeat([]byte{0}, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := flattenCellsCacheZipDataLimited(buf.Bytes(), 100); err == nil {
		t.Fatal("expected an entry exceeding the size limit to be rejected")
	}

	// The same data must still succeed under a limit that actually fits it.
	entries, err := flattenCellsCacheZipDataLimited(buf.Bytes(), 1000)
	if err != nil {
		t.Fatalf("expected content within the limit to succeed, got: %v", err)
	}
	if len(entries) != 1 || len(entries[0].Data) != 1000 {
		t.Fatalf("entries = %#v", entries)
	}
}
