package store

import (
	"testing"

	"papermgr/internal/model"
)

func TestFieldStringEmptyCollections(t *testing.T) {
	m := model.Meta{}
	for _, f := range []string{"authors", "keywords"} {
		if got := fieldString(m, f); got != "" {
			t.Errorf("fieldString(%q) = %q, want empty", f, got)
		}
		if fieldProvided(m, f) {
			t.Errorf("fieldProvided(%q) = true for empty collection", f)
		}
	}

	m.Authors = []model.Author{{Given: "Ada", Family: "Lovelace"}}
	m.Keywords = []string{"testing"}
	for _, f := range []string{"authors", "keywords"} {
		if got := fieldString(m, f); got == "" {
			t.Errorf("fieldString(%q) is empty for a populated collection", f)
		}
		if !fieldProvided(m, f) {
			t.Errorf("fieldProvided(%q) = false for a populated collection", f)
		}
	}
}
