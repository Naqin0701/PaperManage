package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"papermgr/internal/model"
)

var (
	// Tokyo Night foreground palette. No background is painted, so Ghostty
	// transparency and blur show through every cell.
	colFg     = lipgloss.AdaptiveColor{Light: "#3760bf", Dark: "#c0caf5"}
	colSoft   = lipgloss.AdaptiveColor{Light: "#3a4a8a", Dark: "#a9b1d6"}
	colAccent = lipgloss.AdaptiveColor{Light: "#2e7de9", Dark: "#7aa2f7"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6172b0", Dark: "#737aa2"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#565f89", Dark: "#6b7394"}
	colGreen  = lipgloss.AdaptiveColor{Light: "#587539", Dark: "#9ece6a"}
	colYellow = lipgloss.AdaptiveColor{Light: "#8c6c3e", Dark: "#e0af68"}
	colRed    = lipgloss.AdaptiveColor{Light: "#f52a65", Dark: "#f7768e"}
	colCyan   = lipgloss.AdaptiveColor{Light: "#007197", Dark: "#7dcfff"}

	styleApp      = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colFg)
	styleRow      = lipgloss.NewStyle().Foreground(colSoft)
	styleDim      = lipgloss.NewStyle().Foreground(colDim)
	styleAccent   = lipgloss.NewStyle().Foreground(colAccent)
	styleGreen    = lipgloss.NewStyle().Foreground(colGreen)
	styleYellow   = lipgloss.NewStyle().Foreground(colYellow)
	styleRed      = lipgloss.NewStyle().Foreground(colRed)
	styleCyan     = lipgloss.NewStyle().Foreground(colCyan)
	styleFaint    = lipgloss.NewStyle().Foreground(colFaint)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colFg)
)

type styledLine struct {
	text  string
	style lipgloss.Style
}

// View renders the whole screen as a flat, fixed three-pane layout.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	switch m.mode {
	case modeHelp:
		return m.renderHelp()
	case modeLogs:
		return m.renderLogs()
	case modeTasks:
		return m.renderTasks()
	case modeReview:
		return m.renderReview()
	case modeConfirm:
		return m.renderConfirm()
	}

	header := m.renderHeader()
	filter := ""
	if m.filterVisible() {
		filter = m.renderFilterBar()
	}
	lines := []string{header}
	if filter != "" {
		lines = append(lines, filter)
	}
	if m.height >= 5 {
		lines = append(lines, "")
	}
	rows := m.height - len(lines) - 1
	if rows < 1 {
		rows = 1
	}
	lines = append(lines, m.renderBody(rows))
	lines = append(lines, m.renderFooter())
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *Model) renderBody(height int) string {
	if m.zoom || m.width < narrowWidth {
		switch m.focus {
		case focusTags:
			return m.renderTags(m.width, height, "")
		case focusDetail:
			return m.renderDetail(m.width, height)
		default:
			return m.renderList(m.width, height, "")
		}
	}
	leftW, midW, rightW := m.layoutWidths()
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderTags(leftW, height, " │ "),
		m.renderList(midW, height, " │ "),
		m.renderDetail(rightW, height),
	)
}

// layoutWidths splits the screen into three panes whose widths always sum to
// m.width. Callers must only use it when m.width >= narrowWidth.
func (m *Model) layoutWidths() (int, int, int) {
	w := m.width
	left := w / 5
	if left < 16 {
		left = 16
	}
	if left > 28 {
		left = 28
	}
	right := w * 38 / 100
	if right < 30 {
		right = 30
	}
	if right > 70 {
		right = 70
	}
	mid := w - left - right
	if mid < 20 {
		need := 20 - mid
		if cut := min(need, right-24); cut > 0 {
			right -= cut
			need -= cut
		}
		if need > 0 {
			if cut := min(need, left-14); cut > 0 {
				left -= cut
			}
		}
		mid = w - left - right
	}
	if mid < 1 {
		mid = 1
		right = w - left - mid
	}
	if right < 1 {
		right = 1
		left = w - mid - right
	}
	return left, mid, right
}

func (m *Model) renderHeader() string {
	rightText := fmt.Sprintf("%d results", len(m.hits))
	rightStyle := styleDim
	if m.busy {
		rightText += "  ·  " + m.busyMsg
		rightStyle = styleYellow
	} else if m.dirty > 0 {
		rightText += fmt.Sprintf("  ·  %d unindexed", m.dirty)
		rightStyle = styleYellow
	} else {
		rightText += "  ·  index fresh"
	}
	if m.schemaIssues > 0 {
		rightText += fmt.Sprintf("  ·  %d schema issue(s)", m.schemaIssues)
		rightStyle = styleRed
	}
	if m.width < 80 {
		rightText = fmt.Sprintf("%d results", len(m.hits))
		if m.dirty > 0 {
			rightText += " !"
			rightStyle = styleYellow
		}
		if m.schemaIssues > 0 {
			rightText += " !schema"
			rightStyle = styleRed
		}
	}
	right := rightStyle.Render(rightText)
	path := m.st.Root
	budget := m.width - 48
	if budget < 6 {
		path = ""
	} else {
		path = elideMiddle(path, budget)
	}
	left := styleApp.Render("papermgr")
	if path != "" {
		left += styleDim.Render("  " + path)
	}
	pad := m.width - 1 - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 0 {
		// The status has priority at narrow widths; trim the library path.
		pathBudget := m.width - 1 - lipgloss.Width(right) - 12
		if pathBudget > 0 {
			left = styleApp.Render("papermgr") + styleDim.Render("  "+elideMiddle(m.st.Root, pathBudget))
		} else {
			left = styleApp.Render("papermgr")
		}
		pad = m.width - 1 - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if pad < 0 {
		return truncateStr(" "+left+right, m.width)
	}
	return " " + left + strings.Repeat(" ", pad) + right
}

func (m *Model) renderFilterBar() string {
	if !m.hasFilter() {
		return ""
	}
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(styleDim.Render("filter "))
	first := true
	add := func(text string) {
		if !first {
			b.WriteString(styleFaint.Render(" · "))
		}
		first = false
		b.WriteString(text)
	}
	if m.pending {
		add(styleYellow.Render("pending review"))
	}
	if q := strings.TrimSpace(m.query); q != "" {
		add(styleAccent.Render("query: ") + styleRow.Render(truncateStr(q, max(12, m.width/2))))
	}
	if len(m.tagSel) > 0 {
		op := styleFaint.Render(" ∧ ")
		if m.tagOr {
			op = styleFaint.Render(" ∨ ")
		}
		var terms []string
		for _, t := range sortedKeys(m.tagSel) {
			terms = append(terms, tagStyle(t).Render("#"+t))
		}
		add(strings.Join(terms, op))
	}
	if len(m.tagNot) > 0 {
		var terms []string
		for _, t := range sortedKeys(m.tagNot) {
			terms = append(terms, styleRed.Render("¬#"+t))
		}
		add(strings.Join(terms, styleFaint.Render(" · ")))
	}
	b.WriteString("  " + styleFaint.Render("[esc] clear"))
	return truncateStr(b.String(), m.width)
}

func (m *Model) renderFooter() string {
	right := styleDim.Render("/ search   ? help   q quit")
	switch m.mode {
	case modeSearch, modeCommand, modeTag:
		prefix := "/"
		if m.mode == modeCommand {
			prefix = ":"
		} else if m.mode == modeTag {
			prefix = "tag>"
		}
		value := m.input.Value()
		line := " " + prefix + " " + value + "▏"
		if m.mode == modeTag {
			line += styleDim.Render("  (comma/space separated, -tag removes)")
		}
		return joinLine(line, styleDim.Render("esc cancel"), m.width)
	case modeNoteFormat:
		return joinLine(" no note   [m] markdown   [t] tex   [p] typst", styleDim.Render("esc cancel"), m.width)
	}

	segments := []struct {
		key  string
		text string
		f    focus
	}{
		{"1", "tags", focusTags},
		{"2", "results", focusList},
		{"3", "detail", focusDetail},
	}
	var left strings.Builder
	left.WriteString(" ")
	for i, seg := range segments {
		if i > 0 {
			left.WriteString("   ")
		}
		label := seg.key + " " + seg.text
		if m.focus == seg.f {
			left.WriteString(styleAccent.Render(label))
		} else {
			left.WriteString(styleDim.Render(label))
		}
	}
	hints := map[focus]string{
		focusTags:   "space select · enter filter · h/l collapse · - exclude · | and/or",
		focusList:   "space mark · o open · e note · t tag · d trash · y citekey",
		focusDetail: "j/k scroll · o PDF · e note · y citekey",
	}
	left.WriteString("   " + styleFaint.Render(hints[m.focus]))
	if len(m.marked) > 0 {
		left.WriteString("   " + styleGreen.Render(fmt.Sprintf("%d marked", len(m.marked))))
	}

	if m.errMsg != "" {
		msg := truncateStr(m.errMsg, max(8, m.width/2))
		left.WriteString("   " + styleRed.Render("· "+msg))
	} else if m.message != "" {
		msg := truncateStr(m.message, max(8, m.width/3))
		left.WriteString("   " + styleDim.Render("· "+msg))
	}
	return joinLine(left.String(), right, m.width)
}

func joinLine(left, right string, width int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw+1 > width {
		// Keep the left side (mode/hotkeys) and trim the right hint first.
		avail := width - lw - 1
		if avail > 0 {
			right = ansi.Truncate(right, avail, "")
			rw = lipgloss.Width(right)
			return left + strings.Repeat(" ", max(1, width-lw-rw-1)) + right
		}
		return renderFixed(ansi.Truncate(left, width, "…"), lipgloss.Style{}, width)
	}
	return left + strings.Repeat(" ", max(1, width-lw-rw-1)) + right
}

func (m *Model) paneHeader(title, suffix string, width int, focused bool) styledLine {
	left := " " + title
	if focused {
		left = "▏" + title
	}
	line := left
	if suffix != "" {
		right := suffix + " "
		pad := width - lipgloss.Width(left) - lipgloss.Width(right)
		if pad < 1 {
			pad = 1
		}
		line += strings.Repeat(" ", pad) + right
	}
	st := styleDim
	if focused {
		st = styleAccent
	}
	return styledLine{text: line, style: st}
}

func (m *Model) renderTags(width, height int, gutter string) string {
	gutterW := lipgloss.Width(gutter)
	contentW := max(1, width-gutterW)
	suffix := fmt.Sprintf("%d", len(m.tags))
	if len(m.tagSel) > 0 {
		suffix = fmt.Sprintf("%d · %d selected", len(m.tags), len(m.tagSel))
	}
	lines := []styledLine{m.paneHeader("TAGS", suffix, contentW, m.focus == focusTags)}
	visible := max(1, height-1)
	top := m.tagScrollTop(visible)
	for i := top; i < len(m.tagItems) && i < top+visible; i++ {
		it := m.tagItems[i]
		if it.section {
			text := "  " + styleFaint.Render("── ") + styleDim.Render(it.label) + styleFaint.Render(" ──")
			lines = append(lines, styledLine{text: text})
			continue
		}
		count := ""
		if it.tag != "" || it.all || it.pending {
			count = fmt.Sprintf("%d", it.count)
		}
		cursor := "  "
		cursorStyled := cursor
		if i == m.tagCursor {
			cursor = "▌ "
			cursorStyled = styleAccent.Render(cursor)
		}
		var prefixPlain, prefixStyled string
		switch {
		case it.tag != "":
			tree := strings.Repeat("│ ", it.depth)
			marker := "• "
			markerStyled := styleDim.Render(marker)
			if it.hasChildren {
				if it.collapsed {
					marker = "▸ "
				} else {
					marker = "▾ "
				}
				markerStyled = styleAccent.Render(marker)
			}
			check := " "
			checkStyled := styleFaint.Render(" ")
			if it.excluded {
				check = "✗"
				checkStyled = styleRed.Render("✗")
			} else if it.selected {
				check = "✓"
				checkStyled = styleGreen.Render("✓")
			}
			prefixPlain = cursor + tree + marker + check + " "
			prefixStyled = cursorStyled + styleFaint.Render(tree) + markerStyled + checkStyled + " "
		case it.queryName != "":
			prefixPlain = cursor + "↳ "
			prefixStyled = cursorStyled + styleAccent.Render("↳ ")
		default:
			prefixPlain = cursor + "   "
			prefixStyled = cursorStyled + "   "
		}
		labelW := contentW - lipgloss.Width(prefixPlain) - lipgloss.Width(count) - 1
		if labelW < 2 {
			labelW = 2
		}
		label := truncateStr(it.label, labelW)
		pad := labelW - lipgloss.Width(label)
		st := tagStyle(it.tag)
		if it.tag == "" {
			st = styleRow
		}
		switch {
		case i == m.tagCursor:
			if it.tag == "" {
				st = styleSelected
			} else {
				st = st.Bold(true)
			}
		case it.pending && m.pending:
			st = styleAccent
		case it.all && !m.hasFilter():
			st = styleAccent
		case it.tag != "" && it.selected:
			st = st.Bold(true)
		}
		text := prefixStyled + st.Render(label) + strings.Repeat(" ", max(0, pad)) + styleDim.Render(count) + " "
		lines = append(lines, styledLine{text: text})
	}
	return joinPanes(lines, contentW, height, gutter)
}

func (m *Model) renderList(width, height int, gutter string) string {
	title := "RESULTS"
	suffix := fmt.Sprintf("%d · %s", len(m.hits), sortLabel(m.sortKey))
	if m.pending {
		title = "PENDING"
		suffix = fmt.Sprintf("%d", len(m.hits))
	}
	gutterW := lipgloss.Width(gutter)
	contentW := max(1, width-gutterW)
	lines := []styledLine{m.paneHeader(title, suffix, contentW, m.focus == focusList)}
	visible := max(1, height-1)
	if m.cursor < m.listTop {
		m.listTop = m.cursor
	}
	if m.cursor >= m.listTop+visible {
		m.listTop = m.cursor - visible + 1
	}
	if m.listTop < 0 {
		m.listTop = 0
	}
	for i := m.listTop; i < len(m.hits) && i < m.listTop+visible; i++ {
		h := m.hits[i]
		cursor := "  "
		cursorStyled := cursor
		if i == m.cursor {
			cursorStyled = styleAccent.Render("▸ ")
		}
		check := "  "
		checkStyled := check
		if m.marked[h.Citekey] {
			checkStyled = styleGreen.Render("✓ ")
		}
		glyph, glyphStyle := statusBadge(h.Tags)
		status := glyphStyle.Render(glyph) + " "
		year := "    "
		if h.Year != nil {
			year = fmt.Sprintf("%4d", *h.Year)
		}
		noteW := 0
		note := ""
		if h.HasNote {
			note = styleGreen.Render("✎")
			noteW = 2
		}
		budget := contentW - 11 - noteW
		if budget < 4 {
			budget = 4
		}
		titleStyle := styleRow
		if i == m.cursor {
			titleStyle = styleSelected
		}
		text := cursorStyled + checkStyled + status + styleDim.Render(year) + " " + titleStyle.Render(truncateStr(h.Title, budget))
		if note != "" {
			text += " " + note
		}
		lines = append(lines, styledLine{text: text})
	}
	if len(m.hits) == 0 {
		lines = append(lines, styledLine{text: "  no papers", style: styleDim})
		if m.hasFilter() {
			lines = append(lines, styledLine{text: "  esc clears filters", style: styleFaint})
		}
	}
	return joinPanes(lines, contentW, height, gutter)
}

func (m *Model) renderDetail(width, height int) string {
	visible := max(1, height-1)
	var body []styledLine
	suffix := ""
	labelW := 8
	valueW := max(10, width-labelW-3)
	addField := func(label, value string, st lipgloss.Style) {
		if strings.TrimSpace(value) == "" {
			return
		}
		lines := wrap(value, valueW)
		for i, line := range lines {
			prefix := " " + styleDim.Render(padPlain(label, labelW)) + " "
			if i > 0 {
				prefix = strings.Repeat(" ", labelW+2)
			}
			body = append(body, styledLine{text: prefix + st.Render(line)})
		}
	}

	if m.detail == nil {
		body = append(body, styledLine{text: " no paper selected", style: styleDim})
		body = append(body, styledLine{})
	} else {
		p := m.detail
		addField("title", p.Meta.Title, styleTitle)
		addField("citekey", p.Meta.Citekey, styleDim)
		body = append(body, styledLine{})

		if len(p.Meta.Authors) > 0 {
			var names []string
			for _, a := range p.Meta.Authors {
				names = append(names, strings.TrimSpace(a.Given+" "+a.Family))
			}
			addField("authors", strings.Join(names, ", "), styleRow)
		}
		year := ""
		if p.Meta.Year != nil {
			year = fmt.Sprint(*p.Meta.Year)
		}
		addField("year", year, styleRow)
		if v := p.Meta.Venue; v != nil {
			if v.Kind == "journal" {
				addField("journal", v.Name, styleRow)
				if v.Short != "" && v.Short != v.Name {
					addField("venue", v.Short, styleRow)
				}
			} else {
				venue := v.Name
				if v.Short != "" && v.Short != venue {
					venue = v.Short + " · " + venue
				}
				addField("venue", venue, styleRow)
			}
		}
		addField("type", p.Meta.EntryType, styleRow)
		body = append(body, styledLine{})

		if len(p.Meta.Tags) > 0 {
			body = append(body, m.tagFieldLines(p.Meta.Tags, labelW, width)...)
		} else {
			addField("tags", "—", styleDim)
		}
		review := p.State.Review.Status
		reviewStyle := styleGreen
		if p.State.Review.Status == "pending" {
			reviewStyle = styleYellow
		}
		if p.State.CitekeyLocked {
			review += "  ·  citekey locked"
		}
		addField("review", review, reviewStyle)
		if p.State.Index.Error != "" {
			addField("issue", p.State.Index.Error, styleRed)
		}
		if !p.State.CitekeyLocked {
			if suggested := m.st.GenerateCitekey(p.Meta); suggested != p.Meta.Citekey {
				addField("rename", suggested+"  (press x)", styleYellow)
			}
		}
		if p.Meta.DOI != "" {
			addField("doi", p.Meta.DOI, styleCyan)
		}
		if p.Meta.ArxivID != "" {
			addField("arxiv", p.Meta.ArxivID, styleCyan)
		}
		if p.Meta.URL != "" {
			addField("url", p.Meta.URL, styleCyan)
		}
		body = append(body, styledLine{})

		notes := 0
		var files []string
		for _, a := range m.attachments {
			if a.Kind == "note" {
				notes++
				continue
			}
			files = append(files, a.Kind+"/"+baseName(a.Rel))
		}
		if notes > 0 {
			addField("note", "present", styleGreen)
		} else {
			addField("note", "none", styleDim)
		}
		if len(files) > 0 {
			addField("files", strings.Join(files, "  "), styleDim)
		}

		md := m.markdownExcerpt()
		if md != "" || p.Meta.Abstract != "" {
			body = append(body, styledLine{})
			body = append(body, styledLine{text: " " + styleFaint.Render(strings.Repeat("─", max(4, width-4))), style: lipgloss.Style{}})
			body = append(body, styledLine{})
			if md != "" {
				body = append(body, styledLine{text: " " + styleDim.Render("markdown"), style: lipgloss.Style{}})
				for _, line := range wrap(md, width-2) {
					body = append(body, styledLine{text: " " + styleRow.Render(line)})
				}
			} else {
				body = append(body, styledLine{text: " " + styleDim.Render("abstract"), style: lipgloss.Style{}})
				for _, line := range wrap(p.Meta.Abstract, width-2) {
					body = append(body, styledLine{text: " " + styleRow.Render(line)})
				}
			}
		}
	}
	m.detailMax = max(0, len(body)-visible)
	if m.detailTop > m.detailMax {
		m.detailTop = m.detailMax
	}
	if m.detailTop < 0 {
		m.detailTop = 0
	}
	if m.detailMax > 0 && m.detailTop > 0 {
		pct := m.detailTop * 100 / m.detailMax
		suffix = fmt.Sprintf("%d%%", pct)
	}
	head := m.paneHeader("DETAIL", suffix, width, m.focus == focusDetail)
	if m.detailTop > 0 && m.detailTop < len(body) {
		body = body[m.detailTop:]
	}
	return joinPanes(append([]styledLine{head}, body...), width, height, "")
}

func joinPanes(lines []styledLine, width, height int, gutter string) string {
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		var line styledLine
		if i < len(lines) {
			line = lines[i]
		}
		rendered := renderFixed(line.text, line.style, width)
		if gutter != "" {
			rendered += styleFaint.Render(gutter)
		}
		out = append(out, rendered)
	}
	return strings.Join(out, "\n")
}

// renderFixed fits plain or pre-styled text into exactly width columns,
// painting horizontal padding with the same background as the content.
func renderFixed(text string, st lipgloss.Style, width int) string {
	text = truncateStr(text, width)
	pad := width - lipgloss.Width(text)
	out := st.Render(text)
	if pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func (m *Model) renderHelp() string {
	help := `Global
  /            search prompt
  :            command prompt
  ?            this help
  q            quit
  1 2 3        focus tags / results / detail
  tab          cycle focus (also shift+tab)
  h l ← →      move focus; in tags: collapse / expand
  z            zoom the focused pane
  esc          clear error, marks, filters; else back to results
  p            pending metadata review
  L            structured logs
  b            background tasks
  R            reindex changed files
  r            reload tags, results and index status
  s            cycle sort
  y / Y        copy citekey / DOI
  U            clear marked papers
  ctrl+a       mark all visible papers
  mouse        click to focus/select, wheel to scroll

Tags
  space        select / deselect tag (multi-select)
  -            exclude tag (NOT)
  |            toggle AND / OR for selected tags
  enter        apply selected tags; with none selected, filter cursor tag
  h l ← →      collapse / expand the cursor tag
  esc          clear all filters

Papers
  j k ↑ ↓      move
  g G          first / last
  ctrl+d/u     page
  space        mark paper (marks clear when the filter changes)
  t            add/remove tags on selection
  o            open PDF
  e            open note (or create one)
  x            rename to suggested citekey
  c            convert PDF to Markdown
  d            move paper to .trash
  enter        open detail pane

Detail
  j k          scroll
  g G          top / bottom
  ctrl+d/u     page

Search
  attention transformer          implicit AND, BM25 ranked
  title:"attention is all"       phrase in a field
  author:vaswani venue:neurips   field filters
  year:2015..2020 added:2026-01..2026-11
  tag:ml/llm                     tag and descendants
  tag:ml/llm/                    exactly this level
  NOT tag:status/skim   -tag:...
  sort:year- sort:added- sort:title+ sort:relevance

Commands
  :tag add ml/llm       :tag rm status/skim
  :tag rename A B       :tag merge A B
  :meta set title=...   :refresh
  :rename old new       :rekey [--dry-run|--force]
  :reindex [--rebuild]  :bib export
  :note new md          :convert
  :query save NAME      :query run NAME
  :clear                :sort year-   :quit

Review
  j/k field   space toggle   a accept   tab candidate
  n/P paper   K keep         esc close`
	lines := strings.Split(help, "\n")
	visible := max(1, m.height-3)
	if m.logTop > len(lines)-visible {
		m.logTop = max(0, len(lines)-visible)
	}
	if m.logTop < 0 {
		m.logTop = 0
	}
	end := m.logTop + visible
	if end > len(lines) {
		end = len(lines)
	}
	body := make([]styledLine, 0, visible)
	for _, l := range lines[m.logTop:end] {
		st := lipgloss.Style{}
		if strings.HasPrefix(l, "Global") || strings.HasPrefix(l, "Tags") || strings.HasPrefix(l, "Papers") || strings.HasPrefix(l, "Detail") ||
			strings.HasPrefix(l, "Search") || strings.HasPrefix(l, "Commands") || strings.HasPrefix(l, "Review") {
			st = styleAccent
		} else if strings.HasPrefix(l, "  ") {
			st = styleDim
		}
		body = append(body, styledLine{text: " " + l, style: st})
	}
	return m.renderFull("HELP", "j/k scroll", body, 0)
}

func (m *Model) renderLogs() string {
	visible := max(1, m.height-3)
	if m.logTop > len(m.logLines)-visible {
		m.logTop = max(0, len(m.logLines)-visible)
	}
	if m.logTop < 0 {
		m.logTop = 0
	}
	end := m.logTop + visible
	if end > len(m.logLines) {
		end = len(m.logLines)
	}
	var body []styledLine
	for _, l := range m.logLines[m.logTop:end] {
		st := styleRow
		switch strings.ToLower(l.level) {
		case "error":
			st = styleRed
		case "warn", "warning":
			st = styleYellow
		case "info":
			st = styleDim
		}
		body = append(body, styledLine{text: " " + l.text, style: st})
	}
	if len(body) == 0 {
		body = append(body, styledLine{text: " no log entries", style: styleDim})
	}
	return m.renderFull("LOGS", "j/k scroll", body, 0)
}

func (m *Model) renderTasks() string {
	var body []styledLine
	for _, t := range m.tasks {
		state := "running"
		st := styleYellow
		if t.Done {
			state = "done"
			st = styleGreen
			if t.Err != "" {
				state = "error: " + t.Err
				st = styleRed
			}
		} else {
			state = "running " + time.Since(t.Started).Round(time.Second).String()
		}
		body = append(body, styledLine{
			text:  fmt.Sprintf(" %-22s %s  %s", t.Label, t.Started.Format("15:04:05"), state),
			style: st,
		})
	}
	if len(body) == 0 {
		body = append(body, styledLine{text: " no background tasks", style: styleDim})
	}
	return m.renderFull("TASKS", "", body, 0)
}

func (m *Model) renderFull(title, hint string, body []styledLine, top int) string {
	visible := max(1, m.height-2)
	if top < 0 {
		top = 0
	}
	if top > len(body)-visible {
		top = max(0, len(body)-visible)
	}
	if top > 0 && top < len(body) {
		body = body[top:]
	}
	lines := []styledLine{m.paneHeader(title, hint, m.width, true)}
	lines = append(lines, body...)
	out := make([]string, 0, m.height)
	for i := 0; i < m.height; i++ {
		text := ""
		st := lipgloss.Style{}
		if i < len(lines) {
			text, st = lines[i].text, lines[i].style
		}
		out = append(out, renderFixed(text, st, m.width))
	}
	out[m.height-1] = styleDim.Render(padPlain("  esc close", m.width))
	return strings.Join(out, "\n")
}

func (m *Model) renderReview() string {
	var body []styledLine
	p := m.currentReview()
	if p == nil {
		body = append(body, styledLine{text: " review queue empty", style: styleDim})
		return m.renderFull("REVIEW", "esc close", body, 0)
	}
	body = append(body, styledLine{text: fmt.Sprintf(" %d/%d  %s", m.reviewCursor+1, len(m.reviewPapers), p.Meta.Title), style: styleTitle})
	body = append(body, styledLine{text: " " + p.Meta.Citekey + "  ·  " + p.State.Review.Reason, style: styleDim})
	body = append(body, styledLine{})
	if len(p.State.Review.Candidates) > 0 {
		c := p.State.Review.Candidates[m.reviewCand]
		body = append(body, styledLine{text: fmt.Sprintf(" candidate %d/%d   %s   confidence %.2f", m.reviewCand+1, len(p.State.Review.Candidates), c.Source, c.Confidence), style: styleAccent})
		body = append(body, styledLine{})
		for i, f := range m.reviewFields {
			check := "[ ]"
			if m.reviewSel[i] {
				check = "[x]"
			}
			cur := truncateStr(fieldPreview(p.Meta, f), 26)
			cand := truncateStr(candidateValue(c.Fields[f]), 56)
			text := fmt.Sprintf(" %s %-12s %-27s → %s", check, f, cur, cand)
			st := styleRow
			if i == m.reviewFieldCursor {
				st = styleSelected
			} else if m.reviewSel[i] {
				st = styleDim
			}
			body = append(body, styledLine{text: text, style: st})
		}
	}
	return m.renderFull("REVIEW", "j/k field  space toggle  a accept  tab candidate  n/P paper  K keep", body, 0)
}

func (m *Model) renderConfirmPlain() string {
	var body []styledLine
	for _, ln := range wrap(m.confirm, max(1, m.width-2)) {
		body = append(body, styledLine{text: " " + styleRow.Render(ln)})
	}
	body = append(body, styledLine{})
	body = append(body, styledLine{text: " " + styleDim.Render("[y] yes    [n] no")})
	return m.renderFull("CONFIRM", "y/n", body, 0)
}

func (m *Model) renderConfirm() string {
	if m.width < 24 || m.height < 12 {
		return m.renderConfirmPlain()
	}
	boxW := m.width - 4
	if boxW > 86 {
		boxW = 86
	}
	innerW := max(8, boxW-6)
	var lines []string
	for _, ln := range wrap(m.confirm, innerW) {
		lines = append(lines, " "+truncateStr(ln, innerW))
	}
	content := styleDim.Render("confirm") + "\n\n" + strings.Join(lines, "\n") + "\n\n" + styleDim.Render("[y] yes    [n] no")
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colFaint).
		Padding(1, 2).
		MaxWidth(boxW).
		Render(content)
	if lipgloss.Width(box) > m.width || len(strings.Split(box, "\n")) > m.height {
		return m.renderConfirmPlain()
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func padPlain(s string, width int) string {
	s = truncateStr(s, width)
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func fitPlain(s string, width int) string {
	return padPlain(s, width)
}

func elideMiddle(s string, width int) string {
	r := []rune(s)
	if width <= 0 {
		return ""
	}
	if len(r) <= width {
		return s
	}
	if width < 6 {
		return string(r[:max(1, width-1)]) + "…"
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return string(r[:left]) + "…" + string(r[len(r)-right:])
}

func sortLabel(s string) string {
	switch s {
	case "year-":
		return "year↓"
	case "year+":
		return "year↑"
	case "added-":
		return "added↓"
	case "added+":
		return "added↑"
	case "title+":
		return "title↑"
	case "title-":
		return "title↓"
	case "updated-":
		return "updated↓"
	case "updated+":
		return "updated↑"
	default:
		return "rel"
	}
}

func tagStyle(tag string) lipgloss.Style {
	switch {
	case tag == "status/to-read":
		return styleYellow
	case tag == "status/reading":
		return styleCyan
	case tag == "status/read":
		return styleGreen
	case tag == "status/skim":
		return styleDim
	case tag == "status" || strings.HasPrefix(tag, "status/"):
		return styleYellow
	case tag == "method" || strings.HasPrefix(tag, "method/"):
		return styleAccent
	case tag == "application" || strings.HasPrefix(tag, "application/"):
		return styleCyan
	default:
		return styleRow
	}
}

func statusBadge(tags []string) (string, lipgloss.Style) {
	best := ""
	rank := map[string]int{"read": 4, "reading": 3, "to-read": 2, "skim": 1}
	for _, t := range tags {
		if !strings.HasPrefix(t, "status/") {
			continue
		}
		s := strings.TrimPrefix(t, "status/")
		r := rank[s]
		if r == 0 && s != "" {
			r = 1 // unknown status still gets a visible marker
		}
		if r > rank[best] {
			best = s
		}
	}
	switch best {
	case "to-read":
		return "●", styleYellow
	case "reading":
		return "◐", styleCyan
	case "read":
		return "✓", styleGreen
	case "skim":
		return "◦", styleDim
	case "":
		return " ", styleFaint
	default:
		return "•", styleDim
	}
}

// tagFieldLines renders the detail pane's tag list with per-namespace colors.
func (m *Model) tagFieldLines(tags []string, labelW, width int) []styledLine {
	prefix := " " + styleDim.Render(padPlain("tags", labelW)) + " "
	indent := strings.Repeat(" ", labelW+2)
	var out []styledLine
	line := prefix
	lineW := labelW + 2
	for _, t := range tags {
		tok := tagStyle(t).Render("#" + t)
		tw := lipgloss.Width(tok)
		if lineW > labelW+2 && lineW+2+tw > width {
			out = append(out, styledLine{text: line})
			line = indent
			lineW = labelW + 2
		}
		if lineW > labelW+2 {
			line += "  "
			lineW += 2
		}
		line += tok
		lineW += tw
	}
	out = append(out, styledLine{text: line})
	return out
}

func wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var out []string
	push := func(line *string) {
		if *line != "" {
			out = append(out, *line)
			*line = ""
		}
	}
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, f := range strings.Fields(para) {
			for lipgloss.Width(f) > width {
				push(&line)
				chunk, rest := splitWidth(f, width)
				out = append(out, chunk)
				f = rest
			}
			switch {
			case line == "":
				line = f
			case lipgloss.Width(line)+1+lipgloss.Width(f) > width:
				out = append(out, line)
				line = f
			default:
				line += " " + f
			}
		}
		push(&line)
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// splitWidth cuts s into the longest prefix that fits width columns.
func splitWidth(s string, width int) (string, string) {
	r := []rune(s)
	w := 0
	for i, ch := range r {
		cw := lipgloss.Width(string(ch))
		if i > 0 && w+cw > width {
			return string(r[:i]), string(r[i:])
		}
		w += cw
	}
	return s, ""
}

func truncateStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return ansi.Truncate(s, n, "…")
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fieldPreview(m model.Meta, f string) string {
	switch f {
	case "title":
		return m.Title
	case "year":
		if m.Year == nil {
			return ""
		}
		return fmt.Sprint(*m.Year)
	case "doi":
		return m.DOI
	case "arxiv_id":
		return m.ArxivID
	case "url":
		return m.URL
	case "venue":
		if m.Venue == nil {
			return ""
		}
		return m.Venue.Name
	case "authors":
		var out []string
		for _, a := range m.Authors {
			out = append(out, a.Family)
		}
		return strings.Join(out, ", ")
	case "abstract":
		return m.Abstract
	}
	return ""
}

func candidateValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []interface{}:
		var parts []string
		for _, item := range x {
			parts = append(parts, candidateValue(item))
		}
		return strings.Join(parts, ", ")
	case map[string]interface{}:
		var parts []string
		for _, k := range []string{"family", "given", "name", "short", "kind", "year", "volume", "pages"} {
			if val, ok := x[k]; ok && val != nil && fmt.Sprint(val) != "" {
				parts = append(parts, k+"="+fmt.Sprint(val))
			}
		}
		if len(parts) == 0 {
			return fmt.Sprint(x)
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(x)
	}
}

// markdownExcerpt reads the first converted Markdown file for the selected
// paper so the detail pane prefers it over the abstract (FR-9.5).
func (m *Model) markdownExcerpt() string {
	if m.detail == nil {
		return ""
	}
	for _, a := range m.attachments {
		if a.Kind != "markdown" {
			continue
		}
		low := strings.ToLower(a.Rel)
		if !strings.HasSuffix(low, ".md") && !strings.HasSuffix(low, ".markdown") {
			continue
		}
		f, err := os.Open(filepath.Join(m.detail.Dir, "attachments", a.Rel))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, 256*1024))
		f.Close()
		if err != nil {
			continue
		}
		return string(data)
	}
	return ""
}
