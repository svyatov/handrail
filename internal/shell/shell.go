// Package shell reads a command line as a bash program, syntax only (ADR 0011).
// It never expands, reads a file or runs anything: it reports what the program
// says, as written.
package shell

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Read parses line and returns its Candidates, each as its Spellings: the
// source text, then the unquoted form where that differs. A command statement
// is one Candidate, and once more without its leading assignments; a redirect
// to a file is one. ok is false when the line will not parse, and then there
// are no Candidates. A truncated line is not a failure: error recovery keeps
// the commands before its malformed tail.
func Read(line string) (cands [][]string, ok bool) {
	// Bash for every command, whatever shell the harness runs, as a declared
	// approximation (docs/spec.md section 2).
	f, err := syntax.NewParser(syntax.RecoverErrors(8)).Parse(strings.NewReader(line), "")
	if err != nil {
		return nil, false
	}
	r := reader{line: line}
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Stmt:
			r.stmt(n)
		case *syntax.Redirect:
			r.redirect(n)
		}
		return true
	})
	return r.cands, true
}

type reader struct {
	line  string
	cands [][]string
}

func (r *reader) add(source, unquoted string) {
	if unquoted == source {
		r.cands = append(r.cands, []string{source})
		return
	}
	r.cands = append(r.cands, []string{source, unquoted})
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
		words := make([]string, 0, len(cmd.Assigns)+len(cmd.Args))
		for _, a := range cmd.Assigns {
			words = append(words, r.assign(a))
		}
		for _, w := range cmd.Args {
			words = append(words, r.word(w))
		}
		r.add(r.src(from, to), strings.Join(words, " "))
		// Each leading assignment prefix is its own level, so a rule reads the
		// command with it and without it.
		if len(cmd.Assigns) > 0 && len(cmd.Args) > 0 {
			r.add(r.src(cmd.Args[0], to), strings.Join(words[len(cmd.Assigns):], " "))
		}
	case *syntax.DeclClause:
		words := []string{cmd.Variant.Value}
		for _, a := range cmd.Args {
			words = append(words, r.assign(a))
		}
		r.add(r.src(from, to), strings.Join(words, " "))
	default:
		source := r.src(from, to)
		r.add(source, source)
	}
}

// redirect adds a redirect whose target is a file. fd duplication, heredocs,
// herestrings and the device files name no file.
func (r *reader) redirect(rd *syntax.Redirect) {
	switch rd.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return
	case syntax.DplIn, syntax.DplOut:
		// Only a bare descriptor duplicates one: Lit is empty for a quoted or
		// expanded target, which bash may open as a file.
		if lit := rd.Word.Lit(); lit != "" && strings.Trim(lit, "0123456789") == "" || lit == "-" {
			return
		}
	}
	target := r.word(rd.Word)
	if target == "/dev/null" || target == "/dev/stdin" || target == "/dev/stdout" ||
		target == "/dev/stderr" || target == "/dev/tty" || strings.HasPrefix(target, "/dev/fd/") {
		return
	}
	n := ""
	if rd.N != nil {
		n = rd.N.Value
	}
	r.add(r.src(rd, rd), n+rd.Op.String()+target)
}

// src is the line's text from the start of one node to the end of another. A
// recovered end has no position, and a node the parser had to close runs to
// the end of the line.
func (r *reader) src(from, to syntax.Node) string {
	end := to.End()
	if end.IsRecovered() {
		return r.line[from.Pos().Offset():]
	}
	return r.line[from.Pos().Offset():end.Offset()]
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
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					b.WriteString(unescape(lit.Value, "$`\"\\\n"))
				} else {
					b.WriteString(r.src(inner, inner))
				}
			}
		default:
			b.WriteString(r.src(p, p))
		}
	}
	return b.String()
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
		i++
		if c := strings.IndexByte(`abeEfnrtv\'"?`, s[i]); c >= 0 {
			b.WriteByte("\a\b\x1b\x1b\f\n\r\t\v\\'\"?"[c])
			continue
		}
		switch s[i] {
		case 'c':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i] & 0x1f)
				continue
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			n, end := digits(s, i, 3, 8)
			b.WriteByte(byte(n & 0xff)) // bash keeps the low eight bits of \777
			i = end - 1
			continue
		case 'x', 'u', 'U':
			width := [...]int{2, 4, 8}[strings.IndexByte("xuU", s[i])]
			if n, end := digits(s, i+1, width, 16); end > i+1 {
				if s[i] == 'x' {
					b.WriteByte(byte(n & 0xff))
				} else {
					b.WriteRune(n)
				}
				i = end - 1
				continue
			}
		}
		b.WriteByte('\\')
		b.WriteByte(s[i])
	}
	return b.String()
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

func earlier(a, b syntax.Node) syntax.Node {
	if a == nil || b.Pos().Offset() < a.Pos().Offset() {
		return b
	}
	return a
}

func later(a, b syntax.Node) syntax.Node {
	if a == nil || b.End().IsRecovered() || !a.End().IsRecovered() && b.End().Offset() > a.End().Offset() {
		return b
	}
	return a
}
