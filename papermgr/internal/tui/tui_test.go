package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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

func TestDetailShowsJournalSeparately(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.detail = &model.Paper{Meta: model.Meta{
		Title: "T",
		Venue: &model.Venue{
			Name:  "IEEE Journal of Selected Topics",
			Short: "IEEE J. Sel. Top.",
			Kind:  "journal",
		},
	}}
	got := m.renderDetail(100, 40)
	for _, want := range []string{"journal", "IEEE Journal of Selected Topics", "venue", "IEEE J. Sel. Top."} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q:\n%s", want, got)
		}
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

func TestTagMultiSelectQuery(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.tags = []model.TagCount{
		{Tag: "application", Count: 2},
		{Tag: "method", Count: 2},
		{Tag: "method/diffusion", Count: 1},
	}
	m.tagParents = map[string]bool{"method": true}
	m.buildTagItems()

	cursor := func(tag string) {
		t.Helper()
		for i, it := range m.tagItems {
			if it.tag == tag {
				m.tagCursor = i
				return
			}
		}
		t.Fatalf("tag %q not in items: %+v", tag, m.tagItems)
	}
	cursor("application")
	m.toggleTagSelection()
	cursor("method/diffusion")
	m.toggleTagSelection()
	if got := m.effectiveQuery(); !strings.Contains(got, "(tag:application AND tag:method/diffusion)") {
		t.Fatalf("AND query = %q", got)
	}
	m.tagOr = true
	if got := m.effectiveQuery(); !strings.Contains(got, "(tag:application OR tag:method/diffusion)") {
		t.Fatalf("OR query = %q", got)
	}
	cursor("application")
	m.toggleTagExclude()
	if !m.tagNot["application"] || m.tagSel["application"] {
		t.Fatalf("exclusion state = sel:%v not:%v", m.tagSel, m.tagNot)
	}
	if got := m.effectiveQuery(); !strings.Contains(got, "NOT tag:application") {
		t.Fatalf("exclusion query = %q", got)
	}
}

func TestSelectTagClearsMarksAndFilters(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.tagItems = []tagItem{
		{label: "All papers", all: true},
		{label: "application", tag: "application"},
	}
	m.query = "cloud"
	m.marked["a"] = true
	m.tagCursor = 1
	m.selectTag()
	if len(m.marked) != 0 {
		t.Fatalf("marks not cleared: %v", m.marked)
	}
	if !m.tagSel["application"] {
		t.Fatalf("tag selection missing: %v", m.tagSel)
	}
	if m.focus != focusList {
		t.Fatalf("focus = %v", m.focus)
	}

	m.tagCursor = 0
	m.selectTag()
	if m.hasFilter() {
		t.Fatalf("filters not cleared: query=%q tags=%v not=%v pending=%v", m.query, m.tagSel, m.tagNot, m.pending)
	}
}

func TestPaneNavigationAndZoom(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.focus = focusList
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.focus != focusTags {
		t.Fatalf("h focus = %v", m.focus)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if m.focus != focusTags {
		t.Fatalf("l in tags should expand, not change focus: %v", m.focus)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusList {
		t.Fatalf("tab focus = %v", m.focus)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if !m.zoom {
		t.Fatal("z did not zoom")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if m.zoom {
		t.Fatal("second z did not restore")
	}
}

func TestTagCollapseWithArrows(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.tags = []model.TagCount{{Tag: "ml", Count: 2}, {Tag: "ml/llm", Count: 2}}
	m.tagParents = map[string]bool{"ml": true}
	m.buildTagItems()
	m.focus = focusTags
	m.tagCursor = 2 // ml
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if !m.collapsedTag["ml"] {
		t.Fatalf("left did not collapse: %v", m.collapsedTag)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if m.collapsedTag["ml"] {
		t.Fatalf("right did not expand: %v", m.collapsedTag)
	}
}

func TestMouseClickFocusesPaneAndTag(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.width, m.height = 150, 40
	m.tagItems = []tagItem{
		{label: "All papers", all: true},
		{label: "application", tag: "application"},
	}
	m.focus = focusList
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 3})
	if m.focus != focusTags {
		t.Fatalf("click focus = %v", m.focus)
	}
	if m.tagCursor != 0 {
		t.Fatalf("click tag cursor = %d", m.tagCursor)
	}
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 4})
	if m.tagCursor != 1 {
		t.Fatalf("second click tag cursor = %d", m.tagCursor)
	}
	before := m.focus
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 0})
	if m.focus != before {
		t.Fatalf("header click changed focus to %v", m.focus)
	}
}

func TestConfirmActionRunsAsCommand(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	called := false
	m.mode = modeConfirm
	m.actionLabel = "test"
	m.action = func() error { called = true; return nil }
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if called {
		t.Fatal("action ran synchronously")
	}
	if cmd == nil {
		t.Fatal("no command returned")
	}
	if m.mode != modeNormal || !m.busy {
		t.Fatalf("mode=%v busy=%v", m.mode, m.busy)
	}
	if msg, ok := cmd().(actionMsg); !ok || msg.err != nil {
		t.Fatalf("command msg = %#v", msg)
	}
	if !called {
		t.Fatal("action did not run in command")
	}
}

func TestWrapSplitsLongTokens(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("a", 80)
	for _, line := range wrap(url, 20) {
		if w := ansi.StringWidth(line); w > 20 {
			t.Fatalf("line %q width %d", line, w)
		}
	}
}

func TestStatusBadge(t *testing.T) {
	cases := map[string]string{
		"status/to-read": "●",
		"status/reading": "◐",
		"status/read":    "✓",
		"status/skim":    "◦",
	}
	for tag, want := range cases {
		got, _ := statusBadge([]string{tag})
		if got != want {
			t.Errorf("%s badge = %q, want %q", tag, got, want)
		}
	}
	if got, _ := statusBadge(nil); got != " " {
		t.Errorf("empty badge = %q", got)
	}
}

func TestLayoutWidthsSumToTerminal(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	for w := narrowWidth; w <= 400; w++ {
		m.width = w
		left, mid, right := m.layoutWidths()
		if left < 1 || mid < 1 || right < 1 {
			t.Fatalf("w=%d widths=%d/%d/%d", w, left, mid, right)
		}
		if left+mid+right != w {
			t.Fatalf("w=%d widths sum to %d (%d/%d/%d)", w, left+mid+right, left, mid, right)
		}
	}
}

func TestViewNeverExceedsTerminal(t *testing.T) {
	s := testStore(t)
	m := newModel(s)
	m.query = "attention transformer"
	m.tagSel["ml/llm"] = true
	m.tagNot["status/read"] = true
	m.marked["x"] = true
	m.message = "a long status message that should be truncated"
	m.confirm = strings.Repeat("long confirmation message ", 6)
	for _, size := range [][2]int{{10, 3}, {20, 5}, {30, 8}, {20, 10}, {40, 12}, {60, 20}, {71, 24}, {72, 24}, {80, 24}, {100, 30}, {150, 44}, {220, 60}} {
		m.width, m.height = size[0], size[1]
		for _, md := range []mode{modeNormal, modeHelp, modeLogs, modeTasks, modeReview, modeConfirm, modeSearch, modeNoteFormat} {
			m.mode = md
			for _, zoom := range []bool{false, true} {
				m.zoom = zoom
				view := m.View()
				for _, line := range strings.Split(view, "\n") {
					if w := ansi.StringWidth(line); w > m.width {
						t.Fatalf("%dx%d mode=%d zoom=%v: line width %d > %d: %q", size[0], size[1], md, zoom, w, m.width, line)
					}
				}
				if got := len(strings.Split(view, "\n")); got > m.height {
					t.Fatalf("%dx%d mode=%d zoom=%v: %d lines", size[0], size[1], md, zoom, got)
				}
			}
		}
	}
}
