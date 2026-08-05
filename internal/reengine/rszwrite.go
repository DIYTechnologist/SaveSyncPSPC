package reengine

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf16"
)

// rszWriter re-emits a parsed RSZ tree. base is the body's intended
// offset within its containing file: field alignment is computed in file
// coordinates (see rszCursor), so re-emitting a body at a different base
// than it was read from is exactly what re-aligns it.
type rszWriter struct {
	buf  []byte
	base int
}

func (w *rszWriter) alignUp(n int) {
	for (w.base+len(w.buf))%n != 0 {
		w.buf = append(w.buf, 0)
	}
}

func (w *rszWriter) u8(v uint8)   { w.buf = append(w.buf, v) }
func (w *rszWriter) u16(v uint16) { w.buf = binary.LittleEndian.AppendUint16(w.buf, v) }
func (w *rszWriter) u32(v uint32) { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }
func (w *rszWriter) u64(v uint64) { w.buf = binary.LittleEndian.AppendUint64(w.buf, v) }

// writeSizedValue mirrors readSizedValue: align to the value's own size,
// then emit exactly ValueSize bytes.
func (w *rszWriter) writeSizedValue(v Value, declared int) error {
	size := declared
	if v.Type == FieldTypeStruct {
		size = len(v.StructBytes)
	}
	if size != 1 {
		w.alignUp(size)
	}
	switch v.Type {
	case FieldTypeStruct:
		w.buf = append(w.buf, v.StructBytes...)
	case FieldTypeBoolean:
		if v.Bool {
			w.u8(1)
		} else {
			w.u8(0)
		}
	case FieldTypeEnum, FieldTypeS8, FieldTypeS16, FieldTypeS32, FieldTypeS64:
		switch v.ValueSize {
		case 1:
			w.u8(uint8(int8(v.Int)))
		case 2:
			w.u16(uint16(int16(v.Int)))
		case 4:
			w.u32(uint32(int32(v.Int)))
		case 8:
			w.u64(uint64(v.Int))
		default:
			return fmt.Errorf("unsupported signed size %d", v.ValueSize)
		}
	case FieldTypeU8, FieldTypeC8:
		w.u8(uint8(v.Uint))
	case FieldTypeU16, FieldTypeC16:
		w.u16(uint16(v.Uint))
	case FieldTypeU32:
		w.u32(uint32(v.Uint))
	case FieldTypeU64:
		w.u64(v.Uint)
	case FieldTypeF32:
		w.u32(math.Float32bits(v.Float32))
	case FieldTypeF64:
		w.u64(math.Float64bits(v.Float64))
	default:
		return fmt.Errorf("writeSizedValue: unexpected type %s", v.Type)
	}
	return nil
}

// writeString re-emits a string at its original declared unit count.
// The reader trims one trailing NUL, so a non-empty string is stored
// with its terminator - but an empty string is stored as length 0 with
// no units at all, and blindly re-adding a terminator would lengthen the
// body and shift everything after it.
func (w *rszWriter) writeString(v Value) {
	w.alignUp(4)
	units := utf16.Encode([]rune(v.Str))
	declared := int(v.DeclaredSize)
	if declared == 0 {
		w.u32(0)
		return
	}
	for len(units) < declared {
		units = append(units, 0)
	}
	units = units[:declared]
	w.u32(uint32(declared))
	for _, u := range units {
		w.u16(u)
	}
}

func (w *rszWriter) writeValue(v Value) error {
	switch v.Type {
	case FieldTypeUnknown:
		return nil // no payload; the (hash, type) header is the whole field
	case FieldTypeArray:
		return w.writeArray(v.Array)
	case FieldTypeClass:
		return w.writeClass(v.Class)
	case FieldTypeString:
		w.writeString(v)
		return nil
	default:
		w.alignUp(4)
		declared := int(v.DeclaredSize)
		if v.Type == FieldTypeStruct {
			declared = len(v.StructBytes)
		}
		w.u32(uint32(declared))
		return w.writeSizedValue(v, declared)
	}
}

func (w *rszWriter) writeArray(a *RSZArray) error {
	if a == nil {
		return fmt.Errorf("nil array")
	}
	w.alignUp(4)
	w.u32(uint32(int32(a.MemberType)))
	w.u32(a.MemberSize)
	w.u32(uint32(len(a.Values)))
	if a.IsClass {
		w.u32(1)
		if a.Hashes != nil {
			w.u32(classArrayMarker)
			for _, h := range a.Hashes {
				w.u32(h)
			}
		}
	} else {
		w.u32(0)
	}
	for _, v := range a.Values {
		if a.IsClass {
			if err := w.writeClass(v.Class); err != nil {
				return err
			}
			continue
		}
		if a.MemberType == FieldTypeString {
			w.writeString(v)
			continue
		}
		if err := w.writeSizedValue(v, int(a.MemberSize)); err != nil {
			return err
		}
	}
	w.alignUp(4)
	return nil
}

func (w *rszWriter) writeClass(c *RSZClass) error {
	if c == nil {
		return fmt.Errorf("nil class")
	}
	w.u32(uint32(len(c.Fields)))
	w.u32(c.Hash)
	for _, f := range c.Fields {
		w.u32(f.Hash)
		w.u32(uint32(int32(f.Type)))
		if err := w.writeValue(f.Value); err != nil {
			return fmt.Errorf("class %#x field %#x: %w", c.Hash, f.Hash, err)
		}
		w.alignUp(4)
	}
	return nil
}

// WriteRSZObjects re-serializes objects into a body laid out for
// dataOffset - the body's offset within its containing file. Writing a
// tree parsed at one dataOffset back out at a different one re-aligns
// every field for the new position, which is what a PC->PS5 conversion
// needs: a PC body sits at 0x20 (16-aligned) and a PS5 body at 0x18
// (8 mod 16), so copying one verbatim into the other's container leaves
// every 16-byte-aligned field 8 bytes out of place.
//
// trailer is appended verbatim after the objects (real bodies end with
// the save's slot number plus any slack the reader stops within - see
// ReadRSZObjects, which parses only up to len(body)-7).
func WriteRSZObjects(objs []RSZObject, dataOffset int, trailer []byte) ([]byte, error) {
	w := &rszWriter{base: dataOffset}
	for i := range objs {
		w.u32(objs[i].OuterHash)
		if err := w.writeClass(&objs[i].Class); err != nil {
			return nil, fmt.Errorf("object %d: %w", i, err)
		}
	}
	w.buf = append(w.buf, trailer...)
	return w.buf, nil
}
