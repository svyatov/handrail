package shell

import (
	"slices"
	"strings"
)

// listed adds the files a listed file program names in its words i up to j
// (docs/spec.md section 1).
func (r *reader) listed(c *call, name string, i, j int) {
	switch name {
	case "find":
		r.findFiles(c, i, j)
	case "git":
		r.gitFiles(c, i, j)
	default:
		if g := programs[name]; g != nil {
			r.program(c, g, i, j)
		}
	}
}

// program adds the files a program with a flag table names. Each reading of
// a flag of uncertain arity counts, so the files of every reading are added.
func (r *reader) program(c *call, g *grammar, i, j int) {
	s := fileScan{r: r, c: c, g: g, j: j}
	s.from(i, reading{})
	if s.unknown {
		r.unread(c, g.writes, i, j)
		return
	}
	for _, rd := range s.readings {
		s.add(rd)
	}
}

// unread adds every word of a program whose grammar handrail could not read
// as a file of its own, which it could not read either: written if the
// program can write, else read.
func (r *reader) unread(c *call, write bool, i, j int) {
	dashes := false
	for k := i; k < j; k++ {
		w := c.words[k]
		if !dashes && (w == "--" || len(w) > 1 && w[0] == '-') {
			dashes = w == "--"
			continue
		}
		if write || w != "-" {
			f := r.named(c.args[k])
			f.Write, f.Unreadable = write, true
			r.file(f)
		}
	}
}

// reading is one way of reading a file program's arguments, as far as it has
// got.
type reading struct {
	operands []int  // the words read as operands
	flagged  []File // the files flags name
	supplied bool   // a flag supplied the pattern or script
	write    bool   // a flag set write mode
	targeted bool   // a flag named the file written
	// ends is the word of a flag after which no operand names a file, or 0,
	// which is never a flag's word: the program's name comes first.
	ends int
}

// fork copies a reading for a second way of reading the same flag.
func (rd reading) fork() reading {
	rd.operands, rd.flagged = slices.Clone(rd.operands), slices.Clone(rd.flagged)
	return rd
}

// files is the operands a reading takes as files: all but a first one that
// is the pattern or script, and none past the word of an ends flag.
func (rd reading) files(g *grammar) []int {
	ops := rd.operands
	if g.first && !rd.supplied && len(ops) > 0 {
		ops = ops[1:]
	}
	if rd.ends > 0 {
		ops = slices.DeleteFunc(slices.Clone(ops), func(k int) bool { return k > rd.ends })
	}
	return ops
}

// flagged adds to a reading the file a flag's argument names, if any, as
// written or read.
func flagged(rd *reading, f *File, write bool) {
	if f != nil {
		f.Write = write
		rd.flagged = append(rd.flagged, *f)
	}
}

// maxReadings bounds the readings of one call, since each flag of uncertain
// arity doubles them. A call past it is read as one with an unknown flag.
const maxReadings = 64

// fileScan reads a file program's arguments. A file program reads flags
// anywhere before --, as GNU's argument permutation does.
type fileScan struct {
	r        *reader
	c        *call
	g        *grammar
	j        int
	readings []reading
	// unknown is true once a flag the table lacks, or too many readings,
	// leaves the grammar unread.
	unknown bool
}

// from reads the arguments from word k.
func (s *fileScan) from(k int, rd reading) {
	for k < s.j && !s.unknown {
		w := s.c.words[k]
		switch {
		case w == "--":
			for k++; k < s.j; k++ {
				rd.operands = append(rd.operands, k)
			}
		case strings.HasPrefix(w, "--"):
			k = s.long(k, &rd)
		case len(w) > 1 && w[0] == '-':
			k = s.cluster(k, &rd)
		default:
			rd.operands = append(rd.operands, k)
			k++
		}
	}
	s.keep(rd)
}

// keep records a reading read to its end, unless it is one too many.
func (s *fileScan) keep(rd reading) {
	if len(s.readings) == maxReadings {
		s.unknown = true
	}
	if !s.unknown {
		s.readings = append(s.readings, rd)
	}
}

// add adds the files one reading of the program's arguments names.
func (s *fileScan) add(rd reading) {
	ops := rd.files(s.g)
	write := s.g.writes && (len(s.g.writeMode) == 0 || rd.write)
	for n, k := range ops {
		f := s.r.named(s.c.args[k])
		f.Write = write && (!s.g.last || !rd.targeted && n == len(ops)-1)
		// A lone - is standard input to a program reading it, and a file
		// named - to one writing it, as rm -- - and cp x - are.
		if f.Write || s.c.words[k] != "-" {
			s.r.file(f)
		}
	}
	for _, f := range rd.flagged {
		s.r.file(f)
	}
}

// long reads the long flag at word k and returns the word after it.
func (s *fileScan) long(k int, rd *reading) int {
	name, value, attached := strings.Cut(s.c.words[k][2:], "=")
	full, a, found := s.g.lookup(name, true)
	switch {
	case !found:
		s.unknown = true
	case attached:
		s.apply(rd, k, full, s.part(k, value))
		return k + 1
	case a == one:
		s.apply(rd, k, full, s.whole(k+1))
		return k + 2
	case a == two:
		s.apply(rd, k, full, s.whole(k+2))
		return k + 3
	case a == either:
		fork := rd.fork()
		s.apply(&fork, k, full, s.whole(k+1))
		s.from(k+2, fork)
	}
	s.apply(rd, k, full, nil)
	return k + 1
}

// cluster reads the word of short flags at k and returns the word after it.
// A flag that takes an argument takes the rest of the word, or the next word
// when it ends the word.
func (s *fileScan) cluster(k int, rd *reading) int {
	w := s.c.words[k]
	for i := 1; i < len(w); i++ {
		full, a, found := s.g.lookup(w[i:i+1], false)
		if !found {
			s.unknown = true
			return k + 1
		}
		if next, took := s.short(k, rd, full, a, w[i+1:]); took {
			return next
		}
	}
	return k + 1
}

// short reads the short flag full of arity a in the word at k, where rest
// follows it. It reports whether this reading takes an argument for it, which
// ends the word, and then returns the word after that argument.
func (s *fileScan) short(k int, rd *reading, full string, a arity, rest string) (int, bool) {
	switch {
	case a == attached:
		s.apply(rd, k, full, s.part(k, rest))
		return k + 1, true
	case (a == one || a == restOrEither) && rest != "":
		s.apply(rd, k, full, s.part(k, rest))
		return k + 1, true
	case a == one:
		s.apply(rd, k, full, s.whole(k+1))
		return k + 2, true
	case a == either && rest != "":
		fork := rd.fork()
		s.apply(&fork, k, full, s.part(k, rest))
		s.from(k+1, fork)
	case a == either || a == restOrEither:
		fork := rd.fork()
		s.apply(&fork, k, full, s.whole(k+1))
		s.from(k+2, fork)
	}
	s.apply(rd, k, full, nil)
	return 0, false
}

// whole is the file word k names as a flag's argument, if there is one.
func (s *fileScan) whole(k int) *File {
	if k >= s.j {
		return nil
	}
	f := s.r.named(s.c.args[k])
	return &f
}

// part is the file the rest of word k names as a flag's argument.
func (s *fileScan) part(k int, rest string) *File {
	if rest == "" {
		return nil
	}
	return &File{Path: rest, Unreadable: s.r.named(s.c.args[k]).Unreadable}
}

// apply takes the effect of the flag at word k, if it has one, with the file
// its argument names, if any.
func (s *fileScan) apply(rd *reading, k int, full string, f *File) {
	switch {
	case slices.Contains(s.g.input, full) || slices.Contains(s.g.output, full):
		flagged(rd, f, slices.Contains(s.g.output, full))
	case slices.Contains(s.g.ends, full):
		rd.ends = k
	case slices.Contains(s.g.pattern, full):
		rd.supplied = true
	case slices.Contains(s.g.patternFile, full):
		rd.supplied = true
		flagged(rd, f, false)
	case slices.Contains(s.g.writeMode, full):
		rd.write = true
	case slices.Contains(s.g.target, full):
		rd.targeted = true
		flagged(rd, f, true)
	}
}

// findFiles adds find's starting points, the operands before its
// expression, which it reads.
func (r *reader) findFiles(c *call, i, j int) {
	for k := r.findOptions(c, i, j); k < j; k++ {
		if w := c.words[k]; len(w) > 1 && w[0] == '-' || w == "(" || w == "!" {
			return
		}
		r.file(r.named(c.args[k]))
	}
}

// findOptions reads find's options from word i and returns the word after
// them.
func (r *reader) findOptions(c *call, i, j int) int {
	k := i
	for ; k < j; k++ {
		switch w := c.words[k]; {
		case w == "--":
			return k + 1
		case w == "-H", w == "-L", w == "-P", w == "-E", w == "-X", w == "-d", w == "-s", w == "-x",
			strings.HasPrefix(w, "-O"):
		case w == "-D":
			k++
		case w == "-f": // BSD: a starting point of its own
			if k+1 < j {
				r.file(r.named(c.args[k+1]))
			}
			k++
		default:
			return k
		}
	}
	return k
}

// gitFiles adds the directories git's -C and --work-tree name, and the
// files its grep and diff subcommands read.
func (r *reader) gitFiles(c *call, i, j int) {
	for k := i; k < j; k++ {
		w := c.words[k]
		if len(w) < 2 || w[0] != '-' {
			if sub := programs["git "+w]; sub != nil {
				r.program(c, sub, k+1, j)
			}
			return
		}
		next, ok := r.gitOption(c, k, j)
		if !ok {
			r.unread(c, false, i, j)
			return
		}
		k = next
	}
}

// gitOption reads the global option at word k, adding the directory it
// names, and returns the last word it takes. ok is false for an option git's
// table lacks or a cluster of short ones.
func (r *reader) gitOption(c *call, k, j int) (last int, ok bool) {
	w := c.words[k]
	name, value, attached := strings.Cut(strings.TrimLeft(w, "-"), "=")
	full, a, found := programs["git"].lookup(name, strings.HasPrefix(w, "--"))
	if !found || !strings.HasPrefix(w, "--") && len(w) > 2 {
		return k, false
	}
	switch {
	case attached:
		if full == "work-tree" {
			r.file(File{Path: value, Unreadable: r.named(c.args[k]).Unreadable})
		}
	case a == one:
		k++
		if k < j && (full == "C" || full == "work-tree") {
			r.file(r.named(c.args[k]))
		}
	}
	return k, true
}

// programs holds the listed file programs' flag tables: each flag's arity
// and at most one effect, from the program's upstream source with GNU and
// BSD unioned. An entry is a spec change; a flag entry is a code change.
var programs = map[string]*grammar{
	// Write.

	// rm: GNU coreutils 9.12 rm.c; FreeBSD rm.c; macOS file_cmds rm(1)
	"rm": {short: "dfiIPRrvWx", long: "force interactive one-file-system no-preserve-root preserve-root recursive dir verbose help version", writes: true},
	// rmdir: GNU coreutils 9.12 rmdir.c; FreeBSD rmdir.c; macOS file_cmds rmdir(1)
	"rmdir": {short: "pv", long: "ignore-fail-on-non-empty path parents verbose help version", writes: true},
	// mkdir: GNU coreutils 9.12 mkdir.c; FreeBSD mkdir.c; macOS file_cmds mkdir(1)
	"mkdir": {short: "m:pvZ", long: "mode= parents verbose context help version", writes: true},
	// touch: GNU coreutils 9.12 touch.c; FreeBSD touch.c; macOS file_cmds touch(1)
	"touch": {short: "A:acd:fhmr:t:", long: "time= no-create date= reference= no-dereference help version", writes: true, input: []string{"r", "reference"}},
	// tee: GNU coreutils 9.12 tee.c; FreeBSD tee.c; macOS shell_cmds tee(1)
	"tee": {short: "aip", long: "append ignore-interrupts output-error help version", writes: true},
	// cp: GNU coreutils 9.12 cp.c; FreeBSD cp.c; macOS file_cmds cp(1)
	"cp": {
		short:  "abcdfHilLNnPpRrsS?t:TuvXxZ",
		long:   "archive attributes-only backup copy-contents debug dereference force interactive link no-clobber no-dereference no-preserve= no-target-directory one-file-system parents path preserve recursive remove-destination sparse= reflink strip-trailing-slashes suffix= symbolic-link target-directory= update verbose keep-directory-symlink context sort help version",
		writes: true,
		last:   true,
		target: []string{"t", "target-directory"},
	},
	// mv: GNU coreutils 9.12 mv.c; FreeBSD mv.c; macOS file_cmds mv(1)
	"mv": {
		short:  "bfhinS:t:TuvZ",
		long:   "backup context debug exchange force interactive no-clobber no-copy no-target-directory strip-trailing-slashes suffix= target-directory= update verbose help version",
		writes: true,
		last:   true,
		target: []string{"t", "target-directory"},
	},
	// sed: GNU sed sed/sed.c; FreeBSD sed main.c; macOS text_cmds sed(1)
	"sed": {
		short:       "abEe:f:HI:i:?l?nrsuz",
		long:        "binary regexp-extended debug expression= file= in-place line-length= null-data zero-terminated quiet posix silent sandbox separate unbuffered follow-symlinks help version",
		writes:      true,
		first:       true,
		pattern:     []string{"e", "expression"},
		patternFile: []string{"f", "file"},
		writeMode:   []string{"i", "I", "in-place"},
	},

	// Read.

	// cat: GNU coreutils 9.12 cat.c; FreeBSD cat.c; macOS text_cmds cat(1)
	"cat": {short: "AbeEnstTuvl", long: "show-all number-nonblank show-ends number squeeze-blank show-tabs show-nonprinting help version"},
	// head: GNU coreutils 9.12 head.c (with obsolete -N); FreeBSD head.c; macOS text_cmds head(1)
	"head": {short: "0123456789bc:klmn:qvz", long: "bytes= lines= quiet silent verbose zero-terminated help version"},
	// tail: GNU coreutils 9.12 tail.c (with obsolete -N); FreeBSD tail.c; macOS text_cmds tail(1)
	"tail": {short: "0123456789b:c:Ffln:qrs:vz", long: "blocks= bytes= debug follow lines= max-unchanged-stats= pid= quiet retry silent sleep-interval= verbose zero-terminated help version"},
	// sort: GNU coreutils 9.12 sort.c; FreeBSD sort.c; macOS text_cmds sort(1)
	"sort": {short: "bcCdfghik:mMno:rRsS:t:T:uVy?z", long: "batch-size= buffer-size= check compress-program= debug dictionary-order field-separator= files0-from= general-numeric-sort heapsort human-numeric-sort ignore-case ignore-leading-blanks ignore-nonprinting key= merge mergesort mmap month-sort numeric-sort output= parallel= qsort radixsort random-sort random-source= reverse sort= stable temporary-directory= unique version-sort zero-terminated help version", input: []string{"files0-from", "random-source"}, output: []string{"o", "output"}},
	// uniq: GNU coreutils 9.12 uniq.c; FreeBSD uniq.c; macOS text_cmds uniq(1)
	"uniq": {short: "0123456789cdD?f:is:uw:z", long: "count repeated all-repeated group ignore-case unique skip-fields= skip-chars= check-chars= zero-terminated help version"},
	// wc: GNU coreutils 9.12 wc.c; FreeBSD wc.c and libxo xo_parse_args; macOS text_cmds wc(1)
	"wc": {short: "clLmw", long: "bytes chars lines words debug files0-from= max-line-length total= libxo= help version", input: []string{"files0-from"}},
	// cut: GNU coreutils 9.12 cut.c; FreeBSD cut.c; macOS text_cmds cut(1)
	"cut": {short: "b:c:d:f:F:nO:swz", long: "bytes= characters= fields= delimiter= no-partial whitespace-delimited only-delimited output-delimiter= complement zero-terminated help version"},
	// paste: GNU coreutils 9.12 paste.c; FreeBSD paste.c; macOS text_cmds paste(1)
	"paste": {short: "d:sz", long: "serial delimiters= zero-terminated help version"},
	// column: util-linux 2.41.2 text-utils/column.c; FreeBSD column.c; macOS text_cmds column(1)
	"column": {short: "C:c:dE:eH:hi:Jl:LN:n:mO:o:p:R:r:S:s:T:tVW:x", long: "columns= fillrows help json keep-empty-lines output-separator= output-width= separator= table table-columns= table-column= table-columns-limit= table-hide= table-name= table-maxout table-noextreme= table-noheadings table-order= table-right= table-truncate= table-wrap= table-empty-lines table-header-repeat tree= tree-id= tree-parent= use-spaces= version"},
	// tr: GNU coreutils 9.12 tr.c; FreeBSD tr.c; macOS text_cmds tr(1)
	"tr": {short: "AcCdstu", long: "complement delete squeeze-repeats truncate-set1 help version"},
	// file: file/file (darwinsys) src/file_opts.h and OPTSTRING; macOS file(1)
	"file": {short: "0bcCdDe:Ef:F:hiIklLm:M:nNpP:rsSvzZ", long: "apple brief checking-printout compile debug dereference exclude= exclude-quiet= extension files-from= help keep-going list magic-file= mime mime-encoding mime-type no-buffer no-dereference no-pad no-sandbox parameter= preserve-date print0 raw separator= special-files uncompress uncompress-noreport version", input: []string{"f", "files-from", "m", "magic-file", "M"}},
	// stat: GNU coreutils 9.12 stat --help; FreeBSD and macOS stat(1)
	"stat": {short: "c:Ff?hHlLnqrst?x", long: "cached= dereference file-system format= printf= terse help version"},
	// diff: GNU diffutils 3.12 src/diff.c; FreeBSD and macOS diff(1)
	"diff": {short: "0123456789aA:bBcC:dD:eEfF:hHiI:lL:nNpPqrsS:tTuU:vwW:x:X:yZ", long: "algorithm= binary brief changed-group-format= color context ed exclude= exclude-from= expand-tabs forward-ed from-file= help horizon-lines= ifdef= ignore-all-space ignore-blank-lines ignore-case ignore-file-name-case ignore-matching-lines= ignore-space-change ignore-tab-expansion ignore-trailing-space inhibit-hunk-merge initial-tab label= left-column line-format= minimal new-file new-group-format= new-line-format= no-dereference no-ignore-file-name-case normal old-group-format= old-line-format= paginate palette= rcs recursive report-identical-files sdiff-merge-assist show-c-function show-function-line= side-by-side speed-large-files starting-file= strip-trailing-cr suppress-blank-empty suppress-common-lines tabsize= text to-file= unchanged-group-format= unchanged-line-format= unidirectional-new-file unified version width=", input: []string{"from-file", "to-file", "X", "exclude-from"}},
	// cmp: GNU diffutils 3.12 src/cmp.c; FreeBSD and macOS cmp(1)
	"cmp": {short: "bchi:ln:svxz", long: "bytes= ignore-initial= print-bytes print-chars quiet silent verbose help version"},
	// comm: GNU coreutils 9.12 comm --help; FreeBSD and macOS comm(1)
	"comm": {short: "123iz", long: "check-order nocheck-order output-delimiter= total zero-terminated help version"},
	// awk: gawk 5.3.2 main.c, mawk init.c and onetrue awk main.c, unioned
	"awk": {short: "abcCd::D::e:E:f:F:ghi:Il:kL::MnNo::Op::Pr?sStv:VW:YZ:", long: "assign= bignum characters-as-bytes copyright csv debug dump dump-variables exec= field-separator= file= gen-pot help include= interactive lint lint-old load= locale= non-decimal-data no-optimize optimize parsedebug persist posix pretty-print profile random= re-interval sandbox source= sprintf= trace traditional usage use-lc-numeric version", first: true, pattern: []string{"e", "source"}, patternFile: []string{"f", "file", "E", "exec"}, input: []string{"i", "include"}},
	// gawk: gawk 5.3.2 main.c optlist and optab
	"gawk": {short: "bcCd::D::e:E:f:F:ghi:Il:kL::MnNo::Op::PrsStv:VW:YZ:", long: "assign= bignum characters-as-bytes copyright csv debug dump-variables exec= field-separator= file= gen-pot help include= lint lint-old load= locale= non-decimal-data no-optimize optimize parsedebug persist posix pretty-print profile re-interval sandbox source= trace traditional use-lc-numeric version", first: true, pattern: []string{"e", "source"}, patternFile: []string{"f", "file", "E", "exec"}, input: []string{"i", "include"}},
	// mawk: mawk(1) (invisible-island) and mawk-snapshots init.c
	"mawk": {short: "f:F:r?v:W:", long: "dump exec= help interactive lint lint-old non-decimal-data posix random= re-interval sprintf= traditional usage version", first: true, patternFile: []string{"f", "exec"}},
	// nawk: onetrue awk main.c (FreeBSD contrib/one-true-awk); macOS awk(1)
	"nawk": {short: "ad::f:F:sv:", long: "csv version", first: true, patternFile: []string{"f"}},
	// strings: GNU binutils strings.c; FreeBSD elftoolchain strings.c; macOS cctools strings(1)
	"strings": {short: "0123456789ade:fhHn:ost:T:U:vVw", long: "all bytes= data encoding= help include-all-whitespace output-separator= print-file-name radix= target= unicode= version"},
	// hexdump: util-linux text-utils/hexdump.c; FreeBSD and macOS hexdump(1)
	"hexdump": {short: "bcCde:f:hL::n:os:vVxX", long: "canonical color format= format-file= help length= no-squeezing one-byte-char one-byte-hex one-byte-octal skip= two-bytes-decimal two-bytes-hex two-bytes-octal version", input: []string{"f", "format-file"}},
	// od: GNU coreutils 9.12 od.c; FreeBSD and macOS od(1)
	"od": {short: "A:aBbcDdeFfHhIij:LlN:OoS:st:vw::Xx", long: "address-radix= endian= format= output-duplicates read-bytes= skip-bytes= strings traditional width help version"},
	// base64: GNU coreutils 9.12 base64 --help; FreeBSD bintrans.c; macOS base64(1)
	"base64": {short: "b:dDhi?o:w:", long: "break= decode ignore-garbage input= output= wrap= help version", input: []string{"i", "input"}, output: []string{"o", "output"}},
	// nl: GNU coreutils 9.12 nl --help; FreeBSD and macOS nl(1)
	"nl": {short: "b:d:f:h:i:l:n:ps:v:w:", long: "body-numbering= footer-numbering= header-numbering= join-blank-lines= line-increment= no-renumber number-format= number-separator= number-width= section-delimiter= starting-line-number= help version"},
	// tac: GNU coreutils 9.12 tac --help
	"tac": {short: "brs:", long: "before regex separator= help version"},
	// rev: util-linux text-utils/rev.c; FreeBSD and macOS rev(1)
	"rev": {short: "0hV", long: "zero help version"},
	// fold: GNU coreutils 9.12 fold.c; FreeBSD and macOS fold(1)
	"fold": {short: "bcsw:0::1::2::3::4::5::6::7::8::9::", long: "bytes characters spaces width= help version"},
	// expand: GNU coreutils 9.12 expand.c; FreeBSD and macOS expand(1)
	"expand": {short: "it:0::1::2::3::4::5::6::7::8::9::", long: "initial tabs= help version"},
	// unexpand: GNU coreutils 9.12 unexpand.c; FreeBSD and macOS unexpand(1)
	"unexpand": {short: ",0123456789at:", long: "all first-only tabs= help version"},
	// fmt: GNU coreutils 9.12 fmt.c; FreeBSD and macOS fmt(1)
	"fmt": {short: "0123456789cd:g:hl:mnp?st?uw:", long: "crown-margin goal= prefix= split-only tagged-paragraph uniform-spacing width= help version"},
	// pr: GNU coreutils 9.12 pr.c; FreeBSD and macOS pr(1)
	"pr": {short: "0123456789aD:cde::Ffh:i::JL:l:mn::N:o:prs::S::tTvw:W:", long: "across columns= date-format= double-space expand-tabs first-line-number= form-feed header= indent= join-lines length= merge no-file-warnings number-lines omit-header omit-pagination output-tabs page-width= pages= sep-string separator show-control-chars show-nonprinting width= help version"},
	// numfmt: GNU coreutils 9.12 numfmt --help
	"numfmt": {short: "d:z", long: "debug delimiter= field= format= from= from-unit= grouping header invalid= padding= round= suffix= to= to-unit= unit-separator= zero-terminated help version"},
	// tsort: GNU coreutils 9.12 tsort --help; FreeBSD and macOS tsort(1)
	"tsort": {short: "dlq", long: "help version"},
	// sha256sum, sha1sum, md5sum: GNU coreutils 9.12 --help; macOS and FreeBSD md5(1) GNU mode
	"sha256sum": sum,
	"sha1sum":   sum,
	"md5sum":    sum,
	// cd: bash manual, cd [-L|[-P [-e]] [-@]] [directory]
	"cd": {short: "LPe@", long: "help"},
	// ls: GNU coreutils 9.12 ls --help; macOS ls(1); FreeBSD ls(1)
	"ls": {
		short: "1@%,ABCD?FGHI?LNOPQRST?UWXZabcdefghiklmnopqrstuvw?xy",
		long:  "all almost-all author escape block-size= ignore-backups color directory dired classify file-type format= full-time group-directories-first group-directories? no-group human-readable si dereference-command-line dereference-command-line-symlink-to-dir hide= hyperlink indicator-style= inode ignore= kibibytes dereference numeric-uid-gid literal hide-control-chars show-control-chars quote-name quoting-style= reverse recursive size sort= time= time-style= tabsize= width= context zero help version",
	},
	// jq: jq 1.7 manual (jqlang.org/manual/v1.7); jq-1.7 and jq-1.7.1 src/main.c.
	// -f sets a mode that makes the first operand the filter file, which reads
	// the same files as a flag taking that file.
	"jq": {
		short:       "L:CMRSVabcef:hjnrs",
		long:        "null-input raw-input slurp compact-output raw-output raw-output0 join-output ascii-output sort-keys color-output monochrome-output tab indent= unbuffered stream stream-errors seq from-file= arg== argjson== slurpfile== rawfile== args jsonargs exit-status binary version build-configuration help run-tests= debug-dump-disasm debug-trace",
		first:       true,
		patternFile: []string{"f", "from-file"},
		input:       []string{"rawfile", "slurpfile", "run-tests"},
		ends:        []string{"args", "jsonargs"},
	},
	// grep, egrep, fgrep: GNU grep src/grep.c; FreeBSD and macOS grep.c
	"grep":  grep,
	"egrep": grep,
	"fgrep": grep,
	// rg: ripgrep 14.1.1 crates/core/flags/defs.rs
	"rg": {
		short:       "0.A:B:C:E:FHILM:NPST:UVabcd:e:f:g:hij:lm:nopqr:st:uvwxz",
		long:        "after-context= auto-hybrid-regex no-auto-hybrid-regex before-context= binary no-binary block-buffered no-block-buffered byte-offset no-byte-offset case-sensitive color= colors= column no-column context= context-separator= no-context-separator count count-matches crlf no-crlf debug dfa-size-limit= encoding= no-encoding engine= field-context-separator= field-match-separator= file= files files-with-matches files-without-match fixed-strings no-fixed-strings follow no-follow generate= glob= glob-case-insensitive no-glob-case-insensitive heading no-heading help hidden no-hidden hostname-bin= hyperlink-format= iglob= ignore-case ignore-file= ignore-file-case-insensitive no-ignore-file-case-insensitive include-zero no-include-zero invert-match no-invert-match json no-json line-buffered no-line-buffered line-number no-line-number line-regexp max-columns= max-columns-preview no-max-columns-preview max-count= max-depth= maxdepth= max-filesize= mmap no-mmap multiline no-multiline multiline-dotall no-multiline-dotall no-config no-ignore ignore no-ignore-dot ignore-dot no-ignore-exclude ignore-exclude no-ignore-files ignore-files no-ignore-global ignore-global no-ignore-messages ignore-messages no-ignore-parent ignore-parent no-ignore-vcs ignore-vcs no-messages messages no-pcre2-unicode pcre2-unicode no-require-git require-git no-unicode unicode null null-data one-file-system no-one-file-system only-matching path-separator= passthru passthrough pcre2 no-pcre2 pcre2-version pre= no-pre pre-glob= pretty quiet regex-size-limit= regexp= replace= search-zip no-search-zip smart-case sort-files no-sort-files sort= sortr= stats no-stats stop-on-nonmatch text no-text threads= trace trim no-trim type= type-add= type-clear= type-not= type-list unrestricted version vimgrep with-filename no-filename word-regexp",
		first:       true,
		pattern:     []string{"e", "regexp"},
		patternFile: []string{"f", "file"},
		input:       []string{"ignore-file"},
	},
	// git's global options, before the subcommand: git-scm.com/docs/git; git.c
	// handle_options. Its -C and --work-tree name a directory it reads.
	"git": {
		short: "C:c:hpPv",
		long:  "help version exec-path html-path man-path info-path paginate no-pager no-lazy-fetch no-replace-objects git-dir= namespace= work-tree= bare config-env= literal-pathspecs no-literal-pathspecs glob-pathspecs noglob-pathspecs icase-pathspecs no-optional-locks shallow-file= list-cmds= attr-source= no-advice",
	},
	// git grep: git-scm.com/docs/git-grep; git 2.54 git grep --help-all
	"git grep": {
		short:       "0123456789A:B:C:EFGHILO::PWace:f:hilm:nopqrvwz",
		long:        "cached no-cached no-index index untracked no-untracked exclude-standard no-exclude-standard recurse-submodules no-recurse-submodules invert-match no-invert-match ignore-case no-ignore-case word-regexp no-word-regexp text no-text textconv no-textconv recursive no-recursive max-depth= extended-regexp no-extended-regexp basic-regexp no-basic-regexp fixed-strings no-fixed-strings perl-regexp no-perl-regexp line-number no-line-number column no-column full-name no-full-name files-with-matches no-files-with-matches name-only no-name-only files-without-match no-files-without-match null no-null only-matching no-only-matching count no-count color no-color break no-break heading no-heading context= no-context before-context= after-context= threads= no-threads show-function no-show-function function-context no-function-context and or not quiet no-quiet all-match no-all-match open-files-in-pager no-open-files-in-pager ext-grep no-ext-grep max-count= no-max-count",
		first:       true,
		pattern:     []string{"e"},
		patternFile: []string{"f"},
	},
	// git diff: git-scm.com/docs/git-diff and diff-options; git diff.c and
	// builtin/diff.c; git diff --no-index --help-all
	"git diff": {
		short:  "0123B::C::DG:I:M::O:RS:U::WX::abhl:pqsuwz",
		long:   "patch no-patch unified raw patch-with-raw patch-with-stat numstat shortstat dirstat cumulative dirstat-by-file check summary name-only name-status stat stat-width= stat-name-width= stat-graph-width= stat-count= compact-summary no-compact-summary binary full-index no-full-index color no-color ws-error-highlight= abbrev no-abbrev src-prefix= dst-prefix= line-prefix= no-prefix default-prefix inter-hunk-context= output-indicator-new= output-indicator-old= output-indicator-context= break-rewrites find-renames irreversible-delete find-copies find-copies-harder no-find-copies-harder no-renames rename-empty no-rename-empty follow no-follow minimal ignore-all-space ignore-space-change ignore-space-at-eol ignore-cr-at-eol ignore-blank-lines ignore-matching-lines= no-ignore-matching-lines indent-heuristic no-indent-heuristic patience histogram diff-algorithm= anchored= word-diff word-diff-regex= color-words color-moved no-color-moved color-moved-ws= no-color-moved-ws relative no-relative text no-text exit-code no-exit-code quiet no-quiet ext-diff no-ext-diff textconv no-textconv ignore-submodules submodule ita-invisible-in-index ita-visible-in-index pickaxe-all pickaxe-regex rotate-to= skip-to= find-object= diff-filter= max-depth= output= cached staged merge-base no-index base ours theirs",
		input:  []string{"O"},
		output: []string{"output"},
	},
}

// sum is the GNU interface of sha256sum, sha1sum and md5sum, which BSD's md5
// also answers to under those names.
var sum = &grammar{short: "bctwz", long: "binary check tag text zero ignore-missing quiet status strict warn help version"}

// grep is GNU grep unioned with FreeBSD and macOS grep; egrep and fgrep take
// the same flags.
var grep = &grammar{
	short:       "0123456789A:B:C:D:EFGHIJLMOPRSTUVX?Zabcd:e:f:hilm:nopqrsuvwxyz",
	long:        "basic-regexp extended-regexp fixed-regexp fixed-strings perl-regexp after-context= before-context= binary-files= byte-offset context? color colour count devices= directories= exclude= exclude-from= exclude-dir= file= files-with-matches files-without-match group-separator= no-group-separator help include= include-dir= ignore-case no-ignore-case initial-tab label= line-buffered line-number line-regexp max-count= mmap no-filename no-messages null null-data only-matching quiet silent recursive dereference-recursive regexp= invert-match text binary unix-byte-offsets version with-filename word-regexp bz2decompress lzma xz decompress",
	first:       true,
	pattern:     []string{"e", "regexp"},
	patternFile: []string{"f", "file"},
	input:       []string{"exclude-from"},
}
