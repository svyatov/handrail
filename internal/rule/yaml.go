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

// The parse errors of the subset, each reported at the line it names.
var (
	errUnexpectedContent = errors.New("unexpected content")
	errTabIndent         = errors.New("tabs cannot be used for indentation")
	errExpectedBlock     = errors.New("expected an indented block")
	errUnexpectedIndent  = errors.New("unexpected indentation")
	errUnexpectedItem    = errors.New("unexpected list item")
	errExpectedKeyValue  = errors.New(`expected "key: value"`)
	errEmptyKey          = errors.New("empty key")
	errFlowStyle         = errors.New("flow style is not supported, use an indented block")
	errUnterminatedQuote = errors.New("unterminated quoted value")
	errTextAfterQuote    = errors.New("unexpected text after a quoted value")
	errInvalidQuote      = errors.New("invalid quoted value")
)

// node is one parsed value: a scalar, a mapping, or a sequence.
type node struct {
	scalar   string
	mapping  []pair
	seq      []*node
	line     int
	isScalar bool
}

// pair is one key and its value inside a mapping, in document order.
type pair struct {
	val  *node
	key  string
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
	reader := &parser{lines: lines, i: 0}
	reader.skip()

	if reader.i >= len(reader.lines) {
		return &node{scalar: "", mapping: nil, seq: nil, line: 1 + frontmatterOffset, isScalar: false}, nil
	}

	doc, err := reader.block(0)
	if err != nil {
		return nil, err
	}

	reader.skip()

	if reader.i < len(reader.lines) {
		return nil, reader.errAt(reader.i, errUnexpectedContent)
	}

	return doc, nil
}

// errAt reports err at the file line of lines[idx].
func (p *parser) errAt(idx int, err error) error {
	return fmt.Errorf("line %d: %w", idx+1+frontmatterOffset, err)
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
	line := p.lines[idx]

	ind := 0
	for ind < len(line) && line[ind] == ' ' {
		ind++
	}

	if ind < len(line) && line[ind] == '\t' {
		return 0, p.errAt(idx, errTabIndent)
	}

	return ind, nil
}

func isItem(trimmed string) bool {
	return trimmed == "-" || strings.HasPrefix(trimmed, "- ")
}

// block parses a mapping or sequence indented at least minInd.
func (p *parser) block(minInd int) (*node, error) {
	p.skip()

	if p.i >= len(p.lines) {
		return nil, p.errAt(p.i, errExpectedBlock)
	}

	ind, err := p.indentOf(p.i)
	if err != nil {
		return nil, err
	}

	if ind < minInd {
		return nil, p.errAt(p.i, errExpectedBlock)
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
		return false, p.errAt(p.i, errUnexpectedIndent)
	}

	return true, nil
}

func (p *parser) mapping(ind int) (*node, error) {
	out := &node{scalar: "", mapping: nil, seq: nil, line: p.i + 1 + frontmatterOffset, isScalar: false}
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
			return nil, p.errAt(p.i, errUnexpectedItem)
		}

		key, rest, ok := strings.Cut(t, ":")
		if !ok {
			return nil, p.errAt(p.i, errExpectedKeyValue)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, p.errAt(p.i, errEmptyKey)
		}

		idx := p.i
		line := idx + 1 + frontmatterOffset
		p.i++

		val, err := p.value(strings.TrimSpace(rest), ind, idx)
		if err != nil {
			return nil, err
		}

		out.mapping = append(out.mapping, pair{val: val, key: key, line: line})
	}

	return out, nil
}

// value parses what a key at indentation ind holds, rest being the text after
// its colon on line idx.
func (p *parser) value(rest string, ind, idx int) (*node, error) {
	line := idx + 1 + frontmatterOffset

	if !absent(rest) {
		scalar, err := parseScalar(rest)
		if err != nil {
			return nil, p.errAt(idx, err)
		}

		return &node{scalar: scalar, mapping: nil, seq: nil, line: line, isScalar: true}, nil
	}
	// Either an indented block, or a sequence at the key's own
	// indentation, or nothing at all.
	p.skip()

	if p.i < len(p.lines) {
		next, err := p.indentOf(p.i)
		if err != nil {
			return nil, err
		}

		if next > ind || (next == ind && isItem(strings.TrimSpace(p.lines[p.i]))) {
			return p.block(next)
		}
	}

	return &node{scalar: "", mapping: nil, seq: nil, line: line, isScalar: true}, nil
}

func (p *parser) seq(ind int) (*node, error) {
	out := &node{scalar: "", mapping: nil, seq: nil, line: p.i + 1 + frontmatterOffset, isScalar: false}
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

		out.seq = append(out.seq, child)
	}

	return out, nil
}

// item parses one sequence item at indentation ind from its trimmed line.
func (p *parser) item(ind int, trimmed string) (*node, error) {
	body := strings.TrimLeft(trimmed[1:], " ")
	if body == "" {
		p.i++

		return p.block(ind + 1)
	}
	// An item that opens with no key is a scalar: a url's first colon has
	// no space after it, and a quoted value is one value, colons and all.
	_, rest, keyed := strings.Cut(body, ":")

	keyed = keyed && (rest == "" || rest[0] == ' ' || rest[0] == '\t')
	if body[0] == '\'' || body[0] == '"' || !keyed {
		s, err := parseScalar(body)
		if err != nil {
			return nil, p.errAt(p.i, err)
		}

		n := &node{scalar: s, mapping: nil, seq: nil, line: p.i + 1 + frontmatterOffset, isScalar: true}
		p.i++

		return n, nil
	}
	// Rewrite "- key: value" as a plain mapping line at the column where
	// the item's own keys align, then parse the item as that mapping.
	col := ind + 1 + (len(trimmed) - 1 - len(body))
	p.lines[p.i] = strings.Repeat(" ", col) + body

	return p.mapping(col)
}

// absent reports whether the value half of a "key: value" line carries no value
// at all, which is how a mapping tells "key:" (a block follows) from `key: ""`
// (an empty string).
func absent(text string) bool { return text == "" || text[0] == '#' }

// parseScalar reads the value half of a "key: value" line, dropping a trailing
// comment. An absent value reads as the empty string.
func parseScalar(text string) (string, error) {
	if absent(text) {
		return "", nil
	}

	if text[0] == '[' || text[0] == '{' {
		return "", errFlowStyle
	}

	if text[0] != '\'' && text[0] != '"' {
		return plainScalar(text), nil
	}

	end := closingQuote(text)
	if end < 0 {
		return "", errUnterminatedQuote
	}

	if rest := strings.TrimSpace(text[end+1:]); rest != "" && rest[0] != '#' {
		return "", errTextAfterQuote
	}

	return unquote(text[:end+1])
}

// plainScalar reads an unquoted value, dropping a trailing comment.
func plainScalar(text string) string {
	// YAML ends a plain scalar at " #", so a value needing those two
	// characters has to be quoted anyway.
	if before, _, ok := strings.Cut(text, " #"); ok {
		return strings.TrimRight(before, " ")
	}

	return text
}

// closingQuote returns the index of the quote that closes the one text starts
// with, or -1 when there is none.
func closingQuote(text string) int {
	quote := text[0]
	for pos := 1; pos < len(text); pos++ {
		switch {
		case quote == '"' && text[pos] == '\\':
			pos++ // an escaped character cannot close the value
		case text[pos] != quote:
		case quote == '\'' && pos+1 < len(text) && text[pos+1] == '\'':
			pos++ // '' is an escaped single quote
		default:
			return pos
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
		return "", errInvalidQuote
	}

	return v, nil
}
