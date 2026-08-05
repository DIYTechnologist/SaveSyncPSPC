package reengine

import (
	"bytes"
	"strings"
	"testing"
)

// buildPlatformBody produces a minimal but structurally real RSZ body: a
// single object whose class is the platform class, carrying the two
// fields conversion has to retarget. Laid out for the given base, since
// field alignment is computed in file coordinates.
func buildPlatformBody(base int, enum int32, flag bool, slotID []byte) []byte {
	b := &rszBuilder{base: base}
	b.u32(0xAAAAAAAA)    // object outer hash
	b.u32(2)             // class field count
	b.u32(platformClass) // class hash

	// Enum field, declared size 4.
	b.u32(fieldPlatformEnum)
	b.u32(uint32(FieldTypeEnum))
	b.alignTo(4)
	b.u32(4)
	b.alignTo(4)
	b.u32(uint32(enum))

	// Boolean field, declared size 1.
	b.u32(fieldPlatformBool)
	b.u32(uint32(FieldTypeBoolean))
	b.alignTo(4)
	b.u32(1)
	var v byte
	if flag {
		v = 1
	}
	b.buf.WriteByte(v)
	b.alignTo(4)

	// Just the slot number: ReadRSZObjects stops once it is within 7
	// bytes of the end, so extra slack here would make it try to parse
	// another object.
	b.buf.Write(slotID)
	return b.buf.Bytes()
}

func pcSaveFixture(t *testing.T, slotID []byte) []byte {
	t.Helper()
	body := buildPlatformBody(PCDataOffset, 3, true, slotID)
	data, err := Build(body, KeyRE2, BuildOptions{HasID: true, SteamID: 11052978})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func platformValuesOf(t *testing.T, data []byte) (int64, bool) {
	t.Helper()
	dec, err := Decode(data, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := ReadRSZObjects(dec.Body, dec.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Class.Hash != platformClass {
			continue
		}
		var e int64
		var f bool
		for _, fl := range o.Class.Fields {
			switch fl.Hash {
			case fieldPlatformEnum:
				e = fl.Value.Int
			case fieldPlatformBool:
				f = fl.Value.Bool
			}
		}
		return e, f
	}
	t.Fatal("platform class not found")
	return 0, false
}

func TestConvertPCToPS5ProducesPS5Shape(t *testing.T) {
	slotID := []byte{0x01, 0x00, 0x00, 0x00}
	out, err := ConvertPCToPS5(pcSaveFixture(t, slotID), KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decode(out, KeyRE2)
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
		t.Errorf("slot id = % x, want % x - the save must stay bound to its slot", got, slotID)
	}
	if e, f := platformValuesOf(t, out); e != ps5Platform.enum || f != ps5Platform.flag {
		t.Errorf("platform fields = (%d, %v), want (%d, %v)", e, f, ps5Platform.enum, ps5Platform.flag)
	}
}

// TestConvertPCToPS5RealignsForTheNewOffset is the regression test for
// the bug that made every early conversion crash the console: a body
// serialized for one base cannot be copied verbatim to another.
func TestConvertPCToPS5RealignsForTheNewOffset(t *testing.T) {
	src := pcSaveFixture(t, []byte{0x00, 0x00, 0x00, 0x00})
	srcDec, err := Decode(src, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ConvertPCToPS5(src, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	outDec, err := Decode(out, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(srcDec.Body, outDec.Body) {
		t.Fatal("body was copied verbatim; it must be re-serialized for the new offset")
	}
	// It must parse at its new offset, and not at the old one.
	if _, err := ReadRSZObjects(outDec.Body, PS5DataOffset); err != nil {
		t.Fatalf("converted body does not parse at its own offset: %v", err)
	}
}

func TestConvertRoundTripsBackToPC(t *testing.T) {
	slotID := []byte{0x15, 0x00, 0x00, 0x00}
	ps5, err := ConvertPCToPS5(pcSaveFixture(t, slotID), KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ConvertPS5ToPC(ps5, KeyRE2, 11052978)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decode(back, KeyRE2)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.HasID || dec.SteamID != 11052978 {
		t.Errorf("got HasID=%v SteamID=%d", dec.HasID, dec.SteamID)
	}
	if dec.DataOffset != PCDataOffset {
		t.Errorf("body at %#x, want %#x", dec.DataOffset, PCDataOffset)
	}
	if e, f := platformValuesOf(t, back); e != pcPlatform.enum || f != pcPlatform.flag {
		t.Errorf("platform fields = (%d, %v), want (%d, %v)", e, f, pcPlatform.enum, pcPlatform.flag)
	}
	if got := dec.Body[len(dec.Body)-len(slotID):]; !bytes.Equal(got, slotID) {
		t.Errorf("slot id = % x, want % x", got, slotID)
	}
}

func TestConvertRefusesCorruptSource(t *testing.T) {
	data := pcSaveFixture(t, []byte{0, 0, 0, 0})
	data[len(data)-1] ^= 0xff // break the stored checksum
	_, err := ConvertPCToPS5(data, KeyRE2)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum refusal, got %v", err)
	}
}

// TestConvertRefusesUnrecognisedLayout guards the case where a save
// doesn't carry the platform fields this converter knows how to
// retarget - shipping it unchanged would produce a save the console
// rejects, so it must fail here instead.
func TestConvertRefusesUnrecognisedLayout(t *testing.T) {
	b := &rszBuilder{base: PCDataOffset}
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

	data, err := Build(b.buf.Bytes(), KeyRE2, BuildOptions{HasID: true, SteamID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertPCToPS5(data, KeyRE2); err == nil {
		t.Fatal("expected conversion to refuse a save without the platform fields")
	}
}
