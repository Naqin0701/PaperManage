// Package tui implements the three-pane Bubble Tea interface.
package tui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"papermgr/internal/index"
	"papermgr/internal/logging"
	"papermgr/internal/model"
	"papermgr/internal/store"
)

type mode int

const (
	modeNormal mode = iota
	modeSearch
	modeCommand
	modeHelp
	modeReview
	modeLogs
	modeTasks
	modeConfirm
	modeTag
	modeNoteFormat
)

type focus int

const (
	focusTags focus = iota
	focusList
	focusDetail
)

type tagItem struct {
	label       string
	tag         string
	queryName   string
	count       int
	depth       int
	hasChildren bool
	collapsed   bool
	all         bool
	pending     bool
	section     bool
}

type task struct {
	Label   string
	Started time.Time
	Done    bool
	Err     string
}

// Model is the Bubble Tea model.
type Model struct {
	st *store.Store

	width, height int
	focus         focus
	mode          mode

	tags         []model.TagCount
	tagParents   map[string]bool
	tagItems     []tagItem
	tagCursor    int
	collapsedTag map[string]bool
	activeTag    string // "" = all
	pending      bool
	pendingCount int

	query   string
	sortKey string
	hits    []model.SearchHit
	listTop int
	cursor  int
	marked  map[string]bool

	detail      *model.Paper
	attachments []store.Attachment
	detailTop   int
	detailMax   int

	input textinput.Model

	message string
	errMsg  string
	dirty   int

	reviewPapers      []*model.Paper
	reviewCursor      int
	reviewCand        int
	reviewFields      []string
	reviewSel         map[int]bool
	reviewFieldCursor int

	logLines []string
	logTop   int

	tasks        []task
	busy         bool
	busyMsg      string
	confirm      string
	action       func() error
	schemaIssues int
	mu           sync.Mutex
}

type tickMsg time.Time
type reindexMsg struct {
	stats store.SyncStats
	err   error
}
type convertMsg struct {
	citekey string
	files   []string
	err     error
}
type refreshMsg struct{ err error }
type actionMsg struct {
	label string
	err   error
}
type editorDoneMsg struct{ err error }

// Run starts the TUI.
func Run(s *store.Store) error {
	m := newModel(s)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func newModel(s *store.Store) *Model {
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Prompt = ""
	m := &Model{
		st:           s,
		focus:        focusList,
		marked:       map[string]bool{},
		collapsedTag: map[string]bool{},
		input:        ti,
		sortKey:      "relevance",
		reviewSel:    map[int]bool{},
	}
	m.refreshTags()
	m.reloadHits()
	m.checkDirty()
	if issues, err := s.SchemaIssues(); err == nil {
		m.schemaIssues = len(issues)
	}
	return m
}

func (m *Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.checkDirty()
		return m, tick()
	case reindexMsg:
		m.busy = false
		m.finishTask("reindex", msg.err)
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.message = fmt.Sprintf("index synced: %d updated, %d unchanged, %d removed", msg.stats.Synced, msg.stats.Unchanged, msg.stats.Removed)
			if len(msg.stats.Errors) > 0 {
				m.errMsg = fmt.Sprintf("%d index error(s); press L for logs", len(msg.stats.Errors))
			}
		}
		m.refreshTags()
		m.reloadHits()
		m.checkDirty()
		return m, nil
	case convertMsg:
		m.busy = false
		m.finishTask("convert", msg.err)
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.message = fmt.Sprintf("converted %s (%d file(s))", msg.citekey, len(msg.files))
			m.reloadHits()
			m.loadDetail()
		}
		return m, nil
	case actionMsg:
		m.busy = false
		m.finishTask(msg.label, msg.err)
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.message = msg.label + " done"
			m.refreshTags()
			m.reloadHits()
			m.loadDetail()
			m.checkDirty()
		}
		return m, nil
	case editorDoneMsg:
		m.busy = false
		m.finishTask("note new", msg.err)
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.loadDetail()
		m.reloadHits()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeSearch, modeCommand, modeTag:
		return m.handleInputKey(msg)
	case modeHelp, modeLogs, modeTasks:
		if key == "esc" || key == "q" || key == "?" || key == "L" || key == "b" {
			m.mode = modeNormal
			return m, nil
		}
		switch key {
		case "j", "down", "ctrl+d", "ctrl+u":
			if key == "ctrl+d" {
				m.logTop += 10
			} else if key == "ctrl+u" {
				m.logTop = max(0, m.logTop-10)
			} else if key == "j" || key == "down" {
				m.logTop++
			} else if m.logTop > 0 {
				m.logTop--
			}
		case "k", "up":
			if m.logTop > 0 {
				m.logTop--
			}
		case "g":
			m.logTop = 0
		}
		return m, nil
	case modeConfirm:
		if key == "y" || key == "Y" {
			act := m.action
			m.action = nil
			m.mode = modeNormal
			if act != nil {
				if err := act(); err != nil {
					m.errMsg = err.Error()
				} else {
					m.message = "done"
					m.refreshTags()
					m.reloadHits()
					m.loadDetail()
				}
			}
			return m, nil
		}
		if key == "n" || key == "N" || key == "esc" {
			m.mode = modeNormal
			m.action = nil
			m.message = "cancelled"
		}
		return m, nil
	case modeReview:
		return m.handleReviewKey(key)
	case modeNoteFormat:
		switch key {
		case "m":
			return m, m.createNote("md")
		case "t":
			return m, m.createNote("tex")
		case "p":
			return m, m.createNote("typ")
		case "esc", "q":
			m.mode = modeNormal
		}
		return m, nil
	}

	// Normal mode.
	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
		m.logTop = 0
	case "/":
		m.mode = modeSearch
		m.input.SetValue(m.query)
		m.input.CursorEnd()
		m.input.Focus()
	case ":":
		m.mode = modeCommand
		m.input.SetValue("")
		m.input.Focus()
	case "tab":
		m.cycleFocus(1)
	case "shift+tab":
		m.cycleFocus(-1)
	case "1":
		m.focus = focusTags
	case "2":
		m.focus = focusList
	case "3":
		m.focus = focusDetail
	case "esc":
		m.focus = focusList
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g":
		m.toEdge(false)
	case "G":
		m.toEdge(true)
	case "ctrl+d":
		m.move(10)
	case "ctrl+u":
		m.move(-10)
	case " ":
		if m.focus == focusList {
			m.toggleMark()
		} else if m.focus == focusTags {
			m.toggleTagCollapse()
		}
	case "t":
		if m.currentHit() != nil || len(m.marked) > 0 {
			m.mode = modeTag
			m.input.SetValue("")
			m.input.Focus()
		}
	case "e":
		return m.openNote()
	case "o":
		return m, m.openPDF()
	case "c":
		return m, m.convert()
	case "d":
		return m.deleteCurrent()
	case "R":
		return m, m.reindex(false)
	case "r":
		m.refreshTags()
		m.reloadHits()
		m.loadDetail()
		m.message = "reloaded"
	case "L":
		m.mode = modeLogs
		m.logLines = m.readLogs()
		m.logTop = 0
	case "b":
		m.mode = modeTasks
		m.logTop = 0
	case "p":
		m.openReview()
	case "s":
		m.cycleSort()
	case "x":
		return m.renameSuggested()
	case "enter":
		if m.focus == focusTags {
			m.selectTag()
		} else if m.focus == focusDetail {
			m.focus = focusList
		} else {
			m.focus = focusDetail
		}
	}
	return m, nil
}

func (m *Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		switch m.mode {
		case modeSearch:
			m.query = val
			m.mode = modeNormal
			m.marked = map[string]bool{}
			m.cursor, m.listTop = 0, 0
			m.reloadHits()
		case modeCommand:
			m.mode = modeNormal
			return m, m.runCommand(val)
		case modeTag:
			m.mode = modeNormal
			return m, m.applyTags(val)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) refreshTags() {
	m.mu.Lock()
	tags, err := m.st.Tags()
	if err != nil {
		m.mu.Unlock()
		m.errMsg = err.Error()
		return
	}
	m.tags = tags
	pending, _ := m.st.PendingPapers()
	m.pendingCount = len(pending)
	m.tagParents = map[string]bool{}
	for _, t := range m.tags {
		parts := strings.Split(t.Tag, "/")
		for i := 1; i < len(parts); i++ {
			m.tagParents[strings.Join(parts[:i], "/")] = true
		}
	}
	m.rebuildTagItemsLocked()
	m.mu.Unlock()
}

func (m *Model) buildTagItems() {
	items := []tagItem{{label: "All papers", all: true, count: m.st.Index.Count()}}
	items = append(items, tagItem{label: "Pending review", pending: true, count: m.pendingCount})
	for _, t := range m.tags {
		if m.ancestorCollapsed(t.Tag) {
			continue
		}
		name := t.Tag
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		items = append(items, tagItem{
			label: name, tag: t.Tag, count: t.Count,
			depth: strings.Count(t.Tag, "/"), hasChildren: m.tagParents[t.Tag],
			collapsed: m.collapsedTag[t.Tag],
		})
	}
	if queries, err := m.st.ListQueries(); err == nil && len(queries) > 0 {
		items = append(items, tagItem{label: "saved queries", section: true})
		for _, q := range queries {
			items = append(items, tagItem{label: q.Name, queryName: q.Name})
		}
	}
	m.tagItems = items
}

func (m *Model) ancestorCollapsed(tag string) bool {
	parts := strings.Split(tag, "/")
	for i := 1; i < len(parts); i++ {
		if m.collapsedTag[strings.Join(parts[:i], "/")] {
			return true
		}
	}
	return false
}

func (m *Model) currentTagKey() string {
	if m.tagCursor < 0 || m.tagCursor >= len(m.tagItems) {
		return ""
	}
	return tagItemKey(m.tagItems[m.tagCursor])
}

func tagItemKey(it tagItem) string {
	switch {
	case it.all:
		return "all"
	case it.pending:
		return "pending"
	case it.queryName != "":
		return "query:" + it.queryName
	case it.tag != "":
		return "tag:" + it.tag
	}
	return ""
}

func (m *Model) rebuildTagItemsLocked() {
	key := m.currentTagKey()
	m.buildTagItems()
	if key != "" {
		for i, it := range m.tagItems {
			if tagItemKey(it) == key {
				m.tagCursor = i
				return
			}
		}
	}
	if m.tagCursor >= len(m.tagItems) {
		m.tagCursor = max(0, len(m.tagItems)-1)
	}
}

func (m *Model) toggleTagCollapse() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tagCursor < 0 || m.tagCursor >= len(m.tagItems) {
		return
	}
	it := m.tagItems[m.tagCursor]
	if it.tag == "" || !it.hasChildren {
		return
	}
	m.collapsedTag[it.tag] = !m.collapsedTag[it.tag]
	m.rebuildTagItemsLocked()
}

func (m *Model) effectiveQuery() string {
	q := strings.TrimSpace(m.query)
	if m.activeTag != "" {
		if q != "" {
			q = "(" + q + ") AND tag:" + m.activeTag
		} else {
			q = "tag:" + m.activeTag
		}
	}
	if !strings.Contains(strings.ToLower(q), "sort:") {
		if q != "" {
			q += " sort:" + m.sortKey
		} else {
			q = "sort:" + m.sortKey
		}
	}
	return q
}

func (m *Model) reloadHits() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending {
		papers, _ := m.st.PendingPapers()
		var hits []model.SearchHit
		for _, p := range papers {
			h := model.SearchHit{ID: p.Meta.ID, Citekey: p.Meta.Citekey, Title: p.Meta.Title, Year: p.Meta.Year,
				Tags: p.Meta.Tags, AddedAt: p.Meta.AddedAt, HasNote: len(m.attachmentsFor(p.Meta.Citekey)) > 0}
			hits = append(hits, h)
		}
		m.hits = hits
	} else {
		hits, err := m.st.Index.Query(m.effectiveQuery())
		if err != nil {
			m.errMsg = err.Error()
			m.hits = nil
		} else {
			m.hits = hits
		}
	}
	if m.cursor >= len(m.hits) {
		m.cursor = max(0, len(m.hits)-1)
	}
	m.loadDetailLocked()
}

func (m *Model) attachmentsFor(citekey string) []store.Attachment {
	atts, _ := m.st.Attachments(citekey)
	return atts
}

func (m *Model) loadDetail() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadDetailLocked()
}

func (m *Model) loadDetailLocked() {
	h := m.currentHit()
	if h == nil {
		m.detail = nil
		m.attachments = nil
		return
	}
	p, err := m.st.Load(h.Citekey)
	if err != nil {
		m.errMsg = err.Error()
		m.detail = nil
		return
	}
	m.detail = p
	m.attachments = m.attachmentsFor(h.Citekey)
	m.detailTop = 0
	m.detailMax = 0
}

func (m *Model) currentHit() *model.SearchHit {
	if len(m.hits) == 0 || m.cursor < 0 || m.cursor >= len(m.hits) {
		return nil
	}
	return &m.hits[m.cursor]
}

func (m *Model) currentCitekey() string {
	if h := m.currentHit(); h != nil {
		return h.Citekey
	}
	return ""
}

func (m *Model) targets() []string {
	if len(m.marked) > 0 {
		var out []string
		for k := range m.marked {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if c := m.currentCitekey(); c != "" {
		return []string{c}
	}
	return nil
}

func (m *Model) cycleFocus(delta int) {
	m.focus = focus((int(m.focus) + delta + 3) % 3)
}

func (m *Model) move(delta int) {
	if m.focus == focusDetail {
		m.detailTop += delta
		if m.detailTop > m.detailMax {
			m.detailTop = m.detailMax
		}
		if m.detailTop < 0 {
			m.detailTop = 0
		}
		return
	}
	if m.focus == focusTags {
		next := m.tagCursor + delta
		for next >= 0 && next < len(m.tagItems) && m.tagItems[next].section {
			next += delta
		}
		if next < 0 {
			next = 0
			for next < len(m.tagItems) && m.tagItems[next].section {
				next++
			}
		}
		if next >= len(m.tagItems) {
			next = len(m.tagItems) - 1
			for next >= 0 && m.tagItems[next].section {
				next--
			}
		}
		if next >= 0 && next < len(m.tagItems) {
			m.tagCursor = next
		}
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.hits) {
		m.cursor = max(0, len(m.hits)-1)
	}
	m.loadDetail()
}

func (m *Model) toEdge(end bool) {
	if m.focus == focusDetail {
		if end {
			m.detailTop = m.detailMax
		} else {
			m.detailTop = 0
		}
		return
	}
	if m.focus == focusTags {
		if end {
			m.tagCursor = len(m.tagItems) - 1
			for m.tagCursor > 0 && m.tagItems[m.tagCursor].section {
				m.tagCursor--
			}
		} else {
			m.tagCursor = 0
			for m.tagCursor < len(m.tagItems)-1 && m.tagItems[m.tagCursor].section {
				m.tagCursor++
			}
		}
		return
	}
	if end {
		m.cursor = max(0, len(m.hits)-1)
	} else {
		m.cursor = 0
	}
	m.loadDetail()
}

func (m *Model) selectTag() {
	if m.tagCursor < 0 || m.tagCursor >= len(m.tagItems) {
		return
	}
	item := m.tagItems[m.tagCursor]
	m.pending = false
	m.activeTag = ""
	switch {
	case item.queryName != "":
		expr, err := m.st.LoadQuery(item.queryName)
		if err != nil {
			m.errMsg = err.Error()
			return
		}
		m.query = expr
		m.message = "query: " + item.queryName
	case item.all:
		m.activeTag = ""
	case item.pending:
		m.pending = true
	case item.tag != "":
		m.activeTag = item.tag
	default:
		return
	}
	m.cursor, m.listTop = 0, 0
	m.marked = map[string]bool{}
	m.reloadHits()
	m.focus = focusList
}

func (m *Model) toggleMark() {
	c := m.currentCitekey()
	if c == "" {
		return
	}
	if m.marked[c] {
		delete(m.marked, c)
	} else {
		m.marked[c] = true
	}
	if m.cursor < len(m.hits)-1 {
		m.cursor++
		m.loadDetail()
	}
}

func (m *Model) cycleSort() {
	order := []string{"relevance", "year-", "added-", "title+", "year+"}
	for i, s := range order {
		if s == m.sortKey {
			m.sortKey = order[(i+1)%len(order)]
			break
		}
	}
	m.reloadHits()
	m.message = "sort: " + m.sortKey
}

func (m *Model) checkDirty() {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.st.DirtyCount()
	if err == nil {
		m.dirty = n
	}
}

func (m *Model) openPDF() tea.Cmd {
	ck := m.currentCitekey()
	if ck == "" {
		return nil
	}
	path, err := m.st.OpenTarget(ck, "pdf", "")
	if err != nil {
		m.errMsg = err.Error()
		return nil
	}
	return m.execExternal(m.st.Global.Viewer, path)
}

func (m *Model) openNote() (tea.Model, tea.Cmd) {
	ck := m.currentCitekey()
	if ck == "" {
		return m, nil
	}
	path, err := m.st.OpenTarget(ck, "note", "")
	if err != nil {
		m.mode = modeNoteFormat
		m.message = "no note yet — choose format"
		return m, nil
	}
	return m, m.execExternal(m.st.Global.Editor, path)
}

func (m *Model) createNote(format string) tea.Cmd {
	ck := m.currentCitekey()
	m.mode = modeNormal
	if ck == "" {
		return nil
	}
	m.mu.Lock()
	path, err := m.st.NoteNew(ck, format, false)
	m.mu.Unlock()
	if err != nil {
		m.errMsg = err.Error()
		return nil
	}
	m.message = "note created"
	m.reloadHits()
	return m.execExternal(m.st.Global.Editor, path)
}

func (m *Model) execExternal(program, path string) tea.Cmd {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		m.errMsg = "no external program configured"
		return nil
	}
	args := parts[1:]
	found := false
	for i, a := range args {
		if strings.Contains(a, "%s") {
			args[i] = strings.ReplaceAll(a, "%s", path)
			found = true
		}
	}
	if !found {
		args = append(args, path)
	}
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

func (m *Model) convert() tea.Cmd {
	ck := m.currentCitekey()
	if ck == "" {
		return nil
	}
	m.busy = true
	m.busyMsg = "converting " + ck
	m.startTask("convert")
	return func() tea.Msg {
		m.mu.Lock()
		defer m.mu.Unlock()
		files, _, err := m.st.Convert(ck)
		return convertMsg{citekey: ck, files: files, err: err}
	}
}

func (m *Model) reindex(rebuild bool) tea.Cmd {
	m.busy = true
	if rebuild {
		m.busyMsg = "rebuilding index"
	} else {
		m.busyMsg = "syncing index"
	}
	m.startTask("reindex")
	return func() tea.Msg {
		m.mu.Lock()
		defer m.mu.Unlock()
		stats, err := m.st.Reindex(rebuild, nil)
		return reindexMsg{stats: stats, err: err}
	}
}

func (m *Model) deleteCurrent() (tea.Model, tea.Cmd) {
	targets := m.targets()
	if len(targets) == 0 {
		return m, nil
	}
	m.mode = modeConfirm
	if len(targets) == 1 {
		m.confirm = "Move " + targets[0] + " to .trash?"
	} else {
		m.confirm = fmt.Sprintf("Move %d papers to .trash?", len(targets))
	}
	m.action = func() error {
		for _, ck := range targets {
			if _, err := m.st.Rm(ck); err != nil {
				return err
			}
			delete(m.marked, ck)
		}
		return nil
	}
	return m, nil
}

func (m *Model) renameSuggested() (tea.Model, tea.Cmd) {
	ck := m.currentCitekey()
	if ck == "" || m.detail == nil {
		return m, nil
	}
	suggested := m.st.GenerateCitekey(m.detail.Meta)
	if suggested == ck || m.detail.State.CitekeyLocked {
		m.message = "citekey already matches the template"
		return m, nil
	}
	m.mode = modeConfirm
	m.confirm = "Rename " + ck + " → " + suggested + "? Relative links will be rewritten."
	m.action = func() error {
		_, err := m.st.RenamePaper(ck, suggested)
		return err
	}
	return m, nil
}

func (m *Model) openReview() {
	m.mu.Lock()
	papers, _ := m.st.PendingPapers()
	m.mu.Unlock()
	m.reviewPapers = papers
	m.reviewCursor = 0
	m.reviewCand = 0
	m.reviewSel = map[int]bool{}
	m.mode = modeReview
	m.loadReviewFields()
}

func (m *Model) currentReview() *model.Paper {
	if len(m.reviewPapers) == 0 || m.reviewCursor >= len(m.reviewPapers) {
		return nil
	}
	return m.reviewPapers[m.reviewCursor]
}

func (m *Model) loadReviewFields() {
	p := m.currentReview()
	m.reviewFields = nil
	m.reviewSel = map[int]bool{}
	if p == nil || len(p.State.Review.Candidates) == 0 {
		return
	}
	if m.reviewCand >= len(p.State.Review.Candidates) {
		m.reviewCand = 0
	}
	for k := range p.State.Review.Candidates[m.reviewCand].Fields {
		m.reviewFields = append(m.reviewFields, k)
	}
	sort.Strings(m.reviewFields)
	m.reviewFieldCursor = 0
	lowConfidence := p.State.Review.Candidates[m.reviewCand].Confidence < 0.7
	for i := range m.reviewFields {
		m.reviewSel[i] = !lowConfidence
	}
}

func (m *Model) handleReviewKey(key string) (tea.Model, tea.Cmd) {
	p := m.currentReview()
	switch key {
	case "esc", "q", "p":
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if len(m.reviewFields) > 0 {
			m.reviewFieldCursor = (m.reviewFieldCursor + 1) % len(m.reviewFields)
		}
	case "k", "up":
		if len(m.reviewFields) > 0 {
			m.reviewFieldCursor = (m.reviewFieldCursor - 1 + len(m.reviewFields)) % len(m.reviewFields)
		}
	case " ":
		if len(m.reviewFields) > 0 {
			m.reviewSel[m.reviewFieldCursor] = !m.reviewSel[m.reviewFieldCursor]
		}
	case "n":
		if m.reviewCursor < len(m.reviewPapers)-1 {
			m.reviewCursor++
			m.reviewCand = 0
			m.loadReviewFields()
		}
	case "P":
		if m.reviewCursor > 0 {
			m.reviewCursor--
			m.reviewCand = 0
			m.loadReviewFields()
		}
	case "tab":
		if p != nil && len(p.State.Review.Candidates) > 1 {
			m.reviewCand = (m.reviewCand + 1) % len(p.State.Review.Candidates)
			m.loadReviewFields()
		}
	case "a", "A", "enter":
		if p == nil {
			return m, nil
		}
		var fields []string
		for i, f := range m.reviewFields {
			if m.reviewSel[i] {
				fields = append(fields, f)
			}
		}
		m.mu.Lock()
		err := m.st.ReviewAcceptFields(p.Meta.Citekey, m.reviewCand, fields)
		m.mu.Unlock()
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.message = "accepted " + p.Meta.Citekey
		m.reopenReview()
	case "K":
		if p != nil {
			m.mu.Lock()
			err := m.st.ReviewKeep(p.Meta.Citekey)
			m.mu.Unlock()
			if err != nil {
				m.errMsg = err.Error()
			} else {
				m.message = "kept current metadata for " + p.Meta.Citekey
				m.reopenReview()
			}
		}
	}
	return m, nil
}

func (m *Model) reopenReview() {
	m.openReview()
	m.refreshTags()
	m.reloadHits()
	if len(m.reviewPapers) == 0 {
		m.mode = modeNormal
		m.message = "review queue empty"
	}
}

func (m *Model) startTask(label string) {
	m.tasks = append(m.tasks, task{Label: label, Started: time.Now()})
	if len(m.tasks) > 20 {
		m.tasks = m.tasks[len(m.tasks)-20:]
	}
}

func (m *Model) finishTask(label string, err error) {
	for i := len(m.tasks) - 1; i >= 0; i-- {
		if m.tasks[i].Label == label && !m.tasks[i].Done {
			m.tasks[i].Done = true
			if err != nil {
				m.tasks[i].Err = err.Error()
			}
			break
		}
	}
}

func (m *Model) readLogs() []string {
	lines := m.st.Log.Tail(200)
	var out []string
	for _, l := range lines {
		out = append(out, logging.ParseLine(l))
	}
	return out
}

// applyTags parses "tag1, tag2, -oldtag" and applies to the selection.
func (m *Model) applyTags(val string) tea.Cmd {
	add := []string{}
	rm := []string{}
	for _, part := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "-") {
			rm = append(rm, strings.TrimPrefix(part, "-"))
		} else {
			add = append(add, part)
		}
	}
	targets := m.targets()
	if len(targets) == 0 {
		return nil
	}
	m.busy = true
	m.busyMsg = "updating tags"
	m.startTask("tag")
	return func() tea.Msg {
		m.mu.Lock()
		defer m.mu.Unlock()
		var firstErr error
		for _, ck := range targets {
			if len(add) > 0 {
				if err := m.st.AddTags(ck, add...); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if len(rm) > 0 {
				if err := m.st.RemoveTags(ck, rm...); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		return actionMsg{label: "tag", err: firstErr}
	}
}

func (m *Model) runCommand(line string) tea.Cmd {
	fields := splitCommand(line)
	if len(fields) == 0 {
		return nil
	}
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "q", "quit":
		return tea.Quit
	case "help":
		m.mode = modeHelp
	case "sort":
		if len(args) > 0 {
			m.sortKey = args[0]
			m.reloadHits()
		}
	case "query":
		if len(args) == 0 {
			m.errMsg = "usage: query save <name> [expr] | query run <name> | query rm <name>"
			return nil
		}
		switch args[0] {
		case "save":
			if len(args) < 2 {
				m.errMsg = "usage: query save <name> [expr]"
				return nil
			}
			expr := strings.Join(args[2:], " ")
			if expr == "" {
				expr = m.query
			}
			m.mu.Lock()
			err := m.st.SaveQuery(args[1], expr)
			m.mu.Unlock()
			if err != nil {
				m.errMsg = err.Error()
			} else {
				m.message = "query saved: " + args[1]
				m.refreshTags()
			}
		case "run":
			if len(args) != 2 {
				m.errMsg = "usage: query run <name>"
				return nil
			}
			m.mu.Lock()
			expr, err := m.st.LoadQuery(args[1])
			m.mu.Unlock()
			if err != nil {
				m.errMsg = err.Error()
				return nil
			}
			m.query = expr
			m.pending = false
			m.activeTag = ""
			m.reloadHits()
			m.message = "query: " + args[1]
		case "rm":
			if len(args) != 2 {
				m.errMsg = "usage: query rm <name>"
				return nil
			}
			m.mu.Lock()
			err := m.st.DeleteQuery(args[1])
			m.mu.Unlock()
			if err != nil {
				m.errMsg = err.Error()
			} else {
				m.message = "query removed: " + args[1]
				m.refreshTags()
			}
		}
	case "search":
		m.query = strings.Join(args, " ")
		m.pending = false
		m.reloadHits()
	case "tag":
		if len(args) < 1 {
			m.errMsg = "usage: tag add|rm <tags> | tag rename <old> <new> | tag merge <a> <b> | tag delete <t>"
			return nil
		}
		sub := args[0]
		rest := args[1:]
		switch sub {
		case "add", "rm":
			return m.applyTags(strings.Join(rest, " "))
		case "rename", "merge":
			if len(rest) != 2 {
				m.errMsg = "expected two tags"
				return nil
			}
			m.busy = true
			m.busyMsg = "tag " + sub
			m.startTask("tag " + sub)
			return func() tea.Msg {
				m.mu.Lock()
				defer m.mu.Unlock()
				var err error
				if sub == "rename" {
					_, err = m.st.RenameTag(rest[0], rest[1], false)
				} else {
					_, err = m.st.MergeTags(rest[0], rest[1], false)
				}
				return actionMsg{label: "tag " + sub, err: err}
			}
		case "delete":
			if len(rest) != 1 {
				m.errMsg = "usage: tag delete <tag>"
				return nil
			}
			m.busy = true
			m.busyMsg = "tag delete"
			m.startTask("tag delete")
			return func() tea.Msg {
				m.mu.Lock()
				defer m.mu.Unlock()
				_, err := m.st.DeleteTag(rest[0], false)
				return actionMsg{label: "tag delete", err: err}
			}
		}
	case "meta":
		if len(args) >= 2 && args[0] == "set" {
			ck := m.currentCitekey()
			kv := strings.Join(args[1:], " ")
			if len(args) >= 3 && !strings.Contains(args[1], "=") {
				ck = args[1]
				kv = strings.Join(args[2:], " ")
			}
			if ck == "" {
				m.errMsg = "no paper selected; usage: meta set [citekey] field=value"
				return nil
			}
			i := strings.Index(kv, "=")
			if i <= 0 {
				m.errMsg = "usage: meta set [citekey] field=value"
				return nil
			}
			m.busy = true
			m.startTask("meta set")
			return func() tea.Msg {
				m.mu.Lock()
				defer m.mu.Unlock()
				err := m.st.MetaSet(ck, kv[:i], kv[i+1:])
				return actionMsg{label: "meta set", err: err}
			}
		}
		m.errMsg = "usage: meta set [citekey] field=value"
	case "refresh":
		m.busy = true
		m.busyMsg = "refreshing metadata"
		m.startTask("meta refresh")
		ck := m.currentCitekey()
		return func() tea.Msg {
			m.mu.Lock()
			defer m.mu.Unlock()
			if ck == "" {
				return actionMsg{label: "meta refresh", err: fmt.Errorf("no paper selected")}
			}
			_, err := m.st.MetaRefresh(ck, false)
			return actionMsg{label: "meta refresh", err: err}
		}
	case "refresh-all":
		m.busy = true
		m.busyMsg = "refreshing all metadata"
		m.startTask("meta refresh --all")
		return func() tea.Msg {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.st.MetaRefreshAll(false, false)
			return actionMsg{label: "meta refresh --all"}
		}
	case "rename":
		if len(args) != 2 {
			m.errMsg = "usage: rename <old> <new> [--yes]"
			return nil
		}
		oldKey, newKey := args[0], args[1]
		m.mu.Lock()
		preview, err := m.st.PreviewRename(oldKey, newKey)
		m.mu.Unlock()
		if err != nil {
			m.errMsg = err.Error()
			return nil
		}
		if contains(args, "--yes") {
			m.busy = true
			m.startTask("rename")
			return func() tea.Msg {
				m.mu.Lock()
				defer m.mu.Unlock()
				_, err := m.st.RenamePaper(oldKey, newKey)
				return actionMsg{label: "rename", err: err}
			}
		}
		m.mode = modeConfirm
		m.confirm = fmt.Sprintf("Rename %s → %s? %d file(s) will have links rewritten.",
			oldKey, newKey, len(preview.ChangedFiles))
		m.action = func() error {
			_, err := m.st.RenamePaper(oldKey, newKey)
			return err
		}
	case "rekey":
		dry := contains(args, "--dry-run")
		force := contains(args, "--force")
		m.mu.Lock()
		ops, err := m.st.PlanRekey(force)
		m.mu.Unlock()
		if err != nil {
			m.errMsg = err.Error()
			return nil
		}
		active := countActive(ops)
		if dry {
			m.message = fmt.Sprintf("rekey plan: %d rename(s) (close with esc; use :rekey to apply)", active)
			for _, op := range ops {
				if !op.Skipped && op.Old != op.New {
					m.message += "  " + op.Old + "→" + op.New
				}
			}
			return nil
		}
		if active == 0 {
			m.message = "nothing to rekey"
			return nil
		}
		m.mode = modeConfirm
		m.confirm = fmt.Sprintf("Rekey %d paper(s) and rewrite relative links?", active)
		m.action = func() error {
			_, err := m.st.ApplyRekey(ops)
			return err
		}
		return nil
	case "reindex":
		return m.reindex(contains(args, "--rebuild"))
	case "bib":
		m.busy = true
		m.startTask("bib export")
		return func() tea.Msg {
			m.mu.Lock()
			defer m.mu.Unlock()
			return actionMsg{label: "bib export", err: m.st.ExportBib()}
		}
	case "note":
		// note new [md|tex|typ]
		format := "md"
		if len(args) >= 2 {
			format = args[1]
		}
		return m.createNote(format)
	case "convert":
		return m.convert()
	case "rm":
		if len(args) == 1 {
			m.busy = true
			m.startTask("rm")
			return func() tea.Msg {
				m.mu.Lock()
				defer m.mu.Unlock()
				_, err := m.st.Rm(args[0])
				return actionMsg{label: "rm", err: err}
			}
		}
		model, cmd := m.deleteCurrent()
		_ = model
		return cmd
	case "open":
		if len(args) > 0 && args[0] == "note" {
			return m.execExternal(m.st.Global.Editor, mustPath(m.st, m.currentCitekey(), "note"))
		}
		return m.execExternal(m.st.Global.Viewer, mustPath(m.st, m.currentCitekey(), "pdf"))
	default:
		m.errMsg = "unknown command: " + cmd
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func countActive(ops []store.RenameOp) int {
	n := 0
	for _, op := range ops {
		if !op.Skipped && op.Old != op.New {
			n++
		}
	}
	return n
}

func mustPath(s *store.Store, ck, mode string) string {
	if ck == "" {
		return ""
	}
	p, err := s.OpenTarget(ck, mode, "")
	if err != nil {
		return ""
	}
	return p
}

func splitCommand(s string) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"' || r == '\'':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

var _ = index.Record{}
