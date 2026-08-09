package reengine

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/sha3"
)

// Lime is the cipher RE4 (2023)'s *Steam* build uses instead of plain
// Blowfish - a lightweight ElGamal-style scheme that derives a per-block
// AES-128 key+IV from an account-bound exponent, AES-128-OFB encrypts
// each 4096-byte chunk, and SHA3-256 checksums each block's plaintext.
// The "private key" is not a secret to discover: it's a public formula,
// the bitwise complement of the low 32 bits of the account's Steam ID.
// This makes the scheme account-binding, not confidentiality-grade
// cryptography.
//
// Format facts (the P/Q/R group constants, the block layout, the
// exponent derivation) were cross-referenced against
// kvasszn/ree-save-editor's public Rust source for facts only, the same
// standard applied to RE2's Blowfish-LE elsewhere in this package (see
// docs/ressave.md's "What's been formally reused vs. reimplemented") -
// this Go implementation is original, not ported. Independently
// confirmed against two real Steam RE4 saves this session: every
// block's SHA3-256 checksum verifies, and the reconstructed body parses
// as valid RSZ at LimeDataOffset. See docs/dev-res2.md.
const (
	limeBlockSize    = 0x1220 // 512 (enc keys) + 4096 (data) + 32 (checksum)
	limeDataSize     = 0x1000
	limeEncKeySize   = 512 // 4 pairs x 128 bytes
	limePairSize     = 128 // two 64-byte big-endian-reversed integers
	limeChecksumSize = 32
	limeTailSize     = 0x80 // unused trailing bytes after the last block ("pretty sure its just an RSA mac" - kvasszn); this package writes zeros and never reads them back

	// LimeDataOffset is the RSZ alignment base for a Lime-decrypted body.
	// Confirmed against real saves: it is the file offset where the
	// encrypted region begins (the 12-byte header, aligned up to 16) -
	// the game reconstructs the decrypted body in place at that same
	// file position, so RSZ's file-offset-relative alignment rule (see
	// convert.go) uses it too.
	LimeDataOffset = 0x10
)

// Group constants for the ElGamal-style scheme, and the fixed public
// exponent every pair is encrypted under. Values are public, baked into
// the game binary - not secret.
var (
	limeP = leBytesToBigInt(mustHex("f33b6fb972a0b72515e45c391829e182ad8a9bdc0a64d3444d79c810ab863717"))
	limeQ = leBytesToBigInt(mustHex("f99db75c39d0db920a72ae1c8c9470c156c54d6e05b269a2a63c648855c39b0b"))
	limeR = leBytesToBigInt(mustHex("e66f544afcce68c5ef07b9a07b277585344a1db61376e831f73b9fbd5f44f715"))
	limeE = big.NewInt(0x14)
	// limeX0 = r^e mod p is independent of both account and plaintext -
	// every single encrypted pair in every Lime save uses this same
	// value as its first 64-byte component. Precomputed once.
	limeX0 = new(big.Int).Exp(limeR, limeE, limeP)
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// leBytesToBigInt interprets b as a little-endian integer (matching the
// community source's own bytes_to_int for this scheme, not Go's
// big.Int.SetBytes default of big-endian).
func leBytesToBigInt(b []byte) *big.Int {
	be := make([]byte, len(b))
	for i, v := range b {
		be[len(b)-1-i] = v
	}
	return new(big.Int).SetBytes(be)
}

// bigIntToLEBytes renders n as a little-endian byte slice of exactly
// size bytes, truncating (from the most-significant end) if n doesn't
// fit - callers choose size large enough that this never happens for
// real values.
func bigIntToLEBytes(n *big.Int, size int) []byte {
	be := n.Bytes()
	out := make([]byte, size)
	for i := 0; i < len(be) && i < size; i++ {
		out[i] = be[len(be)-1-i]
	}
	return out
}

// limeExponent derives the per-account private exponent: bitwise-NOT of
// the low 32 bits of the Steam ID (as a 64-bit value), reduced mod Q.
func limeExponent(steamID uint64) *big.Int {
	masked := steamID & 0xffffffff
	notMasked := ^masked
	u := new(big.Int).SetUint64(notMasked)
	return u.Mod(u, limeQ)
}

// limeDecryptPair recovers the 8-byte plaintext chunk a (c0, c1) pair
// encodes, given the account exponent u: x = c0^u mod p (which equals
// the x1 the encrypting side computed, since (r^e)^u == (r^u)^e mod p),
// then k = c1 / x - an exact integer division since c1 was formed as
// x1*plaintext with no modular reduction.
func limeDecryptPair(c0, c1 []byte, u *big.Int) []byte {
	x0 := leBytesToBigInt(c0)
	ct := leBytesToBigInt(c1)
	x := new(big.Int).Exp(x0, u, limeP)
	if x.Sign() == 0 {
		return make([]byte, 8)
	}
	k := new(big.Int).Div(ct, x)
	return bigIntToLEBytes(k, 8)
}

// limeEncryptPair is limeDecryptPair's inverse: encodes an 8-byte
// plaintext chunk for the account exponent u.
func limeEncryptPair(pt []byte, u *big.Int) (c0, c1 []byte) {
	s := new(big.Int).Exp(limeR, u, limeP)
	x1 := new(big.Int).Exp(s, limeE, limeP)
	ptInt := leBytesToBigInt(pt)
	ct := new(big.Int).Mul(x1, ptInt)
	return bigIntToLEBytes(limeX0, 64), bigIntToLEBytes(ct, 64)
}

func limeAESOFB(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	stream := cipher.NewOFB(block, iv)
	out := make([]byte, len(data))
	stream.XORKeyStream(out, data)
	return out, nil
}

// LimeDecoded is a parsed Lime-cipher DSSS save (RE4's Steam shape).
type LimeDecoded struct {
	Body      []byte // decrypted, still-undecoded RSZ field data, truncated to its real length
	HashValid bool
}

// LimeHeaderValid does a structural-only check of a Lime-cipher DSSS
// file's header (magic, version, flags) without needing the account ID
// LimeDecode requires to actually decrypt it - for callers (like
// engine.Engine.Inspect) that only need to confirm "this is shaped like
// a Lime save," not decrypt it.
func LimeHeaderValid(data []byte) error {
	if len(data) < LimeDataOffset+limeTailSize+8+4 {
		return errors.New("file too short to be a Lime DSSS save")
	}
	if string(data[0:4]) != "DSSS" {
		return errors.New("missing DSSS magic")
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 2 {
		return fmt.Errorf("unsupported DSSS version %d (only 2 is known)", version)
	}
	flags := binary.LittleEndian.Uint32(data[8:12])
	if flags != flagMandarin {
		return fmt.Errorf("flags %#x aren't the Lime shape (want %#x)", flags, flagMandarin)
	}
	return nil
}

// LimeDecode parses and decrypts a Lime-cipher DSSS save. steamID must
// be the account the save belongs to - a wrong one produces a checksum
// mismatch on the very first block, reported as an error rather than
// silently returning garbage.
func LimeDecode(data []byte, steamID uint64) (LimeDecoded, error) {
	if err := LimeHeaderValid(data); err != nil {
		return LimeDecoded{}, err
	}

	decryptedLen := binary.LittleEndian.Uint64(data[len(data)-12 : len(data)-4])
	numBlocks := int((decryptedLen + limeDataSize - 1) / limeDataSize)
	need := LimeDataOffset + numBlocks*limeBlockSize
	if need > len(data) {
		return LimeDecoded{}, fmt.Errorf("file too short for %d Lime blocks (decrypted_len=%d)", numBlocks, decryptedLen)
	}

	u := limeExponent(steamID)
	body := make([]byte, 0, numBlocks*limeDataSize)
	for i := 0; i < numBlocks; i++ {
		block := data[LimeDataOffset+i*limeBlockSize : LimeDataOffset+(i+1)*limeBlockSize]
		encKey := block[:limeEncKeySize]
		encData := block[limeEncKeySize : limeEncKeySize+limeDataSize]
		checksum := block[limeEncKeySize+limeDataSize : limeEncKeySize+limeDataSize+limeChecksumSize]

		var keyIV [32]byte
		for p := 0; p < 4; p++ {
			pair := encKey[p*limePairSize : (p+1)*limePairSize]
			k := limeDecryptPair(pair[:64], pair[64:128], u)
			copy(keyIV[p*8:], k)
		}

		plain, err := limeAESOFB(keyIV[:16], keyIV[16:32], encData)
		if err != nil {
			return LimeDecoded{}, fmt.Errorf("block %d: %w", i, err)
		}
		computed := sha3.Sum256(plain)
		if !bytes.Equal(computed[:], checksum) {
			return LimeDecoded{}, fmt.Errorf("block %d: checksum mismatch - wrong Steam ID, or the save is corrupt", i)
		}
		body = append(body, plain...)
	}
	body = body[:decryptedLen]

	storedHash := binary.LittleEndian.Uint32(data[len(data)-4:])
	computedHash := murmur3_32(data[:len(data)-4], 0xffffffff)

	return LimeDecoded{Body: body, HashValid: storedHash == computedHash}, nil
}

// LimeEncode builds a Lime-cipher DSSS file (RE4's Steam shape) around
// body (RSZ field data serialized at LimeDataOffset). steamID selects
// which account's key the file will decrypt with - it must be the
// account that will load it, or LimeDecode will reject every block.
func LimeEncode(body []byte, steamID uint64) ([]byte, error) {
	numBlocks := (len(body) + limeDataSize - 1) / limeDataSize
	u := limeExponent(steamID)

	var buf bytes.Buffer
	buf.WriteString("DSSS")
	writeU32(&buf, 2)
	writeU32(&buf, flagMandarin)
	writeU32(&buf, 0) // alignment padding to a 16-byte boundary; the reader only seeks past this, never reads it

	for i := 0; i < numBlocks; i++ {
		start := i * limeDataSize
		end := start + limeDataSize
		var chunk [limeDataSize]byte
		if end > len(body) {
			copy(chunk[:], body[start:])
		} else {
			copy(chunk[:], body[start:end])
		}

		var keyIV [32]byte
		if _, err := rand.Read(keyIV[:]); err != nil {
			return nil, fmt.Errorf("generating block key: %w", err)
		}

		checksum := sha3.Sum256(chunk[:])

		encData, err := limeAESOFB(keyIV[:16], keyIV[16:32], chunk[:])
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}

		for p := 0; p < 4; p++ {
			c0, c1 := limeEncryptPair(keyIV[p*8:(p+1)*8], u)
			buf.Write(c0)
			buf.Write(c1)
		}
		buf.Write(encData)
		buf.Write(checksum[:])
	}

	buf.Write(make([]byte, limeTailSize))
	var lenBytes [8]byte
	binary.LittleEndian.PutUint64(lenBytes[:], uint64(len(body)))
	buf.Write(lenBytes[:])

	hash := murmur3_32(buf.Bytes(), 0xffffffff)
	var hashBytes [4]byte
	binary.LittleEndian.PutUint32(hashBytes[:], hash)
	buf.Write(hashBytes[:])

	return buf.Bytes(), nil
}
