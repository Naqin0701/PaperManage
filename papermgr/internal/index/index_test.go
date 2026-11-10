package index

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	x, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	recs := []Record{
		{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Citekey: "vaswani-2017-attention", Title: "Attention Is All You Need",
			Authors: "Ashish Vaswani; Noam Shazeer", Abstract: "The dominant sequence transduction models are based on complex recurrent networks.",
			Keywords: "transformer, attention", Venue: "NeurIPS", Year: intp(2017), AddedAt: "2026-11-10T14:30:00+08:00",
			Tags: []string{"ml/llm/transformer", "status/read"}, Content: "attention transformer self attention", Note: "my note about attention"},
		{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Citekey: "lewis-2020-rag", Title: "Retrieval-Augmented Generation for Knowledge-Intensive NLP",
			Authors: "Patrick Lewis", Abstract: "We explore retrieval augmented generation.", Year: intp(2020),
			AddedAt: "2026-11-11T09:00:00+08:00", Tags: []string{"ml/llm/rag"}, Content: "retrieval augmented generation"},
		{ID: "01CCCCCCCCCCCCCCCCCCCCCCCC", Citekey: "old-2019-thing", Title: "A Thing", Year: intp(2019),
			AddedAt: "2026-10-01T09:00:00+08:00", Tags: []string{"ml/llm"}},
	}
	for _, r := range recs {
		if err := x.Upsert(r); err != nil {
			t.Fatal(err)
		}
	}
	return x
}

func intp(i int) *int { return &i }

func keys(t *testing.T, x *Index, q string) []string {
	t.Helper()
	hits, err := x.Query(q)
	if err != nil {
		t.Fatalf("Query(%q): %v", q, err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.Citekey)
	}
	return out
}

func TestSearchBasics(t *testing.T) {
	x := newTestIndex(t)
	tests := []struct {
		q    string
		want int
	}{
		{"attention", 1},
		{"transformer", 1},
		{"ml/llm", 0}, // bare term searches text, not tags
		{"title:attention", 1},
		{"title:rag", 0},
		{"abstract:retrieval", 1},
		{"author:vaswani", 1},
		{"venue:neurips", 1},
		{"tag:ml", 3},
		{"tag:ml/llm", 3},
		{"tag:ml/llm/", 1},
		{"tag:ml/llm/rag", 1},
		{"year:2017", 1},
		{"year:2017..2020", 3},
		{"year:..2019", 2},
		{"year:2020..*", 1},
		{"type:article", 0},
		{"title:attention OR title:rag", 1},
		{"retrieval AND tag:ml/llm/rag", 1},
		{"NOT tag:ml/llm/rag", 2},
		{"attention OR retrieval", 2},
		{"note:attention", 1},
		{"content:retrieval", 1},
		{"content:attention", 1},
		{"title:\"attention is all\"", 1},
	}
	for _, tt := range tests {
		got := keys(t, x, tt.q)
		if len(got) != tt.want {
			t.Errorf("Query(%q) returned %v, want %d hits", tt.q, got, tt.want)
		}
	}
}

func TestSortAndRelevance(t *testing.T) {
	x := newTestIndex(t)
	got := keys(t, x, "sort:year-")
	if len(got) != 3 || got[0] != "lewis-2020-rag" || got[2] != "vaswani-2017-attention" {
		t.Errorf("sort:year- = %v", got)
	}
	got = keys(t, x, "attention OR retrieval sort:year+")
	if len(got) != 2 || got[0] != "vaswani-2017-attention" {
		t.Errorf("sort:year+ = %v", got)
	}
}

func TestTagCounts(t *testing.T) {
	x := newTestIndex(t)
	tags, err := x.TagCounts()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][2]int{}
	for _, tc := range tags {
		m[tc.Tag] = [2]int{tc.Count, tc.Explicit}
	}
	if m["ml"][0] != 3 {
		t.Errorf("ml count = %v, want 3", m["ml"])
	}
	if m["ml/llm"][0] != 3 || m["ml/llm"][1] != 1 {
		t.Errorf("ml/llm = %v, want count 3 explicit 1", m["ml/llm"])
	}
	if m["ml/llm/rag"][0] != 1 {
		t.Errorf("rag = %v", m["ml/llm/rag"])
	}
}

func TestRenamePreservesID(t *testing.T) {
	x := newTestIndex(t)
	if err := x.RenameCitekey("old-2019-thing", "new-2019-thing"); err != nil {
		t.Fatal(err)
	}
	hits, err := x.Query("citekey:new-2019-thing")
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if hits[0].ID != "01CCCCCCCCCCCCCCCCCCCCCCCC" {
		t.Errorf("ID changed: %s", hits[0].ID)
	}
}

func TestRebuild(t *testing.T) {
	x := newTestIndex(t)
	if err := x.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if x.Count() != 0 {
		t.Errorf("count after rebuild = %d", x.Count())
	}
}

func TestIndexFileCreated(t *testing.T) {
	dir := t.TempDir()
	x, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x.Close()
	if _, err := os.Stat(filepath.Join(dir, "library.sqlite")); err != nil {
		t.Fatal(err)
	}
}
