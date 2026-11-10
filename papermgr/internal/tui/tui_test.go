package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"papermgr/internal/config"
	"papermgr/internal/model"
	"papermgr/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	cfg := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(t.TempDir(), "lib")
	s, err := store.Init(root, config.Global{Editor: "true", Viewer: "true", LogDir: filepath.Join(state, "log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestViewModelRendersAllModes(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.width, m.height = 120, 40
	if got := m.View(); got == "" {
		t.Fatal("empty view")
	}
	modes := []mode{modeHelp, modeLogs, modeTasks, modeReview, modeConfirm, modeNoteFormat, modeNormal}
	for _, md := range modes {
		m.mode = md
		if got := m.View(); got == "" {
			t.Errorf("empty view for mode %d", md)
		}
	}
	m.mode = modeSearch
	m.input.SetValue("tag:ml")
	if got := m.View(); got == "" {
		t.Error("empty search view")
	}
}

func TestTagItemsIncludeAllAndSavedQueries(t *testing.T) {
	s := testStore(t)
	if err := s.SaveQuery("recent", "year:2020..2026"); err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	if len(m.tagItems) < 3 {
		t.Fatalf("tag items = %+v", m.tagItems)
	}
	if !m.tagItems[0].all || m.tagItems[0].label != "All papers" {
		t.Errorf("first item = %+v", m.tagItems[0])
	}
	found := false
	for _, it := range m.tagItems {
		if it.queryName == "recent" {
			found = true
		}
	}
	if !found {
		t.Errorf("saved query missing from sidebar: %+v", m.tagItems)
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestPanelHotkeysAndTabCycle(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	if m.focus != focusList {
		t.Fatalf("default focus = %v, want results", m.focus)
	}
	key := func(k tea.KeyMsg) {
		m.handleKey(k)
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.focus != focusTags {
		t.Fatalf("key 1 focus = %v", m.focus)
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.focus != focusDetail {
		t.Fatalf("key 3 focus = %v", m.focus)
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.focus != focusList {
		t.Fatalf("key 2 focus = %v", m.focus)
	}
	key(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusDetail {
		t.Fatalf("tab from results focus = %v", m.focus)
	}
	key(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusTags {
		t.Fatalf("tab from detail focus = %v", m.focus)
	}
	key(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != focusDetail {
		t.Fatalf("shift+tab from tags focus = %v", m.focus)
	}
}

func TestViewAtManySizes(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {150, 44}, {220, 60}} {
		m.width, m.height = size[0], size[1]
		m.mode = modeNormal
		if v := m.View(); v == "" {
			t.Errorf("empty view at %dx%d", size[0], size[1])
		}
		m.mode = modeHelp
		if v := m.View(); v == "" {
			t.Errorf("empty help at %dx%d", size[0], size[1])
		}
		m.mode = modeNormal
	}
}

func TestDetailScroll(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.focus = focusDetail
	m.detailMax = 20
	m.detailTop = 0
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.detailTop != 1 {
		t.Fatalf("detail scroll = %d, want 1", m.detailTop)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if m.detailTop != 20 {
		t.Fatalf("detail bottom = %d, want 20", m.detailTop)
	}
}

func TestTagTreeCollapse(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.tags = []model.TagCount{
		{Tag: "ml", Count: 3},
		{Tag: "ml/llm", Count: 3},
		{Tag: "ml/llm/rag", Count: 1},
		{Tag: "status", Count: 1},
	}
	m.pendingCount = 0
	m.tagParents = map[string]bool{"ml": true, "ml/llm": true}
	m.buildTagItems()
	has := func(tag string) bool {
		for _, it := range m.tagItems {
			if it.tag == tag {
				return true
			}
		}
		return false
	}
	if !has("ml/llm") || !has("ml/llm/rag") {
		t.Fatalf("expanded tree missing children: %+v", m.tagItems)
	}
	m.collapsedTag["ml"] = true
	m.buildTagItems()
	if has("ml/llm") || has("ml/llm/rag") {
		t.Fatalf("collapsed tree still shows children: %+v", m.tagItems)
	}
	if !has("ml") || !has("status") {
		t.Fatalf("collapsed tree lost roots: %+v", m.tagItems)
	}
}
