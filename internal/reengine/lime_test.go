package reengine

import (
	"bytes"
	"math/big"
	"testing"
)

// TestLimeEncodeDecodeRoundTrip covers a multi-block body (Lime blocks
// hold 4096 bytes each), confirming the block-splitting logic in both
// directions. Confirmed separately against two real Steam RE4 saves
// (every block's SHA3-256 checksum verifies, and the reconstructed body
// parses as valid RSZ) - see docs/dev-res2.md; this fixture pins the
// mechanism with fast, synthetic data.
func TestLimeEncodeDecodeRoundTrip(t *testing.T) {
	body := bytes.Repeat([]byte("SAVEDATA"), 600) // 4800 bytes, spans 2 blocks
	const steamID = 11052978

	data, err := LimeEncode(body, steamID)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[0:4]) != "DSSS" {
		t.Fatalf("missing DSSS magic")
	}

	dec, err := LimeDecode(data, steamID)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.HashValid {
		t.Error("hash invalid")
	}
	if !bytes.Equal(dec.Body, body) {
		t.Fatalf("body mismatch: got %d bytes, want %d", len(dec.Body), len(body))
	}
}

func TestLimeEncodeDecodeShortBody(t *testing.T) {
	body := []byte{1, 2, 3, 4}
	data, err := LimeEncode(body, 42)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := LimeDecode(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec.Body, body) {
		t.Fatalf("body mismatch: got %x, want %x", dec.Body, body)
	}
}

func TestLimeDecodeRejectsWrongSteamID(t *testing.T) {
	data, err := LimeEncode([]byte("hello world, this is a test body for lime encoding"), 11052978)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LimeDecode(data, 99999999); err == nil {
		t.Fatal("expected a wrong Steam ID to fail")
	}
}

func TestLimeDecodeRejectsBadMagic(t *testing.T) {
	data, err := LimeEncode([]byte("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if _, err := LimeDecode(data, 1); err == nil {
		t.Fatal("expected bad magic to be rejected")
	}
}

// TestLimeDecodeRejectsNonLimeFlags covers handing a plain-Blowfish
// container to LimeDecode - it must refuse rather than misinterpret the
// bytes as Lime blocks.
func TestLimeDecodeRejectsNonLimeFlags(t *testing.T) {
	data, err := Build(bytes.Repeat([]byte("X"), 8), KeyRE2, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LimeDecode(data, 1); err == nil {
		t.Fatal("expected non-Lime flags to be rejected")
	}
}

// TestLimeDecodeDetectsTamperedTrailer covers the outer murmur3 hash
// specifically (as opposed to a per-block SHA3 checksum, which fails
// decoding outright - see TestLimeDecodeRejectsWrongSteamID, which
// exercises that path since a wrong key corrupts every block's AES
// output).
func TestLimeDecodeDetectsTamperedTrailer(t *testing.T) {
	data, err := LimeEncode([]byte("tamper test body, long enough to be realistic"), 7)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	dec, err := LimeDecode(data, 7)
	if err != nil {
		t.Fatal(err)
	}
	if dec.HashValid {
		t.Fatal("expected HashValid=false after corrupting the trailing hash")
	}
}

func TestLimeDecodeRejectsTruncatedFile(t *testing.T) {
	if _, err := LimeDecode([]byte("DSSS"), 1); err == nil {
		t.Fatal("expected a truncated file to be rejected")
	}
}

// TestLimeExponentDerivation pins the exact per-account key derivation:
// bitwise-NOT of the low 32 bits of the Steam ID, reduced mod Q - not
// the raw 64-bit ID, which was the first (wrong) hypothesis tried
// against real data before finding this in the community reference
// source (see docs/dev-res2.md).
func TestLimeExponentDerivation(t *testing.T) {
	u := limeExponent(11052978)
	masked := uint64(11052978) & 0xffffffff
	want := new(big.Int).SetUint64(^masked)
	want.Mod(want, limeQ)
	if u.Cmp(want) != 0 {
		t.Fatalf("limeExponent(11052978) = %s, want %s", u.String(), want.String())
	}
}
