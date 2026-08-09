package reengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Resident Evil Requiem (RE9) is the first title here whose two platforms
// use the *same* cipher and the same container shape, differing only in
// which account key feeds the cipher. Both sides are Mandarin
// (flags=0x10), neither carries an ID field, and both put the body at
// MandarinDataOffset - so unlike RE2/RE3/RE4/RE7/Village, converting
// between them needs no body re-serialization and no re-alignment at all.
// The decrypted body is carried across verbatim apart from a single
// 4-byte account-identity field (see below), patched in place.
const (
	// KeyRE9PS5 is the PS5 build's fixed Mandarin account key. Unlike the
	// PC side - which keys off the player's own SteamID64 - the PS5 build
	// uses this one hardcoded constant for every save: console saves are
	// already bound to an account by the OS savedata container, so the
	// payload's cipher doesn't need to be. Recovered from the real PS5
	// eboot.bin by sweeping every 8-byte window in it against the
	// known-plaintext oracle (MandarinKeyOracle) - the same technique that
	// found RE4's Blowfish key - which matched exactly one offset
	// (0x4fc9fb9) out of ~550M candidates.
	//
	// Region-independent: two different real PS5 eboot.bin builds (the
	// one this was recovered from, and a USA build of a different size
	// and hash) both contain this exact constant exactly once, along with
	// the same Mandarin seeds and SplitMix64 constants - the USA build is
	// simply shifted by 0x30. So no per-region key handling is needed.
	KeyRE9PS5 uint64 = 394424879635983

	// RE9AccountClass/RE9AccountField locate Requiem's account-identity
	// field: a u32 in the save's header class that is *identical across
	// every save belonging to one account* and differs between accounts.
	// It is Requiem's equivalent of the platform-identity enum+bool pair
	// RE2/RE3/RE4 carry (which Requiem does not have - searching for those
	// hashes finds nothing, same as RE7 and Village), but it identifies
	// the owning account rather than the platform, so its correct value
	// cannot be derived - it has to be copied from a save the destination
	// already owns.
	//
	// Getting this wrong is not a loud failure: a real PS5 rejected a save
	// carrying the PC account's value by simply not offering "Load Game"
	// at all - the same silent-omission symptom RE3's wrong-Steam-ID bug
	// produced (see TestCases.md).
	RE9AccountClass = 0x92470294
	RE9AccountField = 0xa4d68992

	// RE9VersionField is a build stamp in the same header class (e.g.
	// 0x1002000). Deliberately *not* rewritten: it describes which build
	// wrote the save's contents, which a conversion doesn't change.
	RE9VersionField = 0x781ee97a
)

// findRE9AccountField locates the account-identity field's raw bytes
// within a decoded body, reporting how many occurrences it saw so a
// caller can refuse a save whose layout isn't the expected one rather
// than silently patching the wrong thing (or nothing).
func findRE9AccountField(objs []RSZObject) (offset, size, count int, value uint32) {
	var walk func(cls *RSZClass)
	walk = func(cls *RSZClass) {
		for i := range cls.Fields {
			f := &cls.Fields[i]
			if cls.Hash == RE9AccountClass && f.Hash == RE9AccountField && f.Value.ValueSize == 4 {
				count++
				offset, size, value = f.Value.ValueOffset, f.Value.ValueSize, uint32(f.Value.Uint)
			}
			if f.Value.Class != nil {
				walk(f.Value.Class)
			}
			if f.Value.Array != nil {
				for j := range f.Value.Array.Values {
					if c := f.Value.Array.Values[j].Class; c != nil {
						walk(c)
					}
				}
			}
		}
	}
	for i := range objs {
		walk(&objs[i].Class)
	}
	return
}

// RE9AccountID reads the account-identity value out of a decrypted
// Requiem body. Used to learn the destination platform's own value from
// a save it already owns.
func RE9AccountID(body []byte) (uint32, error) {
	objs, err := ReadRSZObjects(body, MandarinDataOffset)
	if err != nil {
		return 0, fmt.Errorf("parsing save: %w", err)
	}
	_, _, count, value := findRE9AccountField(objs)
	if count != 1 {
		return 0, fmt.Errorf("expected exactly one account-identity field, found %d", count)
	}
	return value, nil
}

// convertRE9 swaps a Requiem save's cipher key and retargets its
// account-identity field, carrying every other byte of the decrypted
// body across untouched. srcKey must match data's own side.
func convertRE9(data []byte, srcKey, dstKey uint64, dstAccountID uint32) ([]byte, error) {
	dec, err := MandarinDecode(data, srcKey)
	if err != nil {
		return nil, fmt.Errorf("decoding source save: %w", err)
	}
	if !dec.HashValid {
		return nil, errors.New("refusing to convert: source save's checksum doesn't match its contents (file is corrupt or truncated)")
	}

	objs, err := ReadRSZObjects(dec.Body, MandarinDataOffset)
	if err != nil {
		return nil, fmt.Errorf("parsing source save's field data: %w", err)
	}
	offset, size, count, _ := findRE9AccountField(objs)
	if count != 1 {
		return nil, fmt.Errorf("expected exactly one account-identity field, found %d - this save's layout isn't the one this converter was built against", count)
	}

	body := append([]byte(nil), dec.Body...)
	if offset < 0 || offset+size > len(body) {
		return nil, fmt.Errorf("account-identity field at %#x is outside the body", offset)
	}
	binary.LittleEndian.PutUint32(body[offset:offset+4], dstAccountID)

	out, err := MandarinEncode(body, dstKey)
	if err != nil {
		return nil, err
	}

	// The output must decode back to exactly the body we put in, and carry
	// the account id we meant; if it doesn't, the console would be the one
	// to find out.
	verify, err := MandarinDecode(out, dstKey)
	if err != nil {
		return nil, fmt.Errorf("converted save failed to decode: %w", err)
	}
	if !verify.HashValid {
		return nil, errors.New("converted save's own checksum doesn't verify")
	}
	if !bytes.Equal(verify.Body, body) {
		return nil, errors.New("converted save's body differs from the source's after a round trip")
	}
	got, err := RE9AccountID(verify.Body)
	if err != nil {
		return nil, fmt.Errorf("converted save: %w", err)
	}
	if got != dstAccountID {
		return nil, fmt.Errorf("converted save carries account id %#x, expected %#x", got, dstAccountID)
	}
	return out, nil
}

// ConvertRE9PCToPS5 rewrites a Requiem Steam save into the PS5's shape.
// steamID is the account the source save belongs to (needed to read it);
// ps5AccountID is the identity value the destination PS5 uses, read from
// a save that console already owns - it cannot be derived.
func ConvertRE9PCToPS5(pcData []byte, steamID uint64, ps5AccountID uint32) ([]byte, error) {
	return convertRE9(pcData, steamID, KeyRE9PS5, ps5AccountID)
}

// ConvertRE9PS5ToPC rewrites a Requiem PS5 save into Steam shape.
// steamID is the account that will load the result, and pcAccountID is
// that account's identity value, read from one of its existing saves.
func ConvertRE9PS5ToPC(ps5Data []byte, steamID uint64, pcAccountID uint32) ([]byte, error) {
	return convertRE9(ps5Data, KeyRE9PS5, steamID, pcAccountID)
}
