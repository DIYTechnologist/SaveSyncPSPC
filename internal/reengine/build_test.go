package reengine

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildRoundTripsWithID(t *testing.T) {
	body := bytes.Repeat([]byte("SAVEDATA"), 20)
	data, err := Build(body, KeyRE2, BuildOptions{HasID: true, SteamID: 11052978})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HashValid {
		t.Fatal("expected valid hash")
	}
	if !decoded.HasID || decoded.SteamID != 11052978 {
		t.Fatalf("got HasID=%v SteamID=%d", decoded.HasID, decoded.SteamID)
	}
	if !bytes.Equal(decoded.Body, body) {
		t.Fatal("body mismatch after round trip")
	}
}

func TestBuildRoundTripsWithoutID(t *testing.T) {
	body := bytes.Repeat([]byte("PS5DATA0"), 15)
	data, err := Build(body, KeyRE2, BuildOptions{HasID: false})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HashValid {
		t.Fatal("expected valid hash")
	}
	if decoded.HasID {
		t.Fatal("expected HasID=false")
	}
	if decoded.DataOffset != 0x18 {
		t.Fatalf("got dataOffset %#x, want 0x18 (no ID field)", decoded.DataOffset)
	}
	if !bytes.Equal(decoded.Body, body) {
		t.Fatal("body mismatch after round trip")
	}
}

// TestBuildRoundTripsUnalignedBody covers the real-save case where the
// payload length isn't a multiple of Blowfish's block size: the trailing
// remainder is stored in the clear and must survive a round trip intact
// (on real saves those bytes carry the save's slot number - dropping
// them silently truncates the file, see Decode).
func TestBuildRoundTripsUnalignedBody(t *testing.T) {
	for _, remainder := range [][]byte{
		{0x00, 0x00, 0x00, 0x00}, // slot 0, as data000.bin
		{0xff, 0xff, 0xff, 0xff}, // slot -1, as the global profile
		{0x15, 0x00, 0x00, 0x00}, // slot 21, as data021Slot.bin
	} {
		body := append(bytes.Repeat([]byte("SAVEDATA"), 10), remainder...)
		data, err := Build(body, KeyRE2, BuildOptions{HasID: true, SteamID: 11052978})
		if err != nil {
			t.Fatalf("remainder %x: %v", remainder, err)
		}
		decoded, err := Decode(data, KeyRE2)
		if err != nil {
			t.Fatalf("remainder %x: %v", remainder, err)
		}
		if !decoded.HashValid {
			t.Fatalf("remainder %x: hash invalid", remainder)
		}
		if !bytes.Equal(decoded.Body, body) {
			t.Fatalf("remainder %x: body mismatch\n got %x\nwant %x", remainder, decoded.Body, body)
		}
	}
}

// TestBuildLeavesRemainderUnencrypted pins the specific mechanism: the
// trailing bytes appear verbatim in the built file, not enciphered.
func TestBuildLeavesRemainderUnencrypted(t *testing.T) {
	remainder := []byte{0x15, 0x00, 0x00, 0x00}
	body := append(bytes.Repeat([]byte("SAVEDATA"), 10), remainder...)
	data, err := Build(body, KeyRE2, BuildOptions{HasID: false})
	if err != nil {
		t.Fatal(err)
	}
	// ...before the 4-byte trailing murmur3 hash.
	got := data[len(data)-4-len(remainder) : len(data)-4]
	if !bytes.Equal(got, remainder) {
		t.Fatalf("got %x, want %x stored in the clear", got, remainder)
	}
}

// TestBuildRejectsNon4AlignedBody covers the case the format cannot
// represent losslessly: the game writer zero-pads to 4 bytes, so such a
// body would read back longer than it was written.
func TestBuildRejectsNon4AlignedBody(t *testing.T) {
	for extra := 1; extra < 4; extra++ {
		body := append(bytes.Repeat([]byte("SAVEDATA"), 8), bytes.Repeat([]byte{0xAA}, extra)...)
		if _, err := Build(body, KeyRE2, BuildOptions{HasID: false}); err == nil {
			t.Fatalf("bodyLen=%d: expected Build to refuse a non-4-aligned body", len(body))
		}
	}
	// The aligned case still builds, and every real save is aligned.
	body := bytes.Repeat([]byte("SAVEDATA"), 8)
	if _, err := Build(body, KeyRE2, BuildOptions{HasID: false}); err != nil {
		t.Fatalf("4-aligned body should build: %v", err)
	}
}

func TestConvertPCToPS5RefusesCorruptSource(t *testing.T) {
	data := buildDSSS(t, KeyRE2, 11052978, bytes.Repeat([]byte("GAMEDATA"), 8))
	data[len(data)-1] ^= 0xff // corrupt the stored checksum

	_, err := ConvertPCToPS5(data, KeyRE2)
	if err == nil {
		t.Fatal("expected conversion to refuse a source whose checksum doesn't match")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConvertPCToPS5DropsIDAndPreservesBody(t *testing.T) {
	body := bytes.Repeat([]byte("GAMEDATA"), 50)
	pcData := buildDSSS(t, KeyRE2, 11052978, body)

	ps5Data, err := ConvertPCToPS5(pcData, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := Decode(ps5Data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HashValid {
		t.Fatal("expected valid hash on converted output")
	}
	if decoded.HasID {
		t.Fatal("expected converted output to drop the HasID field")
	}
	if !bytes.Equal(decoded.Body, body) {
		t.Fatal("expected body content to be preserved across conversion")
	}
}

func TestConvertPCToPS5RejectsAlreadyPS5Shaped(t *testing.T) {
	// A PS5-shaped input has no ID field to strip - Decode still parses
	// it fine (HasID just comes out false), so ConvertPCToPS5 succeeds
	// but is a no-op on the header shape. This documents that behavior
	// rather than asserting it should error, since nothing about the
	// container format itself distinguishes "already converted" from
	// "genuinely PS5" - only the flags observed in it.
	body := bytes.Repeat([]byte("ALREADY0"), 10)
	ps5Shaped, err := Build(body, KeyRE2, BuildOptions{HasID: false})
	if err != nil {
		t.Fatal(err)
	}
	converted, err := ConvertPCToPS5(ps5Shaped, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(converted, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Body, body) {
		t.Fatal("body should still round-trip")
	}
}
