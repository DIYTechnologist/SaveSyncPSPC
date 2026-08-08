package reengine

import (
	"errors"
	"fmt"
)

// Body offsets for each platform's container shape. A PC save carries an
// account-ID field and so starts its body at 0x20 - true for every
// encrypted+HasID title observed (RE2, RE3, RE4). A PS5 save has no ID
// field; its offset depends on whether that title's PS5 build is
// encrypted (0x18, RE2/RE4) or not (0xc, RE3/RE7/RE Village - see
// TitleConfig.PS5Unencrypted). The difference matters far beyond the
// header: RSZ field alignment is computed against the body's absolute
// file offset, so a body cannot be moved between offsets without
// re-serializing it.
const (
	PCDataOffset         = 0x20
	PS5DataOffset        = 0x18 // encrypted PS5 shape (RE2, RE4)
	ps5UnencryptedOffset = 0xc  // unencrypted PS5 shape (RE3, RE7, RE Village)
)

// The platform-identity fields' *names* are shared across every RE
// Engine title sampled so far - fieldPlatformEnum/fieldPlatformBool hash
// the same regardless of title - but the *class* that contains them is
// title-specific (0x8b7dd7a1 for RE2, 0x4a5aa7b for RE3), presumably
// because each game has its own top-level settings type. See
// TitleConfig.PlatformClass.
const (
	fieldPlatformEnum = 0xb41fa365 // PC = 3, PS5 = 2
	fieldPlatformBool = 0xe231b945 // PC = true, PS5 = false
)

type platformValues struct {
	enum int64
	flag bool
}

var (
	pcPlatform  = platformValues{enum: 3, flag: true}
	ps5Platform = platformValues{enum: 2, flag: false}
)

// TitleConfig bundles the per-RE-Engine-title facts a conversion needs.
// Confirmed so far: RE2 (Key, PlatformClass 0x8b7dd7a1, PS5Unencrypted
// false) and RE3 (Key, PlatformClass 0x4a5aa7b, PS5Unencrypted true).
type TitleConfig struct {
	// Key is this title's Blowfish key. Used for the PC side always, and
	// for the PS5 side too unless PS5Unencrypted.
	Key []byte
	// PlatformClass is this title's settings class hash carrying the two
	// platform-identity fields (fieldPlatformEnum/fieldPlatformBool). Zero
	// means the title has no such field at all - confirmed for RE7 by
	// searching every class in a real PC and a real PS5 save (thousands
	// of classes checked, zero matches) - in which case convert skips
	// retargeting entirely and relies on the container-level differences
	// (encryption, ID field, body offset) alone.
	PlatformClass uint32
	// PS5Unencrypted selects the flags=0 PS5 container shape confirmed on
	// real RE3/RE7/RE Village saves (no blowfish_option field, no
	// DSSSDSSS check block, no ID field, body at ps5UnencryptedOffset) in
	// place of the encrypted-no-ID shape RE2/RE4 use.
	PS5Unencrypted bool
}

func (t TitleConfig) ps5DataOffset() int {
	if t.PS5Unencrypted {
		return ps5UnencryptedOffset
	}
	return PS5DataOffset
}

// retargetPlatform rewrites the platform-identifying fields in place,
// reporting how many it changed so callers can fail loudly if a save
// doesn't have the expected shape rather than silently shipping one that
// will be rejected on the console.
func retargetPlatform(cls *RSZClass, to platformValues, platformClass uint32) int {
	n := 0
	if cls.Hash == platformClass {
		for i := range cls.Fields {
			switch cls.Fields[i].Hash {
			case fieldPlatformEnum:
				cls.Fields[i].Value.Int = to.enum
				n++
			case fieldPlatformBool:
				cls.Fields[i].Value.Bool = to.flag
				n++
			}
		}
	}
	for i := range cls.Fields {
		if c := cls.Fields[i].Value.Class; c != nil {
			n += retargetPlatform(c, to, platformClass)
		}
		if a := cls.Fields[i].Value.Array; a != nil {
			for j := range a.Values {
				if c := a.Values[j].Class; c != nil {
					n += retargetPlatform(c, to, platformClass)
				}
			}
		}
	}
	return n
}

// convert rebuilds a save for the other platform: re-serializing the
// field data for the destination's body offset and retargeting the
// platform fields. The save's trailing slot number is carried through
// unchanged - it identifies which slot the file belongs to, and the
// output must be written into the container for that same slot.
func convert(data, srcKey []byte, dstOffset int, dstPlatform platformValues, dstHasID, dstUnencrypted bool, dstKey []byte, steamID uint64, platformClass uint32) ([]byte, error) {
	dec, err := Decode(data, srcKey)
	if err != nil {
		return nil, fmt.Errorf("decoding source save: %w", err)
	}
	if !dec.HashValid {
		return nil, errors.New("refusing to convert: source save's checksum doesn't match its contents (file is corrupt or truncated)")
	}

	objs, err := ReadRSZObjects(dec.Body, dec.DataOffset)
	if err != nil {
		return nil, fmt.Errorf("parsing source save's field data: %w", err)
	}

	if platformClass != 0 {
		patched := 0
		for i := range objs {
			patched += retargetPlatform(&objs[i].Class, dstPlatform, platformClass)
		}
		if patched != 2 {
			return nil, fmt.Errorf("expected to retarget exactly 2 platform fields, found %d - this save's layout isn't the one this converter was built against", patched)
		}
	}

	// Re-emit at the source's own offset first, purely to learn where the
	// object section ends: everything after it is trailing data (the slot
	// number, plus any slack) that must be carried through verbatim.
	probe, err := WriteRSZObjects(objs, dec.DataOffset, nil)
	if err != nil {
		return nil, fmt.Errorf("re-serializing source save: %w", err)
	}
	if len(probe) > len(dec.Body) {
		return nil, fmt.Errorf("re-serialized field data is longer than the source (%d > %d); layout was not reproduced faithfully", len(probe), len(dec.Body))
	}
	trailer := dec.Body[len(probe):]

	body, err := WriteRSZObjects(objs, dstOffset, trailer)
	if err != nil {
		return nil, fmt.Errorf("re-serializing for the destination: %w", err)
	}

	out, err := Build(body, dstKey, BuildOptions{Unencrypted: dstUnencrypted, HasID: dstHasID, SteamID: steamID})
	if err != nil {
		return nil, err
	}

	// The output must parse back cleanly at its new offset; if it doesn't,
	// the console would be the one to find out.
	verify, err := Decode(out, dstKey)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to decode: %w", err)
	}
	if verify.DataOffset != dstOffset {
		return nil, fmt.Errorf("converted save's body landed at %#x, expected %#x", verify.DataOffset, dstOffset)
	}
	reobjs, err := ReadRSZObjects(verify.Body, verify.DataOffset)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to re-parse: %w", err)
	}
	if len(reobjs) != len(objs) {
		return nil, fmt.Errorf("converted save has %d objects, source had %d", len(reobjs), len(objs))
	}
	return out, nil
}

// ConvertPCToPS5 rewrites a PC (Steam) save into one a PS5 will load, per
// t's title-specific facts. Confirmed working against a real console for
// RE2 (encrypted PS5 shape); RE3's unencrypted PS5 shape round-trips
// against real saves but hasn't itself been confirmed in-game yet.
func (t TitleConfig) ConvertPCToPS5(pcData []byte) ([]byte, error) {
	dstKey := t.Key
	if t.PS5Unencrypted {
		dstKey = nil
	}
	return convert(pcData, t.Key, t.ps5DataOffset(), ps5Platform, false, t.PS5Unencrypted, dstKey, 0, t.PlatformClass)
}

// ConvertPS5ToPC rewrites a PS5 save into PC (Steam) shape. steamID is
// written into the account-ID field PC saves carry; it should be the
// Steam ID of the account that will load the save. This direction uses
// the same mechanism as ConvertPCToPS5 in reverse but RE2's has not been
// confirmed in-game.
func (t TitleConfig) ConvertPS5ToPC(ps5Data []byte, steamID uint64) ([]byte, error) {
	srcKey := t.Key
	if t.PS5Unencrypted {
		srcKey = nil
	}
	return convert(ps5Data, srcKey, PCDataOffset, pcPlatform, true, false, t.Key, steamID, t.PlatformClass)
}

// RE2 is the confirmed-working title config (in-game verified, both
// directions implemented; see docs/ressave.md and docs/dev-res2.md).
var RE2 = TitleConfig{Key: KeyRE2, PlatformClass: 0x8b7dd7a1}

// RE3 is confirmed against real saves at the format level (PC decrypts
// and PS5 parses as plaintext with the expected offset; the platform
// fields were found by diffing real PC/PS5 saves, matching RE2's field
// hashes exactly) but the converted output has not been loaded in-game.
var RE3 = TitleConfig{Key: KeyRE3, PlatformClass: 0x4a5aa7b, PS5Unencrypted: true}

// RE7 is confirmed against real saves at the format level: PC decrypts
// with KeyRE7 at the usual offset, PS5 parses as plaintext at the
// unencrypted offset, both checksums valid. Unlike RE2/RE3/RE4 it has no
// PlatformClass - searching every class in a real PC save (~4300
// classes) and a real PS5 save (~1900 classes) found zero occurrences of
// the shared platform-identity fields, so conversion relies solely on
// the container-level differences. Converted output has not been loaded
// in-game.
var RE7 = TitleConfig{Key: KeyRE7, PS5Unencrypted: true}

// ConvertPCToPS5 rewrites a PC (Steam) save into one a PS5 will load,
// using RE2's title facts. Confirmed working against a real console on
// two different saves. Kept as a free function for backward
// compatibility; equivalent to RE2.ConvertPCToPS5(pcData).
func ConvertPCToPS5(pcData, key []byte) ([]byte, error) {
	return TitleConfig{Key: key, PlatformClass: RE2.PlatformClass}.ConvertPCToPS5(pcData)
}

// ConvertPS5ToPC rewrites a PS5 save into PC (Steam) shape using RE2's
// title facts. Kept as a free function for backward compatibility;
// equivalent to RE2.ConvertPS5ToPC(ps5Data, steamID).
func ConvertPS5ToPC(ps5Data, key []byte, steamID uint64) ([]byte, error) {
	return TitleConfig{Key: key, PlatformClass: RE2.PlatformClass}.ConvertPS5ToPC(ps5Data, steamID)
}
