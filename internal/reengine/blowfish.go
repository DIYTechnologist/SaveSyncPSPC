// Package reengine decodes the "DSSS" save-container format Capcom's RE
// Engine uses (Resident Evil 2/3/7/8 remakes and others). The container
// shape and cipher scheme here are public, community-documented facts
// (see docs/dev.md) - independently confirmed against a real RE2 (2019)
// PC save this session, not ported from any other tool's source.
package reengine

import (
	"golang.org/x/crypto/blowfish"
)

// blowfishLE wraps the standard (big-endian) Blowfish block cipher from
// golang.org/x/crypto/blowfish to emulate the little-endian block
// convention RE Engine actually uses (documented elsewhere as
// "BlowfishLE"). Blowfish's Feistel rounds and P-array/S-box
// initialization operate on two 32-bit halves of each 8-byte block; the
// only difference between the "LE" and standard "BE" variants is whether
// each 4-byte half is read/written as a little- or big-endian integer.
// Reversing the byte order of each half before and after an otherwise
// -standard big-endian block operation produces the identical result a
// native little-endian implementation would, since byte order is purely
// a serialization convention around the same underlying 32-bit values.
type blowfishLE struct {
	cipher *blowfish.Cipher
}

func newBlowfishLE(key []byte) (*blowfishLE, error) {
	c, err := blowfish.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &blowfishLE{cipher: c}, nil
}

func reverse4(b []byte) {
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
}

func swapBlockHalves(block []byte) {
	reverse4(block[0:4])
	reverse4(block[4:8])
}

func (c *blowfishLE) encryptBlock(dst, src []byte) {
	var tmp [8]byte
	copy(tmp[:], src[:8])
	swapBlockHalves(tmp[:])
	c.cipher.Encrypt(tmp[:], tmp[:])
	swapBlockHalves(tmp[:])
	copy(dst[:8], tmp[:])
}

func (c *blowfishLE) decryptBlock(dst, src []byte) {
	var tmp [8]byte
	copy(tmp[:], src[:8])
	swapBlockHalves(tmp[:])
	c.cipher.Decrypt(tmp[:], tmp[:])
	swapBlockHalves(tmp[:])
	copy(dst[:8], tmp[:])
}

// decryptBlowfishCBC decrypts data under key using Blowfish-LE in CBC
// mode with an all-zero IV and no padding, matching RE Engine's usage
// exactly: a length not a multiple of 8 is truncated to the last full
// block, the same way the reference implementation's NoPadding mode
// does, rather than treated as an error.
func decryptBlowfishCBC(key, data []byte) ([]byte, error) {
	c, err := newBlowfishLE(key)
	if err != nil {
		return nil, err
	}
	n := len(data) - len(data)%8
	out := make([]byte, n)
	var prev [8]byte // zero IV
	for i := 0; i < n; i += 8 {
		block := data[i : i+8]
		var decrypted [8]byte
		c.decryptBlock(decrypted[:], block)
		for j := 0; j < 8; j++ {
			decrypted[j] ^= prev[j]
		}
		copy(out[i:i+8], decrypted[:])
		copy(prev[:], block)
	}
	return out, nil
}

// encryptBlowfishCBC is decryptBlowfishCBC's inverse, for round-tripping
// a re-encoded save back into the same container shape.
func encryptBlowfishCBC(key, data []byte) ([]byte, error) {
	c, err := newBlowfishLE(key)
	if err != nil {
		return nil, err
	}
	n := len(data) - len(data)%8
	out := make([]byte, n)
	var prev [8]byte // zero IV
	for i := 0; i < n; i += 8 {
		var xored [8]byte
		for j := 0; j < 8; j++ {
			xored[j] = data[i+j] ^ prev[j]
		}
		var encrypted [8]byte
		c.encryptBlock(encrypted[:], xored[:])
		copy(out[i:i+8], encrypted[:])
		copy(prev[:], encrypted[:])
	}
	return out, nil
}
