// Package query implements the PaperManage search grammar and AST.
package query

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Node is a query AST node.
type Node interface{ node() }

// Binary is an AND or OR node.
type Binary struct {
	Op   string // AND | OR
	Kids []Node
}

// Not negates a child.
type Not struct{ Child Node }

// Term is a leaf.
type Term struct {
	Field  string // "" for bare term
	Value  string
	Prefix bool
	Range  bool
	Start  string
	End    string
	Pos    int // 1-based byte column for diagnostics
	Quoted bool
}

func (*Binary) node() {}
func (*Not) node()    {}
func (*Term) node()   {}

// Query is a parsed query.
type Query struct {
	Raw  string
	Root Node
	Sort string // relevance | year- | year+ | added- | added+ | title+ | title-
}

var Fields = map[string]bool{
	"title": true, "abstract": true, "author": true, "authors": true, "affiliation": true,
	"venue": true, "year": true, "added": true, "updated": true,
	"tag": true, "keyword": true, "keywords": true, "doi": true,
	"arxiv": true, "citekey": true, "type": true, "content": true,
	"note": true, "web": true, "md": true,
}

var Sorts = map[string]bool{
	"relevance": true, "year-": true, "year+": true, "added-": true,
	"added+": true, "title+": true, "title-": true, "updated-": true,
	"updated+": true,
}

// ParseError locates a syntax error.
type ParseError struct {
	Pos int // 1-based
	Msg string
}

func (e *ParseError) Error() string { return fmt.Sprintf("%s at col %d", e.Msg, e.Pos) }

type parser struct {
	src  string
	i    int
	sort string
}

// Parse parses a query expression. An empty expression matches everything.
func Parse(src string) (*Query, error) {
	p := &parser{src: src, sort: "relevance"}
	p.skipSpace()
	if p.eof() {
		return &Query{Raw: src, Sort: p.sort}, nil
	}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if !p.eof() {
		return nil, p.errf("unexpected %q, expected AND, OR or end of query", p.peekRune())
	}
	return &Query{Raw: src, Root: StripSort(root), Sort: p.sort}, nil
}

func (p *parser) eof() bool { return p.i >= len(p.src) }

func (p *parser) peekRune() rune {
	if p.eof() {
		return 0
	}
	r := []rune(p.src[p.i:])
	if len(r) == 0 {
		return 0
	}
	return r[0]
}

func (p *parser) errf(format string, a ...interface{}) error {
	return &ParseError{Pos: p.i + 1, Msg: fmt.Sprintf(format, a...)}
}

func (p *parser) skipSpace() {
	for !p.eof() {
		r := p.peekRune()
		if !unicode.IsSpace(r) {
			return
		}
		p.i += len(string(r))
	}
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	kids := []Node{left}
	for {
		p.skipSpace()
		if !p.matchKeyword("OR") {
			break
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		kids = append(kids, right)
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return &Binary{Op: "OR", Kids: kids}, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	kids := []Node{left}
	for {
		p.skipSpace()
		if p.eof() || p.peekRune() == ')' {
			break
		}
		if p.matchKeyword("OR") {
			// rewind: let caller handle OR
			p.i -= 2
			break
		}
		p.matchKeyword("AND")
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		kids = append(kids, right)
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return &Binary{Op: "AND", Kids: kids}, nil
}

func (p *parser) parseUnary() (Node, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errf("unexpected end of query, expected field or term")
	}
	if p.peekRune() == '-' {
		p.i++
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{Child: child}, nil
	}
	if p.matchKeyword("NOT") {
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{Child: child}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errf("unexpected end of query, expected field or term")
	}
	pos := p.i + 1
	if p.peekRune() == '(' {
		p.i++
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.peekRune() != ')' {
			return nil, p.errf("expected ')'")
		}
		p.i++
		return n, nil
	}
	if p.peekRune() == ')' {
		return nil, p.errf("unexpected ')', expected field or term")
	}

	// Quoted bare phrase.
	if p.peekRune() == '"' {
		val, err := p.parseQuoted()
		if err != nil {
			return nil, err
		}
		return &Term{Value: val, Pos: pos, Quoted: true}, nil
	}

	start := p.i
	word := p.scanBare()
	if word == "" {
		return nil, p.errf("unexpected %q, expected field or term", p.peekRune())
	}
	// field:value ?
	if !p.eof() && p.peekRune() == ':' {
		save := p.i
		p.i++
		field := strings.ToLower(word)
		if field == "sort" {
			valuePos := p.i + 1
			val, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			if !Sorts[val] {
				return nil, &ParseError{Pos: valuePos, Msg: fmt.Sprintf("unknown sort %q", val)}
			}
			p.sort = val
			// sort is a modifier, not a filter; represent as nil and strip later
			return &Term{Field: "__sort__", Value: val, Pos: pos}, nil
		}
		if !Fields[field] {
			p.i = save
			return nil, p.errf("unknown field %q", word)
		}
		if field == "authors" {
			field = "author"
		}
		if field == "keywords" {
			field = "keyword"
		}
		term, err := p.parseValueTerm(field, pos)
		if err != nil {
			return nil, err
		}
		return term, nil
	}
	_ = start
	return &Term{Value: word, Pos: pos}, nil
}

func (p *parser) parseValueTerm(field string, pos int) (*Term, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errf("expected value for field %q", field)
	}
	if p.peekRune() == '"' {
		val, err := p.parseQuoted()
		if err != nil {
			return nil, err
		}
		return &Term{Field: field, Value: val, Pos: pos, Quoted: true}, nil
	}
	valuePos := p.i + 1
	raw := p.scanBare()
	if raw == "" {
		return nil, p.errf("expected value for field %q", field)
	}
	t := &Term{Field: field, Value: raw, Pos: pos}
	if strings.HasSuffix(t.Value, "*") && len(t.Value) > 1 {
		t.Prefix = true
		t.Value = strings.TrimSuffix(t.Value, "*")
	}
	// Ranges (year, added, updated).
	if field == "year" || field == "added" || field == "updated" {
		if strings.Contains(t.Value, "..") {
			parts := strings.SplitN(t.Value, "..", 2)
			t.Range = true
			t.Start = parts[0]
			t.End = parts[1]
			t.Value = ""
			if t.Start == "" && t.End == "" {
				return nil, &ParseError{Pos: valuePos, Msg: fmt.Sprintf("empty range for field %q", field)}
			}
			if field == "year" {
				for _, s := range []string{t.Start, t.End} {
					if s == "" {
						continue
					}
					if _, err := strconv.Atoi(s); err != nil {
						return nil, &ParseError{Pos: valuePos, Msg: fmt.Sprintf("invalid year range %q", s)}
					}
				}
			}
		}
	}
	return t, nil
}

func (p *parser) parseValue() (string, error) {
	p.skipSpace()
	if p.eof() {
		return "", p.errf("expected value")
	}
	if p.peekRune() == '"' {
		return p.parseQuoted()
	}
	v := p.scanBare()
	if v == "" {
		return "", p.errf("expected value")
	}
	return v, nil
}

func (p *parser) parseQuoted() (string, error) {
	start := p.i + 1
	p.i++ // opening quote
	var b strings.Builder
	for !p.eof() {
		r := p.peekRune()
		p.i += len(string(r))
		if r == '"' {
			return b.String(), nil
		}
		if r == '\\' && !p.eof() {
			n := p.peekRune()
			p.i += len(string(n))
			b.WriteRune(n)
			continue
		}
		b.WriteRune(r)
	}
	return "", &ParseError{Pos: start, Msg: "unterminated quoted string"}
}

func (p *parser) scanBare() string {
	start := p.i
	for !p.eof() {
		r := p.peekRune()
		if unicode.IsSpace(r) || r == '(' || r == ')' || r == '"' || r == ':' {
			break
		}
		p.i += len(string(r))
	}
	return p.src[start:p.i]
}

func (p *parser) matchKeyword(kw string) bool {
	if p.i+len(kw) > len(p.src) {
		return false
	}
	if !strings.EqualFold(p.src[p.i:p.i+len(kw)], kw) {
		return false
	}
	if p.i+len(kw) < len(p.src) {
		r := rune(p.src[p.i+len(kw)])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return false
		}
	}
	// prevent matching "OR" inside a token following '-'
	p.i += len(kw)
	return true
}

// StripSort removes sort pseudo-terms from the AST.
func StripSort(n Node) Node {
	switch v := n.(type) {
	case *Term:
		if v.Field == "__sort__" {
			return nil
		}
		return v
	case *Not:
		c := StripSort(v.Child)
		if c == nil {
			return nil
		}
		return &Not{Child: c}
	case *Binary:
		var kids []Node
		for _, k := range v.Kids {
			if c := StripSort(k); c != nil {
				kids = append(kids, c)
			}
		}
		if len(kids) == 0 {
			return nil
		}
		if len(kids) == 1 {
			return kids[0]
		}
		return &Binary{Op: v.Op, Kids: kids}
	}
	return n
}

// Walk visits every Term in the AST.
func Walk(n Node, fn func(*Term)) {
	switch v := n.(type) {
	case *Term:
		fn(v)
	case *Not:
		Walk(v.Child, fn)
	case *Binary:
		for _, k := range v.Kids {
			Walk(k, fn)
		}
	}
}

// PositiveTexts returns text leaves that are not under NOT, for ranking.
func PositiveTexts(n Node) []*Term {
	var out []*Term
	var rec func(Node, bool)
	rec = func(node Node, neg bool) {
		switch v := node.(type) {
		case *Term:
			if neg || v.Field == "__sort__" {
				return
			}
			switch v.Field {
			case "", "title", "abstract", "author", "affiliation", "venue", "keyword", "content", "note", "web", "md":
				out = append(out, v)
			}
		case *Not:
			rec(v.Child, !neg)
		case *Binary:
			for _, k := range v.Kids {
				rec(k, neg)
			}
		}
	}
	rec(n, false)
	return out
}
