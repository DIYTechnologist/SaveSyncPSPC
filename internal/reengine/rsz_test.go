package reengine

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// rszBuilder assembles a synthetic RSZ body so these tests don't depend
// on (and never commit) real save data. base mirrors the body's offset
// within its containing file, which is what field alignment is computed
// against.
type rszBuilder struct {
	buf  bytes.Buffer
	base int
}

func (b *rszBuilder) u32(v uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], v)
	b.buf.Write(raw[:])
}

// alignTo pads so the *file* offset of the next byte written is a
// multiple of n - the same rule the reader applies.
func (b *rszBuilder) alignTo(n int) {
	for (b.base+b.buf.Len())%n != 0 {
		b.buf.WriteByte(0xCD) // recognisable filler, never valid data
	}
}

// structField appends one Struct-typed field carrying data verbatim.
func (b *rszBuilder) structField(hash uint32, data []byte) {
	b.u32(hash)
	b.u32(uint32(FieldTypeStruct))
	b.alignTo(4)
	b.u32(uint32(len(data)))
	b.alignTo(len(data))
	b.buf.Write(data)
}

// buildStructBody produces a body holding a single object whose class
// has one Struct field. Everything after the payload is slack the reader
// stops within, mirroring how real bodies end.
func buildStructBody(base int, payload []byte) []byte {
	b := &rszBuilder{base: base}
	b.u32(0xAAAAAAAA) // object's outer hash
	b.u32(1)          // class field count
	b.u32(0xBBBBBBBB) // class hash
	b.structField(0xCCCCCCCC, payload)
	b.buf.Write(make([]byte, 4)) // trailing slack
	return b.buf.Bytes()
}

// TestReadRSZObjectsAlignsAgainstFileOffset is the regression test for
// the bug that made every PS5 save unparseable: alignment is relative to
// the body's offset within the file, not the body itself. A PS5 body
// starts at 0x18 (8- but not 16-aligned) and a PC body at 0x20 (16-
// aligned), so a 16-byte Struct lands 8 bytes apart between them. Parsing
// with a body-relative assumption silently desynchronises on PS5 only -
// which is exactly why this went unnoticed while PC saves parsed fine.
func TestReadRSZObjectsAlignsAgainstFileOffset(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5A}, 16)

	for _, base := range []int{0x18, 0x20} {
		body := buildStructBody(base, payload)

		objs, err := ReadRSZObjects(body, base)
		if err != nil {
			t.Fatalf("base %#x: %v", base, err)
		}
		if len(objs) != 1 {
			t.Fatalf("base %#x: got %d objects, want 1", base, len(objs))
		}
		fields := objs[0].Class.Fields
		if len(fields) != 1 {
			t.Fatalf("base %#x: got %d fields, want 1", base, len(fields))
		}
		if got := fields[0].Value.StructBytes; !bytes.Equal(got, payload) {
			t.Fatalf("base %#x: struct payload = % x, want % x", base, got, payload)
		}
	}
}

// TestReadRSZObjectsWrongBaseDesynchronises pins the failure mode: the
// bytes are identical, only the declared base differs, and the reader
// must not quietly return plausible-looking wrong data.
func TestReadRSZObjectsWrongBaseDesynchronises(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5A}, 16)
	body := buildStructBody(0x18, payload) // laid out for a PS5 body

	objs, err := ReadRSZObjects(body, 0x20) // ...read as if it were PC
	if err != nil {
		return // desync detected outright - acceptable
	}
	for _, o := range objs {
		for _, f := range o.Class.Fields {
			if bytes.Equal(f.Value.StructBytes, payload) {
				t.Fatal("reading with the wrong base returned the correct payload; " +
					"the alignment base is not actually being honoured")
			}
		}
	}
}

func TestReadRSZObjectsRejectsImpossibleFieldCount(t *testing.T) {
	b := &rszBuilder{}
	b.u32(0xAAAAAAAA)
	b.u32(0xFFFFFFFF) // absurd field count
	b.u32(0xBBBBBBBB)
	b.buf.Write(make([]byte, 32))

	if _, err := ReadRSZObjects(b.buf.Bytes(), 0); err == nil {
		t.Fatal("expected an error rather than a runaway allocation")
	}
}

func TestReadRSZObjectsRejectsImpossibleArrayLength(t *testing.T) {
	b := &rszBuilder{}
	b.u32(0xAAAAAAAA)
	b.u32(1)                          // one field
	b.u32(0xBBBBBBBB)                 // class hash
	b.u32(0xCCCCCCCC)                 // field hash
	arrayTag := int32(FieldTypeArray) // -1; via a variable so the conversion isn't a constant overflow
	b.u32(uint32(arrayTag))
	b.u32(uint32(FieldTypeU32)) // member type
	b.u32(4)                    // member size
	b.u32(0xFFFFFFFF)           // absurd length
	b.u32(0)                    // ArrayType::Value
	b.buf.Write(make([]byte, 32))

	if _, err := ReadRSZObjects(b.buf.Bytes(), 0); err == nil {
		t.Fatal("expected an error rather than a runaway allocation")
	}
}
