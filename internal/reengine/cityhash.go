package reengine

import "encoding/binary"

// CityHash64 is Google's CityHash algorithm (the plain, unseeded 64-bit
// variant - "CityHash64" in the original C++, `cityhasher::hash::<u64>`
// in the Rust crate the community save-editor tooling uses). Ported
// directly from that crate's implementation for Mandarin's per-block
// checksum (see mandarin.go) - the checksum is fatal to get wrong (a
// mismatch is rejected by the format itself), so this is a faithful
// port, not a reimplementation from the public algorithm description,
// to guarantee bit-for-bit agreement.
//
// Verified against the reference crate's own doctest vectors:
// cityHash64("world") == 16436542438370751598.
const (
	cityK0 = 0xc3a5c85c97cb3127
	cityK1 = 0xb492b66fbe98f273
	cityK2 = 0x9ae16a3b2f90404f
)

func cityRotate64(val uint64, shift uint) uint64 {
	if shift == 0 {
		return val
	}
	return (val >> shift) | (val << (64 - shift))
}

func cityShiftMix(val uint64) uint64 {
	return val ^ (val >> 47)
}

func cityHashLen16WithMul(u, v, mul uint64) uint64 {
	a := (u ^ v) * mul
	a ^= a >> 47
	b := (v ^ a) * mul
	b ^= b >> 47
	return b * mul
}

func cityHashLen16(u, v uint64) uint64 {
	return cityHashLen16WithMul(u, v, 0x9ddfea08eb382d69)
}

func cityFetch64(b []byte, off int) uint64 {
	return binary.LittleEndian.Uint64(b[off : off+8])
}

func cityWeakHashLen32WithSeeds(w, x, y, z, a, b uint64) (uint64, uint64) {
	a += w
	b = cityRotate64(b+a+z, 21)
	c := a
	a += x
	a += y
	b += cityRotate64(a, 44)
	return a + z, b + c
}

func cityWeakHashLen32WithSeedsBytes(data []byte, off int, a, b uint64) (uint64, uint64) {
	return cityWeakHashLen32WithSeeds(
		cityFetch64(data, off),
		cityFetch64(data, off+8),
		cityFetch64(data, off+16),
		cityFetch64(data, off+24),
		a, b,
	)
}

func cityHash64Len0to16(s []byte) uint64 {
	l := len(s)
	if l >= 8 {
		mul := cityK2 + uint64(l)*2
		a := cityFetch64(s, 0) + cityK2
		b := cityFetch64(s, l-8)
		c := cityRotate64(b, 37)*mul + a
		d := (cityRotate64(a, 25) + b) * mul
		return cityHashLen16WithMul(c, d, mul)
	}
	if l >= 4 {
		mul := cityK2 + uint64(l)*2
		a := uint64(binary.LittleEndian.Uint32(s[0:4]))
		return cityHashLen16WithMul(uint64(l)+(a<<3), uint64(binary.LittleEndian.Uint32(s[l-4:l])), mul)
	}
	if l > 0 {
		a := uint32(s[0])
		b := uint32(s[l>>1])
		c := uint32(s[l-1])
		y := a + (b << 8)
		z := uint32(l) + (c << 2)
		return cityShiftMix(uint64(y)*cityK2^uint64(z)*cityK0) * cityK2
	}
	return cityK2
}

func cityHash64Len17to32(s []byte) uint64 {
	l := len(s)
	mul := cityK2 + uint64(l)*2
	a := cityFetch64(s, 0) * cityK1
	b := cityFetch64(s, 8)
	c := cityFetch64(s, l-8) * mul
	d := cityFetch64(s, l-16) * cityK2
	return cityHashLen16WithMul(
		cityRotate64(a+b, 43)+cityRotate64(c, 30)+d,
		a+cityRotate64(b+cityK2, 18)+c,
		mul,
	)
}

func cityHash64Len33to64(s []byte) uint64 {
	l := len(s)
	mul := cityK2 + uint64(l)*2
	a := cityFetch64(s, 0) * cityK2
	b := cityFetch64(s, 8)
	c := cityFetch64(s, l-24)
	d := cityFetch64(s, l-32)
	e := cityFetch64(s, 16) * cityK2
	f := cityFetch64(s, 24) * 9
	g := cityFetch64(s, l-8)
	h := cityFetch64(s, l-16) * mul

	u := cityRotate64(a+g, 43) + (cityRotate64(b, 30)+c)*9
	v := ((a + g) ^ d) + f + 1
	w := bswap64((u+v)*mul) + h
	x := cityRotate64(e+f, 42) + c
	y := (bswap64((v+w)*mul) + g) * mul
	z := e + f + c
	a2 := bswap64((x+z)*mul+y) + b
	b2 := cityShiftMix((z+a2)*mul+d+h) * mul
	return b2 + x
}

func bswap64(v uint64) uint64 {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return binary.BigEndian.Uint64(b[:])
}

// cityHash64 is the >64-byte path (CityHash64's main loop).
func cityHash64(s []byte) uint64 {
	l := len(s)
	if l <= 32 {
		if l <= 16 {
			return cityHash64Len0to16(s)
		}
		return cityHash64Len17to32(s)
	}
	if l <= 64 {
		return cityHash64Len33to64(s)
	}

	x := cityFetch64(s, l-40)
	y := cityFetch64(s, l-16) + cityFetch64(s, l-56)
	z := cityHashLen16(cityFetch64(s, l-48)+uint64(l), cityFetch64(s, l-24))
	v0, v1 := cityWeakHashLen32WithSeedsBytes(s, l-64, uint64(l), z)
	w0, w1 := cityWeakHashLen32WithSeedsBytes(s, l-32, y+cityK1, x)
	x = x*cityK1 + cityFetch64(s, 0)

	// Decrease len to the nearest multiple of 64, and operate on 64-byte chunks.
	n := (l - 1) &^ 63
	for off := 0; off < n; off += 64 {
		x = cityRotate64(x+y+v0+cityFetch64(s, off+8), 37) * cityK1
		y = cityRotate64(y+v1+cityFetch64(s, off+48), 42) * cityK1
		x ^= w1
		y += v0 + cityFetch64(s, off+40)
		z = cityRotate64(z+w0, 33) * cityK1
		v0, v1 = cityWeakHashLen32WithSeedsBytes(s, off, v1*cityK1, x+w0)
		w0, w1 = cityWeakHashLen32WithSeedsBytes(s, off+32, z+w1, y+cityFetch64(s, off+16))
		z, x = x, z
	}

	return cityHashLen16(
		cityHashLen16(v0, w0)+cityShiftMix(y)*cityK1+z,
		cityHashLen16(v1, w1)+x,
	)
}
