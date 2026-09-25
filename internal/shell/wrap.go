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
	to    syntax.Node
	seen  map[int]bool // the words a Candidate already starts at
	args  []*syntax.Word
	words []string // each argument's unquoted form
	// detached is true once a level runs under a Wrapper that gives its
	// command none of the call's standard input.
	detached bool
}

// level adds the command that runs from word start up to word end as a
// Candidate, then follows it into the program it names.
func (r *reader) level(cmd *call, start, end int) {
	if start >= end || cmd.seen[start] {
		return
	}

	cmd.seen[start] = true

	to := cmd.to
	if end < len(cmd.args) {
		to = cmd.args[end-1]
	}

	r.add(r.src(cmd.args[start], to), strings.Join(cmd.words[start:end], " "))
	r.follow(cmd, start, end)
}

// follow reads through the command at word start, which runs up to word end,
// when it names a listed shell or a Wrapper. A Wrapper that is a subcommand,
// such as mise exec, is listed under both words.
func (r *reader) follow(cmd *call, start, end int) {
	// An expansion in command position runs code the call does not hold.
	if !literal(cmd.args[start]) {
		r.gaveUp = true

		return
	}

	name := path.Base(cmd.words[start])
	r.listed(cmd, name, start+1, end)

	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh":
		r.shell(cmd, start+1, end)

		return
	case "eval":
		r.eval(cmd, start+1, end)

		return
	case "find":
		r.find(cmd, start+1, end)

		return
	}

	gram, next := wrappers[name], start+1
	if gram == nil && next < end {
		gram, next = wrappers[name+" "+cmd.words[next]], next+1
	}

	if gram != nil {
		cmd.detached = cmd.detached || gram.detaches
		s := scan{r: r, c: cmd, g: gram, done: map[int]bool{}, split: "", j: end, splitLiteral: false}
		s.from(next)
	}
}

// eval reads the code eval runs: its words start up to end, after a leading
// --.
func (r *reader) eval(cmd *call, start, end int) {
	if start < end && cmd.words[start] == "--" {
		start++
	}

	r.code(cmd.text(start, end))
}

// find adds the command each -exec, -execdir, -ok and -okdir action runs. It
// ends at a ; or at a + that follows {}.
func (r *reader) find(cmd *call, start, end int) {
	for pos := start; pos < end; pos++ {
		switch cmd.words[pos] {
		case "-exec", "-execdir", "-ok", "-okdir":
			last := pos + 1
			for last < end && cmd.words[last] != ";" && (cmd.words[last] != "+" || cmd.words[last-1] != "{}") {
				last++
			}

			r.level(cmd, pos+1, last)
			pos = last
		}
	}
}

// shell reads the code a listed shell runs: the operand after a -c, which
// any short-flag cluster may hold, or its standard input when it names no
// script file. -o and -O take an option name, and -s reads standard input
// whatever the operands.
func (r *reader) shell(cmd *call, start, end int) {
	var opts shellOptions

	pos := opts.read(cmd, start, end)
	switch {
	case opts.command:
		if pos < end {
			r.code(cmd.text(pos, pos+1))
		}
	case opts.stdin || pos >= end:
		r.stdin(cmd)
	}
}

// shellOptions is what a listed shell's options say it runs.
type shellOptions struct {
	command bool // a -c: the first operand is the code
	stdin   bool // a -s: standard input is the code
}

// read reads the options from word start up to end, and returns the word
// after them.
func (o *shellOptions) read(cmd *call, start, end int) int {
	pos := start
	for ; pos < end; pos++ {
		word := cmd.words[pos]
		if word == "-" || word == "--" {
			return pos + 1
		}

		if len(word) < 2 || word[0] != '-' && word[0] != '+' {
			break
		}

		pos += o.option(word)
	}

	return pos
}

// option reads one word of options, and returns how many words their
// arguments take.
func (o *shellOptions) option(word string) int {
	if strings.HasPrefix(word, "--") {
		if word == "--rcfile" || word == "--init-file" {
			return 1
		}

		return 0
	}

	taken := 0

	for _, flag := range word[1:] {
		switch flag {
		case 'c':
			o.command = true
		case 's':
			o.stdin = true
		case 'o', 'O':
			taken++
		}
	}

	return taken
}

// stdin reads what a shell with no script reads from its standard input. A
// literal heredoc or herestring is code the call holds; a pipe or a file is
// not. A shell xargs runs reads none of the call's input.
func (r *reader) stdin(cmd *call) {
	if cmd.detached {
		return
	}

	if r.fed[cmd.st] {
		r.gaveUp = true
	}

	for _, redir := range cmd.st.Redirs {
		if redir.N != nil && redir.N.Value != "0" {
			continue
		}

		switch redir.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			r.heredoc(redir)
		case syntax.WordHdoc:
			r.code(r.word(redir.Word), literal(redir.Word))
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn:
			r.gaveUp = true
		default:
			// An output redirect feeds the call nothing to read.
		}
	}
}

// heredoc reads the body of a heredoc a shell reads as its script.
func (r *reader) heredoc(redir *syntax.Redirect) {
	if redir.Hdoc == nil {
		return
	}
	// A quoted delimiter leaves the body as written.
	if d := redir.Word.Lit(); d == "" || strings.Contains(d, `\`) {
		r.code(redir.Hdoc.Lit(), true)
	} else {
		r.code(r.dquoted(redir.Hdoc.Parts, "$`\\\n"))
	}
}

// text joins words start up to end as eval joins them, and reports whether
// each is literal.
func (c *call) text(start, end int) (string, bool) {
	expands := func(w *syntax.Word) bool { return !literal(w) }

	return strings.Join(c.words[start:end], " "), !slices.ContainsFunc(c.args[start:end], expands)
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
	// stop lists the short and long flags that make the program run no
	// command, as command -v looks one up.
	stop []string
	// script lists the flags whose argument is a script, as env -S's is.
	script []string

	// pattern to ends, and writes, first and last below, describe a listed
	// file program (docs/spec.md section 1).

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

	// operands is how many operands come before the command, as timeout's
	// DURATION does.
	operands int
	// assigns is true for a program that takes NAME=VALUE operands before the
	// command, as env and sudo do.
	assigns bool
	// permute is true for a program that reads flags among its operands, so
	// that its command starts only after --, as mise exec's does.
	permute bool
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

	// writes is true for a program that writes its file operands: always, or
	// only under a writeMode flag where it has one, as sed has -i.
	writes bool
	// first is true for a program whose first operand is its pattern or
	// script, unless a pattern or patternFile flag supplied one.
	first bool
	// last is true for a program that writes its last operand, or a target
	// flag's argument, and reads the others, as cp does.
	last bool
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
	"timeout": {
		short:    "fk:ps:v",
		long:     "foreground kill-after= preserve-status signal= verbose help version",
		operands: 1,
	},
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
		short: "ABbEeHh?iKklNnPSsVva:c:C:D:g:p:R:r:T:t:U:u:",
		long: "askpass auth-type= background bell close-from= chdir= preserve-env edit group= set-home help host= login " +
			"login-class= remove-timestamp reset-timestamp list no-update non-interactive preserve-groups prompt= chroot= " +
			"role= stdin shell type= command-timeout= other-user= user= version validate",
		assigns: true,
		stop:    []string{"e", "edit"},
	},
	// env: GNU coreutils env --help; FreeBSD and macOS env(1)
	"env": {
		short: "0iC:L:P:S:U:u:v",
		long: "ignore-environment null unset= chdir= split-string= ignore-signal default-signal block-signal " +
			"list-signal-handling debug help version",
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
		short: "bcCdeghn:pq:rs:tvwx",
		long: "beep color no-color differences exec chgexit errexit help interval= precise equexit= no-rerun shotsdir= " +
			"no-title version no-wrap",
		joined: true,
	},
	// su: util-linux su(1). A nested shell: its command is only the script
	// its -c names.
	"su": {
		short: "c:fg:G:hlmPps:Vw:",
		long: "command= session-command= fast group= supp-group= help login preserve-environment pty shell= version " +
			"whitelist-environment=",
		permute: true,
		script:  []string{"c", "command", "session-command"},
	},
	// xargs: GNU findutils xargs --help; FreeBSD and macOS xargs(1)
	"xargs": {
		short: "0a:d:E:e::I:i::J:L:l::n:oP:prR:S:s:tx",
		long: "null arg-file= delimiter= eof replace max-lines max-args= max-procs= interactive no-run-if-empty " +
			"max-chars= verbose exit open-tty process-slot-var= show-limits help version",
		detaches: true,
	},
	"mise exec": mise,
	"mise x":    mise,
	// direnv exec: direnv(1), direnv exec DIR COMMAND
	"direnv exec": {operands: 1},
	// devbox run: Jetify devbox docs, devbox run --help
	"devbox run": {
		short: "c:e:hlq",
		long:  "config= env= env-file= environment= help list omit-nix-env pure quiet recompute",
	},
}

// mise is mise exec and its alias mise x, from the mise docs and mise exec
// --help.
var mise = &grammar{
	short: "c:C:E:hj:qvy",
	long: "command= jobs= allow-env= allow-net= allow-read= allow-write= deny-all deny-env deny-net deny-read " +
		"deny-write fresh-env no-deps raw help cd= env= quiet verbose yes locked silent",
	permute: true,
	script:  []string{"c", "command"},
}

// lookup finds a flag in the table by its short letter, or by its long name
// exactly or, as getopt_long does, by unique prefix, and returns its full
// name, its arity, and whether it is there. An ambiguous prefix is not found.
func (g *grammar) lookup(name string, long bool) (string, arity, bool) {
	if !long {
		i := strings.Index(g.short, name)
		if name == ":" || name == "?" || i < 0 {
			return "", either, false
		}

		return name, marked(g.short[i+1:]), true
	}

	var (
		full    string
		takes   arity
		matches int
	)

	for field := range strings.FieldsSeq(g.long) {
		bare := strings.TrimRight(field, "=?")
		if bare == name {
			return bare, marked(field[len(bare):]), true
		}

		if strings.HasPrefix(bare, name) {
			full, takes = bare, marked(field[len(bare):])
			matches++
		}
	}

	if matches != 1 {
		return "", either, false
	}

	return full, takes, true
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
	done map[int]bool
	// split is the string a splitting flag gave, and splitLiteral whether the
	// call holds it literally.
	split        string
	j            int
	splitLiteral bool
}

// withArg and withTwo are how many words a flag spans with the argument it
// takes in the next word, or the two it takes in the next two.
const (
	withArg = 2
	withTwo = 3
)

// from reads the Wrapper's arguments from word pos.
func (s *scan) from(pos int) {
	if s.done[pos] {
		return
	}

	s.done[pos] = true
	if pos >= s.j {
		s.operands(pos)

		return
	}

	word := s.c.words[pos]
	switch {
	case word == "--":
		s.operands(pos + 1)
	case strings.HasPrefix(word, "--"):
		s.long(pos, word)
	case len(word) > 1 && word[0] == '-':
		s.cluster(pos, word)
	case s.g.permute:
		s.from(pos + 1)
	default:
		s.operands(pos)
	}
}

// long reads a word that is one long flag, its argument attached after an =
// or else in the next word.
func (s *scan) long(pos int, word string) {
	name, value, attached := strings.Cut(word[2:], "=")

	// A flag the table lacks has no name, is declared, and is read both ways.
	full, takes, found := s.g.lookup(name, true)
	s.r.gaveUp = s.r.gaveUp || !found

	if slices.Contains(s.g.stop, full) {
		return
	}

	if slices.Contains(s.g.script, full) {
		s.script(pos, value, attached)
	}

	if takes != none && !attached {
		s.from(pos + withArg)
	}

	if takes != one || attached {
		s.from(pos + 1)
	}
}

// cluster reads a word of short flags. A flag that takes an argument takes
// the rest of the word, or the next word when it ends the word.
func (s *scan) cluster(pos int, word string) {
	for letter := 1; letter < len(word); letter++ {
		full, takes, found := s.g.lookup(word[letter:letter+1], false)
		s.r.gaveUp = s.r.gaveUp || !found

		if slices.Contains(s.g.stop, full) {
			return
		}

		if slices.Contains(s.g.script, full) {
			s.script(pos, word[letter+1:], letter < len(word)-1)
		}

		if takes == none {
			continue
		}

		if takes == attached {
			break
		}

		if letter == len(word)-1 {
			s.from(pos + withArg)
		} else {
			s.from(pos + 1)
		}

		if takes == one {
			return
		}
	}

	s.from(pos + 1)
}

// script reads a flag's argument as code: the rest of word pos when attached,
// else the next word. A program that splits it into the first words of its
// command, as env -S does, keeps it for the operands that follow.
func (s *scan) script(pos int, rest string, attached bool) {
	var (
		text string
		lit  bool
	)

	switch {
	case attached:
		text, lit = rest, literal(s.c.args[pos])
	case pos+1 < s.j:
		text, lit = s.c.text(pos+1, pos+withArg)
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
func (s *scan) operands(pos int) {
	pos = s.assigns(pos + s.g.operands)
	if s.split != "" {
		s.splitCode(pos)
	}

	switch {
	case pos >= s.j:
	case s.g.dashC && (s.c.words[pos] == "-c" || s.c.words[pos] == "--command"):
		s.script(pos, "", false)
	case s.g.joined:
		s.r.level(s.c, pos, s.j)
		s.r.code(s.c.text(pos, s.j))
	default:
		s.r.level(s.c, pos, s.j)
	}
}

// assigns skips the NAME=VALUE operands from word pos, where the program
// takes them, and returns the word after them. env reads a lone - as -i.
func (s *scan) assigns(pos int) int {
	for s.g.assigns && pos < s.j {
		name, _, ok := strings.Cut(s.c.words[pos], "=")
		if s.c.words[pos] != "-" && (!ok || !syntax.ValidName(name)) {
			break
		}

		pos++
	}

	return pos
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
