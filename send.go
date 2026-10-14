package main

import (
	"path"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The popup has two sides, a tab each: this server's folders and those of
// the machine you're at, browsed over ssh. tab switches; each keeps its own
// folder. ^s copies what's selected on one side into the folder shown on
// the other. A drop always lands on this server's side.

// remoteSide is your machine's side: the folder shown there.
type remoteSide struct {
	dir     string // as asked for ("~/Downloads"), then where it is
	home    string // there; "" until it's been listed
	entries []entry
	loading bool
	err     string
}

type remoteListedMsg struct {
	asked string
	l     remoteListing
	err   error
}

type remoteMadeMsg struct {
	dir string
	err error
}

// you is the machine you're at, as found: the one the other side shows.
func (m model) you() (sender, bool) {
	if len(m.senders) == 0 {
		return sender{}, false
	}
	return m.senders[0], true
}

// remoteReady says whether your machine's folders can be shown: the way
// back to it works.
func (m model) remoteReady() (sender, bool) {
	s, ok := m.you()
	if !ok || s.Mobile {
		return s, false
	}
	r := m.probes[s.Host]
	return s, r != nil && !r.running && r.err == nil
}

// remoteDirShown is the folder there a copy goes to: the one shown, or
// where the side opens before it's been looked at.
func (m model) remoteDirShown() string {
	if m.remote.dir == "" {
		return "~/Downloads"
	}
	return m.remote.dir
}

// switchSide flips between this server's folders and yours. Your side is
// listed the first time it's shown.
func (m model) switchSide() (tea.Model, tea.Cmd) {
	if _, ok := m.you(); !ok {
		return m, nil
	}
	// A search ends with the side it was on; the cursor that was on its
	// results goes back to the top.
	if m.filter.Value() != "" {
		m.filter.SetValue("")
		m.cursor, m.offset = 0, 0
	}
	m.onRemote = !m.onRemote
	m.err, m.flash = "", ""
	// Each side keeps where its cursor was.
	m.cursor, m.otherCursor = m.otherCursor, m.cursor
	m.offset, m.otherOffset = m.otherOffset, m.offset
	if m.onRemote && m.remote.home == "" {
		m.pendingSelect = firstEntry
		if _, ok := m.remoteReady(); ok && !m.remote.loading {
			return m, m.remoteList(m.remoteDirShown())
		}
	}
	m.clamp()
	return m, nil
}
func (m *model) remoteList(dir string) tea.Cmd {
	s, ok := m.remoteReady()
	if !ok {
		return nil
	}
	m.remote.loading, m.remote.err = true, ""
	ctx, target, hidden := m.ctx, s.target(m.cfg, ""), m.cfg.ShowHidden
	return func() tea.Msg {
		l, err := listRemote(ctx, target, dir, hidden)
		return remoteListedMsg{asked: dir, l: l, err: err}
	}
}

func (m model) remoteListed(msg remoteListedMsg) (tea.Model, tea.Cmd) {
	r := &m.remote
	r.loading = false
	if msg.err != nil {
		r.err = msg.err.Error()
		// ~/Downloads that isn't there, or a Mac keeping it from ssh: home
		// instead, once, with the reason said.
		if msg.asked != "~" && r.home == "" {
			r.dir = "~"
			return m, m.remoteList("~")
		}
		return m, nil
	}
	first := r.home == ""
	r.dir, r.home, r.entries = msg.l.dir, msg.l.home, msg.l.entries
	if !m.onRemote {
		// Listed while the other side has the keys (a pane, or the first
		// look): its cursor on the first entry.
		if first {
			m.otherCursor, m.otherOffset = firstIndex(m.remoteRowsQ("")), 0
		}
		return m, nil
	}
	m.clamp()
	return m, nil
}

func (m *model) remoteEnter(dir string) tea.Cmd {
	m.clearMarks(true)
	m.pendingSelect = m.remote.dir
	m.remote.entries = nil
	m.cursor, m.offset = 0, 0
	m.setFilter("")
	return m.remoteList(dir)
}

// remoteMkdir makes a folder there, in the one shown, and goes in.
func (m model) remoteMkdir(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
		return m, nil
	}
	s, ok := m.remoteReady()
	if !ok {
		return m, nil
	}
	ctx, target, dir := m.ctx, s.target(m.cfg, ""), m.remoteDirShown()
	m.remote.loading = true
	return m, func() tea.Msg {
		made, err := mkdirRemote(ctx, target, dir, name)
		return remoteMadeMsg{dir: made, err: err}
	}
}

// copyAcross copies what's selected into the folder shown on the other
// side: from here to your machine, or from it to here.
func (m model) copyAcross() (tea.Model, tea.Cmd) {
	items := m.chosen()
	if s, ok := m.you(); ok && s.Mobile {
		return m.taildropAcross(s, items)
	}
	s, ready := m.remoteReady()
	if len(items) == 0 || !ready {
		return m, nil
	}
	m.err, m.flash = "", ""
	target := s.target(m.cfg, "")
	var cmds []tea.Cmd
	if !m.anyRunning() {
		cmds = append(cmds, tick())
	}
	for _, e := range items {
		m.nextID++
		j := &job{id: m.nextID, src: e.path, name: path.Base(e.path), from: s.Name, target: target, start: time.Now()}
		if m.onRemote {
			// From there to here: what a drop does.
			j.dest = m.dir
		} else {
			j.dest, j.out, j.home = m.remoteDirShown(), true, m.remote.home
		}
		m.jobs = append(m.jobs, j)
		cmds = append(cmds, m.runJob(j))
	}
	m.clearMarks(m.onRemote)
	return m, tea.Batch(cmds...)
}

// taildropAcross sends what's selected here to a phone or tablet, with
// Taildrop: into its Tailscale folder, there being no other to choose.
func (m model) taildropAcross(s sender, items []entry) (tea.Model, tea.Cmd) {
	if len(items) == 0 || m.onRemote {
		return m, nil
	}
	m.err, m.flash = "", ""
	var cmds []tea.Cmd
	if !m.anyRunning() {
		cmds = append(cmds, tick())
	}
	for _, e := range items {
		m.nextID++
		j := &job{id: m.nextID, src: e.path, name: path.Base(e.path), from: s.Name, target: s.Host,
			dest: taildropFolder, out: true, taildrop: true, start: time.Now()}
		m.jobs = append(m.jobs, j)
		cmds = append(cmds, m.runJob(j))
	}
	m.clearMarks(false)
	return m, tea.Batch(cmds...)
}

// runJob copies a job's file or folder, either way, reporting as it goes.
func (m model) runJob(j *job) tea.Cmd {
	id, ctx, ch, target := j.id, m.ctx, m.progress, j.target
	var last time.Time
	report := func(n int64) {
		j.bytes.Store(n)
		if time.Since(last) > 100*time.Millisecond {
			last = time.Now()
			select {
			case ch <- progressMsg{id}:
			default:
			}
		}
	}
	total := func(n int64) { j.total.Store(n) }
	return func() tea.Msg {
		var got string
		var err error
		switch {
		case j.taildrop:
			got, err = j.dest, taildropSend(ctx, target, j.src, total, report)
		case j.out:
			got, err = sendTo(ctx, target, j.src, j.dest, total, report)
		default:
			got, err = fetch(ctx, target, j.src, j.dest, total, report)
		}
		return jobDoneMsg{id: id, dest: got, err: err}
	}
}

func (m model) anyRunningExcept(not *job) bool {
	for _, j := range m.jobs {
		if j != not && j.state == jobRunning {
			return true
		}
	}
	return false
}

// remoteRows are the folder there, with what's on its way into it at the
// top.
func (m model) remoteRowsQ(q string) []entry {
	r := m.remote
	if q != "" {
		return rank(r.entries, q)
	}
	var out []entry
	if r.home != "" && r.dir != "/" {
		out = append(out, entry{name: "..", path: path.Dir(r.dir), dir: true, up: true})
	}
	for _, j := range m.jobs {
		if j.state == jobRunning && j.out && j.dest == r.dir {
			out = append(out, entry{name: j.name, job: j})
		}
	}
	return append(out, r.entries...)
}

// sideTabs are the two sides as tabs, the one shown as a pill: this
// server, and your machine with how the way back to it is.
func (m model) sideTabs() string {
	here := serverName()
	cell := func(on bool, text, mark string) string {
		if on {
			return pill(look.Accent, text+mark)
		}
		return " " + styleText.Render(text) + mark + " "
	}
	out := " " + cell(!m.onRemote, here, "")
	s, ok := m.you()
	switch {
	case m.finding:
		out += " " + m.spin.View() + styleDim.Render(" finding your machine")
	case !ok:
	default:
		mark := m.youMark(m.onRemote)
		out += " " + cell(m.onRemote, s.Name, mark)
	}
	return out
}

// viewSides is the header: the tabs, then where the side shown is.
func (m model) viewSides() string {
	left := m.sideTabs() + "  "
	where := tildify(m.dir)
	if s, _ := m.you(); m.onRemote && s.Mobile {
		where = "" // no folders: its panel says where Taildrop puts things
	} else if m.onRemote {
		where = m.remoteDirShown()
		if m.remote.home != "" {
			where = remoteTilde(m.remote.dir, m.remote.home)
		}
	}
	right := ""
	if m.onRemote && m.remote.loading {
		right = m.spin.View() + " "
	}
	room := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	line := left + styleTree.Bold(true).Render(shortenLeft(where, max(8, room)))
	if pad := m.width - lipgloss.Width(line) - lipgloss.Width(right); pad > 0 && right != "" {
		line += strings.Repeat(" ", pad) + right
	}
	return ansi.Truncate(line, m.width, "")
}

// otherSide is where ^s copies to, as it's shown: "macbook-pro:~/Downloads".
func (m model) otherSide() string {
	if m.onRemote {
		return serverName() + ":" + tildify(m.dir)
	}
	s, _ := m.you()
	where := m.remoteDirShown()
	if m.remote.home != "" {
		where = remoteTilde(m.remote.dir, m.remote.home)
	}
	return s.Name + ":" + where
}

func (m model) remoteStatus(w int) string {
	if m.remote.err != "" {
		return " " + styleErr.Render(shorten(clean(m.remote.err, false), w)) + "\n"
	}
	return styleDim.Render(shorten(" ^s sends what's selected to "+m.otherSide()+".", w)) + "\n"
}

// handleRemote is a key on your machine's side.
func (m model) handleRemote(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.remote
	switch k.String() {
	case "enter", "right":
		if e, ok := m.selected(); ok && (e.dir || e.up) {
			return m, m.remoteEnter(e.path)
		}
		if k.String() == "enter" && m.filter.Value() != "" && len(m.rows()) == 0 {
			return m.remoteMkdir(m.filter.Value())
		}
		return m, nil
	case "left", "backspace":
		if m.filter.Value() == "" {
			if r.dir != "/" && r.home != "" {
				return m, m.remoteEnter(path.Dir(r.dir))
			}
			return m, nil
		}
	case "ctrl+g":
		return m, m.remoteEnter("~/Downloads")
	case "ctrl+d", "ctrl+e":
		// Not on your machine: deleting there can't be taken back from
		// here, and its paths mean nothing to an agent on this one.
		return m, nil
	}
	return m.handleCommon(k)
}
