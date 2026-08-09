package reengine

import (
	"bytes"
	"testing"
)

// re4PCSaveFixture builds a synthetic Steam-shaped RE4 save (Lime
// container around an RSZ body carrying RE4PlatformClass), the same
// spirit as convert_test.go's pcSaveFixture for RE2/RE3.
func re4PCSaveFixture(t *testing.T, steamID uint64, slotID []byte) []byte {
	t.Helper()
	body := buildPlatformBodyFor(RE4PlatformClass, LimeDataOffset, int32(re4PCPlatform.enum), re4PCPlatform.flag, slotID)
	data, err := LimeEncode(body, steamID)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRE4ConvertPCToPS5ProducesPS5Shape mirrors
// TestConvertPCToPS5ProducesPS5Shape but for RE4's asymmetric
// Lime<->Blowfish pair. Confirmed separately against two real Steam RE4
// saves, including a full PC->PS5->PC round trip (see docs/dev-res2.md);
// this fixture pins the mechanism with fast, synthetic data.
func TestRE4ConvertPCToPS5ProducesPS5Shape(t *testing.T) {
	const steamID = 11052978
	slotID := []byte{0x00, 0x00, 0x00, 0x00}

	out, err := ConvertRE4PCToPS5(re4PCSaveFixture(t, steamID, slotID), steamID)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decode(out, KeyRE4)
	if err != nil {
		t.Fatal(err)
	}
	if dec.HasID {
		t.Error("PS5 saves carry no ID field")
	}
	if dec.DataOffset != PS5DataOffset {
		t.Errorf("body at %#x, want %#x", dec.DataOffset, PS5DataOffset)
	}
	if !dec.HashValid {
		t.Error("checksum invalid")
	}
	if got := dec.Body[len(dec.Body)-len(slotID):]; !bytes.Equal(got, slotID) {
		t.Errorf("slot id = % x, want % x", got, slotID)
	}

	objs, err := ReadRSZObjects(dec.Body, dec.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	var foundEnum int64
	var foundFlag bool
	for _, o := range objs {
		if o.Class.Hash != RE4PlatformClass {
			continue
		}
		for _, f := range o.Class.Fields {
			switch f.Hash {
			case fieldPlatformEnum:
				foundEnum = f.Value.Int
			case fieldPlatformBool:
				foundFlag = f.Value.Bool
			}
		}
	}
	if foundEnum != re4PS5Platform.enum || foundFlag != re4PS5Platform.flag {
		t.Errorf("platform fields = (%d, %v), want (%d, %v)", foundEnum, foundFlag, re4PS5Platform.enum, re4PS5Platform.flag)
	}
}

func TestRE4ConvertRoundTripsBackToPC(t *testing.T) {
	const steamID = 11052978
	slotID := []byte{0x01, 0x00, 0x00, 0x00}

	ps5, err := ConvertRE4PCToPS5(re4PCSaveFixture(t, steamID, slotID), steamID)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ConvertRE4PS5ToPC(ps5, steamID)
	if err != nil {
		t.Fatal(err)
	}

	dec, err := LimeDecode(back, steamID)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.HashValid {
		t.Error("checksum invalid")
	}
	if got := dec.Body[len(dec.Body)-len(slotID):]; !bytes.Equal(got, slotID) {
		t.Errorf("slot id = % x, want % x", got, slotID)
	}

	objs, err := ReadRSZObjects(dec.Body, LimeDataOffset)
	if err != nil {
		t.Fatal(err)
	}
	var foundEnum int64
	var foundFlag bool
	for _, o := range objs {
		if o.Class.Hash != RE4PlatformClass {
			continue
		}
		for _, f := range o.Class.Fields {
			switch f.Hash {
			case fieldPlatformEnum:
				foundEnum = f.Value.Int
			case fieldPlatformBool:
				foundFlag = f.Value.Bool
			}
		}
	}
	if foundEnum != re4PCPlatform.enum || foundFlag != re4PCPlatform.flag {
		t.Errorf("platform fields = (%d, %v), want (%d, %v)", foundEnum, foundFlag, re4PCPlatform.enum, re4PCPlatform.flag)
	}

	// A different Steam ID must not be able to read the converted save.
	if _, err := LimeDecode(back, steamID+1); err == nil {
		t.Fatal("expected a different Steam ID to fail to decode the converted save")
	}
}

func TestRE4ConvertRefusesCorruptSource(t *testing.T) {
	const steamID = 11052978
	data := re4PCSaveFixture(t, steamID, []byte{0, 0, 0, 0})
	data[len(data)-1] ^= 0xff // break the stored murmur3 checksum
	_, err := ConvertRE4PCToPS5(data, steamID)
	if err == nil {
		t.Fatal("expected a checksum refusal")
	}
}

// TestRE4ConvertRefusesUnrecognisedLayout guards the case where a save
// doesn't carry the platform fields this converter knows how to
// retarget.
func TestRE4ConvertRefusesUnrecognisedLayout(t *testing.T) {
	b := &rszBuilder{base: LimeDataOffset}
	b.u32(0xAAAAAAAA)
	b.u32(1)
	b.u32(0xBBBBBBBB) // some other class
	b.u32(0xCCCCCCCC)
	b.u32(uint32(FieldTypeU32))
	b.alignTo(4)
	b.u32(4)
	b.alignTo(4)
	b.u32(7)
	b.buf.Write(make([]byte, 4))

	const steamID = 11052978
	data, err := LimeEncode(b.buf.Bytes(), steamID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertRE4PCToPS5(data, steamID); err == nil {
		t.Fatal("expected conversion to refuse a save without the platform fields")
	}
}
