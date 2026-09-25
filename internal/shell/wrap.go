package shell

import (
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// call is one simple command's words, from its first argument, as the
// Wrappers in it are stripped one level at a time.
type call struct {
	st    *syntax.Stmt
	args  []*syntax.Word
	words []string // each argument's unquoted form
	to    syntax.Node
	seen  map[int]bool // the words a Candidate already starts at
	// detached is true once a level runs under a Wrapper that gives its
	// command none of the call's standard input.
	detached bool
}

// level adds the command that runs from word i up to word j as a Candidate,
// then follows it into the program it names.
func (r *reader) level(c *call, i, j int) {
	if i >= j || c.seen[i] {
		return
	}

	c.seen[i] = true

	to := c.to
	if j < len(c.args) {
		to = c.args[j-1]
	}

	r.add(r.src(c.args[i], to), strings.Join(c.words[i:j], " "))
	r.follow(c, i, j)
}

// follow reads through the command at word i, which runs up to word j, when
// it names a listed shell or a Wrapper. A Wrapper that is a subcommand, such
// as mise exec, is listed under both words.
func (r *reader) follow(c *call, i, j int) {
	// An expansion in command position runs code the call does not hold.
	if !literal(c.args[i]) {
		r.gaveUp = true

		return
	}

	name := path.Base(c.words[i])
	r.listed(c, name, i+1, j)

	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh":
		r.shell(c, i+1, j)

		return
	case "eval":
		r.eval(c, i+1, j)

		return
	case "find":
		r.find(c, i+1, j)

		return
	}

	g, k := wrappers[name], i+1
	if g == nil && k < j {
		g, k = wrappers[name+" "+c.words[k]], k+1
	}

	if g != nil {
		c.detached = c.detached || g.detaches
		s := scan{r: r, c: c, g: g, j: j, done: map[int]bool{}}
		s.from(k)
	}
}

// eval reads the code eval runs: its words i up to j, after a leading --.
func (r *reader) eval(c *call, i, j int) {
	if i < j && c.words[i] == "--" {
		i++
	}

	r.code(c.text(i, j))
}

// find adds the command each -exec, -execdir, -ok and -okdir action runs. It
// ends at a ; or at a + that follows {}.
func (r *reader) find(c *call, i, j int) {
	for k := i; k < j; k++ {
		switch c.words[k] {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := k + 1
			for end < j && c.words[end] != ";" && (c.words[end] != "+" || c.words[end-1] != "{}") {
				end++
			}

			r.level(c, k+1, end)
			k = end
		}
	}
}

// shell reads the code a listed shell runs: the operand after a -c, which
// any short-flag cluster may hold, or its standard input when it names no
// script file. -o and -O take an option name, and -s reads standard input
// whatever the operands.
func (r *reader) shell(c *call, i, j int) {
	var o shellOptions

	k := o.read(c, i, j)
	switch {
	case o.command:
		if k < j {
			r.code(c.text(k, k+1))
		}
	case o.stdin || k >= j:
		r.stdin(c)
	}
}

// shellOptions is what a listed shell's options say it runs.
type shellOptions struct {
	command bool // a -c: the first operand is the code
	stdin   bool // a -s: standard input is the code
}

// read reads the options from word i up to j, and returns the word after
// them.
func (o *shellOptions) read(c *call, i, j int) int {
	k := i
	for ; k < j; k++ {
		w := c.words[k]
		if w == "-" || w == "--" {
			return k + 1
		}

		if len(w) < 2 || w[0] != '-' && w[0] != '+' {
			break
		}

		k += o.option(w)
	}

	return k
}

// option reads one word of options, and returns how many words their
// arguments take.
func (o *shellOptions) option(w string) int {
	if strings.HasPrefix(w, "--") {
		if w == "--rcfile" || w == "--init-file" {
			return 1
		}

		return 0
	}

	n := 0

	for _, f := range w[1:] {
		switch f {
		case 'c':
			o.command = true
		case 's':
			o.stdin = true
		case 'o', 'O':
			n++
		}
	}

	return n
}

// stdin reads what a shell with no script reads from its standard input. A
// literal heredoc or herestring is code the call holds; a pipe or a file is
// not. A shell xargs runs reads none of the call's input.
func (r *reader) stdin(c *call) {
	if c.detached {
		return
	}

	if r.fed[c.st] {
		r.gaveUp = true
	}

	for _, rd := range c.st.Redirs {
		if rd.N != nil && rd.N.Value != "0" {
			continue
		}

		switch rd.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			r.heredoc(rd)
		case syntax.WordHdoc:
			r.code(r.word(rd.Word), literal(rd.Word))
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn:
			r.gaveUp = true
		default:
			// An output redirect feeds the call nothing to read.
		}
	}
}

// heredoc reads the body of a heredoc a shell reads as its script.
func (r *reader) heredoc(rd *syntax.Redirect) {
	if rd.Hdoc == nil {
		return
	}
	// A quoted delimiter leaves the body as written.
	if d := rd.Word.Lit(); d == "" || strings.Contains(d, `\`) {
		r.code(rd.Hdoc.Lit(), true)
	} else {
		r.code(r.dquoted(rd.Hdoc.Parts, "$`\\\n"))
	}
}

// text joins words i up to j as eval joins them, and reports whether each is
// literal.
func (c *call) text(i, j int) (string, bool) {
	return strings.Join(c.words[i:j], " "), !slices.ContainsFunc(c.args[i:j], func(w *syntax.Word) bool { return !literal(w) })
}

// literal reports whether a word holds no expansion.
func literal(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if _, ok := inner.(*syntax.Lit); !ok {
					return false
				}
			}
		default:
			return false
		}
	}

	return true
}

// grammar is a Wrapper's argument grammar: its flag table, kept as data from
// the program's upstream documentation with GNU and BSD unioned, and what
// follows the flags.
type grammar struct {
	// short lists the short flags as getopt does: a letter followed by ':'
	// takes an argument, one followed by '::' takes the rest of its word if
	// any, one followed by '?' may or may not take one, and one followed by
	// ':?' takes the rest of its word, else may or may not take one.
	short string
	// long lists the long flags, marked the same way with a trailing '=' or
	// '?', or '==' for two arguments.
	long string
	// operands is how many operands come before the command, as timeout's
	// DURATION does.
	operands int
	// assigns is true for a program that takes NAME=VALUE operands before the
	// command, as env and sudo do.
	assigns bool
	// permute is true for a program that reads flags among its operands, so
	// that its command starts only after --, as mise exec's does.
	permute bool
	// stop lists the short and long flags that make the program run no
	// command, as command -v looks one up.
	stop []string
	// script lists the flags whose argument is a script, as env -S's is.
	script []string
	// joined is true for a program that joins its command's words and passes
	// them to sh -c, as watch does.
	joined bool
	// dashC is true for a program that passes the argument of a -c or
	// --command after its operands to sh -c, as flock does.
	dashC bool
	// detaches is true for a program that gives its command none of its own
	// standard input, as xargs reads it for arguments.
	detaches bool
	// splits is true for a program whose script flag's argument is split into
	// the first words of its command, as env -S's is.
	splits bool

	// The rest describe a listed file program (docs/spec.md section 1).

	// writes is true for a program that writes its file operands: always, or
	// only under a writeMode flag where it has one, as sed has -i.
	writes bool
	// first is true for a program whose first operand is its pattern or
	// script, unless a pattern or patternFile flag supplied one.
	first bool
	// last is true for a program that writes its last operand, or a target
	// flag's argument, and reads the others, as cp does.
	last bool
	// pattern lists the flags whose argument is the pattern or script, and
	// patternFile those whose argument is a file holding it, which is read.
	pattern, patternFile []string
	// writeMode lists the flags that make the program write its operands.
	writeMode []string
	// target lists the flags whose argument names the file written.
	target []string
	// input and output list the flags whose argument, the last where a flag
	// takes two, names a file the program reads or writes, as sort -o does.
	input, output []string
	// ends lists the flags after which no operand past the pattern names a
	// file, as jq --args makes them values.
	ends []string
}

// arity is how many arguments a flag takes: none, one, either, for a flag
// the implementations disagree on or the table lacks, attached, getopt's
// optional argument, which is the rest of the flag's word and never the next
// word, or two, as jq's --arg NAME VALUE takes. restOrEither is the rest of
// the flag's word, and with nothing there either, as GNU sed -i[SUFFIX] and
// BSD sed -i SUFFIX agree only on -i.bak.
type arity byte

const (
	none arity = iota
	one
	either
	attached
	two
	restOrEither
)

var wrappers = map[string]*grammar{
	// timeout: GNU coreutils timeout --help; FreeBSD and macOS timeout(1)
	"timeout": {short: "fk:ps:v", long: "foreground kill-after= preserve-status signal= verbose help version", operands: 1},
	// time: GNU time 1.9 time --help; FreeBSD and macOS time(1)
	"time": {short: "af:hlo:pqv", long: "append format= output= portability quiet verbose help version"},
	// nice: GNU coreutils nice --help, with obsolete -N; FreeBSD and macOS nice(1)
	"nice": {short: "n:0123456789", long: "adjustment= help version"},
	// nohup: GNU coreutils nohup --help; FreeBSD and macOS nohup(1)
	"nohup": {long: "help version"},
	// stdbuf: GNU coreutils stdbuf --help; FreeBSD stdbuf(1)
	"stdbuf": {short: "e:i:o:", long: "error= input= output= help version"},
	// command: bash manual, Bash Builtins, command [-pVv]
	"command": {short: "pvV", stop: []string{"v", "V"}},
	// builtin: bash manual, Bash Builtins, builtin [shell-builtin [args]]
	"builtin": {},
	// noglob: zsh manual, Precommand Modifiers
	"noglob": {},
	// exec: bash manual, Bourne Shell Builtins, exec [-cl] [-a name]
	"exec": {short: "a:cl"},
	// sudo: sudo 1.9 sudo(8)
	"sudo": {
		short:   "ABbEeHh?iKklNnPSsVva:c:C:D:g:p:R:r:T:t:U:u:",
		long:    "askpass auth-type= background bell close-from= chdir= preserve-env edit group= set-home help host= login login-class= remove-timestamp reset-timestamp list no-update non-interactive preserve-groups prompt= chroot= role= stdin shell type= command-timeout= other-user= user= version validate",
		assigns: true,
		stop:    []string{"e", "edit"},
	},
	// env: GNU coreutils env --help; FreeBSD and macOS env(1)
	"env": {
		short:   "0iC:L:P:S:U:u:v",
		long:    "ignore-environment null unset= chdir= split-string= ignore-signal default-signal block-signal list-signal-handling debug help version",
		assigns: true,
		script:  []string{"S", "split-string"},
		splits:  true,
	},
	// doas: OpenBSD doas(1); OpenDoas doas(1)
	"doas": {short: "a:C:Lnsu:"},
	// setsid: util-linux setsid(1)
	"setsid": {short: "cfwhV", long: "ctty fork wait help version"},
	// flock: util-linux flock(1)
	"flock": {
		short:    "eE:FhnosuVw:x",
		long:     "shared exclusive unlock nonblock nb no-fork close wait= timeout= conflict-exit-code= verbose help version",
		operands: 1,
		dashC:    true,
	},
	// watch: procps-ng watch(1)
	"watch": {
		short:  "bcCdeghn:pq:rs:tvwx",
		long:   "beep color no-color differences exec chgexit errexit help interval= precise equexit= no-rerun shotsdir= no-title version no-wrap",
		joined: true,
	},
	// su: util-linux su(1). A nested shell: its command is only the script
	// its -c names.
	"su": {
		short:   "c:fg:G:hlmPps:Vw:",
		long:    "command= session-command= fast group= supp-group= help login preserve-environment pty shell= version whitelist-environment=",
		permute: true,
		script:  []string{"c", "command", "session-command"},
	},
	// xargs: GNU findutils xargs --help; FreeBSD and macOS xargs(1)
	"xargs": {
		short:    "0a:d:E:e::I:i::J:L:l::n:oP:prR:S:s:tx",
		long:     "null arg-file= delimiter= eof replace max-lines max-args= max-procs= interactive no-run-if-empty max-chars= verbose exit open-tty process-slot-var= show-limits help version",
		detaches: true,
	},
	"mise exec": mise,
	"mise x":    mise,
	// direnv exec: direnv(1), direnv exec DIR COMMAND
	"direnv exec": {operands: 1},
	// devbox run: Jetify devbox docs, devbox run --help
	"devbox run": {short: "c:e:hlq", long: "config= env= env-file= environment= help list omit-nix-env pure quiet recompute"},
}

// mise is mise exec and its alias mise x, from the mise docs and mise exec
// --help.
var mise = &grammar{
	short:   "c:C:E:hj:qvy",
	long:    "command= jobs= allow-env= allow-net= allow-read= allow-write= deny-all deny-env deny-net deny-read deny-write fresh-env no-deps raw help cd= env= quiet verbose yes locked silent",
	permute: true,
	script:  []string{"c", "command"},
}

// lookup finds a flag in the table by its short letter, or by its long name
// exactly or, as getopt_long does, by unique prefix, and returns its full
// name. An ambiguous prefix is not found.
func (g *grammar) lookup(name string, long bool) (full string, a arity, found bool) {
	if !long {
		i := strings.Index(g.short, name)
		if name == ":" || name == "?" || i < 0 {
			return "", either, false
		}

		return name, marked(g.short[i+1:]), true
	}

	matches := 0

	for f := range strings.FieldsSeq(g.long) {
		bare := strings.TrimRight(f, "=?")
		if bare == name {
			return bare, marked(f[len(bare):]), true
		}

		if strings.HasPrefix(bare, name) {
			full, a = bare, marked(f[len(bare):])
			matches++
		}
	}

	if matches != 1 {
		return "", either, false
	}

	return full, a, true
}

// marked reads the arity mark that follows a flag's name.
func marked(rest string) arity {
	switch {
	case rest == "":
		return none
	case strings.HasPrefix(rest, "::"):
		return attached
	case strings.HasPrefix(rest, "=="):
		return two
	case strings.HasPrefix(rest, ":?"):
		return restOrEither
	case rest[0] == ':' || rest[0] == '=':
		return one
	case rest[0] == '?':
		return either
	}

	return none
}

// scan reads one Wrapper's flags and adds a level at each word where its
// command can start. Each reading of a flag of uncertain arity is followed,
// and a word a reading already reached is not read again.
type scan struct {
	r    *reader
	c    *call
	g    *grammar
	j    int
	done map[int]bool
	// split is the string a splitting flag gave, and splitLiteral whether the
	// call holds it literally.
	split        string
	splitLiteral bool
}

// from reads the Wrapper's arguments from word k.
func (s *scan) from(k int) {
	if s.done[k] {
		return
	}

	s.done[k] = true
	if k >= s.j {
		s.operands(k)

		return
	}

	w := s.c.words[k]
	switch {
	case w == "--":
		s.operands(k + 1)
	case strings.HasPrefix(w, "--"):
		s.long(k, w)
	case len(w) > 1 && w[0] == '-':
		s.cluster(k, w)
	case s.g.permute:
		s.from(k + 1)
	default:
		s.operands(k)
	}
}

// long reads a word that is one long flag, its argument attached after an =
// or else in the next word.
func (s *scan) long(k int, w string) {
	name, value, attached := strings.Cut(w[2:], "=")

	a, full := s.flag(name, true)
	if slices.Contains(s.g.stop, full) {
		return
	}

	if slices.Contains(s.g.script, full) {
		s.script(k, value, attached)
	}

	if a != none && !attached {
		s.from(k + 2)
	}

	if a != one || attached {
		s.from(k + 1)
	}
}

// cluster reads a word of short flags. A flag that takes an argument takes
// the rest of the word, or the next word when it ends the word.
func (s *scan) cluster(k int, w string) {
	for i := 1; i < len(w); i++ {
		a, full := s.flag(w[i:i+1], false)
		if slices.Contains(s.g.stop, full) {
			return
		}

		if slices.Contains(s.g.script, full) {
			s.script(k, w[i+1:], i < len(w)-1)
		}

		if a == none {
			continue
		}

		if a == attached {
			break
		}

		if i == len(w)-1 {
			s.from(k + 2)
		} else {
			s.from(k + 1)
		}

		if a == one {
			return
		}
	}

	s.from(k + 1)
}

// flag returns a flag's arity and full name. A flag the table lacks has no
// name, is declared, and is read both ways.
func (s *scan) flag(name string, long bool) (arity, string) {
	full, a, found := s.g.lookup(name, long)
	if !found {
		s.r.gaveUp = true
	}

	return a, full
}

// script reads a flag's argument as code: the rest of word k when attached,
// else the next word. A program that splits it into the first words of its
// command, as env -S does, keeps it for the operands that follow.
func (s *scan) script(k int, rest string, attached bool) {
	var (
		text string
		lit  bool
	)

	switch {
	case attached:
		text, lit = rest, literal(s.c.args[k])
	case k+1 < s.j:
		text, lit = s.c.text(k+1, k+2)
	default:
		return
	}

	if s.g.splits {
		s.split, s.splitLiteral = text, lit

		return
	}

	s.r.code(text, lit)
}

// operands skips what comes before the command and adds its level.
func (s *scan) operands(k int) {
	k = s.assigns(k + s.g.operands)
	if s.split != "" {
		s.splitCode(k)
	}

	switch {
	case k >= s.j:
	case s.g.dashC && (s.c.words[k] == "-c" || s.c.words[k] == "--command"):
		s.script(k, "", false)
	case s.g.joined:
		s.r.level(s.c, k, s.j)
		s.r.code(s.c.text(k, s.j))
	default:
		s.r.level(s.c, k, s.j)
	}
}

// assigns skips the NAME=VALUE operands from word k, where the program takes
// them, and returns the word after them. env reads a lone - as -i.
func (s *scan) assigns(k int) int {
	for s.g.assigns && k < s.j {
		name, _, ok := strings.Cut(s.c.words[k], "=")
		if s.c.words[k] != "-" && (!ok || !syntax.ValidName(name)) {
			break
		}

		k++
	}

	return k
}

// splitCode reads the code a splitting flag's string starts, with the
// operands from word k quoted after it.
func (s *scan) splitCode(k int) {
	words, lit := []string{s.split}, s.splitLiteral
	for i := k; i < s.j; i++ {
		quoted, _ := syntax.Quote(s.c.words[i], syntax.LangBash)
		words = append(words, quoted)
		lit = lit && literal(s.c.args[i])
	}

	s.r.code(strings.Join(words, " "), lit)
}
