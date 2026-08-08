package reengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Per-title Blowfish keys for the "DSSS" save family, as documented by
// community reverse-engineering of each game's executable (see
// docs/dev.md) - these are fixed, hardcoded constants baked into each
// game's binary; they aren't derived from a Steam ID or any other
// per-save value the way some other RE Engine titles' schemes are.
//
// Verified against real saves: KeyRE2 (both platforms) and KeyRE3 (real
// Steam saves - all four slots decode with valid checksums and correct
// embedded slot numbers). KeyRE4 was recovered from the PS5 build's own
// eboot.bin and confirmed against a real PS5 save; see docs/dev-res2.md.
// KeyRE7/KeyRE8 remain untested - they belong to the same container
// family and cost nothing to carry, but treat them as unverified.
//
// A per-title key is not the whole story: which cipher a save uses varies
// by title *and* platform. RE4 on Steam doesn't use Blowfish at all (it
// uses an ElGamal+AES-OFB scheme keyed off the Steam account ID, so there
// is no fixed key to find), while its PS5 build does - KeyRE4 is
// PS5-only. Several titles' PS5 saves are not encrypted at all; see
// flagsSupported and Decode below.
var (
	KeyRE2 = []byte(`K<>$cl%isqA|~nV4W5~3z_Q)j]5DHdB9sb{cI9Hn&Gqc-zO8O6zf`)
	KeyRE3 = []byte(`mAz{]jeQ+uxyNH*d<Dt2kC5r=3M9RV6c$TaG[b|}^%&)En4F(Wvp`)
	KeyRE4 = []byte(`wa9Ui_tFKa_6E_D5gVChjM69xMKDX8QxEykYKhzb4cRNLknpCZUra`)
	KeyRE7 = []byte(`hHGb4nS653aRT29jy`)
	KeyRE8 = []byte(`j1lL1AOR31sd4HKJS90fs`)
)

// Save-container flags. Only Blowfish and HasID are implemented here;
// the rest are listed so an unsupported file can be refused by name
// rather than silently mis-read (a DEFLATE body would otherwise decode
// "successfully" into still-compressed bytes).
const (
	flagBlowfish = 0x1
	flagHasID    = 0x2
	flagCitrus   = 0x4
	flagDeflate  = 0x8
	// flagMandarin and RE4's "Lime" cipher share bit 0x10; which one a
	// file means is a property of the title, not the flag.
	flagMandarin = 0x10

	// flagsSupported is everything Decode/Build can faithfully round-trip.
	flagsSupported = flagBlowfish | flagHasID
)

func flagNames(flags uint32) string {
	var names []string
	for _, f := range []struct {
		bit  uint32
		name string
	}{
		{flagCitrus, "CITRUS"},
		{flagDeflate, "DEFLATE"},
		{flagMandarin, "MANDARIN/LIME"},
	} {
		if flags&f.bit != 0 {
			names = append(names, f.name)
		}
	}
	if rest := flags &^ (flagsSupported | flagCitrus | flagDeflate | flagMandarin); rest != 0 {
		names = append(names, fmt.Sprintf("unknown(%#x)", rest))
	}
	return strings.Join(names, ", ")
}

var dsssCheck = []byte("DSSSDSSS")

// Decoded is a parsed DSSS save file: the fully decrypted body (the
// still-proprietary RE Engine field serialization beneath - not
// interpreted any further here) plus the header facts worth surfacing.
type Decoded struct {
	SteamID    uint64 // the account ID embedded in the header, if HasID
	HasID      bool
	Body       []byte // decrypted, still-undecoded field data
	DataOffset int    // Body's offset within the original file, for reference
	HashValid  bool   // whether the trailing murmur3 checksum matched
}

// Decode parses a DSSS save file for the "Blowfish + HasID" title family
// (RE2/RE3/RE4/RE7/RE8's own container shape - other RE Engine titles use
// different encryption schemes entirely, e.g. RE4's Steam build's
// per-account "Lime" cipher, and aren't handled here). key is one of
// KeyRE2/KeyRE3/KeyRE4/KeyRE7/KeyRE8, or nil if flags is 0 (see below).
// This only decrypts the container down to its raw field bytes - the RE
// Engine object serialization inside Body (Capcom's own type/hash-tagged
// field format) isn't parsed further.
//
// flags == 0 is a distinct, unencrypted shape confirmed on real PS5 saves
// (RE3/RE7/RE Village): no blowfish_option field, no DSSSDSSS check
// block, no ID field - the body starts immediately after the 4-byte
// flags field, in the clear. key is ignored for this shape (pass nil).
func Decode(data []byte, key []byte) (Decoded, error) {
	if len(data) < 20 {
		return Decoded{}, errors.New("file too short to be a DSSS save")
	}
	if string(data[0:4]) != "DSSS" {
		return Decoded{}, errors.New("missing DSSS magic")
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 2 {
		return Decoded{}, fmt.Errorf("unsupported DSSS version %d (only 2 is known)", version)
	}
	flags := binary.LittleEndian.Uint32(data[8:12])
	// Refuse rather than mis-read: a body carrying any of these needs
	// decompression or a different cipher this package doesn't implement,
	// and returning it as-is would look like a successful decode.
	if extra := flags &^ flagsSupported; extra != 0 {
		return Decoded{}, fmt.Errorf("unsupported save flags %#x (%s) - only Blowfish/HasID, or 0 (unencrypted), are implemented", extra, flagNames(flags))
	}

	pos := 12
	var bodyEncrypted, hasID bool
	var steamID uint64

	if flags&flagBlowfish != 0 {
		blowfishOption := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4

		// bodyEncrypted tracks whether the payload (and the ID field) are
		// actually enciphered. A check block already sitting in the clear
		// means the whole file has been decrypted by something else - e.g.
		// a dump from another save editor - so nothing after it may be
		// decrypted again, or we'd hand back convincing-looking garbage.
		bodyEncrypted = true
		if blowfishOption > 0 {
			if pos+8 > len(data) {
				return Decoded{}, errors.New("truncated before DSSSDSSS check block")
			}
			check := append([]byte(nil), data[pos:pos+8]...)
			if string(check) == string(dsssCheck) {
				bodyEncrypted = false
			} else {
				if blowfishOption != 3 {
					return Decoded{}, fmt.Errorf("unsupported blowfish_option %d (only 3 is known)", blowfishOption)
				}
				decrypted, err := decryptBlowfishCBC(key, check)
				if err != nil {
					return Decoded{}, fmt.Errorf("decrypting DSSSDSSS check block: %w", err)
				}
				if string(decrypted) != string(dsssCheck) {
					return Decoded{}, errors.New("DSSSDSSS check failed after decrypt - wrong key for this title?")
				}
			}
			pos += 8
		} else {
			bodyEncrypted = false
		}

		hasID = flags&flagHasID != 0
		if hasID {
			pos = alignUp(pos, 8)
			if pos+8 > len(data) {
				return Decoded{}, errors.New("truncated before ID field")
			}
			idBytes := append([]byte(nil), data[pos:pos+8]...)
			if bodyEncrypted {
				decrypted, err := decryptBlowfishCBC(key, idBytes)
				if err != nil {
					return Decoded{}, fmt.Errorf("decrypting ID field: %w", err)
				}
				idBytes = decrypted
			}
			steamID = binary.LittleEndian.Uint64(idBytes)
			pos += 8
		}
	} else if flags != 0 {
		return Decoded{}, fmt.Errorf("flags %#x: unsupported combination (only 0, or flags including Blowfish, are implemented)", flags)
	}
	// flags == 0 falls through here with pos still at 12 and
	// bodyEncrypted/hasID/steamID at their zero values: unencrypted, no ID.

	dataOffset := pos
	if pos+4 > len(data) {
		return Decoded{}, errors.New("truncated before body")
	}
	payloadLen := len(data) - pos - 4 // minus the trailing murmur3 hash
	if payloadLen < 0 {
		return Decoded{}, errors.New("file too short for a body + trailing hash")
	}
	payload := data[pos : pos+payloadLen]
	// A payload's length is not required to be a multiple of Blowfish's
	// 8-byte block size: the cipher covers only the aligned prefix, and
	// RE Engine stores any remaining 1-7 bytes in the clear. Those bytes
	// are real content, not padding - on every real save observed they
	// carry the save's own slot number (0 for data000.bin, -1 for the
	// data00-1.bin global profile, 21 for data021Slot.bin), so they must
	// be carried through rather than dropped. Whether a given save's slot
	// number lands in the clear or inside the encrypted region is purely
	// a function of that save's payload length.
	body := make([]byte, 0, payloadLen)
	if bodyEncrypted {
		decrypted, err := decryptBlowfishCBC(key, payload)
		if err != nil {
			return Decoded{}, fmt.Errorf("decrypting body: %w", err)
		}
		body = append(body, decrypted...)
		body = append(body, payload[len(decrypted):]...)
	} else {
		body = append(body, payload...)
	}

	storedHash := binary.LittleEndian.Uint32(data[len(data)-4:])
	computedHash := murmur3_32(data[:len(data)-4], 0xffffffff)

	return Decoded{
		SteamID:    steamID,
		HasID:      hasID,
		Body:       body,
		DataOffset: dataOffset,
		HashValid:  storedHash == computedHash,
	}, nil
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func alignUp(value, align int) int {
	rem := value % align
	if rem == 0 {
		return value
	}
	return value + (align - rem)
}
