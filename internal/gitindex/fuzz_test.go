package gitindex

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// An index is the user's own file, but a repository unpacked from an archive
// brings one along, so whatever it holds, reading it ends in a path or an
// error.
func FuzzDecoder(f *testing.F) {
	for _, version := range []uint32{2, 3, 4} {
		f.Add(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32([]byte("DIRC"), version), 1))
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		dec, err := newDecoder(bytes.NewReader(data), 20)
		if err != nil {
			return
		}

		for range dec.count {
			_, err = dec.next()
			if err != nil {
				return
			}
		}
	})
}

// The second seed is the bitmap that once decoded into four billion positions.
func FuzzLink(f *testing.F) {
	for _, bitmap := range [][]byte{
		bitmapOf(64, 1<<33, 0b1011),
		bitmapOf(0xffffffff, 0xffffffff<<1|1),
	} {
		body := append(make([]byte, 20), bitmap...)
		size := uint32(len(body)) //nolint:gosec // a seed is a few dozen bytes
		f.Add(append(binary.BigEndian.AppendUint32([]byte("link"), size), body...))
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _, _ = link(data, 20)
	})
}
