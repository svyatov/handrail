// White box, because the bitmap decoder is unexported, and the input that
// matters here would hang the binary rather than fail a script test
// (ADR 0009).

package gitindex

import (
	"encoding/binary"
	"testing"
)

// bitmapOf is an EWAH bitmap of n bits holding the given words.
func bitmapOf(n uint32, words ...uint64) []byte {
	b := binary.BigEndian.AppendUint32(nil, n)
	b = binary.BigEndian.AppendUint32(b, uint32(len(words))) //nolint:gosec // a test bitmap holds a handful of words
	for _, w := range words {
		b = binary.BigEndian.AppendUint64(b, w)
	}
	return b
}

// A marker word says "this many words of ones" in 32 bits, so sixteen bytes
// can describe four billion deleted entries. Decoding them one position at a
// time took the hook down.
func TestALongRunOfOnesCostsOneRun(t *testing.T) {
	t.Parallel()
	deleted, err := ewah(bitmapOf(0xffffffff, 0xffffffff<<1|1))
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || !deleted.has(0) || !deleted.has(0xfffffffe) || deleted.has(0xffffffff) {
		t.Errorf("ewah() = %v, want the one run [0, 0xffffffff)", deleted)
	}
}

func TestLiteralWordsFollowTheirRun(t *testing.T) {
	t.Parallel()
	// One word of ones, then one literal word with bits 0 and 2 set.
	deleted, err := ewah(bitmapOf(192, 1<<33|1<<1|1, 0b101))
	if err != nil {
		t.Fatal(err)
	}
	for pos, want := range map[uint64]bool{0: true, 63: true, 64: true, 65: false, 66: true, 67: false} {
		if deleted.has(pos) != want {
			t.Errorf("has(%d) = %v, want %v", pos, !want, want)
		}
	}
}
