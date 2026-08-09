package reengine

import (
	"bytes"
	"testing"
)

const (
	testSteamID64    = 76561197971318706
	testPCAccountID  = 0x6fc16e9c
	testPS5AccountID = 0x1cb70f8e
)

// buildRE9Body makes a synthetic Requiem body carrying the header class
// and account-identity field the converter retargets, plus a trailing
// slot number, so these tests never need real save data. bulk is the
// size of a filler Struct field used to push the body across multiple
// Mandarin blocks (each block holds at least 0x4000 bytes).
func buildRE9Body(accountID uint32, bulk int) []byte {
	b := &rszBuilder{base: MandarinDataOffset}
	b.u32(0xAAAAAAAA) // object outer hash
	b.u32(1)          // outer class: one field
	b.u32(0xBBBBBBBB) // outer class hash
	b.u32(0xCCCCCCCC) // field hash
	b.u32(uint32(FieldTypeClass))
	b.u32(3) // inner class: three fields
	b.u32(RE9AccountClass)
	b.u32(RE9AccountField)
	b.u32(uint32(FieldTypeU32))
	b.alignTo(4)
	b.u32(4)
	b.maskAlignTo(4)
	b.u32(accountID)
	b.u32(RE9VersionField)
	b.u32(uint32(FieldTypeU32))
	b.alignTo(4)
	b.u32(4)
	b.maskAlignTo(4)
	b.u32(0x1002000)
	b.structField(0xDDDDDDDD, bytes.Repeat([]byte{0x5A}, bulk))
	body := b.buf.Bytes()
	// trailing slack: the reader stops before the last 7 bytes, which is
	// where a real save keeps its slot number
	return append(body, 0, 0, 0, 0)
}

// TestConvertRE9RoundTripIsLossless is the property that makes Requiem
// different from every other title here: because both platforms share a
// container shape and body offset, a conversion is a pure key swap and
// the decrypted body must survive it byte-for-byte, in both directions.
func TestConvertRE9RoundTripIsLossless(t *testing.T) {
	body := buildRE9Body(testPCAccountID, 32768) // spans several blocks

	pc, err := MandarinEncode(body, testSteamID64)
	if err != nil {
		t.Fatal(err)
	}

	ps5, err := ConvertRE9PCToPS5(pc, testSteamID64, testPS5AccountID)
	if err != nil {
		t.Fatalf("PC->PS5: %v", err)
	}
	dec, err := MandarinDecode(ps5, KeyRE9PS5)
	if err != nil {
		t.Fatalf("PS5 output doesn't decode with the PS5 key: %v", err)
	}
	if !dec.HashValid {
		t.Error("PS5 output's checksum invalid")
	}
	// Everything must survive verbatim except the one account-identity
	// field, which must now hold the PS5's value.
	if want := buildRE9Body(testPS5AccountID, 32768); !bytes.Equal(dec.Body, want) {
		t.Fatal("PC->PS5 changed something other than the account-identity field")
	}
	if got, err := RE9AccountID(dec.Body); err != nil || got != testPS5AccountID {
		t.Fatalf("PS5 account id = %#x (err %v), want %#x", got, err, testPS5AccountID)
	}

	back, err := ConvertRE9PS5ToPC(ps5, testSteamID64, testPCAccountID)
	if err != nil {
		t.Fatalf("PS5->PC: %v", err)
	}
	redec, err := MandarinDecode(back, testSteamID64)
	if err != nil {
		t.Fatalf("PC output doesn't decode with the Steam key: %v", err)
	}
	if !redec.HashValid {
		t.Error("PC output's checksum invalid")
	}
	if !bytes.Equal(redec.Body, body) {
		t.Fatal("PS5->PC did not restore the original body exactly")
	}
	if got, err := RE9AccountID(redec.Body); err != nil || got != testPCAccountID {
		t.Fatalf("PC account id = %#x (err %v), want %#x", got, err, testPCAccountID)
	}
}

// TestConvertRE9PCToPS5RejectsWrongSteamID covers the failure mode that
// differs from RE2/RE3: a wrong account id here can't silently produce a
// save that merely goes missing from the load list - it can't decrypt the
// source at all, so it must fail loudly.
func TestConvertRE9PCToPS5RejectsWrongSteamID(t *testing.T) {
	pc, err := MandarinEncode(buildRE9Body(testPCAccountID, 64), testSteamID64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertRE9PCToPS5(pc, testSteamID64+1, testPS5AccountID); err == nil {
		t.Fatal("expected a wrong Steam ID to be rejected")
	}
}

// TestConvertRE9PS5UsesFixedKey pins that the PS5 side is not
// account-bound: the same PS5 output must be readable with the constant
// alone, no Steam ID involved.
func TestConvertRE9PS5UsesFixedKey(t *testing.T) {
	body := buildRE9Body(testPCAccountID, 64)
	pc, err := MandarinEncode(body, testSteamID64)
	if err != nil {
		t.Fatal(err)
	}
	ps5, err := ConvertRE9PCToPS5(pc, testSteamID64, testPS5AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MandarinDecode(ps5, KeyRE9PS5); err != nil {
		t.Fatalf("PS5 output must decode with KeyRE9PS5 alone: %v", err)
	}
	// and converting it back for a *different* account must produce a file
	// only that account can read
	other := uint64(testSteamID64 + 1000)
	back, err := ConvertRE9PS5ToPC(ps5, other, testPCAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MandarinDecode(back, testSteamID64); err == nil {
		t.Fatal("a save converted for another account should not decode with the original id")
	}
	if _, err := MandarinDecode(back, other); err != nil {
		t.Fatalf("save should decode for the account it was converted for: %v", err)
	}
}

func TestConvertRE9RefusesCorruptSource(t *testing.T) {
	pc, err := MandarinEncode(buildRE9Body(testPCAccountID, 64), testSteamID64)
	if err != nil {
		t.Fatal(err)
	}
	pc[len(pc)-1] ^= 0xff // break the trailing container hash
	if _, err := ConvertRE9PCToPS5(pc, testSteamID64, testPS5AccountID); err == nil {
		t.Fatal("expected a corrupt source to be refused")
	}
}
