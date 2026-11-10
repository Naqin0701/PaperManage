package store

import (
	"strings"
	"testing"
)

func TestRewriteTextIsSimultaneousAndSkipsWikilinks(t *testing.T) {
	src := "see papers/a/x.pdf and ../b/y.pdf, [z](a/z.pdf), [[papers/a/keep]]"
	out, wikis := rewriteText(src, map[string]string{"a": "b", "b": "c"})
	if strings.Contains(out, "papers/b/x.pdf") == false {
		t.Errorf("papers/a not rewritten: %s", out)
	}
	if !strings.Contains(out, "../c/y.pdf") {
		t.Errorf("chained mapping applied twice or not at all: %s", out)
	}
	if !strings.Contains(out, "(b/z.pdf)") {
		t.Errorf("relative (a/) link not rewritten: %s", out)
	}
	if !strings.Contains(out, "[[papers/a/keep]]") {
		t.Errorf("wikilink was modified: %s", out)
	}
	if len(wikis) != 1 {
		t.Errorf("wikilinks reported = %v", wikis)
	}
}
