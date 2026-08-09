package reengine

import (
	"bytes"
	"math/big"
	"testing"
)

// TestMandarinEncodeDecodeRoundTrip covers a multi-block body (blocks
// hold 1-8 units of 0x4000 bytes each, chosen by the SplitMix64
// sequence), confirming the block-splitting logic in both directions.
// Confirmed separately against a real Steam Deck Resident Evil Requiem
// save (every block's CityHash64 checksum verifies, and the trailing
// murmur3 file hash matches) - see TODO.md's Requiem entry; this
// fixture pins the mechanism with fast, synthetic data.
func TestMandarinEncodeDecodeRoundTrip(t *testing.T) {
	body := bytes.Repeat([]byte("SAVEDATA"), 20000) // 160,000 bytes, spans several blocks
	const key = 76561197971318706

	data, err := MandarinEncode(body, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[0:4]) != "DSSS" {
		t.Fatalf("missing DSSS magic")
	}

	dec, err := MandarinDecode(data, key)
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

func TestMandarinEncodeDecodeShortBody(t *testing.T) {
	body := []byte{1, 2, 3, 4}
	data, err := MandarinEncode(body, 42)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := MandarinDecode(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec.Body, body) {
		t.Fatalf("body mismatch: got %x, want %x", dec.Body, body)
	}
}

func TestMandarinDecodeRejectsWrongKey(t *testing.T) {
	data, err := MandarinEncode([]byte("hello world, this is a test body for mandarin encoding"), 76561197971318706)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MandarinDecode(data, 99999999); err == nil {
		t.Fatal("expected a wrong key to fail")
	}
}

func TestMandarinDecodeRejectsBadMagic(t *testing.T) {
	data, err := MandarinEncode([]byte("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if _, err := MandarinDecode(data, 1); err == nil {
		t.Fatal("expected bad magic to be rejected")
	}
}

// TestMandarinDecodeRejectsNonMandarinFlags covers handing a plain
// container to MandarinDecode - it must refuse rather than misinterpret
// the bytes as Mandarin blocks.
func TestMandarinDecodeRejectsNonMandarinFlags(t *testing.T) {
	data, err := Build(bytes.Repeat([]byte("X"), 8), KeyRE2, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MandarinDecode(data, 1); err == nil {
		t.Fatal("expected non-Mandarin flags to be rejected")
	}
}

// TestMandarinDecodeDetectsTamperedTrailer covers the outer murmur3 hash
// specifically (as opposed to a per-block CityHash64 checksum, which
// fails decoding outright - see TestMandarinDecodeRejectsWrongKey, which
// exercises that path since a wrong key corrupts every block's AES
// output).
func TestMandarinDecodeDetectsTamperedTrailer(t *testing.T) {
	data, err := MandarinEncode([]byte("tamper test body, long enough to be realistic"), 7)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	dec, err := MandarinDecode(data, 7)
	if err != nil {
		t.Fatal(err)
	}
	if dec.HashValid {
		t.Fatal("expected HashValid=false after corrupting the trailing hash")
	}
}

func TestMandarinDecodeRejectsTruncatedFile(t *testing.T) {
	if _, err := MandarinDecode([]byte("DSSS"), 1); err == nil {
		t.Fatal("expected a truncated file to be rejected")
	}
}

// TestMandarinExponentDerivation pins the exact per-account key
// derivation: bitwise-NOT of the *full* 64-bit key, reduced mod Q - no
// 32-bit masking, unlike Lime's exponent (see TestLimeExponentDerivation)
// - confirmed against a real save using the raw SteamID64.
func TestMandarinExponentDerivation(t *testing.T) {
	u := mandarinExponent(76561197971318706)
	want := new(big.Int).SetUint64(^uint64(76561197971318706))
	want.Mod(want, limeQ)
	if u.Cmp(want) != 0 {
		t.Fatalf("mandarinExponent(...) = %s, want %s", u.String(), want.String())
	}
}

func TestCityHash64ReferenceVector(t *testing.T) {
	got := cityHash64([]byte("world"))
	want := uint64(16436542438370751598)
	if got != want {
		t.Fatalf("cityHash64(world) = %d, want %d", got, want)
	}
}

// TestMandarinKeyOracleRecoversKey pins the key-discovery oracle: it must
// identify the key a file was built with, without trial-decrypting it.
func TestMandarinKeyOracleRecoversKey(t *testing.T) {
	const key = 76561197971318706
	data, err := MandarinEncode(bytes.Repeat([]byte("SAVEDATA"), 4096), key)
	if err != nil {
		t.Fatal(err)
	}
	mask, state := MandarinKeyOracle(data)
	if got := MandarinMaskFor(state, key); got != mask {
		t.Fatalf("oracle mask %#x doesn't match the real key's %#x", mask, got)
	}
	if got := MandarinMaskFor(state, key+1); got == mask {
		t.Fatal("a wrong key produced the same mask")
	}
}
