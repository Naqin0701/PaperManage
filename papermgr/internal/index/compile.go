package index

import (
	"fmt"
	"strings"

	"papermgr/internal/query"
)

// compile turns an AST into a SQL WHERE clause with placeholders.
func compile(n query.Node) (string, []interface{}) {
	if n == nil {
		return "", nil
	}
	switch v := n.(type) {
	case *query.Binary:
		var parts []string
		var args []interface{}
		for _, k := range v.Kids {
			s, a := compile(k)
			if s == "" {
				continue
			}
			parts = append(parts, "("+s+")")
			args = append(args, a...)
		}
		if len(parts) == 0 {
			return "", nil
		}
		return strings.Join(parts, " "+v.Op+" "), args
	case *query.Not:
		s, a := compile(v.Child)
		if s == "" {
			return "1=1", nil
		}
		return "NOT (" + s + ")", a
	case *query.Term:
		return compileTerm(v)
	}
	return "", nil
}

func compileTerm(t *query.Term) (string, []interface{}) {
	if t.Field == "__sort__" {
		return "", nil
	}
	switch t.Field {
	case "tag":
		tag := strings.TrimSuffix(t.Value, "/")
		if tag == "" {
			return "1=0", nil
		}
		esc := escapeLike(tag)
		if strings.HasSuffix(t.Value, "/") {
			return `EXISTS (SELECT 1 FROM tags WHERE tags.paper_id = papers.id AND tags.tag = ? COLLATE NOCASE)`, []interface{}{tag}
		}
		return `EXISTS (SELECT 1 FROM tags WHERE tags.paper_id = papers.id AND (tags.tag = ? COLLATE NOCASE OR tags.tag LIKE ? ESCAPE '\' COLLATE NOCASE))`,
			[]interface{}{tag, esc + "/%"}
	case "year":
		return compileInt(t, "year")
	case "added":
		return compileDate(t, "added_at")
	case "updated":
		return compileDate(t, "updated_at")
	case "doi":
		if t.Prefix {
			return `doi LIKE ? COLLATE NOCASE`, []interface{}{escapeLike(t.Value) + "%"}
		}
		return `doi = ? COLLATE NOCASE`, []interface{}{t.Value}
	case "arxiv":
		if t.Prefix {
			return `arxiv_id LIKE ? COLLATE NOCASE`, []interface{}{escapeLike(t.Value) + "%"}
		}
		return `arxiv_id = ? COLLATE NOCASE`, []interface{}{t.Value}
	case "citekey":
		if t.Prefix {
			return `citekey LIKE ? COLLATE NOCASE`, []interface{}{escapeLike(t.Value) + "%"}
		}
		return `citekey = ? COLLATE NOCASE`, []interface{}{t.Value}
	case "type":
		return `entry_type = ? COLLATE NOCASE`, []interface{}{t.Value}
	default:
		// Full-text fields and bare terms.
		expr := ftsExprFor(t)
		if expr == "" {
			return "", nil
		}
		return `rowid IN (SELECT rowid FROM papers_fts WHERE papers_fts MATCH ?)`, []interface{}{expr}
	}
}

func compileInt(t *query.Term, col string) (string, []interface{}) {
	if t.Range {
		var parts []string
		var args []interface{}
		if t.Start != "" {
			parts = append(parts, col+" >= ?")
			args = append(args, t.Start)
		}
		if t.End != "" {
			parts = append(parts, col+" <= ?")
			args = append(args, t.End)
		}
		if len(parts) == 0 {
			return "", nil
		}
		return strings.Join(parts, " AND "), args
	}
	if t.Prefix {
		return `CAST(` + col + ` AS TEXT) LIKE ?`, []interface{}{escapeLike(t.Value) + "%"}
	}
	return col + " = ?", []interface{}{t.Value}
}

func compileDate(t *query.Term, col string) (string, []interface{}) {
	if col == "added_at" {
		col = "added_at"
	}
	if t.Range {
		var parts []string
		var args []interface{}
		if t.Start != "" {
			parts = append(parts, "substr("+col+",1,10) >= ?")
			args = append(args, normalizeDateStart(t.Start))
		}
		if t.End != "" {
			parts = append(parts, "substr("+col+",1,10) <= ?")
			args = append(args, normalizeDateEnd(t.End))
		}
		if len(parts) == 0 {
			return "", nil
		}
		return strings.Join(parts, " AND "), args
	}
	if strings.Count(t.Value, "-") == 0 && len(t.Value) == 4 {
		return "substr(" + col + ",1,4) = ?", []interface{}{t.Value}
	}
	if len(t.Value) == 7 {
		return "substr(" + col + ",1,7) = ?", []interface{}{t.Value}
	}
	return "substr(" + col + ",1,10) = ?", []interface{}{t.Value}
}

func normalizeDateStart(s string) string {
	switch len(s) {
	case 4:
		return s + "-01-01"
	case 7:
		return s + "-01"
	default:
		return s
	}
}

func normalizeDateEnd(s string) string {
	switch len(s) {
	case 4:
		return s + "-12-31"
	case 7:
		return s + "-31"
	default:
		return s
	}
}

var fieldColumn = map[string]string{
	"title": "title", "abstract": "abstract", "keyword": "keywords",
	"author": "authors", "affiliation": "affiliations", "venue": "venue",
	"content": "content", "note": "note", "web": "web", "md": "md",
}

func ftsExprFor(t *query.Term) string {
	phrase := `"` + strings.ReplaceAll(t.Value, `"`, `""`) + `"`
	if t.Prefix {
		phrase += "*"
	}
	col, ok := fieldColumn[t.Field]
	if !ok || t.Field == "" {
		return phrase
	}
	return col + " : " + phrase
}

// positiveFTS builds a single MATCH expression for relevance ranking.
// Only positive text leaves participate; filters are applied separately.
func positiveFTS(n query.Node) (string, bool) {
	terms := query.PositiveTexts(n)
	if len(terms) == 0 {
		return "", false
	}
	var parts []string
	for _, t := range terms {
		if t.Field == "tag" || t.Field == "year" || t.Field == "added" || t.Field == "updated" ||
			t.Field == "doi" || t.Field == "arxiv" || t.Field == "citekey" || t.Field == "type" {
			continue
		}
		parts = append(parts, ftsExprFor(t))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " AND "), true
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// DebugSQL is exposed for tests that assert compilation shapes.
func DebugSQL(expr string) (string, []interface{}, error) {
	q, err := query.Parse(expr)
	if err != nil {
		return "", nil, err
	}
	s, a := compile(query.StripSort(q.Root))
	return s, a, nil
}

var _ = fmt.Sprintf
