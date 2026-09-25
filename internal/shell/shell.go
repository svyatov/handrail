// Package shell reads a command line as a bash program, syntax only (ADR 0011).
// It never expands, reads a file or runs anything: it reports what the program
// says, as written.
package shell

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Read parses line and returns its Candidates, each as its Spellings: the
// source text, then the unquoted form where that differs. A command statement
// is one Candidate, once more without its leading assignments, and once more
// for each Wrapper removed; code a listed shell runs adds its own. A redirect
// to a file is one. It also returns each file the line names, once. ok is
// false when the line will not parse, and then there are no Candidates, and
// when the line runs code handrail cannot read, which keeps the Candidates
// found. A truncated line is not a failure: error recovery keeps the commands
// before its malformed tail.
func Read(line string) (cands [][]string, files []File, ok bool) {
	var r reader
	if !r.read(line) {
		return nil, nil, false
	}
	return r.cands, r.files, !r.gaveUp
}

// Patch reads the patch a command line hands to apply_patch in the one form
// Codex intercepts and applies itself rather than running: apply_patch or
// applypatch fed a heredoc as the line's only statement, alone or after
// cd <dir> &&. body is the heredoc as written, since Codex applies it without
// a shell and nothing in it expands; dir is the cd operand, or empty.
func Patch(line string) (dir, body string, ok bool) {
	if !strings.Contains(line, "apply_patch") && !strings.Contains(line, "applypatch") {
		return "", "", false
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(line), "")
	if err != nil || len(f.Stmts) != 1 {
		return "", "", false
	}
	r := reader{text: line}
	dir, st, ok := r.patchDir(f.Stmts[0])
	if !ok {
		return "", "", false
	}
	body, ok = r.patchBody(st)
	if !ok {
		return "", "", false
	}
	return dir, body, true
}

// File is one file a command's syntax names: the target of a redirect to a
// file, or a file a listed program writes or reads.
type File struct {
	Path  string
	Write bool
	// Unreadable is true when the name holds an expansion, ~user, a glob or a
	// brace, which only running the shell resolves. Path is then the source
	// text, a best-effort reading.
	Unreadable bool
}

// reader collects the Candidates of one program as the walk meets them.
type reader struct {
	text  string // the program's source: the line, or code nested in it
	cands [][]string
	files []File
	// gaveUp is true once the program runs code handrail cannot read.
	gaveUp bool
	// fed holds the statements whose standard input comes from outside the
	// call: a pipe, or the pipe or input redirect of a group that holds them.
	fed map[*syntax.Stmt]bool
	// depth is how many re-parses deep the program is.
	depth int
}

// patchDir reads the cd <dir> && a patch statement may open with, and returns
// the directory, or empty, and the statement that follows.
func (r *reader) patchDir(st *syntax.Stmt) (string, *syntax.Stmt, bool) {
	and, isList := st.Cmd.(*syntax.BinaryCmd)
	if !isList {
		return "", st, true
	}
	cd, isCall := and.X.Cmd.(*syntax.CallExpr)
	if and.Op != syntax.AndStmt || !isCall || len(cd.Args) != 2 || cd.Args[0].Lit() != "cd" || !literal(cd.Args[1]) {
		return "", nil, false
	}
	return r.word(cd.Args[1]), and.Y, true
}

// patchBody reads the heredoc a lone apply_patch or applypatch is fed.
func (r *reader) patchBody(st *syntax.Stmt) (string, bool) {
	call, isCall := st.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) != 1 || len(st.Redirs) != 1 {
		return "", false
	}
	rd := st.Redirs[0]
	if name := call.Args[0].Lit(); name != "apply_patch" && name != "applypatch" || rd.Op != syntax.Hdoc || rd.Hdoc == nil {
		return "", false
	}
	return r.src(rd.Hdoc, rd.Hdoc), true
}

// read parses code and adds its Candidates, and reports whether it parsed.
func (r *reader) read(code string) bool {
	// Bash for every command, whatever shell the harness runs, as a declared
	// approximation (docs/spec.md section 2). Recovery supplies missing closing
	// tokens, one per open construct, so the bound only has to exceed the
	// nesting a real command line reaches.
	f, err := syntax.NewParser(syntax.RecoverErrors(8)).Parse(strings.NewReader(code), "")
	if err != nil {
		return false
	}
	r.text, r.fed = code, map[*syntax.Stmt]bool{}
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.BinaryCmd:
			// A pipeline nests to the left, so each pipe's right side is
			// every piped statement.
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				r.fed[n.Y] = true
			}
		case *syntax.Stmt:
			r.feed(n)
			r.stmt(n)
		case *syntax.Redirect:
			r.redirect(n)
		}
		return true
	})
	return true
}

// feed marks every statement a group holds as fed when the group itself is.
// The walk meets a group before the statements it holds, and each of them
// reads the group's input. A redirect alone, or a pipe with nothing after it,
// is a statement with no command.
func (r *reader) feed(st *syntax.Stmt) {
	if _, call := st.Cmd.(*syntax.CallExpr); call || st.Cmd == nil || !r.fed[st] && !input(st) {
		return
	}
	syntax.Walk(st.Cmd, func(m syntax.Node) bool {
		if st, ok := m.(*syntax.Stmt); ok {
			r.fed[st] = true
		}
		return true
	})
}

// input reports whether a statement redirects its standard input.
func input(st *syntax.Stmt) bool {
	return slices.ContainsFunc(st.Redirs, func(rd *syntax.Redirect) bool {
		switch rd.Op {
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			return rd.N == nil || rd.N.Value == "0"
		default:
			return false
		}
	})
}

// code reads code a listed shell or Wrapper runs, as $() contents are read.
// Code the call does not hold literally is read best-effort, each expansion as
// its source text, and declared. Re-parsing stops after four levels.
func (r *reader) code(text string, literal bool) {
	if r.depth == 4 {
		r.gaveUp = true
		return
	}
	inner := reader{depth: r.depth + 1}
	parsed := inner.read(text)
	r.cands = append(r.cands, inner.cands...)
	for _, f := range inner.files {
		r.file(f)
	}
	r.gaveUp = r.gaveUp || !literal || !parsed || inner.gaveUp
}

// add records one Candidate, with one Spelling where both are the same.
func (r *reader) add(source, unquoted string) {
	if unquoted == source {
		r.cands = append(r.cands, []string{source})
		return
	}
	r.cands = append(r.cands, []string{source, unquoted})
}

// file records a file the program names, once however many wrapping levels
// name it.
func (r *reader) file(f File) {
	if !slices.Contains(r.files, f) {
		r.files = append(r.files, f)
	}
}

// stmt adds a statement that runs a command. A statement that only groups
// others (a list, a pipeline, a subshell, a loop) adds nothing of its own,
// because the walk reaches the statements it holds.
func (r *reader) stmt(st *syntax.Stmt) {
	var from, to syntax.Node = st.Cmd, st.Cmd
	if len(st.Redirs) > 0 {
		from, to = earlier(st.Cmd, st.Redirs[0]), later(st.Cmd, st.Redirs[len(st.Redirs)-1])
	}
	switch cmd := st.Cmd.(type) {
	case nil, *syntax.BinaryCmd, *syntax.Block, *syntax.Subshell, *syntax.IfClause,
		*syntax.WhileClause, *syntax.ForClause, *syntax.CaseClause, *syntax.FuncDecl:
	case *syntax.CallExpr:
		r.command(st, cmd, from, to)
	case *syntax.DeclClause:
		words := append(make([]string, 0, 1+len(cmd.Args)), cmd.Variant.Value)
		for _, a := range cmd.Args {
			words = append(words, r.assign(a))
		}
		r.add(r.src(from, to), strings.Join(words, " "))
	default:
		source := r.src(from, to)
		r.add(source, source)
	}
}

// command adds a simple command statement, which runs from one node to
// another, and follows it into the program it names.
func (r *reader) command(st *syntax.Stmt, cmd *syntax.CallExpr, from, to syntax.Node) {
	words := make([]string, 0, len(cmd.Assigns)+len(cmd.Args))
	for _, a := range cmd.Assigns {
		words = append(words, r.assign(a))
	}
	for _, w := range cmd.Args {
		words = append(words, r.word(w))
	}
	r.add(r.src(from, to), strings.Join(words, " "))
	if len(cmd.Args) == 0 {
		return
	}
	// Each leading assignment prefix is its own level, so a rule reads the
	// command with it and without it.
	if len(cmd.Assigns) > 0 {
		r.add(r.src(cmd.Args[0], to), strings.Join(words[len(cmd.Assigns):], " "))
	}
	c := &call{st: st, args: cmd.Args, words: words[len(cmd.Assigns):], to: to, seen: map[int]bool{0: true}}
	r.follow(c, 0, len(c.args))
}

// redirect adds a redirect whose target is a file. fd duplication, heredocs,
// herestrings and the device files name no file.
func (r *reader) redirect(rd *syntax.Redirect) {
	// A target the parser had to supply is not in the line, so it names no file.
	if rd.Word == nil || rd.Word.Pos().IsRecovered() || namesNoFile(rd) {
		return
	}
	target := r.word(rd.Word)
	if slices.Contains(devices, target) || strings.HasPrefix(target, "/dev/fd/") {
		return
	}
	n := ""
	if rd.N != nil {
		n = rd.N.Value
	}
	r.add(r.src(rd, rd), n+rd.Op.String()+target)
	f := r.named(rd.Word)
	switch rd.Op {
	case syntax.RdrIn, syntax.DplIn:
		r.file(f)
	case syntax.RdrInOut:
		r.file(f)
		f.Write = true
		r.file(f)
	default:
		f.Write = true
		r.file(f)
	}
}

// devices are the device files a redirect may name, besides /dev/fd/N.
var devices = []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/tty"}

// namesNoFile reports whether a redirect's operator names no file whatever its
// target: a heredoc, a herestring, or an fd duplication.
func namesNoFile(rd *syntax.Redirect) bool {
	switch rd.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return true
	case syntax.DplIn, syntax.DplOut:
		// Only a bare descriptor duplicates one: Lit is empty for a quoted or
		// expanded target, which bash may open as a file.
		lit := rd.Word.Lit()
		return lit != "" && strings.Trim(lit, "0123456789") == "" || lit == "-"
	default:
		// Every other operator names a file.
		return false
	}
}

// named reads a word that names a file. A name only running the shell
// resolves keeps its source text and says so.
func (r *reader) named(w *syntax.Word) File {
	if !literal(w) || tilde(w) || slices.ContainsFunc(w.Parts, pattern) {
		return File{Path: r.src(w, w), Unreadable: true}
	}
	return File{Path: r.word(w)}
}

// tilde reports whether a word starts with ~user, which names another user's
// home directory. A bare ~ is the home directory, kept as written.
func tilde(w *syntax.Word) bool {
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && len(lit.Value) > 1 && lit.Value[0] == '~' && lit.Value[1] != '/'
}

// pattern reports whether an unquoted part holds an unescaped glob or brace
// character: only quoting and a backslash make them literal.
func pattern(part syntax.WordPart) bool {
	lit, ok := part.(*syntax.Lit)
	if !ok {
		return false
	}
	for i := 0; i < len(lit.Value); i++ {
		switch lit.Value[i] {
		case '\\':
			i++
		case '*', '?', '[', '{':
			return true
		}
	}
	return false
}

// src is the line's text from the start of one node to the end of another. A
// recovered end has no position, nor does the end of a token the parser
// supplied, and a node the parser had to close runs to the end of the line.
func (r *reader) src(from, to syntax.Node) string {
	end := to.End()
	if end.IsRecovered() || !end.IsValid() {
		return r.text[from.Pos().Offset():]
	}
	return r.text[from.Pos().Offset():end.Offset()]
}

// assign is an assignment's unquoted form. A declaration's plain word, such as
// a flag or a quoted "NAME=value", arrives as a naked assignment holding it.
// An indexed or array assignment stays as written.
func (r *reader) assign(a *syntax.Assign) string {
	if a.Naked && a.Name == nil && a.Value != nil {
		return r.word(a.Value)
	}
	if a.Name == nil || a.Index != nil || a.Array != nil || a.Value == nil {
		return r.src(a, a)
	}
	op := "="
	if a.Append {
		op = "+="
	}
	return a.Name.Value + op + r.word(a.Value)
}

// word is a word's unquoted form: quotes and escapes removed, and every
// expansion left as its source text, since handrail never expands.
func (r *reader) word(w *syntax.Word) string {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(unescape(p.Value, ""))
		case *syntax.SglQuoted:
			if p.Dollar {
				b.WriteString(ansiC(p.Value))
			} else {
				b.WriteString(p.Value)
			}
		case *syntax.DblQuoted:
			s, _ := r.dquoted(p.Parts, "$`\"\\\n")
			b.WriteString(s)
		default:
			b.WriteString(r.src(p, p))
		}
	}
	return b.String()
}

// dquoted is the unquoted form of the parts of a double-quoted string or a
// heredoc body, where a backslash quotes only the characters in special, and
// reports whether it holds no expansion.
func (r *reader) dquoted(parts []syntax.WordPart, special string) (string, bool) {
	var b strings.Builder
	literal := true
	for _, part := range parts {
		if lit, ok := part.(*syntax.Lit); ok {
			b.WriteString(unescape(lit.Value, special))
		} else {
			b.WriteString(r.src(part, part))
			literal = false
		}
	}
	return b.String(), literal
}

// unescape removes the backslashes bash removes. Unquoted, a backslash quotes
// any character; inside double quotes only the ones in special. Either way a
// backslash-newline is a line continuation and goes whole.
func unescape(s, special string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		next := s[i+1]
		switch {
		case next == '\n':
			i++
		case special == "" || strings.IndexByte(special, next) >= 0:
			i++
			b.WriteByte(next)
		default:
			b.WriteByte('\\')
		}
	}
	return b.String()
}

// ansiC decodes the body of a $'...' string as bash does, since r$'\155' is
// rm to bash and a rule must read it so. An unknown escape keeps its
// backslash, as bash keeps it.
func ansiC(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i = ansiEscape(&b, s, i+1)
	}
	return b.String()
}

// ansiEscape writes what the escape at s[i], just after its backslash, stands
// for, and returns the index of the escape's last character.
func ansiEscape(b *strings.Builder, s string, i int) int {
	if c := strings.IndexByte(`abeEfnrtv\'"?`, s[i]); c >= 0 {
		b.WriteByte("\a\b\x1b\x1b\f\n\r\t\v\\'\"?"[c])
		return i
	}
	switch s[i] {
	case 'c':
		if i+1 < len(s) {
			b.WriteByte(s[i+1] & 0x1f)
			return i + 1
		}
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n, end := digits(s, i, 3, 8)
		b.WriteByte(byte(n & 0xff)) // bash keeps the low eight bits of \777
		return end - 1
	case 'x', 'u', 'U':
		width := [...]int{2, 4, 8}[strings.IndexByte("xuU", s[i])]
		if n, end := digits(s, i+1, width, 16); end > i+1 {
			if s[i] == 'x' {
				b.WriteByte(byte(n & 0xff))
			} else {
				b.WriteRune(n)
			}
			return end - 1
		}
	}
	b.WriteByte('\\')
	b.WriteByte(s[i])
	return i
}

// digits reads up to maximum digits in base from s[start:], and returns their
// value and the index after the last one.
func digits(s string, start, maximum int, base rune) (n rune, end int) {
	for end = start; end < len(s) && end-start < maximum; end++ {
		d := base // not a digit
		switch c := s[end]; {
		case '0' <= c && c <= '9':
			d = rune(c - '0')
		case 'a' <= c|0x20 && c|0x20 <= 'f':
			d = rune(c|0x20-'a') + 10
		}
		if d >= base {
			break
		}
		n = n*base + d
	}
	return n, end
}

// earlier and later pick the node that starts first and the one that ends
// last, so a statement's text spans its command and its redirects in whatever
// order they were written. a may be nil, a statement of redirects alone.
func earlier(a, b syntax.Node) syntax.Node {
	if a == nil || b.Pos().Offset() < a.Pos().Offset() {
		return b
	}
	return a
}

// A recovered end has no offset and means the end of the line, so it is
// later than any end that has one.
func later(a, b syntax.Node) syntax.Node {
	switch {
	case a == nil, b.End().IsRecovered():
		return b
	case a.End().IsRecovered():
		return a
	case b.End().Offset() > a.End().Offset():
		return b
	}
	return a
}
