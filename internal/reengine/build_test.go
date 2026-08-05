package reengine

import (
	"bytes"
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
