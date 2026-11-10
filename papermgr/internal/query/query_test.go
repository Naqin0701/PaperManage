package query

import (
	"strings"
	"testing"
)

func TestParseTable(t *testing.T) {
	tests := []struct {
		in   string
		want string // rendered AST
		sort string
	}{
		{"", "", "relevance"},
		{"transformer", "transformer", "relevance"},
		{"attention is all", "AND(attention,is,all)", "relevance"},
		{"attention AND transformer", "AND(attention,transformer)", "relevance"},
		{"attention OR transformer", "OR(attention,transformer)", "relevance"},
		{"NOT attention", "NOT(attention)", "relevance"},
		{"-attention", "NOT(attention)", "relevance"},
		{"title:attention", "title:attention", "relevance"},
		{`title:"attention is all you need"`, `title:"attention is all you need"`, "relevance"},
		{"tag:ml/llm", "tag:ml/llm", "relevance"},
		{"tag:ml/llm/", "tag:ml/llm/", "relevance"},
		{"year:2015..2020", "year:2015..2020", "relevance"},
		{"year:..2020", "year:..2020", "relevance"},
		{"added:2026-01..2026-11", "added:2026-01..2026-11", "relevance"},
		{"(a OR b) c", "AND(OR(a,b),c)", "relevance"},
		{"title:attention sort:year-", "title:attention", "year-"},
		{"sort:added-", "", "added-"},
		{"doi:10.1000/xyz", "doi:10.1000/xyz", "relevance"},
	}
	for _, tt := range tests {
		q, err := Parse(tt.in)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tt.in, err)
		}
		got := render(q.Root)
		if got != tt.want {
			t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if q.Sort != tt.sort {
			t.Errorf("Parse(%q) sort = %q, want %q", tt.in, q.Sort, tt.sort)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in      string
		wantPos int
	}{
		{"foo )", 5},
		{"(foo", 5},
		{`title:"unterminated`, 7},
		{"unknownfield:x", 13},
		{"year:abcd..2020", 6},
		{"sort:bogus", 6},
	}
	for _, tt := range tests {
		_, err := Parse(tt.in)
		if err == nil {
			t.Fatalf("Parse(%q) expected error", tt.in)
		}
		pe, ok := err.(*ParseError)
		if !ok {
			t.Fatalf("Parse(%q) error type %T", tt.in, err)
		}
		if pe.Pos != tt.wantPos {
			t.Errorf("Parse(%q) pos = %d, want %d (%v)", tt.in, pe.Pos, tt.wantPos, err)
		}
	}
}

func TestErrorMentionsExpectation(t *testing.T) {
	_, err := Parse("attention )")
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("expected actionable error, got %v", err)
	}
}

func render(n Node) string {
	switch v := n.(type) {
	case nil:
		return ""
	case *Term:
		s := v.Value
		if v.Range {
			s = v.Start + ".." + v.End
		}
		if v.Prefix {
			s += "*"
		}
		if v.Field != "" {
			if v.Quoted {
				return v.Field + `:"` + s + `"`
			}
			return v.Field + ":" + s
		}
		if v.Quoted {
			return `"` + s + `"`
		}
		return s
	case *Not:
		return "NOT(" + render(v.Child) + ")"
	case *Binary:
		var parts []string
		for _, k := range v.Kids {
			parts = append(parts, render(k))
		}
		return v.Op + "(" + strings.Join(parts, ",") + ")"
	}
	return "?"
}
