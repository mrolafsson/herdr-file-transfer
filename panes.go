package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Wide enough, the two machines are side by side, a pane each: this
// server's on the left, yours on the right. tab moves between them, and ^s
// sends what's selected in one into the folder shown in the other. Narrower,
// they're tabs, one at a time, with the same keys.

// panesMinWidth is the narrowest popup that has room for two panes.
const panesMinWidth = 110

// panes says whether the machines are side by side: when there's room, and
// there's something to do on both. While the way back to yours doesn't
// work, what to do about it has the whole popup. A phone or tablet has no
// folders to show: its pane is how to send with Taildrop, beside the folder
// it lands in.
func (m model) panes() bool {
	s, ok := m.you()
	if !ok || m.width < panesMinWidth {
		return false
	}
	if s.Mobile {
		return true
	}
	r := m.probes[s.Host]
	return r == nil || r.err == nil
}

// paneWidths are the left and right panes' widths, a divider between.
func (m model) paneWidths() (int, int) {
	l := (m.width - 1) / 2
	return l, m.width - 1 - l
}

// side is an index for what's kept per side: 0 here, 1 your machine.
func side(remote bool) int {
	if remote {
		return 1
	}
	return 0
}

// rowsFor are a side's rows: the side shown with its search, the other as
// it is.
func (m model) rowsFor(remote bool) []entry {
	if remote == m.onRemote {
		return m.rows()
	}
	if remote {
		return m.remoteRowsQ("")
	}
	return m.localRowsQ("")
}

// panelFor is the setup panel a side shows in place of its folder, if any:
// your machine's side while the way back to it doesn't work, and with tabs,
// this server's too (dropping needs it). A phone or tablet's side is always
// its panel, and this server's never: Taildrop needs a folder to land in.
func (m model) panelFor(remote bool) (sender, *probeResult, bool) {
	if len(m.senders) == 0 {
		return sender{}, nil, false
	}
	if s := m.senders[0]; s.Mobile {
		return s, m.probes[s.Host], remote
	}
	if m.panes() && !remote {
		return sender{}, nil, false
	}
	if remote == m.onRemote && (m.naming != nil || m.filter.Value() != "") && !(m.panes() && remote) {
		return sender{}, nil, false
	}
	s := m.senders[0]
	r := m.probes[s.Host]
	// While it checks again, the panel stays: no flicker to the list.
	if r == nil || r.err == nil {
		return sender{}, nil, false
	}
	return s, r, true
}

// firstIndex is where a freshly listed folder's cursor goes: its first
// entry, not "..".
func firstIndex(rs []entry) int {
	if len(rs) > 1 && rs[0].up {
		return 1
	}
	return 0
}

// paneLines draws one side h lines tall and w wide: its folder, or its
// setup panel.
func (m model) paneLines(remote bool, w, h int) []string {
	var out []string
	if s, r, ok := m.panelFor(remote); ok {
		var lines []string
		for _, l := range m.viewSetup(s, r) {
			lines = append(lines, strings.Split(ansi.Wordwrap(l, max(10, w-4), ""), "\n")...)
		}
		block := 0
		for _, l := range lines {
			block = max(block, lipgloss.Width(l))
		}
		left := strings.Repeat(" ", max(2, (w-block)/2))
		for top := max(0, (h-len(lines))/2); len(out) < top; {
			out = append(out, "")
		}
		for _, l := range lines {
			out = append(out, left+l)
		}
		return fitLines(out, w, h)
	}

	rs := m.rowsFor(remote)
	active := remote == m.onRemote
	cursor, offset := m.cursor, m.offset
	if !active {
		cursor, offset = m.otherCursor, m.otherOffset
		cursor = max(0, min(cursor, len(rs)-1))
		offset = max(0, min(offset, cursor))
		if cursor >= offset+h {
			offset = cursor - h + 1
		}
	}
	marks := m.marks[side(remote)]
	for i := offset; i < len(rs) && len(out) < h; i++ {
		_, marked := marks[rs[i].path]
		out = append(out, m.viewEntry(rs[i], active && i == cursor, w, marked))
	}
	if len(rs) == 0 {
		q := ""
		if active {
			q = m.filter.Value()
		}
		switch {
		case q != "" && !remote && m.treeFor == m.dir && m.tree == nil:
			out = append(out, "   "+m.spin.View()+" Searching…")
		case q != "" && !remote:
			out = append(out, styleDim.Render("   Nothing matches. enter makes a folder called ")+
				styleTree.Render(shorten(q, max(10, w-50))))
		case q != "":
			out = append(out, styleDim.Render("   Nothing matches."))
		case remote && m.remote.loading:
			out = append(out, "   "+m.spin.View()+styleDim.Render(" Listing your machine…"))
		case remote && m.remote.err != "":
			out = append(out, "   "+styleErr.Render(clean(m.remote.err, false)))
		case remote:
			out = append(out, styleDim.Render("   Empty."))
		default:
			out = append(out, styleDim.Render("   Empty. Drop files here to copy them in."))
		}
	}
	return fitLines(out, w, h)
}

// fitLines makes lines exactly h of them, each w wide.
func fitLines(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := range out {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "…")
		}
		out[i] = l + strings.Repeat(" ", max(0, w-lipgloss.Width(l)))
	}
	return out
}

// viewBody is the folders: two panes side by side, or the side shown.
func (m model) viewBody(h int) []string {
	if !m.panes() {
		return m.paneLines(m.onRemote, m.width, h)
	}
	lw, rw := m.paneWidths()
	left, right := m.paneLines(false, lw, h), m.paneLines(true, rw, h)
	bar := styleDim.Render("│")
	out := make([]string, h)
	for i := range out {
		out[i] = left[i] + bar + right[i]
	}
	return out
}

// paneTitle is a side's name, a pill when it's the one keys go to, and the
// folder it shows.
func (m model) paneTitle(remote bool, w int) string {
	name, mark := serverName(), ""
	where := tildify(m.dir)
	if remote {
		s, _ := m.you()
		name, mark = s.Name, m.youMark(remote == m.onRemote)
		where = m.remoteDirShown()
		if m.remote.home != "" {
			where = remoteTilde(m.remote.dir, m.remote.home)
		}
		if s.Mobile {
			where = ""
		}
	}
	cell := " " + styleText.Render(name) + mark + " "
	if remote == m.onRemote {
		cell = pill(look.Accent, name+mark)
	}
	line := cell + " "
	room := w - lipgloss.Width(line) - 1
	if where != "" && room > 4 {
		line += styleTree.Bold(remote == m.onRemote).Render(shortenLeft(where, room))
	}
	return line
}

// youMark is how the way back to your machine is, after its name: on the
// pill, in the pill's own ink.
func (m model) youMark(onPill bool) string {
	s, _ := m.you()
	r := m.probes[s.Host]
	switch {
	case m.finding || r == nil || r.running:
		return " " + m.spin.View()
	case s.Mobile && m.taildrop != nil && m.taildrop.err == "":
		return " " + m.spin.View() // waiting for Taildrop
	case r.err != nil && onPill:
		return " ✗"
	case r.err != nil:
		return " " + styleErr.Render("✗")
	case onPill:
		return " ✓"
	}
	return " " + styleOK.Render("✓")
}

// viewPanesHeader is each pane's title over it, and over the divider an
// arrow: the way ^s sends, from the pane the keys go to.
func (m model) viewPanesHeader() string {
	lw, rw := m.paneWidths()
	arrow := "→"
	if s, _ := m.you(); s.Mobile && m.onRemote {
		arrow = " " // nothing's sent from a phone or tablet's pane
	} else if m.onRemote {
		arrow = "←"
	}
	left := " " + m.paneTitle(false, lw-1)
	return fitLines([]string{left}, lw, 1)[0] + styleTree.Bold(true).Render(arrow) + fitLines([]string{" " + m.paneTitle(true, rw-1)}, rw, 1)[0]
}

// ── marks ────────────────────────────────────────────────────────────────────

// toggleMark marks or unmarks what's selected, and moves on.
func (m model) toggleMark() (tea.Model, tea.Cmd) {
	e, ok := m.selected()
	if !ok || !deletable(e) {
		return m, nil
	}
	marks := m.marks[side(m.onRemote)]
	if _, on := marks[e.path]; on {
		delete(marks, e.path)
	} else {
		marks[e.path] = e
	}
	m.move(1)
	return m, nil
}

// markAll marks everything in the folder shown, or with all of it marked,
// none of it.
func (m model) markAll() (tea.Model, tea.Cmd) {
	marks := m.marks[side(m.onRemote)]
	var all []entry
	for _, e := range m.rows() {
		if deletable(e) {
			all = append(all, e)
		}
	}
	every := len(all) > 0
	for _, e := range all {
		if _, on := marks[e.path]; !on {
			every = false
		}
	}
	for _, e := range all {
		if every {
			delete(marks, e.path)
		} else {
			marks[e.path] = e
		}
	}
	return m, nil
}

// chosen is what a key acts on: what's marked on the side shown, or else
// what's selected.
func (m model) chosen() []entry {
	marks := m.marks[side(m.onRemote)]
	if len(marks) > 0 {
		var out []entry
		for _, e := range m.rows() {
			if _, on := marks[e.path]; on {
				out = append(out, e)
			}
		}
		// Marked, then searched away from: still chosen.
		if len(out) < len(marks) {
			seen := map[string]bool{}
			for _, e := range out {
				seen[e.path] = true
			}
			for p, e := range marks {
				if !seen[p] {
					out = append(out, e)
				}
			}
		}
		return out
	}
	if e, ok := m.selected(); ok && deletable(e) {
		return []entry{e}
	}
	return nil
}

func (m *model) clearMarks(remote bool) {
	m.marks[side(remote)] = map[string]entry{}
}
