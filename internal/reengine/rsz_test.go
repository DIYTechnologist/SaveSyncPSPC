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

// TestClampArrayPreallocation is a regression test for the preallocation
// amplification readArray guards against: readArray's own bounds check
// compares a claimed array length against remaining *bytes*, not
// remaining/memberSize, so for any element type wider than one byte a
// claimed length can pass that check while still being far larger than
// the element count the remaining bytes could really supply -
// preallocating make([]Value, 0, length) directly off such a length
// amplifies a modest byte count into a much larger up-front allocation
// (Value is a struct well over one byte). clampArrayPreallocation must
// cap what's handed to make(), regardless of how large the claimed
// length is, while still returning the real length unchanged when it's
// already small.
func TestClampArrayPreallocation(t *testing.T) {
	cases := []struct {
		length uint32
		want   int
	}{
		{length: 0, want: 0},
		{length: 10, want: 10},
		{length: maxArrayPreallocation, want: maxArrayPreallocation},
		{length: maxArrayPreallocation + 1, want: maxArrayPreallocation},
		{length: 0xFFFFFFFF, want: maxArrayPreallocation},
	}
	for _, c := range cases {
		if got := clampArrayPreallocation(c.length); got != c.want {
			t.Errorf("clampArrayPreallocation(%d) = %d, want %d", c.length, got, c.want)
		}
	}
}

// TestReadRSZObjectsBoundsPreallocationForWideElements exercises the
// amplification scenario end to end: a U64 (8-byte) array whose claimed
// length passes readArray's remaining-*bytes* check (length is well
// under the buffer size) but which the actual remaining bytes couldn't
// possibly back at 8 bytes/element. Before clampArrayPreallocation, this
// shape is exactly what let a modest file force a preallocation far
// larger than the file itself; parsing must still fail cleanly on the
// undersized buffer rather than hang or blow up memory.
func TestReadRSZObjectsBoundsPreallocationForWideElements(t *testing.T) {
	b := &rszBuilder{}
	b.u32(0xAAAAAAAA)
	b.u32(1)          // one field
	b.u32(0xBBBBBBBB) // class hash
	b.u32(0xCCCCCCCC) // field hash
	arrayTag := int32(FieldTypeArray)
	b.u32(uint32(arrayTag))
	b.u32(uint32(FieldTypeU64)) // member type - 8 bytes/element
	b.u32(8)                    // member size
	b.u32(20000)                // length: within remaining bytes, but 20000 elements need 160000 bytes
	b.u32(0)                    // ArrayType::Value
	b.buf.Write(make([]byte, 20000))

	if _, err := ReadRSZObjects(b.buf.Bytes(), 0); err == nil {
		t.Fatal("expected an error: not enough bytes to back a 20000-element U64 array")
	}
}
