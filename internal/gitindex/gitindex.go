// Package gitindex reads a working tree's git index in process: which paths it
// tracks, without spawning git. It reads no further into the index than the
// question needs, so what it costs does not grow with the repository.
package gitindex

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Dirs returns the git directory of the working tree at root, which holds its
// index, and the common one, which holds config and info/exclude. Both are ""
// when root is not a working tree. A linked worktree or a submodule keeps its
// .git elsewhere and leaves a pointer file behind; a linked worktree then
// shares everything but its index and HEAD with the main checkout.
func Dirs(root string) (own, common string, err error) {
	git := filepath.Join(root, ".git")
	fi, err := os.Stat(git)
	if err != nil {
		return "", "", nil //nolint:nilerr // no .git is not a failure, it is "not a working tree"
	}
	if fi.IsDir() {
		return git, git, nil
	}

	data, err := os.ReadFile(git)
	if err != nil {
		return "", "", err
	}
	own = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if own == "" {
		return "", "", fmt.Errorf("%s names no git directory", git)
	}
	if !filepath.IsAbs(own) {
		own = filepath.Join(root, own)
	}

	shared, err := os.ReadFile(filepath.Join(own, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return own, own, nil // a submodule: its own git dir is the whole story
	}
	if err != nil {
		return "", "", err
	}
	common = strings.TrimSpace(string(shared))
	if !filepath.IsAbs(common) {
		common = filepath.Join(own, common)
	}
	return own, filepath.Clean(common), nil
}

// Under reports whether the index of the working tree at root tracks dir, a
// slash-separated path relative to root: dir itself, a path below it, or one
// above it whose checkout supplies it. A tree with no index tracks nothing.
//
// Entries are sorted by path, so the read stops at the first one past dir.
// A split index is the exception: .git/index holds only the changes since the
// last split, and names the shared index holding the rest in an extension
// after its entries. Where a shared index exists, .git/index is read whole,
// then the shared index with the early stop.
func Under(root, dir string) (bool, error) {
	own, common, err := Dirs(root)
	if err != nil || own == "" {
		return false, err
	}
	index := filepath.Join(own, "index")
	if _, err := os.Stat(index); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	hashLen, err := hashLen(common)
	if err != nil {
		return false, err
	}
	s := scan{hashLen: hashLen, below: dir + "/"}

	// ponytail: a shared index left behind by --no-split-index makes this read
	// a plain index whole until git expires it (two weeks by default); the
	// answer stays right, only the cost grows.
	shared, err := filepath.Glob(filepath.Join(own, "sharedindex.*"))
	if err != nil || len(shared) == 0 {
		return s.file(index, nil)
	}
	return s.split(own, index)
}

// scan is one Under question, asked of one index file at a time.
type scan struct {
	hashLen int
	below   string // dir with a trailing slash
}

// under reports whether an entry tracks dir: a path below it, dir itself, or
// a path above it, such as a submodule whose checkout supplies dir or a sparse
// index's directory entry, which ends in a slash. Any case counts, since a
// case-insensitive filesystem checks every spelling out to the same place.
// That keeps the early stop sound for a lowercase dir: an uppercase letter
// sorts before its lowercase one, so every other spelling comes earlier, and
// so does every path above dir.
func (s scan) under(name string) bool {
	hasPrefix := func(str, prefix string) bool {
		return len(str) >= len(prefix) && strings.EqualFold(str[:len(prefix)], prefix)
	}
	return hasPrefix(name, s.below) || hasPrefix(s.below, strings.TrimSuffix(name, "/")+"/")
}

// file reads the index at path up to the first entry past dir, passing over
// the entries whose position deleted holds.
func (s scan) file(path string, deleted bitmap) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	d, err := newDecoder(f, s.hashLen)
	if err != nil {
		return false, err
	}
	for i := range uint64(d.count) {
		name, err := d.next()
		if err != nil {
			return false, err
		}
		if !deleted.has(i) && s.under(name) {
			return true, nil
		}
		if name > s.below {
			return false, nil
		}
	}
	return false, nil
}

// split reads the split index at index whole, then the shared index it links
// to, which sits beside it in own, with the early stop.
func (s scan) split(own, index string) (bool, error) {
	data, err := os.ReadFile(index)
	if err != nil {
		return false, err
	}
	d, err := newDecoder(bytes.NewReader(data), s.hashLen)
	if err != nil {
		return false, err
	}
	for range d.count {
		name, err := d.next()
		if err != nil {
			return false, err
		}
		if s.under(name) {
			return true, nil
		}
	}
	if len(data)-s.hashLen < d.off {
		return false, errors.New("truncated git index")
	}
	base, deleted, err := link(data[d.off:len(data)-s.hashLen], s.hashLen)
	if err != nil || base == "" {
		return false, err
	}
	return s.file(filepath.Join(own, "sharedindex."+base), deleted)
}

// link reads a split index's link extension out of the extensions that follow
// its entries: the shared index's object name in hex, and the positions of the
// shared entries this index deletes. The name is "" when there is no link.
// The replace bitmap after them is not read: git writes a replaced entry with
// an empty path, and the shared entry it replaces keeps its path.
func link(ext []byte, hashLen int) (string, bitmap, error) {
	for len(ext) >= 8 {
		size := uint64(binary.BigEndian.Uint32(ext[4:8]))
		if size > uint64(len(ext[8:])) {
			return "", nil, errors.New("truncated git index extension")
		}
		sig, body := string(ext[:4]), ext[8:8+size]
		ext = ext[8+size:]
		if sig != "link" {
			continue
		}
		if len(body) < hashLen {
			return "", nil, errors.New("truncated git index link extension")
		}
		deleted, err := ewah(body[hashLen:])
		return hex.EncodeToString(body[:hashLen]), deleted, err
	}
	return "", nil, nil
}

// bitmap holds set bits as sorted, disjoint [start, end) runs, so what it
// costs grows with the bitmap's bytes, not with the positions they describe.
type bitmap [][2]uint64

// add sets [start, end), which begins at or after every run already held.
func (m *bitmap) add(start, end uint64) {
	if n := len(*m); n > 0 && (*m)[n-1][1] == start {
		(*m)[n-1][1] = end
		return
	}
	*m = append(*m, [2]uint64{start, end})
}

// has reports whether bit pos is set.
func (m *bitmap) has(pos uint64) bool {
	_, found := slices.BinarySearchFunc(*m, pos, func(run [2]uint64, pos uint64) int {
		switch {
		case run[1] <= pos:
			return -1
		case run[0] > pos:
			return 1
		default:
			return 0
		}
	})
	return found
}

// ewah decodes git's EWAH bitmap into its set bits. It is a bit count, a word
// count, then the words: each marker word says how many words of all ones or
// all zeros follow, stored as nothing, and how many literal words follow it,
// stored as themselves, low bit first.
func ewah(b []byte) (bitmap, error) {
	if len(b) < 8 {
		return nil, errors.New("truncated git index bitmap")
	}
	bits := uint64(binary.BigEndian.Uint32(b))
	words := uint64(binary.BigEndian.Uint32(b[4:]))
	if words > uint64(len(b[8:]))/8 {
		return nil, errors.New("truncated git index bitmap")
	}
	word := func(i uint64) uint64 { return binary.BigEndian.Uint64(b[8+8*i:]) }

	var set bitmap
	var pos uint64
	for i := uint64(0); i < words; {
		marker := word(i)
		i++
		end := pos + (marker>>1&0xffffffff)*64
		if marker&1 != 0 && pos < min(end, bits) {
			set.add(pos, min(end, bits))
		}
		pos = end
		literals := marker >> 33
		if literals > words-i {
			return nil, errors.New("truncated git index bitmap")
		}
		for range literals {
			for bit := range uint64(64) {
				if word(i)>>bit&1 != 0 {
					set.add(pos+bit, pos+bit+1)
				}
			}
			pos += 64
			i++
		}
	}
	return set, nil
}

// decoder reads index entries one at a time, in index order.
type decoder struct {
	r       *bufio.Reader
	off     int // bytes read so far, where the extensions start once every entry is
	version uint32
	count   uint32
	hashLen int
	prev    string // the last path read, which a v4 path is cut from
}

// hashLen is the length of an object name in the repository whose common git
// directory is common: 32 bytes where its config sets extensions.objectFormat
// to sha256, else SHA-1's 20. Section and key names are case-insensitive.
func hashLen(common string) (int, error) {
	data, err := os.ReadFile(filepath.Join(common, "config"))
	if err != nil {
		return 0, err
	}
	var section string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "["); ok {
			section, _, _ = strings.Cut(rest, "]")
			section = strings.ToLower(strings.TrimSpace(section))
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		if section == "extensions" && strings.EqualFold(strings.TrimSpace(key), "objectformat") &&
			strings.EqualFold(strings.TrimSpace(value), "sha256") {
			return 32, nil
		}
	}
	return 20, nil
}

func newDecoder(r io.Reader, hashLen int) (*decoder, error) {
	d := &decoder{r: bufio.NewReader(r), hashLen: hashLen}
	header, err := d.read(12)
	if err != nil {
		return nil, err
	}
	if string(header[:4]) != "DIRC" {
		return nil, errors.New("not a git index")
	}
	d.version = binary.BigEndian.Uint32(header[4:])
	d.count = binary.BigEndian.Uint32(header[8:])
	return d, nil
}

// read returns the next n bytes.
func (d *decoder) read(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(d.r, b); err != nil {
		return nil, fmt.Errorf("truncated git index: %w", err)
	}
	d.off += n
	return b, nil
}

// path reads a NUL-terminated path, NUL included in what it counts.
func (d *decoder) path() (string, error) {
	b, err := d.r.ReadBytes(0)
	if err != nil {
		return "", fmt.Errorf("truncated git index: %w", err)
	}
	d.off += len(b)
	return string(b[:len(b)-1]), nil
}

// next returns the path of the next entry. An entry is 40 bytes of stat data,
// the object name, 2 bytes of flags, from v3 2 more when the flags say so, and
// the NUL-terminated path, padded with NULs to a multiple of 8 bytes.
func (d *decoder) next() (string, error) {
	fixed := 40 + d.hashLen + 2
	b, err := d.read(fixed)
	if err != nil {
		return "", err
	}
	if d.version >= 3 && binary.BigEndian.Uint16(b[fixed-2:])&0x4000 != 0 {
		if _, err := d.read(2); err != nil {
			return "", err
		}
		fixed += 2
	}
	if d.version == 4 {
		return d.nextV4()
	}
	name, err := d.path()
	if err != nil {
		return "", err
	}
	size := fixed + len(name) + 1
	if _, err := d.read((size+7)&^7 - size); err != nil {
		return "", err
	}
	return name, nil
}

// nextV4 reads a v4 path: a varint count of bytes to cut from the end of the
// previous path, then the NUL-terminated rest, with no padding.
func (d *decoder) nextV4() (string, error) {
	cut, err := d.varint()
	if err != nil {
		return "", err
	}
	if cut > uint64(len(d.prev)) {
		return "", errors.New("corrupt git index: a path cuts more than the previous one holds")
	}
	rest, err := d.path()
	if err != nil {
		return "", err
	}
	d.prev = d.prev[:uint64(len(d.prev))-cut] + rest
	return d.prev, nil
}

// varint reads git's offset encoding: 7 bits a byte, most significant first,
// each continuation adding one so no value has two spellings.
func (d *decoder) varint() (uint64, error) {
	var v uint64
	for {
		b, err := d.read(1)
		if err != nil {
			return 0, err
		}
		c := b[0]
		v = v<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			return v, nil
		}
		v++
	}
}
