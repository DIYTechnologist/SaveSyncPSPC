package reengine

import (
	"errors"
	"fmt"
)

// Body offsets for each platform's container shape. A PC save carries an
// account-ID field and so starts its body at 0x20; a PS5 save has no ID
// field and starts at 0x18. The difference matters far beyond the header:
// RSZ field alignment is computed against the body's absolute file
// offset, and 0x20 is 16-aligned while 0x18 is not, so a body cannot be
// moved between the two without re-serializing it.
const (
	PCDataOffset  = 0x20
	PS5DataOffset = 0x18
)

// platformClass and its two fields are the only values observed to split
// cleanly by platform across every real save sampled (both characters,
// both accounts, autosaves and manual slots). Leaving them at their
// source-platform values produces a save the game parses correctly and
// then refuses as "not compatible".
const platformClass = 0x8b7dd7a1

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

// retargetPlatform rewrites the platform-identifying fields in place,
// reporting how many it changed so callers can fail loudly if a save
// doesn't have the expected shape rather than silently shipping one that
// will be rejected on the console.
func retargetPlatform(cls *RSZClass, to platformValues) int {
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
			n += retargetPlatform(c, to)
		}
		if a := cls.Fields[i].Value.Array; a != nil {
			for j := range a.Values {
				if c := a.Values[j].Class; c != nil {
					n += retargetPlatform(c, to)
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
func convert(data, key []byte, dstOffset int, dstPlatform platformValues, dstHasID bool, steamID uint64) ([]byte, error) {
	dec, err := Decode(data, key)
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

	patched := 0
	for i := range objs {
		patched += retargetPlatform(&objs[i].Class, dstPlatform)
	}
	if patched != 2 {
		return nil, fmt.Errorf("expected to retarget exactly 2 platform fields, found %d - this save's layout isn't the one this converter was built against", patched)
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

	out, err := Build(body, key, BuildOptions{HasID: dstHasID, SteamID: steamID})
	if err != nil {
		return nil, err
	}

	// The output must parse back cleanly at its new offset; if it doesn't,
	// the console would be the one to find out.
	verify, err := Decode(out, key)
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

// ConvertPCToPS5 rewrites a PC (Steam) save into one a PS5 will load.
// Confirmed working against a real console on two different saves.
func ConvertPCToPS5(pcData, key []byte) ([]byte, error) {
	return convert(pcData, key, PS5DataOffset, ps5Platform, false, 0)
}

// ConvertPS5ToPC rewrites a PS5 save into PC (Steam) shape. steamID is
// written into the account-ID field PC saves carry; it should be the
// Steam ID of the account that will load the save. This direction uses
// the same mechanism as ConvertPCToPS5 in reverse but has not been
// confirmed in-game.
func ConvertPS5ToPC(ps5Data, key []byte, steamID uint64) ([]byte, error) {
	return convert(ps5Data, key, PCDataOffset, pcPlatform, true, steamID)
}
