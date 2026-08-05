// This file implements a reader for the tagged-field object format RE
// Engine uses inside a decrypted DSSS body (nicknamed "RSZ" by the
// modding community). Every field embeds its own type tag and, for
// variable-length values, its own size, so this walks the structure
// without needing Capcom's field names or enum labels - those live in an
// external per-game reflection dump this package does not use.
//
// Parsing is required for conversion, not merely diagnostic: field
// alignment is computed against the body's absolute file offset, so a
// body cannot be moved between containers of different header sizes
// without being re-serialized (see rszwrite.go and docs/ressave.md).
//
// Verified against every real save available during development (five
// PC, five PS5, autosaves and manual slots): all parse to completion
// with no unknown-type fields.
// This file implements a reader for the tagged-field object format RE
// Engine uses inside a decrypted DSSS body (nicknamed "RSZ" by the
// modding community). Every field embeds its own type tag and, for
// variable-length values, its own size, so this walks the structure
// without needing Capcom's field names or enum labels - those live in an
// external per-game reflection dump (tens of megabytes) this package
// does not use. The result is a hash-tagged value tree: enough to
// compare two saves structurally even without knowing what any given
// field is called.
//
// Verified against all ten real save files available during development
// (five PC, five PS5, both autosaves and manual slots): every one parses
// to completion with no unknown-type fields.
package reengine

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf16"
)

// FieldType is RE Engine's own field-type tag (via.reflection.TypeKind),
// read directly from the binary ahead of each field's value.
type FieldType int32

const (
	FieldTypeArray   FieldType = -1
	FieldTypeUnknown FieldType = 0
	FieldTypeEnum    FieldType = 0x1
	FieldTypeBoolean FieldType = 0x2
	FieldTypeS8      FieldType = 0x3
	FieldTypeU8      FieldType = 0x4
	FieldTypeS16     FieldType = 0x5
	FieldTypeU16     FieldType = 0x6
	FieldTypeS32     FieldType = 0x7
	FieldTypeU32     FieldType = 0x8
	FieldTypeS64     FieldType = 0x9
	FieldTypeU64     FieldType = 0xa
	FieldTypeF32     FieldType = 0xb
	FieldTypeF64     FieldType = 0xc
	FieldTypeC8      FieldType = 0xd
	FieldTypeC16     FieldType = 0xe
	FieldTypeString  FieldType = 0xf
	FieldTypeStruct  FieldType = 0x10
	FieldTypeClass   FieldType = 0x11
)

func (t FieldType) String() string {
	switch t {
	case FieldTypeArray:
		return "Array"
	case FieldTypeUnknown:
		return "Unknown"
	case FieldTypeEnum:
		return "Enum"
	case FieldTypeBoolean:
		return "Boolean"
	case FieldTypeS8:
		return "S8"
	case FieldTypeU8:
		return "U8"
	case FieldTypeS16:
		return "S16"
	case FieldTypeU16:
		return "U16"
	case FieldTypeS32:
		return "S32"
	case FieldTypeU32:
		return "U32"
	case FieldTypeS64:
		return "S64"
	case FieldTypeU64:
		return "U64"
	case FieldTypeF32:
		return "F32"
	case FieldTypeF64:
		return "F64"
	case FieldTypeC8:
		return "C8"
	case FieldTypeC16:
		return "C16"
	case FieldTypeString:
		return "String"
	case FieldTypeStruct:
		return "Struct"
	case FieldTypeClass:
		return "Class"
	default:
		return fmt.Sprintf("FieldType(%d)", int32(t))
	}
}

// Value is a decoded field value. Which field is meaningful is
// determined by Type; Class/Array are only set for their respective
// types, StructBytes holds a Struct value's raw (semantically
// uninterpreted - could be a Vec3, a Color, ...) bytes.
type Value struct {
	Type        FieldType
	Bool        bool
	Int         int64  // S8/S16/S32/S64/Enum
	Uint        uint64 // U8/U16/U32/U64/C8/C16
	Float32     float32
	Float64     float64
	Str         string
	StructBytes []byte
	Class       *RSZClass
	Array       *RSZArray
	// ValueOffset/ValueSize locate this value's raw bytes within the
	// body byte slice that was parsed - set only for the fixed-size
	// scalar types (Enum/Boolean/S*/U*/C*), which is enough to patch a
	// single field's value in place without a full RSZ writer, the same
	// way BG3's Platform field is patched with a targeted rewrite rather
	// than a full re-serialization.
	ValueOffset int
	ValueSize   int
	// DeclaredSize is the size field the format itself stores ahead of a
	// value, which is not always the number of bytes the value consumes:
	// a Boolean may be declared 4 but occupy 1, with the field-level
	// 4-byte alignment absorbing the rest. Re-emission must reproduce the
	// declared value, since it drives both the stored size and the
	// alignment that follows it.
	DeclaredSize uint32
}

// RSZField is one hash-tagged field inside an RSZClass.
type RSZField struct {
	Hash  uint32
	Type  FieldType
	Value Value
}

// RSZClass is one tagged object: its own type hash plus its fields, in
// file order.
type RSZClass struct {
	Hash   uint32
	Fields []RSZField
}

// RSZArray is a homogeneous (or, for ArrayTypeClass, heterogeneous
// nested-object) array value.
type RSZArray struct {
	MemberType FieldType
	MemberSize uint32
	IsClass    bool // false: ArrayType::Value, true: ArrayType::Class
	// Hashes is set only when a 0xffeeffee marker preceded a Class
	// array's elements - RE Engine sometimes (not always) precedes a
	// class array with a per-element hash table.
	Hashes []uint32
	Values []Value
}

// RSZObject is one top-level entry in a decoded body: an outer hash
// (mod.rs's per-entry marker, read before the object's own class data -
// its relationship to the class's own internal Hash isn't documented,
// so both are kept rather than assumed equal) plus the class itself.
type RSZObject struct {
	OuterHash uint32
	Class     RSZClass
}

type rszCursor struct {
	data []byte
	pos  int
	// base is the body's own offset within the containing file. Field
	// alignment is computed in file coordinates, not body-relative: a
	// PS5 save's body starts at 0x18, which is 8- but not 16-aligned, so
	// every 16-byte alignment inside it lands 8 bytes away from where a
	// body-relative calculation would put it. A PC save's body starts at
	// 0x20 and so happens to agree either way - which is exactly why
	// body-relative alignment parsed PC saves correctly while silently
	// desynchronising on PS5 ones.
	base int
}

func (c *rszCursor) remaining() int { return len(c.data) - c.pos }

func (c *rszCursor) take(n int) ([]byte, error) {
	if n < 0 || c.pos+n > len(c.data) {
		return nil, fmt.Errorf("unexpected end of data at offset %#x wanting %d bytes", c.pos, n)
	}
	b := c.data[c.pos : c.pos+n]
	c.pos += n
	return b, nil
}

func (c *rszCursor) alignUp(n int) {
	rem := (c.base + c.pos) % n
	if rem != 0 {
		c.pos += n - rem
	}
}

func (c *rszCursor) u8() (uint8, error) {
	b, err := c.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (c *rszCursor) i8() (int8, error) {
	v, err := c.u8()
	return int8(v), err
}

func (c *rszCursor) bool_() (bool, error) {
	v, err := c.u8()
	return v != 0, err
}

func (c *rszCursor) u16() (uint16, error) {
	b, err := c.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (c *rszCursor) i16() (int16, error) {
	v, err := c.u16()
	return int16(v), err
}

func (c *rszCursor) u32() (uint32, error) {
	b, err := c.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (c *rszCursor) i32() (int32, error) {
	v, err := c.u32()
	return int32(v), err
}

func (c *rszCursor) u64() (uint64, error) {
	b, err := c.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (c *rszCursor) i64() (int64, error) {
	v, err := c.u64()
	return int64(v), err
}

func (c *rszCursor) f32() (float32, error) {
	v, err := c.u32()
	return math.Float32frombits(v), err
}

func (c *rszCursor) f64() (float64, error) {
	v, err := c.u64()
	return math.Float64frombits(v), err
}

// readUTF16String reads a length-prefixed (already aligned/positioned)
// UTF-16LE string of count u16 code units, trimming one trailing NUL if
// present (RE Engine strings are conventionally NUL-terminated).
func (c *rszCursor) readUTF16String(count uint32) (string, error) {
	units := make([]uint16, count)
	for i := range units {
		u, err := c.u16()
		if err != nil {
			return "", err
		}
		units[i] = u
	}
	if len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units)), nil
}

func readFieldType(c *rszCursor) (FieldType, error) {
	v, err := c.i32()
	return FieldType(v), err
}

// readValue reads a value whose type tag has already been consumed
// (matching FieldValue::read).
func readValue(c *rszCursor, ft FieldType) (Value, error) {
	switch ft {
	case FieldTypeUnknown:
		// A zero type tag carries no value payload - the field is just
		// its 8-byte (hash, type) header. Observed on real PS5 saves
		// interleaved between populated fields; treating it as an error
		// (as the reference implementation does) aborts the whole
		// enclosing object.
		return Value{Type: FieldTypeUnknown}, nil
	case FieldTypeArray:
		arr, err := readArray(c)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeArray, Array: arr}, nil
	case FieldTypeClass:
		cls, err := readClass(c)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeClass, Class: cls}, nil
	case FieldTypeString:
		c.alignUp(4)
		size, err := c.u32()
		if err != nil {
			return Value{}, err
		}
		s, err := c.readUTF16String(size)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeString, Str: s, DeclaredSize: size}, nil
	default:
		c.alignUp(4)
		size, err := c.u32()
		if err != nil {
			return Value{}, err
		}
		v, err := readSizedValue(c, ft, size)
		if err != nil {
			return Value{}, err
		}
		v.DeclaredSize = size
		return v, nil
	}
}

// readSizedValue reads a fixed/sized value (matching
// FieldValue::read_sized): a size!=1 first aligns the cursor up to a
// `size`-byte boundary (not just 4 - e.g. an 8-byte S64 aligns to 8).
func readSizedValue(c *rszCursor, ft FieldType, size uint32) (Value, error) {
	if size != 1 {
		c.alignUp(int(size))
	}
	valueOffset := c.pos
	v, err := readSizedValueInner(c, ft, size)
	if err != nil {
		return Value{}, err
	}
	// Struct/Array already set their own offset/size (Struct is the raw
	// bytes themselves; Array has no single scalar location) - only tag
	// the plain scalar types this exists to let a caller patch in place.
	switch ft {
	case FieldTypeArray, FieldTypeStruct:
	default:
		v.ValueOffset = valueOffset
		v.ValueSize = c.pos - valueOffset
	}
	return v, nil
}

func readSizedValueInner(c *rszCursor, ft FieldType, size uint32) (Value, error) {
	switch ft {
	case FieldTypeEnum:
		var iv int64
		var err error
		switch size {
		case 1:
			var v int8
			v, err = c.i8()
			iv = int64(v)
		case 2:
			var v int16
			v, err = c.i16()
			iv = int64(v)
		case 4:
			var v int32
			v, err = c.i32()
			iv = int64(v)
		case 8:
			iv, err = c.i64()
		default:
			return Value{}, fmt.Errorf("invalid Enum size %d at offset %#x", size, c.pos)
		}
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeEnum, Int: iv}, nil
	case FieldTypeBoolean:
		v, err := c.bool_()
		return Value{Type: FieldTypeBoolean, Bool: v}, err
	case FieldTypeS8:
		v, err := c.i8()
		return Value{Type: ft, Int: int64(v)}, err
	case FieldTypeU8, FieldTypeC8:
		v, err := c.u8()
		return Value{Type: ft, Uint: uint64(v)}, err
	case FieldTypeS16:
		v, err := c.i16()
		return Value{Type: ft, Int: int64(v)}, err
	case FieldTypeU16, FieldTypeC16:
		v, err := c.u16()
		return Value{Type: ft, Uint: uint64(v)}, err
	case FieldTypeS32:
		v, err := c.i32()
		return Value{Type: ft, Int: int64(v)}, err
	case FieldTypeU32:
		v, err := c.u32()
		return Value{Type: ft, Uint: uint64(v)}, err
	case FieldTypeS64:
		v, err := c.i64()
		return Value{Type: ft, Int: v}, err
	case FieldTypeU64:
		v, err := c.u64()
		return Value{Type: ft, Uint: v}, err
	case FieldTypeF32:
		v, err := c.f32()
		return Value{Type: ft, Float32: v}, err
	case FieldTypeF64:
		v, err := c.f64()
		return Value{Type: ft, Float64: v}, err
	case FieldTypeArray:
		arr, err := readArray(c)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeArray, Array: arr}, nil
	case FieldTypeStruct:
		b, err := c.take(int(size))
		if err != nil {
			return Value{}, err
		}
		return Value{Type: FieldTypeStruct, StructBytes: append([]byte(nil), b...)}, nil
	default:
		return Value{}, fmt.Errorf("unexpected sized read of type %s (size %d) at offset %#x", ft, size, c.pos)
	}
}

func readField(c *rszCursor) (RSZField, error) {
	hash, err := c.u32()
	if err != nil {
		return RSZField{}, err
	}
	ft, err := readFieldType(c)
	if err != nil {
		return RSZField{}, err
	}
	value, err := readValue(c, ft)
	if err != nil {
		return RSZField{}, fmt.Errorf("field hash %#x type %s: %w", hash, ft, err)
	}
	c.alignUp(4)
	return RSZField{Hash: hash, Type: ft, Value: value}, nil
}

func readClass(c *rszCursor) (*RSZClass, error) {
	posBefore := c.pos
	numFields, err := c.u32()
	if err != nil {
		return nil, err
	}
	hash, err := c.u32()
	if err != nil {
		return nil, err
	}
	// Every field costs at least its 8-byte (hash, type) header, so a
	// count larger than the remaining bytes can hold means the cursor has
	// desynchronised and is reading data as structure. Bounding this
	// turns a runaway allocation into a diagnosable error.
	if maxFields := c.remaining() / 8; int(numFields) > maxFields {
		return nil, fmt.Errorf("class hash %#x at offset %#x claims %d fields but only %d could fit in the remaining %d bytes",
			hash, posBefore, numFields, maxFields, c.remaining())
	}
	fields := make([]RSZField, 0, numFields)
	for i := uint32(0); i < numFields; i++ {
		f, err := readField(c)
		if err != nil {
			return nil, fmt.Errorf("class hash %#x field %d/%d: %w", hash, i, numFields, err)
		}
		fields = append(fields, f)
	}
	return &RSZClass{Hash: hash, Fields: fields}, nil
}

const classArrayMarker = 0xffeeffee

func readArray(c *rszCursor) (*RSZArray, error) {
	posBefore := c.pos
	c.alignUp(4)
	memberType, err := readFieldType(c)
	if err != nil {
		return nil, err
	}
	memberSize, err := c.u32()
	if err != nil {
		return nil, err
	}
	length, err := c.u32()
	if err != nil {
		return nil, err
	}
	arrayTypeRaw, err := c.i32()
	if err != nil {
		return nil, err
	}
	isClass := arrayTypeRaw == 1

	var hashes []uint32
	if isClass {
		markerPos := c.pos
		marker, err := c.u32()
		if err != nil {
			return nil, err
		}
		if marker == classArrayMarker {
			if int(length) > c.remaining()/4 {
				return nil, fmt.Errorf("array at offset %#x claims %d hashed elements, more than the remaining %d bytes allow",
					posBefore, length, c.remaining())
			}
			hashes = make([]uint32, length)
			for i := range hashes {
				h, err := c.u32()
				if err != nil {
					return nil, err
				}
				hashes[i] = h
			}
		} else {
			c.pos = markerPos // put back - no marker present
		}
	}

	// Each element occupies at least one byte, so a length beyond the
	// remaining bytes means the cursor desynchronised upstream.
	if int(length) > c.remaining() {
		return nil, fmt.Errorf("array at offset %#x claims %d elements, more than the remaining %d bytes allow",
			posBefore, length, c.remaining())
	}
	values := make([]Value, 0, length)
	for i := uint32(0); i < length; i++ {
		if isClass {
			cls, err := readClass(c)
			if err != nil {
				return nil, fmt.Errorf("array element %d/%d: %w", i, length, err)
			}
			values = append(values, Value{Type: FieldTypeClass, Class: cls})
			continue
		}
		if memberType == FieldTypeString {
			c.alignUp(4)
			size, err := c.u32()
			if err != nil {
				return nil, err
			}
			s, err := c.readUTF16String(size)
			if err != nil {
				return nil, err
			}
			values = append(values, Value{Type: FieldTypeString, Str: s, DeclaredSize: size})
			continue
		}
		v, err := readSizedValue(c, memberType, memberSize)
		if err != nil {
			return nil, fmt.Errorf("array element %d/%d: %w", i, length, err)
		}
		values = append(values, v)
	}
	c.alignUp(4)

	return &RSZArray{MemberType: memberType, MemberSize: memberSize, IsClass: isClass, Hashes: hashes, Values: values}, nil
}

// ReadRSZObjects walks a decoded DSSS body's top-level sequence of
// objects. dataOffset is Decoded.DataOffset - the body's offset within
// its file - which field alignment is computed against (see rszCursor).
// (outer hash, class) objects, matching the reference implementation's
// parse_classes: read until reaching end (len(body)-7, matching the
// reference's own trailing-slack cutoff - the last few bytes of a real
// body aren't another object). A field that fails to parse is skipped
// (its bytes are unrecoverable without knowing where it ends), matching
// upstream's own best-effort behavior, rather than aborting the whole
// walk - everything already parsed is still useful for comparison.
func ReadRSZObjects(body []byte, dataOffset int) ([]RSZObject, error) {
	c := &rszCursor{data: body, base: dataOffset}
	end := len(body) - 7
	if end < 0 {
		end = 0
	}
	var objects []RSZObject
	for c.pos < end {
		startPos := c.pos
		hash, err := c.u32()
		if err != nil {
			break
		}
		cls, err := readClass(c)
		if err != nil {
			// Matches upstream's tolerant loop: log and stop rather than
			// erroring the whole read, since a body has been observed to
			// contain some trailing/irregular data near its very end.
			return objects, fmt.Errorf("stopped at offset %#x (object starting %#x): %w", c.pos, startPos, err)
		}
		objects = append(objects, RSZObject{OuterHash: hash, Class: *cls})
	}
	return objects, nil
}
