package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"papermgr/internal/model"
)

func TestListQueryAppendsDefaultSort(t *testing.T) {
	cases := []struct {
		expr string
		sort string
		want string
	}{
		{"", "added-", "sort:added-"},
		{"tag:ml/llm", "added-", "tag:ml/llm sort:added-"},
		{"tag:ml/llm sort:year-", "added-", "tag:ml/llm sort:year-"},
		{"", "relevance", "sort:relevance"},
		{"cloud", "relevance", "cloud sort:relevance"},
	}
	for _, tc := range cases {
		got, err := listQuery(tc.expr, listOptions{Sort: tc.sort})
		if err != nil {
			t.Fatalf("%q: %v", tc.expr, err)
		}
		if got != tc.want {
			t.Errorf("%q sort=%s: got %q, want %q", tc.expr, tc.sort, got, tc.want)
		}
	}
	if _, err := listQuery("", listOptions{Sort: "bogus"}); err == nil {
		t.Fatal("invalid sort accepted")
	}
}

func TestApplyLimit(t *testing.T) {
	hits := []model.SearchHit{{Citekey: "a"}, {Citekey: "b"}, {Citekey: "c"}}
	got, total := applyLimit(hits, 2)
	if total != 3 || len(got) != 2 || got[1].Citekey != "b" {
		t.Fatalf("limit: total=%d hits=%v", total, got)
	}
	got, total = applyLimit(hits, 0)
	if total != 3 || len(got) != 3 {
		t.Fatalf("no limit: total=%d hits=%v", total, got)
	}
}

func TestLayoutFitsWidth(t *testing.T) {
	for w := 20; w <= 240; w++ {
		lay := makeLayout(w, listOptions{}, false)
		used := lay.prefixW + lay.titleW + gapFor(lay.citekeyW) + gapFor(lay.tagsW)
		if used > w {
			t.Fatalf("w=%d layout uses %d: %+v", w, used, lay)
		}
	}
}

func TestPrettyRowFitsWidth(t *testing.T) {
	year := 2026
	h := model.SearchHit{
		Citekey: "harandi-2026-multi-sensor-mapping-aquatic-vegetation",
		Title:   "Multi-Sensor Mapping of Aquatic Vegetation Using Sentinel-2, SAR–Optical Fusion and Derived Spectral and SAR Indices in South Florida",
		Year:    &year,
		Tags:    []string{"application/aquatic", "multi-sensor", "status/to-read"},
		HasNote: true,
	}
	st := newCLIStyles(false)
	for w := 20; w <= 240; w++ {
		lay := makeLayout(w, listOptions{}, false)
		row := prettyRow(h, lay, st)
		if got := ansi.StringWidth(row); got > w {
			t.Fatalf("w=%d row width=%d: %q", w, got, row)
		}
	}
}

func TestParseFZFSelection(t *testing.T) {
	in := "\x1b[32ma-1\x1b[0m\tsome display\nb-2\tanother display\n"
	got := parseFZFSelection(in)
	if len(got) != 2 || got[0] != "a-1" || got[1] != "b-2" {
		t.Fatalf("selection = %q", got)
	}
}

func TestHasSortField(t *testing.T) {
	if !hasSortField("tag:ml sort:year-") || !hasSortField("SORT:title+") {
		t.Fatal("sort field not detected")
	}
	if hasSortField("title:\"sort:year-\"") {
		t.Fatal("quoted sort text treated as a sort field")
	}
}

func TestPrepareOutputConflicts(t *testing.T) {
	var opts listOptions
	cmd := &cobra.Command{}
	addListFlags(cmd, &opts, "added-")
	if err := cmd.Flags().Set("fzf", "true"); err != nil {
		t.Fatal(err)
	}
	opts.TSV = true
	if err := prepareOutput(cmd, &opts); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestCleanField(t *testing.T) {
	if got := cleanField("a\tb\nc  d"); got != "a b c d" {
		t.Fatalf("cleanField = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/tmp/a b"); got != "'/tmp/a b'" {
		t.Fatalf("quote = %s", got)
	}
	if got := shellQuote("it's"); got != "'it'\\''s'" {
		t.Fatalf("quote apostrophe = %s", got)
	}
}
