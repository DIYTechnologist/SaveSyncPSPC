package reengine

import "testing"

func TestMurmur3_32KnownVectors(t *testing.T) {
	cases := []struct {
		data []byte
		seed uint32
		want uint32
	}{
		{[]byte(""), 0, 0},
		{[]byte("test"), 0, 3127628307},
		{[]byte("Hello, world!"), 0, 3224780355},
		{[]byte(""), 0xffffffff, 2180083513},
		{[]byte("The quick brown fox jumps over the lazy dog"), 42, 880582914},
	}
	for _, c := range cases {
		if got := murmur3_32(c.data, c.seed); got != c.want {
			t.Errorf("murmur3_32(%q, %d) = %d, want %d", c.data, c.seed, got, c.want)
		}
	}
}
