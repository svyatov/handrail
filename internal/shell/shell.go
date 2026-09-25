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
// to a file is one. It also returns each file the line names, once. The bool
// is false when the line will not parse, and then there are no Candidates, and
// when the line runs code handrail cannot read, which keeps the Candidates
// found. A truncated line is not a failure: error recovery keeps the commands
// before its malformed tail.
func Read(line string) ([][]string, []File, bool) {
	var r reader
	if !r.read(line) {
		return nil, nil, false
	}

	return r.cands, r.files, !r.gaveUp
}

// Patch reads the patch a command line hands to apply_patch in the one form
// Codex intercepts and applies itself rather than running: apply_patch or
// applypatch fed a heredoc as the line's only statement, alone or after
// cd <dir> &&. It is nil for a line in any other form.
func Patch(line string) *PatchCall {
	if !strings.Contains(line, "apply_patch") && !strings.Contains(line, "applypatch") {
		return nil
	}

	prog, err := syntax.NewParser().Parse(strings.NewReader(line), "")
	if err != nil || len(prog.Stmts) != 1 {
		return nil
	}

	lineReader := reader{text: line, fed: nil, cands: nil, files: nil, depth: 0, gaveUp: false}

	dir, stmt, isPatch := lineReader.patchDir(prog.Stmts[0])
	if !isPatch {
		return nil
	}

	body, isPatch := lineReader.patchBody(stmt)
	if !isPatch {
		return nil
	}

	return &PatchCall{Dir: dir, Body: body}
}

// PatchCall is a patch Codex applies itself.
type PatchCall struct {
	// Dir is the cd operand, or empty.
	Dir string
	// Body is the heredoc as written, since Codex applies it without a shell
	// and nothing in it expands.
	Body string
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
	text string // the program's source: the line, or code nested in it
	// fed holds the statements whose standard input comes from outside the
	// call: a pipe, or the pipe or input redirect of a group that holds them.
	fed   map[*syntax.Stmt]bool
	cands [][]string
	files []File
	// depth is how many re-parses deep the program is.
	depth int
	// gaveUp is true once the program runs code handrail cannot read.
	gaveUp bool
}

// patchDir reads the cd <dir> && a patch statement may open with, and returns
// the directory, or empty, and the statement that follows.
func (r *reader) patchDir(stmt *syntax.Stmt) (string, *syntax.Stmt, bool) {
	and, isList := stmt.Cmd.(*syntax.BinaryCmd)
	if !isList {
		return "", stmt, true
	}

	cd, isCall := and.X.Cmd.(*syntax.CallExpr)
	if and.Op != syntax.AndStmt || !isCall || len(cd.Args) != 2 || cd.Args[0].Lit() != "cd" || !literal(cd.Args[1]) {
		return "", nil, false
	}

	return r.word(cd.Args[1]), and.Y, true
}

// patchBody reads the heredoc a lone apply_patch or applypatch is fed.
func (r *reader) patchBody(stmt *syntax.Stmt) (string, bool) {
	call, isCall := stmt.Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) != 1 || len(stmt.Redirs) != 1 {
		return "", false
	}

	redir := stmt.Redirs[0]
	if name := call.Args[0].Lit(); name != "apply_patch" && name != "applypatch" ||
		redir.Op != syntax.Hdoc || redir.Hdoc == nil {
		return "", false
	}

	return r.src(redir.Hdoc, redir.Hdoc), true
}

// maxRecovered bounds the closing tokens the parser may supply. Recovery
// supplies one per open construct, so the bound only has to exceed the
// nesting a real command line reaches.
const maxRecovered = 8

// read parses code and adds its Candidates, and reports whether it parsed.
func (r *reader) read(code string) bool {
	// Bash for every command, whatever shell the harness runs, as a declared
	// approximation (docs/spec.md section 2).
	prog, err := syntax.NewParser(syntax.RecoverErrors(maxRecovered)).Parse(strings.NewReader(code), "")
	if err != nil {
		return false
	}

	r.text, r.fed = code, map[*syntax.Stmt]bool{}

	syntax.Walk(prog, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.BinaryCmd:
			// A pipeline nests to the left, so each pipe's right side is
			// every piped statement.
			if node.Op == syntax.Pipe || node.Op == syntax.PipeAll {
				r.fed[node.Y] = true
			}
		case *syntax.Stmt:
			r.feed(node)
			r.stmt(node)
		case *syntax.Redirect:
			r.redirect(node)
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

// maxDepth is how many re-parses deep code nested in the line is read.
const maxDepth = 4

// code reads code a listed shell or Wrapper runs, as $() contents are read.
// Code the call does not hold literally is read best-effort, each expansion as
// its source text, and declared. Re-parsing stops after maxDepth levels.
func (r *reader) code(text string, literal bool) {
	if r.depth == maxDepth {
		r.gaveUp = true

		return
	}

	inner := reader{text: "", fed: nil, cands: nil, files: nil, depth: r.depth + 1, gaveUp: false}
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
func (r *reader) stmt(stmt *syntax.Stmt) {
	var start, end syntax.Node = stmt.Cmd, stmt.Cmd
	if len(stmt.Redirs) > 0 {
		start, end = earlier(stmt.Cmd, stmt.Redirs[0]), later(stmt.Cmd, stmt.Redirs[len(stmt.Redirs)-1])
	}

	switch cmd := stmt.Cmd.(type) {
	case nil, *syntax.BinaryCmd, *syntax.Block, *syntax.Subshell, *syntax.IfClause,
		*syntax.WhileClause, *syntax.ForClause, *syntax.CaseClause, *syntax.FuncDecl:
	case *syntax.CallExpr:
		r.command(stmt, cmd, start, end)
	case *syntax.DeclClause:
		words := append(make([]string, 0, 1+len(cmd.Args)), cmd.Variant.Value)
		for _, a := range cmd.Args {
			words = append(words, r.assign(a))
		}

		r.add(r.src(start, end), strings.Join(words, " "))
	default:
		source := r.src(start, end)
		r.add(source, source)
	}
}

// command adds a simple command statement, which runs from one node to
// another, and follows it into the program it names.
func (r *reader) command(stmt *syntax.Stmt, cmd *syntax.CallExpr, start, end syntax.Node) {
	words := make([]string, 0, len(cmd.Assigns)+len(cmd.Args))
	for _, a := range cmd.Assigns {
		words = append(words, r.assign(a))
	}

	for _, w := range cmd.Args {
		words = append(words, r.word(w))
	}

	r.add(r.src(start, end), strings.Join(words, " "))

	if len(cmd.Args) == 0 {
		return
	}
	// Each leading assignment prefix is its own level, so a rule reads the
	// command with it and without it.
	if len(cmd.Assigns) > 0 {
		r.add(r.src(cmd.Args[0], end), strings.Join(words[len(cmd.Assigns):], " "))
	}

	invocation := &call{
		st:       stmt,
		to:       end,
		seen:     map[int]bool{0: true},
		args:     cmd.Args,
		words:    words[len(cmd.Assigns):],
		detached: false,
	}
	r.follow(invocation, 0, len(invocation.args))
}

// redirect adds a redirect whose target is a file. fd duplication, heredocs,
// herestrings and the device files name no file.
func (r *reader) redirect(redir *syntax.Redirect) {
	// A target the parser had to supply is not in the line, so it names no file.
	if redir.Word == nil || redir.Word.Pos().IsRecovered() || namesNoFile(redir) {
		return
	}

	target := r.word(redir.Word)
	if device(target) {
		return
	}

	n := ""
	if redir.N != nil {
		n = redir.N.Value
	}

	r.add(r.src(redir, redir), n+redir.Op.String()+target)

	file := r.named(redir.Word)
	switch redir.Op {
	case syntax.RdrIn, syntax.DplIn:
		r.file(file)
	case syntax.RdrInOut:
		r.file(file)
		file.Write = true
		r.file(file)
	default:
		file.Write = true
		r.file(file)
	}
}

// device reports whether a redirect target is a device file: /dev/fd/N or
// one of the standard ones.
func device(target string) bool {
	switch target {
	case "/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	default:
		return strings.HasPrefix(target, "/dev/fd/")
	}
}

// namesNoFile reports whether a redirect's operator names no file whatever its
// target: a heredoc, a herestring, or an fd duplication.
func namesNoFile(redir *syntax.Redirect) bool {
	switch redir.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return true
	case syntax.DplIn, syntax.DplOut:
		// Only a bare descriptor duplicates one: Lit is empty for a quoted or
		// expanded target, which bash may open as a file.
		lit := redir.Word.Lit()

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
		return File{Path: r.src(w, w), Write: false, Unreadable: true}
	}

	return File{Path: r.word(w), Write: false, Unreadable: false}
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
func (r *reader) assign(assign *syntax.Assign) string {
	if assign.Naked && assign.Name == nil && assign.Value != nil {
		return r.word(assign.Value)
	}

	if assign.Name == nil || assign.Index != nil || assign.Array != nil || assign.Value == nil {
		return r.src(assign, assign)
	}

	op := "="
	if assign.Append {
		op = "+="
	}

	return assign.Name.Value + op + r.word(assign.Value)
}

// word is a word's unquoted form: quotes and escapes removed, and every
// expansion left as its source text, since handrail never expands.
func (r *reader) word(w *syntax.Word) string {
	var buf strings.Builder

	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			buf.WriteString(unescape(part.Value, ""))
		case *syntax.SglQuoted:
			if part.Dollar {
				buf.WriteString(ansiC(part.Value))
			} else {
				buf.WriteString(part.Value)
			}
		case *syntax.DblQuoted:
			s, _ := r.dquoted(part.Parts, "$`\"\\\n")
			buf.WriteString(s)
		default:
			buf.WriteString(r.src(part, part))
		}
	}

	return buf.String()
}

// dquoted is the unquoted form of the parts of a double-quoted string or a
// heredoc body, where a backslash quotes only the characters in special, and
// reports whether it holds no expansion.
func (r *reader) dquoted(parts []syntax.WordPart, special string) (string, bool) {
	var buf strings.Builder

	literal := true

	for _, part := range parts {
		if lit, ok := part.(*syntax.Lit); ok {
			buf.WriteString(unescape(lit.Value, special))
		} else {
			buf.WriteString(r.src(part, part))

			literal = false
		}
	}

	return buf.String(), literal
}

// unescape removes the backslashes bash removes. Unquoted, a backslash quotes
// any character; inside double quotes only the ones in special. Either way a
// backslash-newline is a line continuation and goes whole.
func unescape(text, special string) string {
	if !strings.Contains(text, `\`) {
		return text
	}

	var buf strings.Builder

	for pos := 0; pos < len(text); pos++ {
		if text[pos] != '\\' || pos == len(text)-1 {
			buf.WriteByte(text[pos])

			continue
		}

		next := text[pos+1]
		switch {
		case next == '\n':
			pos++
		case special == "" || strings.IndexByte(special, next) >= 0:
			pos++

			buf.WriteByte(next)
		default:
			buf.WriteByte('\\')
		}
	}

	return buf.String()
}

// ansiC decodes the body of a $'...' string as bash does, since r$'\155' is
// rm to bash and a rule must read it so. An unknown escape keeps its
// backslash, as bash keeps it.
func ansiC(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}

	var buf strings.Builder

	for pos := 0; pos < len(text); pos++ {
		if text[pos] != '\\' || pos == len(text)-1 {
			buf.WriteByte(text[pos])

			continue
		}

		pos = ansiEscape(&buf, text, pos+1)
	}

	return buf.String()
}

// The arithmetic of an ANSI-C escape: \cX keeps the low five bits of X, an
// octal or \x escape keeps the low eight of its value, an octal escape reads
// up to three digits, and a hex letter counts from ten.
const (
	ctrlBits    = 0x1f
	byteBits    = 0xff
	octalDigits = 3
	octal       = 8
	hex         = 16
	hexLetter   = 10
)

// ansiEscape writes what the escape at text[pos], just after its backslash,
// stands for, and returns the index of the escape's last character.
func ansiEscape(buf *strings.Builder, text string, pos int) int {
	if c := strings.IndexByte(`abeEfnrtv\'"?`, text[pos]); c >= 0 {
		buf.WriteByte("\a\b\x1b\x1b\f\n\r\t\v\\'\"?"[c])

		return pos
	}

	switch text[pos] {
	case 'c':
		if pos+1 < len(text) {
			buf.WriteByte(text[pos+1] & ctrlBits)

			return pos + 1
		}
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return digits(buf, text, pos, octalDigits, octal, true) - 1
	case 'x', 'u', 'U':
		width := [...]int{2, 4, 8}[strings.IndexByte("xuU", text[pos])]
		if end := digits(buf, text, pos+1, width, hex, text[pos] == 'x'); end > pos+1 {
			return end - 1
		}
	}

	buf.WriteByte('\\')
	buf.WriteByte(text[pos])

	return pos
}

// digits reads up to maximum digits in base from text[start:], writes what
// they stand for, a byte or a rune, and returns the index after the last one.
// With no digit it writes nothing. A byte keeps the low eight bits, as bash
// keeps those of \777.
func digits(buf *strings.Builder, text string, start, maximum int, base rune, asByte bool) int {
	var value rune

	end := start
	for ; end < len(text) && end-start < maximum; end++ {
		digit := digitValue(text[end])
		if digit >= base {
			break
		}

		value = value*base + digit
	}

	switch {
	case end == start:
	case asByte:
		buf.WriteByte(byte(value & byteBits))
	default:
		buf.WriteRune(value)
	}

	return end
}

// digitValue is a hex digit's value, or hex for a character that is a digit
// in no base an escape reads.
func digitValue(c byte) rune {
	switch {
	case '0' <= c && c <= '9':
		return rune(c - '0')
	case 'a' <= c|0x20 && c|0x20 <= 'f':
		return rune(c|0x20-'a') + hexLetter
	default:
		return hex
	}
}

// earlier and later pick the node that starts first and the one that ends
// last, so a statement's text spans its command and its redirects in whatever
// order they were written. cmd may be nil, a statement of redirects alone.
func earlier(cmd, redir syntax.Node) syntax.Node {
	if cmd == nil || redir.Pos().Offset() < cmd.Pos().Offset() {
		return redir
	}

	return cmd
}

// A recovered end has no offset and means the end of the line, so it is
// later than any end that has one.
func later(cmd, redir syntax.Node) syntax.Node {
	switch {
	case cmd == nil, redir.End().IsRecovered():
		return redir
	case cmd.End().IsRecovered():
		return cmd
	case redir.End().Offset() > cmd.End().Offset():
		return redir
	}

	return cmd
}
