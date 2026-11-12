package tui

import tea "github.com/charmbracelet/bubbletea"

// narrowWidth is the smallest terminal width that still fits three panes.
const narrowWidth = 72

// bodyTop returns the screen row where the pane body starts.
func (m *Model) bodyTop() int {
	top := 1 // header
	if m.filterVisible() {
		top++ // filter bar
	}
	if m.height >= 5 {
		top++ // blank separator
	}
	return top
}

// bodyHeight returns the number of rows available to the panes.
func (m *Model) bodyHeight() int {
	h := m.height - m.bodyTop() - 1 // footer
	if h < 1 {
		h = 1
	}
	return h
}

// paneAt maps a screen column to a pane. Narrow and zoomed layouts have a
// single visible pane, so the focused pane wins.
func (m *Model) paneAt(x int) focus {
	if m.zoom || m.width < narrowWidth {
		return m.focus
	}
	left, mid, _ := m.layoutWidths()
	switch {
	case x < left:
		return focusTags
	case x < left+mid:
		return focusList
	default:
		return focusDetail
	}
}

// tagScrollTop mirrors the visible window used by renderTags.
func (m *Model) tagScrollTop(visible int) int {
	if m.tagCursor >= visible {
		return m.tagCursor - visible + 1
	}
	return 0
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m, nil
	}
	wheelUp := msg.Button == tea.MouseButtonWheelUp
	wheelDown := msg.Button == tea.MouseButtonWheelDown
	if msg.Action != tea.MouseActionPress && !wheelUp && !wheelDown {
		return m, nil
	}
	top := m.bodyTop()
	bodyH := m.bodyHeight()
	if msg.Y < top || msg.Y >= top+bodyH {
		return m, nil
	}
	f := m.paneAt(msg.X)
	m.focus = f

	if wheelUp || wheelDown {
		delta := 3
		if wheelUp {
			delta = -3
		}
		m.move(delta)
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	row := msg.Y - top - 1 // pane header occupies the first body row
	if row < 0 {
		return m, nil
	}
	switch f {
	case focusTags:
		idx := m.tagScrollTop(bodyH-1) + row
		if idx >= 0 && idx < len(m.tagItems) && !m.tagItems[idx].section {
			m.tagCursor = idx
		}
	case focusList:
		idx := m.listTop + row
		if idx >= 0 && idx < len(m.hits) {
			m.cursor = idx
			m.loadDetail()
		}
	}
	return m, nil
}
