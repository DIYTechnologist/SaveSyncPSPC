package reengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// BuildOptions controls the header shape Build produces.
type BuildOptions struct {
	// HasID includes an encrypted account-ID field in the header (the PC
	// build's shape - RE Engine's Steam-account verification). PS5 saves
	// observed this session don't set this: account identity there comes
	// from the PS5 container itself (Garlic's sce_sys/param.sfo), not
	// from inside the .bin payload.
	HasID   bool
	SteamID uint64
}

// Build assembles a fresh DSSS container around body (already-decrypted,
// still-encoded RSZ field data - see Decode's doc comment) using the
// Blowfish+HasID title family's shape (blowfish_option=3, i.e. every
// real PC/PS5 RE2/RE3/RE7/RE8 save observed this session). body may be
// any length: as with the game's own writer, Blowfish covers only the
// 8-byte-aligned prefix and any trailing remainder is stored in the
// clear (see Decode).
func Build(body []byte, key []byte, opts BuildOptions) ([]byte, error) {
	// The game writer zero-pads the file to a 4-byte boundary before
	// checksumming, which means a body that isn't already 4-aligned
	// cannot be represented losslessly: it would read back longer than
	// it was written. Every real save's body is 4-aligned (RSZ fields
	// are 4-aligned and the trailing slot number is 4 bytes), so this
	// only rejects synthesized input, and rejecting beats silently
	// changing the caller's data.
	if len(body)%4 != 0 {
		return nil, fmt.Errorf("body length %d isn't 4-byte aligned; the format pads to 4 and could not round-trip it unchanged", len(body))
	}

	var buf bytes.Buffer
	buf.WriteString("DSSS")
	writeU32(&buf, 2) // version
	flags := uint32(flagBlowfish)
	if opts.HasID {
		flags |= flagHasID
	}
	writeU32(&buf, flags)
	writeU32(&buf, 3) // blowfish_option

	encCheck, err := encryptBlowfishCBC(key, dsssCheck)
	if err != nil {
		return nil, fmt.Errorf("encrypting DSSSDSSS check block: %w", err)
	}
	buf.Write(encCheck)

	if opts.HasID {
		var idPlain [8]byte
		binary.LittleEndian.PutUint64(idPlain[:], opts.SteamID)
		encID, err := encryptBlowfishCBC(key, idPlain[:])
		if err != nil {
			return nil, fmt.Errorf("encrypting ID field: %w", err)
		}
		buf.Write(encID)
	}

	aligned := len(body) - len(body)%8
	encBody, err := encryptBlowfishCBC(key, body[:aligned])
	if err != nil {
		return nil, fmt.Errorf("encrypting body: %w", err)
	}
	buf.Write(encBody)
	buf.Write(body[aligned:]) // trailing remainder stays in the clear

	hash := murmur3_32(buf.Bytes(), 0xffffffff)
	var hashBytes [4]byte
	binary.LittleEndian.PutUint32(hashBytes[:], hash)
	buf.Write(hashBytes[:])

	return buf.Bytes(), nil
}

// ConvertPCToPS5 rewrites a PC-shaped DSSS save (Blowfish+HasID) into a
// PS5-shaped one (Blowfish only, no ID field) - the container-level
// shape difference confirmed this session between real PC and PS5 RE2
// saves. The decrypted field data itself is carried through unchanged;
// whether that's sufficient for the game to accept the result (as
// opposed to something platform-specific also living inside the RSZ
// field data itself, which this package doesn't parse) hasn't been
// confirmed and needs a real device test.
func ConvertPCToPS5(pcData []byte, key []byte) ([]byte, error) {
	decoded, err := Decode(pcData, key)
	if err != nil {
		return nil, fmt.Errorf("decoding PC save: %w", err)
	}
	// Converting a file whose own checksum doesn't match would launder
	// corrupt input into output carrying a freshly-valid one, hiding the
	// damage from every check downstream.
	if !decoded.HashValid {
		return nil, errors.New("refusing to convert: source save's checksum doesn't match its contents (file is corrupt or truncated)")
	}
	return Build(decoded.Body, key, BuildOptions{HasID: false})
}
