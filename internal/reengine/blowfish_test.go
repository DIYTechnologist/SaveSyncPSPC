package reengine

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/blowfish"
)

// TestUnderlyingBlowfishKnownVector sanity-checks the golang.org/x/crypto
// dependency itself against Bruce Schneier's original published
// zero-key/zero-plaintext Blowfish test vector (standard big-endian
// Blowfish, independent of this package's LE wrapping).
func TestUnderlyingBlowfishKnownVector(t *testing.T) {
	c, err := blowfish.NewCipher(make([]byte, 8))
	if err != nil {
		t.Fatal(err)
	}
	var dst [8]byte
	c.Encrypt(dst[:], make([]byte, 8))
	want := []byte{0x4e, 0xf9, 0x97, 0x45, 0x61, 0x98, 0xdd, 0x78}
	if !bytes.Equal(dst[:], want) {
		t.Fatalf("got %x, want %x", dst, want)
	}
}

func TestBlowfishLERoundTripsSingleBlock(t *testing.T) {
	key := KeyRE2
	c, err := newBlowfishLE(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("DSSSDSSS")
	var enc, dec [8]byte
	c.encryptBlock(enc[:], plain)
	c.decryptBlock(dec[:], enc[:])
	if !bytes.Equal(dec[:], plain) {
		t.Fatalf("round trip mismatch: got %x, want %x", dec, plain)
	}
}

func TestDecryptBlowfishCBCRoundTrips(t *testing.T) {
	key := KeyRE2
	plain := bytes.Repeat([]byte("0123456789ABCDEF"), 17) // 272 bytes, multiple of 8
	encrypted, err := encryptBlowfishCBC(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, plain) {
		t.Fatal("ciphertext should not equal plaintext")
	}
	decrypted, err := decryptBlowfishCBC(key, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("round trip mismatch: got %q, want %q", decrypted, plain)
	}
}

func TestDecryptBlowfishCBCTruncatesToBlockAlignment(t *testing.T) {
	key := KeyRE2
	plain := append(bytes.Repeat([]byte("A"), 16), []byte("xyz")...) // 19 bytes, not block-aligned
	encrypted, err := encryptBlowfishCBC(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != 16 {
		t.Fatalf("expected output truncated to 16 bytes (2 blocks), got %d", len(encrypted))
	}
}

func TestDifferentKeysProduceDifferentCiphertext(t *testing.T) {
	plain := []byte("DSSSDSSS")
	a, err := encryptBlowfishCBC(KeyRE2, plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptBlowfishCBC(KeyRE3, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("different keys produced identical ciphertext")
	}
}
