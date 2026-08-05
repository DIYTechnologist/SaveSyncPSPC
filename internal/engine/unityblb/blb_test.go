package unityblb

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	entries := []Entry{
		{Name: "gameinfo.json", Data: []byte(`{"version":2,"protoBufVersion":13}`)},
		{Name: "screenshot.jpg", Data: bytes.Repeat([]byte{0xff, 0xd8}, 50)},
		{Name: "scene-objects.bin", Data: []byte("scene-data")},
		{Name: "global-objects.bin", Data: []byte("global-data")},
		{Name: "CellsCache/baked-batch-cells-11-17-11.bin", Data: []byte("cell-a")},
		{Name: "CellsCache/baked-batch-cells-11-18-12.bin", Data: []byte("cell-b")},
	}
	encoded, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte{0x1f, 0x8b}) {
		t.Fatal("expected a gzip-wrapped result")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(decoded), len(entries))
	}
	for i, e := range entries {
		if decoded[i].Name != e.Name {
			t.Fatalf("entry %d name = %q, want %q", i, decoded[i].Name, e.Name)
		}
		if !bytes.Equal(decoded[i].Data, e.Data) {
			t.Fatalf("entry %d data = %q, want %q", i, decoded[i].Data, e.Data)
		}
	}
}

func TestDecodeEmptyContainer(t *testing.T) {
	encoded, err := Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 0 {
		t.Fatalf("got %d entries, want 0", len(decoded))
	}
}

func TestDecodeRejectsNonGzip(t *testing.T) {
	if _, err := Decode([]byte("not a gzip file")); err == nil {
		t.Fatal("expected an error for non-gzip input")
	}
}

func TestDecodeRejectsTruncatedEntry(t *testing.T) {
	// A valid gzip stream wrapping a name-length byte claiming more bytes
	// than actually follow.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte{10, 'a', 'b', 'c'}) // name length 10, only 3 bytes follow
	gw.Close()

	if _, err := Decode(buf.Bytes()); err == nil {
		t.Fatal("expected an error for a truncated entry")
	}
}

func TestFind(t *testing.T) {
	entries := []Entry{
		{Name: "gameinfo.json", Data: []byte("a")},
		{Name: "screenshot.jpg", Data: []byte("b")},
	}
	if data, ok := Find(entries, "gameinfo.json"); !ok || string(data) != "a" {
		t.Fatalf("Find(gameinfo.json) = %q, %v", data, ok)
	}
	if _, ok := Find(entries, "missing"); ok {
		t.Fatal("expected Find to report missing entry as not found")
	}
}

func TestEncodeRejectsOverlongName(t *testing.T) {
	longName := string(make([]byte, 256))
	_, err := Encode([]Entry{{Name: longName, Data: []byte("x")}})
	if err == nil {
		t.Fatal("expected an error for a name longer than 255 bytes")
	}
}
