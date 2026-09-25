package rule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// This is a parser for the block-style YAML subset that ADR 0003 defines for
// rule frontmatter: mappings of scalars, plus a list of mappings for
// conditions. Anything outside that subset (flow style, anchors, multi-line
// scalars, multiple documents) is a parse error, which is the strictness the
// spec asks for. A full YAML parser would mean a third-party runtime
// dependency, which the spec forbids.

// node is one parsed value: a scalar, a mapping, or a sequence.
type node struct {
	line     int
	isScalar bool
	scalar   string
	mapping  []pair
	seq      []*node
}

// pair is one key and its value inside a mapping, in document order.
type pair struct {
	key  string
	val  *node
	line int
}

func (n *node) has(key string) bool {
	for _, kv := range n.mapping {
		if kv.key == key {
			return true
		}
	}

	return false
}

// isMapping reports whether n is neither a scalar nor a sequence. An empty
// document is a mapping with no keys.
func (n *node) isMapping() bool { return !n.isScalar && n.seq == nil }

type parser struct {
	// lines is owned by the parser, which rewrites sequence item lines in
	// place as it reads them. Callers pass a slice nobody else holds.
	lines []string
	i     int
}

// frontmatterOffset is the "---" line every rule file opens with: the parser
// never sees it, so error messages add it back to name the file's own line.
const frontmatterOffset = 1

// parseYAML parses the lines of a rule's frontmatter as one block-style document.
func parseYAML(lines []string) (*node, error) {
	p := &parser{lines: lines}
	p.skip()

	if p.i >= len(p.lines) {
		return &node{line: 1 + frontmatterOffset}, nil
	}

	n, err := p.block(0)
	if err != nil {
		return nil, err
	}

	p.skip()

	if p.i < len(p.lines) {
		return nil, p.errf(p.i, "unexpected content")
	}

	return n, nil
}

func (p *parser) errf(idx int, format string, a ...any) error {
	return fmt.Errorf("line %d: %s", idx+1+frontmatterOffset, fmt.Sprintf(format, a...))
}

// skip advances past blank and comment lines.
func (p *parser) skip() {
	for p.i < len(p.lines) {
		t := strings.TrimSpace(p.lines[p.i])
		if t != "" && !strings.HasPrefix(t, "#") {
			return
		}

		p.i++
	}
}

func (p *parser) indentOf(idx int) (int, error) {
	s := p.lines[idx]

	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}

	if n < len(s) && s[n] == '\t' {
		return 0, p.errf(idx, "tabs cannot be used for indentation")
	}

	return n, nil
}

func isItem(trimmed string) bool {
	return trimmed == "-" || strings.HasPrefix(trimmed, "- ")
}

// block parses a mapping or sequence indented at least minInd.
func (p *parser) block(minInd int) (*node, error) {
	p.skip()

	if p.i >= len(p.lines) {
		return nil, p.errf(p.i, "expected an indented block")
	}

	ind, err := p.indentOf(p.i)
	if err != nil {
		return nil, err
	}

	if ind < minInd {
		return nil, p.errf(p.i, "expected an indented block")
	}

	if isItem(strings.TrimSpace(p.lines[p.i])) {
		return p.seq(ind)
	}

	return p.mapping(ind)
}

// next advances to the next line of a block indented at ind, and reports false
// where the block has ended: at the end of the input or at a shallower line.
func (p *parser) next(ind int) (bool, error) {
	p.skip()

	if p.i >= len(p.lines) {
		return false, nil
	}

	cur, err := p.indentOf(p.i)
	if err != nil {
		return false, err
	}

	if cur < ind {
		return false, nil
	}

	if cur > ind {
		return false, p.errf(p.i, "unexpected indentation")
	}

	return true, nil
}

func (p *parser) mapping(ind int) (*node, error) {
	n := &node{line: p.i + 1 + frontmatterOffset}
	for {
		more, err := p.next(ind)
		if err != nil {
			return nil, err
		}

		if !more {
			break
		}

		t := strings.TrimSpace(p.lines[p.i])
		if isItem(t) {
			return nil, p.errf(p.i, "unexpected list item")
		}

		key, rest, ok := strings.Cut(t, ":")
		if !ok {
			return nil, p.errf(p.i, "expected %q", "key: value")
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, p.errf(p.i, "empty key")
		}

		idx := p.i
		line := idx + 1 + frontmatterOffset
		p.i++

		val, err := p.value(strings.TrimSpace(rest), ind, idx)
		if err != nil {
			return nil, err
		}

		n.mapping = append(n.mapping, pair{key: key, val: val, line: line})
	}

	return n, nil
}

// value parses what a key at indentation ind holds, rest being the text after
// its colon on line idx.
func (p *parser) value(rest string, ind, idx int) (*node, error) {
	line := idx + 1 + frontmatterOffset

	s, present, err := parseScalar(rest)
	if err != nil {
		return nil, p.errf(idx, "%v", err)
	}

	if present {
		return &node{isScalar: true, scalar: s, line: line}, nil
	}
	// Either an indented block, or a sequence at the key's own
	// indentation, or nothing at all.
	p.skip()

	if p.i < len(p.lines) {
		nx, err := p.indentOf(p.i)
		if err != nil {
			return nil, err
		}

		if nx > ind || (nx == ind && isItem(strings.TrimSpace(p.lines[p.i]))) {
			return p.block(nx)
		}
	}

	return &node{isScalar: true, line: line}, nil
}

func (p *parser) seq(ind int) (*node, error) {
	n := &node{line: p.i + 1 + frontmatterOffset}
	for {
		more, err := p.next(ind)
		if err != nil {
			return nil, err
		}

		if !more {
			break
		}

		t := strings.TrimSpace(p.lines[p.i])
		if !isItem(t) {
			break
		}

		child, err := p.item(ind, t)
		if err != nil {
			return nil, err
		}

		n.seq = append(n.seq, child)
	}

	return n, nil
}

// item parses one sequence item at indentation ind, t being its line trimmed.
func (p *parser) item(ind int, t string) (*node, error) {
	body := strings.TrimLeft(t[1:], " ")
	if body == "" {
		p.i++

		return p.block(ind + 1)
	}
	// An item that opens with no key is a scalar: a url's first colon has
	// no space after it, and a quoted value is one value, colons and all.
	_, rest, keyed := strings.Cut(body, ":")

	keyed = keyed && (rest == "" || rest[0] == ' ' || rest[0] == '\t')
	if body[0] == '\'' || body[0] == '"' || !keyed {
		s, _, err := parseScalar(body)
		if err != nil {
			return nil, p.errf(p.i, "%v", err)
		}

		n := &node{isScalar: true, scalar: s, line: p.i + 1 + frontmatterOffset}
		p.i++

		return n, nil
	}
	// Rewrite "- key: value" as a plain mapping line at the column where
	// the item's own keys align, then parse the item as that mapping.
	col := ind + 1 + (len(t) - 1 - len(body))
	p.lines[p.i] = strings.Repeat(" ", col) + body

	return p.mapping(col)
}

// parseScalar reads the value half of a "key: value" line, dropping a trailing
// comment. present is false when the line carries no value at all, which is how
// a mapping tells "key:" (a block follows) from `key: ""` (an empty string).
func parseScalar(s string) (value string, present bool, err error) {
	if s == "" || s[0] == '#' {
		return "", false, nil
	}

	if s[0] == '[' || s[0] == '{' {
		return "", false, errors.New("flow style is not supported, use an indented block")
	}

	if s[0] != '\'' && s[0] != '"' {
		return plainScalar(s), true, nil
	}

	end := closingQuote(s)
	if end < 0 {
		return "", false, errors.New("unterminated quoted value")
	}

	if rest := strings.TrimSpace(s[end+1:]); rest != "" && rest[0] != '#' {
		return "", false, errors.New("unexpected text after a quoted value")
	}

	value, err = unquote(s[:end+1])

	return value, true, err
}

// plainScalar reads an unquoted value, dropping a trailing comment.
func plainScalar(s string) string {
	// YAML ends a plain scalar at " #", so a value needing those two
	// characters has to be quoted anyway.
	if before, _, ok := strings.Cut(s, " #"); ok {
		return strings.TrimRight(before, " ")
	}

	return s
}

// closingQuote returns the index of the quote that closes the one s starts
// with, or -1 when there is none.
func closingQuote(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++ // an escaped character cannot close the value
		case s[i] != q:
		case q == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++ // '' is an escaped single quote
		default:
			return i
		}
	}

	return -1
}

// unquote resolves the two quoting styles the rule format needs. s is the
// quoted value with nothing after the closing quote, so its first byte is one
// of the two quotes and there is no third style to fall through to.
func unquote(s string) (string, error) {
	if s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}

	v, err := strconv.Unquote(s)
	if err != nil {
		return "", errors.New("invalid quoted value")
	}

	return v, nil
}
