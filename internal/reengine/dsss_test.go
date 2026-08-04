package reengine

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildDSSS assembles a synthetic DSSS file matching the PC (Blowfish+
// HasID) title shape via this package's own Build, so tests don't depend
// on any real game save file.
func buildDSSS(t *testing.T, key []byte, steamID uint64, body []byte) []byte {
	t.Helper()
	data, err := Build(body, key, BuildOptions{HasID: true, SteamID: steamID})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodeSyntheticFixture(t *testing.T) {
	body := bytes.Repeat([]byte("SAVEDATA"), 20) // 160 bytes, block-aligned
	data := buildDSSS(t, KeyRE2, 11052978, body)

	decoded, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HashValid {
		t.Fatal("expected valid hash")
	}
	if !decoded.HasID {
		t.Fatal("expected HasID true")
	}
	if decoded.SteamID != 11052978 {
		t.Fatalf("got steamID %d, want 11052978", decoded.SteamID)
	}
	if !bytes.Equal(decoded.Body, body) {
		t.Fatalf("got body %q, want %q", decoded.Body, body)
	}
	if decoded.DataOffset != 0x20 {
		t.Fatalf("got dataOffset %#x, want 0x20", decoded.DataOffset)
	}
}

func TestDecodeRejectsWrongKey(t *testing.T) {
	data := buildDSSS(t, KeyRE2, 11052978, []byte("12345678"))
	if _, err := Decode(data, KeyRE3); err == nil {
		t.Fatal("expected an error decoding with the wrong title's key")
	}
}

func TestDecodeRejectsBadMagic(t *testing.T) {
	data := buildDSSS(t, KeyRE2, 1, []byte("12345678"))
	data[0] = 'X'
	if _, err := Decode(data, KeyRE2); err == nil {
		t.Fatal("expected an error for bad magic")
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	data := buildDSSS(t, KeyRE2, 1, []byte("12345678"))
	binary.LittleEndian.PutUint32(data[4:8], 3)
	if _, err := Decode(data, KeyRE2); err == nil {
		t.Fatal("expected an error for unsupported version")
	}
}

func TestDecodeDetectsTamperedHash(t *testing.T) {
	data := buildDSSS(t, KeyRE2, 1, []byte("12345678"))
	data[len(data)-1] ^= 0xff // corrupt the stored murmur3 hash
	decoded, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.HashValid {
		t.Fatal("expected HashValid=false after corrupting the trailing hash")
	}
}

func TestDecodeRejectsTruncatedFile(t *testing.T) {
	if _, err := Decode([]byte("DSSS"), KeyRE2); err == nil {
		t.Fatal("expected an error for a truncated file")
	}
}

// TestDecodeRejectsUnsupportedFlags covers flags whose bodies need
// decompression or a different cipher: decoding one would otherwise
// "succeed" while handing back still-compressed bytes, and rebuilding it
// would silently drop the flag.
func TestDecodeRejectsUnsupportedFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		bit  uint32
	}{
		{"CITRUS", flagCitrus},
		{"DEFLATE", flagDeflate},
		{"MANDARIN/LIME", flagMandarin},
		{"unknown", 0x80},
	} {
		data := buildDSSS(t, KeyRE2, 1, []byte("12345678"))
		flags := binary.LittleEndian.Uint32(data[8:12])
		binary.LittleEndian.PutUint32(data[8:12], flags|tc.bit)
		rehash(data)

		_, err := Decode(data, KeyRE2)
		if err == nil {
			t.Fatalf("%s: expected Decode to refuse an unsupported flag", tc.name)
		}
		if !strings.Contains(err.Error(), "unsupported save flags") {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
	}
}

// TestDecodeHandlesPlaintextCheckBlock covers a file that some other
// tool already decrypted: the DSSSDSSS check block sits in the clear,
// which means nothing after it may be decrypted again. Decrypting anyway
// yields convincing-looking garbage with a valid checksum, so this is
// the worst kind of silent failure.
func TestDecodeHandlesPlaintextCheckBlock(t *testing.T) {
	body := bytes.Repeat([]byte("PLAINTXT"), 8)
	data, err := Build(body, KeyRE2, BuildOptions{HasID: false})
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the check block in the clear and leave the body as-is,
	// mimicking a decrypted dump.
	copy(data[16:24], dsssCheck)
	plainBody := append([]byte(nil), body...)
	copy(data[24:24+len(plainBody)], plainBody)
	rehash(data)

	decoded, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HashValid {
		t.Fatal("expected a valid checksum")
	}
	if !bytes.Equal(decoded.Body, plainBody) {
		t.Fatalf("body was decrypted despite the plaintext check block:\n got %q\nwant %q",
			decoded.Body[:16], plainBody[:16])
	}
}

// rehash recomputes a file's trailing murmur3 after the test has edited
// its header, so decoding fails for the reason under test rather than
// for a checksum mismatch.
func rehash(data []byte) {
	h := murmur3_32(data[:len(data)-4], 0xffffffff)
	binary.LittleEndian.PutUint32(data[len(data)-4:], h)
}
