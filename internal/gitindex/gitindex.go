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

var (
	errNoGitDir        = errors.New("names no git directory")
	errTruncated       = errors.New("truncated git index")
	errTruncatedExt    = errors.New("truncated git index extension")
	errTruncatedLink   = errors.New("truncated git index link extension")
	errTruncatedBitmap = errors.New("truncated git index bitmap")
	errNotIndex        = errors.New("not a git index")
	errCutPastPrevious = errors.New("corrupt git index: a path cuts more than the previous one holds")
)

// GitDirs are the git directories of a working tree: Own holds its index, and
// Common holds config and info/exclude. Both are "" when there is no working
// tree. A linked worktree or a submodule keeps its .git elsewhere and leaves a
// pointer file behind; a linked worktree then shares everything but its index
// and HEAD with the main checkout.
type GitDirs struct {
	Own, Common string
}

// Dirs returns the git directories of the working tree at root.
func Dirs(root string) (GitDirs, error) {
	git := filepath.Join(root, ".git")

	fi, err := os.Stat(git)
	if err != nil {
		return GitDirs{Own: "", Common: ""}, nil //nolint:nilerr // no .git is not a failure, it is "not a working tree"
	}

	if fi.IsDir() {
		return GitDirs{Own: git, Common: git}, nil
	}

	data, err := os.ReadFile(git)
	if err != nil {
		return GitDirs{Own: "", Common: ""}, err
	}

	own := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if own == "" {
		return GitDirs{Own: "", Common: ""}, fmt.Errorf("%s %w", git, errNoGitDir)
	}

	if !filepath.IsAbs(own) {
		own = filepath.Join(root, own)
	}

	shared, err := os.ReadFile(filepath.Join(own, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return GitDirs{Own: own, Common: own}, nil // a submodule: its own git dir is the whole story
	}

	if err != nil {
		return GitDirs{Own: "", Common: ""}, err
	}

	common := strings.TrimSpace(string(shared))
	if !filepath.IsAbs(common) {
		common = filepath.Join(own, common)
	}

	return GitDirs{Own: own, Common: filepath.Clean(common)}, nil
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
	index, err := locate(root)
	if err != nil || index.path == "" {
		return false, err
	}

	search := scan{below: dir + "/", hashLen: index.hashLen}
	own := filepath.Dir(index.path)

	// ponytail: a shared index left behind by --no-split-index makes this read
	// a plain index whole until git expires it (two weeks by default); the
	// answer stays right, only the cost grows.
	shared, err := filepath.Glob(filepath.Join(own, "sharedindex.*"))
	if err != nil || len(shared) == 0 {
		return search.file(index.path, nil)
	}

	return search.split(own, index.path)
}

// Paths returns, sorted, every path the index of the working tree at root
// tracks that keep accepts. Unlike Under it reads the whole index, a split
// index's shared one included: a pattern has no place in the sort order to
// stop at.
func Paths(root string, keep func(string) bool) ([]string, error) {
	index, err := locate(root)
	if err != nil || index.path == "" {
		return nil, err
	}

	data, err := os.ReadFile(index.path)
	if err != nil {
		return nil, err
	}

	dec, err := newDecoder(bytes.NewReader(data), index.hashLen)
	if err != nil {
		return nil, err
	}

	paths, err := dec.collect(nil, keep)
	if err != nil {
		return nil, err
	}

	shared, err := sharedPaths(filepath.Dir(index.path), data, dec.off, index.hashLen, keep)
	if err != nil {
		return nil, err
	}

	paths = append(paths, shared...)
	slices.Sort(paths)

	// An unmerged path has an entry per stage.
	return slices.Compact(paths), nil
}

// located is a working tree's index, and the length of an object name in its
// repository.
type located struct {
	path    string
	hashLen int
}

// locate returns the index of the working tree at root. Its path is "" where
// there is no working tree, or no index yet, which tracks nothing.
func locate(root string) (located, error) {
	var none located

	dirs, err := Dirs(root)
	if err != nil || dirs.Own == "" {
		return none, err
	}

	path := filepath.Join(dirs.Own, "index")

	_, err = os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return none, nil
	}

	hashLen, err := hashLen(dirs.Common)

	return located{path: path, hashLen: hashLen}, err
}

// sharedIndex reads the link extension of the index in data, whose entries
// end at off: the path of the shared index it links to, which sits beside it
// in own, and the positions of the shared entries it deletes. The path is ""
// when there is no link.
func sharedIndex(own string, data []byte, off, hashLen int) (string, bitmap, error) {
	if len(data)-hashLen < off {
		return "", nil, errTruncated
	}

	base, deleted, err := link(data[off:len(data)-hashLen], hashLen)
	if err != nil || base == "" {
		return "", nil, err
	}

	return filepath.Join(own, "sharedindex."+base), deleted, nil
}

// sharedPaths returns the paths keep accepts from the shared index the index
// in data links to, less the entries it deletes, and none without a link.
func sharedPaths(own string, data []byte, off, hashLen int, keep func(string) bool) ([]string, error) {
	shared, deleted, err := sharedIndex(own, data, off, hashLen)
	if err != nil || shared == "" {
		return nil, err
	}

	indexFile, err := os.Open(shared)
	if err != nil {
		return nil, err
	}
	defer indexFile.Close()

	dec, err := newDecoder(indexFile, hashLen)
	if err != nil {
		return nil, err
	}

	return dec.collect(deleted, keep)
}

// scan is one Under question, asked of one index file at a time.
type scan struct {
	below   string // dir with a trailing slash
	hashLen int
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
	indexFile, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer indexFile.Close()

	dec, err := newDecoder(indexFile, s.hashLen)
	if err != nil {
		return false, err
	}

	found := false
	err = dec.each(deleted, func(name string) bool {
		found = s.under(name)

		return !found && name <= s.below
	})

	return found, err
}

// split reads the split index at index whole, then the shared index it links
// to, which sits beside it in own, with the early stop.
func (s scan) split(own, index string) (bool, error) {
	data, err := os.ReadFile(index)
	if err != nil {
		return false, err
	}

	dec, err := newDecoder(bytes.NewReader(data), s.hashLen)
	if err != nil {
		return false, err
	}

	found := false

	err = dec.each(nil, func(name string) bool {
		found = s.under(name)

		return !found
	})
	if err != nil || found {
		return found, err
	}

	shared, deleted, err := sharedIndex(own, data, dec.off, s.hashLen)
	if err != nil || shared == "" {
		return false, err
	}

	return s.file(shared, deleted)
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
			return "", nil, errTruncatedExt
		}

		sig, body := string(ext[:4]), ext[8:8+size]
		ext = ext[8+size:]

		if sig != "link" {
			continue
		}

		if len(body) < hashLen {
			return "", nil, errTruncatedLink
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
func ewah(data []byte) (bitmap, error) {
	const (
		headerLen     = 8         // the 32-bit bit count and the 32-bit word count
		wordBits      = 64        // bits in a word, run or literal
		runMask       = 1<<32 - 1 // a marker's bits 1-32, once shifted down, count its run's words
		literalsShift = 33        // a marker's bits 33-63 count its literal words
	)

	if len(data) < headerLen {
		return nil, errTruncatedBitmap
	}

	bits := uint64(binary.BigEndian.Uint32(data))

	words := uint64(binary.BigEndian.Uint32(data[4:]))
	if words > uint64(len(data[headerLen:]))/8 {
		return nil, errTruncatedBitmap
	}

	word := func(i uint64) uint64 { return binary.BigEndian.Uint64(data[headerLen+8*i:]) }

	var (
		set bitmap
		pos uint64
	)

	for idx := uint64(0); idx < words; {
		marker := word(idx)
		idx++

		end := pos + (marker>>1&runMask)*wordBits
		if marker&1 != 0 && pos < min(end, bits) {
			set.add(pos, min(end, bits))
		}

		pos = end

		literals := marker >> literalsShift
		if literals > words-idx {
			return nil, errTruncatedBitmap
		}

		for range literals {
			for bit := range uint64(wordBits) {
				if word(idx)>>bit&1 != 0 {
					set.add(pos+bit, pos+bit+1)
				}
			}

			pos += wordBits
			idx++
		}
	}

	return set, nil
}

// decoder reads index entries one at a time, in index order.
type decoder struct {
	r       *bufio.Reader
	prev    string // the last path read, which a v4 path is cut from
	off     int    // bytes read so far, where the extensions start once every entry is
	hashLen int
	version uint32
	count   uint32
}

// hashLen is the length of an object name in the repository whose common git
// directory is common: 32 bytes where its config sets extensions.objectFormat
// to sha256, else SHA-1's 20. Section and key names are case-insensitive.
func hashLen(common string) (int, error) {
	const sha1Len, sha256Len = 20, 32

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
			return sha256Len, nil
		}
	}

	return sha1Len, nil
}

func newDecoder(r io.Reader, hashLen int) (*decoder, error) {
	const headerLen = 12 // "DIRC", the version and the entry count, 4 bytes each

	dec := &decoder{r: bufio.NewReader(r), prev: "", off: 0, hashLen: hashLen, version: 0, count: 0}

	header, err := dec.read(headerLen)
	if err != nil {
		return nil, err
	}

	if string(header[:4]) != "DIRC" {
		return nil, errNotIndex
	}

	dec.version = binary.BigEndian.Uint32(header[4:])
	dec.count = binary.BigEndian.Uint32(header[8:])

	return dec, nil
}

// collect reads every entry, passing over the positions deleted holds, and
// returns the paths keep accepts.
func (d *decoder) collect(deleted bitmap, keep func(string) bool) ([]string, error) {
	var paths []string

	err := d.each(deleted, func(name string) bool {
		if keep(name) {
			paths = append(paths, name)
		}

		return true
	})

	return paths, err
}

// each reads the entries in order, passing each path to yield but those at
// the positions deleted holds, until yield returns false or the entries end.
func (d *decoder) each(deleted bitmap, yield func(name string) bool) error {
	for pos := range uint64(d.count) {
		name, err := d.next()
		if err != nil {
			return err
		}

		if !deleted.has(pos) && !yield(name) {
			return nil
		}
	}

	return nil
}

// read returns the next size bytes.
func (d *decoder) read(size int) ([]byte, error) {
	buf := make([]byte, size)

	_, err := io.ReadFull(d.r, buf)
	if err != nil {
		return nil, fmt.Errorf("truncated git index: %w", err)
	}

	d.off += size

	return buf, nil
}

// path reads a NUL-terminated path, NUL included in what it counts.
func (d *decoder) path() (string, error) {
	raw, err := d.r.ReadBytes(0)
	if err != nil {
		return "", fmt.Errorf("truncated git index: %w", err)
	}

	d.off += len(raw)

	return string(raw[:len(raw)-1]), nil
}

// next returns the path of the next entry. An entry is 40 bytes of stat data,
// the object name, 2 bytes of flags, from v3 2 more when the flags say so, and
// the NUL-terminated path, padded with NULs to a multiple of 8 bytes.
func (d *decoder) next() (string, error) {
	const (
		statLen        = 40 // ten 32-bit fields: ctime and mtime in two each, dev, ino, mode, uid, gid, size
		flagsLen       = 2
		pathCutVersion = 4 // from v4 a path is cut from the previous one
		entryAlign     = 8
	)

	fixed := statLen + d.hashLen + flagsLen

	entry, err := d.read(fixed)
	if err != nil {
		return "", err
	}

	if d.version >= 3 && binary.BigEndian.Uint16(entry[fixed-flagsLen:])&0x4000 != 0 {
		_, err = d.read(flagsLen)
		if err != nil {
			return "", err
		}

		fixed += flagsLen
	}

	if d.version == pathCutVersion {
		return d.nextV4()
	}

	name, err := d.path()
	if err != nil {
		return "", err
	}

	size := fixed + len(name) + 1

	_, err = d.read((size+entryAlign-1)&^(entryAlign-1) - size)
	if err != nil {
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
		return "", errCutPastPrevious
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
	const (
		bitsPerByte = 7
		more        = 0x80 // the high bit: another byte follows
	)

	var value uint64

	for {
		b, err := d.read(1)
		if err != nil {
			return 0, err
		}

		c := b[0]

		value = value<<bitsPerByte | uint64(c&^more)
		if c&more == 0 {
			return value, nil
		}

		value++
	}
}
