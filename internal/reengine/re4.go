package reengine

import (
	"errors"
	"fmt"
)

// RE4PlatformClass is RE4's settings class carrying the two
// platform-identity fields - the same field hashes (fieldPlatformEnum,
// fieldPlatformBool) RE2 and RE3 use, found by diffing a real
// Lime-decrypted Steam save against a real PS5 save. Unlike RE2/RE3's
// binary PC/PS5 split (enum 3 vs 2), RE4's enum uses a wider range
// (observed: PC=5, PS5=2) - plausibly because it ships on more platforms
// simultaneously (also Xbox, PS4/Xbox One). TENTATIVE: only one real
// save per platform was available to confirm this, unlike RE2/RE3's
// multi-sample confirmation (3-4 saves per side) - and the boolean
// field was observed false on *both* sides in this single sample, so it
// may not be platform-discriminating for RE4 at all. Not yet tested
// in-game.
const RE4PlatformClass = 0x100e60

var (
	re4PCPlatform  = platformValues{enum: 5, flag: false}
	re4PS5Platform = platformValues{enum: 2, flag: false}
)

// ConvertRE4PCToPS5 rewrites a Steam (Lime-encrypted) RE4 save into the
// PS5 (plain-Blowfish) shape. steamID must be the account pcData
// belongs to - Lime decryption depends on it. RE4 needs its own
// converter rather than TitleConfig's generic one because its two sides
// use genuinely different cipher families (Lime vs Blowfish), not just
// different flags within the same one. Format-level round trip confirmed
// against two real Steam saves (see docs/dev-res2.md); not yet tested
// in-game, and RE4PlatformClass's mapping is unconfirmed beyond one
// sample per side.
func ConvertRE4PCToPS5(pcData []byte, steamID uint64) ([]byte, error) {
	dec, err := LimeDecode(pcData, steamID)
	if err != nil {
		return nil, fmt.Errorf("decoding source save: %w", err)
	}
	if !dec.HashValid {
		return nil, errors.New("refusing to convert: source save's checksum doesn't match its contents (file is corrupt or truncated)")
	}

	objs, err := ReadRSZObjects(dec.Body, LimeDataOffset)
	if err != nil {
		return nil, fmt.Errorf("parsing source save's field data: %w", err)
	}

	patched := 0
	for i := range objs {
		patched += retargetPlatform(&objs[i].Class, re4PS5Platform, RE4PlatformClass)
	}
	if patched != 2 {
		return nil, fmt.Errorf("expected to retarget exactly 2 platform fields, found %d - this save's layout isn't the one this converter was built against", patched)
	}

	probe, err := WriteRSZObjects(objs, LimeDataOffset, nil)
	if err != nil {
		return nil, fmt.Errorf("re-serializing source save: %w", err)
	}
	if len(probe) > len(dec.Body) {
		return nil, fmt.Errorf("re-serialized field data is longer than the source (%d > %d); layout was not reproduced faithfully", len(probe), len(dec.Body))
	}
	trailer := dec.Body[len(probe):]

	body, err := WriteRSZObjects(objs, PS5DataOffset, trailer)
	if err != nil {
		return nil, fmt.Errorf("re-serializing for the destination: %w", err)
	}

	out, err := Build(body, KeyRE4, BuildOptions{HasID: false})
	if err != nil {
		return nil, err
	}

	verify, err := Decode(out, KeyRE4)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to decode: %w", err)
	}
	if verify.DataOffset != PS5DataOffset {
		return nil, fmt.Errorf("converted save's body landed at %#x, expected %#x", verify.DataOffset, PS5DataOffset)
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

// ConvertRE4PS5ToPC rewrites a PS5 RE4 save into Steam (Lime) shape.
// steamID selects which account's key the produced save will decrypt
// with - it must be the account that will load it, or the game will
// reject every block's checksum. See ConvertRE4PCToPS5's doc comment
// for the shared caveats (format-level only, platform field unconfirmed
// beyond one sample).
func ConvertRE4PS5ToPC(ps5Data []byte, steamID uint64) ([]byte, error) {
	dec, err := Decode(ps5Data, KeyRE4)
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
		patched += retargetPlatform(&objs[i].Class, re4PCPlatform, RE4PlatformClass)
	}
	if patched != 2 {
		return nil, fmt.Errorf("expected to retarget exactly 2 platform fields, found %d - this save's layout isn't the one this converter was built against", patched)
	}

	probe, err := WriteRSZObjects(objs, dec.DataOffset, nil)
	if err != nil {
		return nil, fmt.Errorf("re-serializing source save: %w", err)
	}
	if len(probe) > len(dec.Body) {
		return nil, fmt.Errorf("re-serialized field data is longer than the source (%d > %d); layout was not reproduced faithfully", len(probe), len(dec.Body))
	}
	trailer := dec.Body[len(probe):]

	body, err := WriteRSZObjects(objs, LimeDataOffset, trailer)
	if err != nil {
		return nil, fmt.Errorf("re-serializing for the destination: %w", err)
	}

	out, err := LimeEncode(body, steamID)
	if err != nil {
		return nil, err
	}

	verify, err := LimeDecode(out, steamID)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to decode: %w", err)
	}
	if !verify.HashValid {
		return nil, errors.New("converted save's checksum doesn't match its own contents")
	}
	reobjs, err := ReadRSZObjects(verify.Body, LimeDataOffset)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to re-parse: %w", err)
	}
	if len(reobjs) != len(objs) {
		return nil, fmt.Errorf("converted save has %d objects, source had %d", len(reobjs), len(objs))
	}
	return out, nil
}
