package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The popup is a folder, its contents listed: the folder it shows is where
// a drop goes. Typing searches it and the folders under it; enter goes into
// one. Dropping files on the popup pastes their paths, which are fetched
// from the sender (the machine you're attached from) over ssh.

// entry is one row of the list: something in the folder shown, or with a
// search, a folder anywhere under it.
type entry struct {
	name string // as shown: its name, or its path from the folder shown
	path string
	dir  bool
	size int64
	up   bool // the ".." row
	job  *job // a copy on its way here: drawn with its progress
}

type jobState int

const (
	jobRunning jobState = iota
	jobDone
	jobFailed
)

// job is one dropped file or folder on its way.
type job struct {
	id     int
	src    string
	dest   string // the folder it's going to, then where it landed
	from   string // the sender's name, or "" for this machine
	target string // the ssh destination, or "" for this machine
	state  jobState
	bytes  atomic.Int64 // written by the copy, read by the view
	total  atomic.Int64 // its size, once the sending side has said
	name   string       // what it'll be called here
	err    string
	start  time.Time
	took   time.Duration
	out    bool   // sent to the other machine, not brought here
	home   string // there, for showing where it went as ~/…
}

type (
	listedMsg struct {
		dir     string
		entries []entry
		err     error
	}
	searchedMsg struct {
		dir   string
		query string
		dirs  []entry
	}
	sendersMsg []sender
	probedMsg  struct {
		host string
		err  error
	}
	progressMsg struct{ id int }
	jobDoneMsg  struct {
		id   int
		dest string
		err  error
	}
	tickMsg struct{}
)

type model struct {
	ctx     context.Context
	cfg     config
	start   string // the folder the popup opened on: ctrl+g goes back
	dir     string
	entries []entry
	tree    []entry // the folders under dir, for search; nil until searched
	treeFor string

	filter  textinput.Model
	spin    spinner.Model
	cursor  int
	offset  int
	width   int
	height  int
	mouseX  int
	mouseY  int
	err     string
	flash   string
	leaving bool // esc pressed once with uploads running
	// naming is the new folder's name being typed (^n), in place of the
	// search; nil the rest of the time.
	naming *textinput.Model
	// confirm is what ^d is about to move to the trash, until y or esc.
	confirm *confirmDelete
	// onRemote: your machine's side is shown, not this server's (tab).
	onRemote bool
	remote   remoteSide
	// otherCursor and otherOffset are the side not shown's (or, with
	// panes, the one keys don't go to): tab swaps them in.
	otherCursor, otherOffset int
	// marks are what space has marked, per side (0 here, 1 yours).
	marks [2]map[string]entry
	// agentPane is the pane the popup was opened from: ^e types a path
	// into it.
	agentPane string
	// demo: a made-up Mac, its way back not set up, never ssh'd to.
	demo bool

	senders []sender
	// probes is what checking each sender found, by host: absent until
	// asked, running while it's asked.
	probes map[string]*probeResult
	// taildrop is files being received with Taildrop (^t, for a phone or
	// tablet); nil when not.
	taildrop *taildropState
	finding  bool
	jobs     []*job
	nextID   int
	progress chan progressMsg
	landed   map[string]bool // what arrived in this session: drawn as new
	// pendingSelect is the folder to put the cursor on once the listing
	// arrives: the one just left, going up.
	pendingSelect string
}

func newModel(ctx context.Context, cfg config, dir string) model {
	ti := textinput.New()
	ti.Prompt = " › "
	ti.Placeholder = "Type to filter… or drop a file to upload"
	ti.PromptStyle, ti.PlaceholderStyle = styleDim, styleDim
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: []string{"◔", "◕"}, FPS: time.Second / 3}
	sp.Style = styleWorking
	return model{
		ctx: ctx, cfg: cfg, start: dir, dir: dir, filter: ti, spin: sp,
		finding: true, progress: make(chan progressMsg, 64), landed: map[string]bool{},
		probes:        map[string]*probeResult{},
		marks:         [2]map[string]entry{{}, {}},
		pendingSelect: firstEntry,
		mouseX:        -1, mouseY: -1,
	}
}

func (m model) Init() tea.Cmd {
	ctx, cfg, demo := m.ctx, m.cfg, m.demo
	return tea.Batch(
		m.list(m.dir),
		func() tea.Msg {
			if demo {
				return sendersMsg{{Host: "macbook", Name: "macbook", Device: "Mac"}}
			}
			return sendersMsg(findSenders(ctx, cfg))
		},
		m.spin.Tick,
		m.waitProgress(),
	)
}

// ── commands ─────────────────────────────────────────────────────────────────

func (m model) list(dir string) tea.Cmd {
	hidden := m.cfg.ShowHidden
	return func() tea.Msg {
		des, err := os.ReadDir(dir)
		if err != nil {
			return listedMsg{dir: dir, err: err}
		}
		var out []entry
		for _, de := range des {
			name := de.Name()
			if strings.HasPrefix(name, ".herdr-file-transfer-") || (!hidden && strings.HasPrefix(name, ".")) {
				continue
			}
			e := entry{name: name, path: filepath.Join(dir, name)}
			info, err := os.Stat(e.path) // follows a link to what it's a link to
			if err == nil {
				e.dir, e.size = info.IsDir(), info.Size()
			}
			out = append(out, e)
		}
		sortEntries(out)
		return listedMsg{dir: dir, entries: out}
	}
}

func (m model) search(dir, query string) tea.Cmd {
	depth, hidden := m.cfg.SearchDepth, m.cfg.ShowHidden
	return func() tea.Msg {
		return searchedMsg{dir: dir, query: query, dirs: walkDirs(dir, depth, hidden)}
	}
}

func (m model) waitProgress() tea.Cmd {
	ch := m.progress
	return func() tea.Msg { return <-ch }
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// drop starts fetching each dropped path into the folder shown.
func (m *model) drop(paths []string) tea.Cmd {
	m.err, m.flash, m.leaving = "", "", false
	var s *sender
	if len(m.senders) > 0 {
		s = &m.senders[0]
	}
	var cmds []tea.Cmd
	if !m.anyRunning() {
		cmds = append(cmds, tick())
	}
	for _, src := range paths {
		m.nextID++
		j := &job{id: m.nextID, src: src, dest: m.dir, start: time.Now()}
		target := ""
		if s != nil && !fromHerdr(src) && !(s.Mobile && existsHere(src)) {
			target, j.from = s.target(m.cfg, src), s.Name
			j.target = target
		}
		j.name = filepath.Base(strings.TrimRight(src, "/"))
		if target == "" && fromHerdr(src) {
			j.name = herdrImageName(src)
		}
		m.jobs = append(m.jobs, j)
		id, dest, ctx, ch := j.id, m.dir, m.ctx, m.progress
		var last time.Time
		report := func(n int64) {
			j.bytes.Store(n)
			// Redraw at most every 100ms; never block the copy on the popup.
			if time.Since(last) > 100*time.Millisecond {
				last = time.Now()
				select {
				case ch <- progressMsg{id}:
				default:
				}
			}
		}
		cmds = append(cmds, func() tea.Msg {
			got, err := fetch(ctx, target, src, dest, func(n int64) { j.total.Store(n) }, report)
			return jobDoneMsg{id: id, dest: got, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (m model) anyRunning() bool {
	for _, j := range m.jobs {
		if j.state == jobRunning {
			return true
		}
	}
	return false
}

// ── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = max(10, m.width-6)
		m.clamp()
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case listedMsg:
		if msg.dir != m.dir {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		m.entries = msg.entries
		m.clamp()
		return m, nil
	case searchedMsg:
		if msg.dir == m.dir {
			m.tree, m.treeFor = msg.dirs, msg.dir
			m.clamp()
		}
		return m, nil
	case sendersMsg:
		m.senders, m.finding = msg, false
		return m, m.probeSender()
	case remoteListedMsg:
		return m.remoteListed(msg)
	case remoteMadeMsg:
		if msg.err != nil {
			m.remote.loading, m.remote.err = false, msg.err.Error()
			return m, nil
		}
		return m, m.remoteEnter(msg.dir)
	case deletedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.flash = msg.done
			delete(m.landed, msg.path)
		}
		return m, m.list(m.dir)
	case actionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.flash = msg.done
		}
		return m, m.recheck()
	case taildropMsg:
		return m.received(msg)
	case probedMsg:
		r := m.probes[msg.host]
		if r == nil {
			return m, nil
		}
		was := r.err
		r.running, r.err = false, msg.err
		if msg.err != nil {
			// Keep checking while the panel says what to do: each step
			// done ticks itself.
			host := msg.host
			return m, tea.Tick(recheckEvery, func(time.Time) tea.Msg { return recheckMsg{host} })
		}
		var load tea.Cmd
		if m.remote.home == "" && !m.remote.loading {
			load = m.remoteList(m.remoteDirShown())
		}
		if was != nil {
			m.flash = msg.host + " is ready: drop files on this window"
			for _, s := range m.senders {
				if s.Host == msg.host {
					m.flash = s.Name + " is ready: drop files on this window"
				}
			}
		}
		return m, load
	case recheckMsg:
		if s, r, ok := m.setupShown(); ok && s.Host == msg.host && !r.running {
			return m, m.recheck()
		}
		return m, nil
	case progressMsg:
		return m, m.waitProgress()
	case tickMsg:
		if m.anyRunning() {
			return m, tick()
		}
		return m, nil
	case jobDoneMsg:
		return m.finish(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) finish(msg jobDoneMsg) (tea.Model, tea.Cmd) {
	for _, j := range m.jobs {
		if j.id != msg.id {
			continue
		}
		j.took = time.Since(j.start)
		if msg.err != nil {
			j.state, j.err = jobFailed, msg.err.Error()
			if errors.Is(msg.err, context.Canceled) {
				j.err = "cancelled"
			}
			continue
		}
		j.state, j.dest = jobDone, msg.dest
		if !j.out {
			m.landed[msg.dest] = true
			// On what just came: ^e hands it to the agent from there.
			if !m.onRemote && filepath.Dir(msg.dest) == m.dir {
				m.pendingSelect = msg.dest
			}
		}
		if j.target != "" {
			// It got in: as whom, for next time, and the check passes.
			rememberUser(j.target)
			m.probes[hostOf(j.target)] = &probeResult{}
		}
	}
	var cmd tea.Cmd
	if !m.anyRunning() {
		m.leaving = false
		cmd = m.list(m.dir) // each row above says how its copy went
	}
	return m, cmd
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A drop is a paste of paths. Some terminals send it bracketed, some
	// as a burst of typed characters, which arrives as one message.
	if k.Type == tea.KeyRunes && (k.Paste || len(k.Runes) > 1) {
		if paths := droppedPaths(string(k.Runes)); paths != nil {
			return m, m.drop(paths)
		}
	}
	m.flash = ""
	if m.naming != nil {
		return m.handleNaming(k)
	}
	if m.confirm != nil {
		return m.handleConfirm(k)
	}
	if m.onRemote {
		return m.handleRemote(k)
	}
	switch k.String() {
	case "enter", "right":
		if e, ok := m.selected(); ok && (e.dir || e.up) {
			return m, m.enter(e.path)
		}
		if k.String() == "enter" && m.filter.Value() != "" && len(m.rows()) == 0 {
			return m.mkdir(m.filter.Value())
		}
		return m, nil
	case "left", "backspace":
		if m.filter.Value() == "" {
			return m, m.enter(filepath.Dir(m.dir))
		}
	case "ctrl+g":
		return m, m.enter(m.start)
	case "ctrl+d":
		if items := m.chosen(); len(items) > 0 {
			c := &confirmDelete{items: items, trash: true, files: -1}
			for _, e := range items {
				c.trash = c.trash && canTrash(e.path)
			}
			if len(items) == 1 && items[0].dir {
				c.files = countFiles(items[0].path, 10000)
			}
			m.confirm = c
		}
		return m, nil
	}
	return m.handleCommon(k)
}

// handleCommon is a key that does the same on either side.
func (m model) handleCommon(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		switch {
		case m.filter.Value() != "":
			m.setFilter("")
		case m.anyRunning() && !m.leaving:
			m.leaving = true
		default:
			return m, tea.Quit
		}
		return m, nil
	case "up", "ctrl+p":
		m.move(-1)
		return m, nil
	case "down":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-max(1, m.listHeight()-1))
		return m, nil
	case "pgdown":
		m.move(max(1, m.listHeight()-1))
		return m, nil
	case "tab", "shift+tab":
		return m.switchSide()
	case "ctrl+s":
		return m.copyAcross()
	case "ctrl+n":
		return m.startNaming()
	case "ctrl+k", "ctrl+a", "ctrl+y", "ctrl+t":
		if _, _, panel := m.setupShown(); panel {
			if cmd, ok := m.setupKey(k.String()); ok {
				return m, cmd
			}
			return m, nil
		}
		switch k.String() {
		case "ctrl+y":
			return m.copyPath()
		case "ctrl+a":
			return m.markAll()
		}
		return m, nil
	case " ":
		if m.filter.Value() == "" {
			return m.toggleMark()
		}
	case "ctrl+e":
		return m.toAgent()
	case "ctrl+r":
		// Check again now: after setting up ssh, without reopening.
		return m, m.recheck()
	}
	before := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	if m.filter.Value() != before {
		if m.onRemote {
			m.cursor, m.offset = 0, 0
			return m, cmd
		}
		return m, tea.Batch(cmd, m.filterChanged())
	}
	return m, cmd
}

// startNaming asks for a new folder's name, starting from what's searched
// for: search, find nothing, ^n, enter.
func (m model) startNaming() (tea.Model, tea.Cmd) {
	ti := textinput.New()
	ti.Prompt = " + "
	ti.Placeholder = "New folder's name… (a/b makes both)"
	ti.PromptStyle, ti.PlaceholderStyle = styleOK, styleDim
	ti.Width = m.filter.Width
	ti.SetValue(strings.TrimSpace(m.filter.Value()))
	ti.CursorEnd()
	m.naming = &ti
	return m, ti.Focus()
}

func (m model) handleNaming(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.naming = nil
		return m, nil
	case "enter":
		name := m.naming.Value()
		m.naming = nil
		return m.mkdir(name)
	}
	ti, cmd := m.naming.Update(k)
	m.naming = &ti
	return m, cmd
}

// mkdir makes a folder (and any between: "a/b") in the folder shown, and
// goes in.
func (m model) mkdir(name string) (tea.Model, tea.Cmd) {
	if m.onRemote {
		return m.remoteMkdir(name)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) || filepath.IsAbs(name) {
		return m, nil
	}
	p := filepath.Join(m.dir, filepath.FromSlash(name))
	if rel, err := filepath.Rel(m.dir, p); err != nil || strings.HasPrefix(rel, "..") {
		m.err = "a new folder goes inside this one"
		return m, nil
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.flash = "made " + tildify(p)
	return m, m.enter(p)
}

func (m *model) setFilter(v string) {
	m.filter.SetValue(v)
	m.filter.CursorEnd()
	m.cursor, m.offset = 0, 0
}

func (m *model) filterChanged() tea.Cmd {
	m.cursor, m.offset = 0, 0
	if m.filter.Value() != "" && m.treeFor != m.dir {
		m.treeFor = m.dir // asked for: don't ask again on the next key
		return m.search(m.dir, m.filter.Value())
	}
	return nil
}

func (m *model) enter(dir string) tea.Cmd {
	if dir == m.dir {
		m.setFilter("")
		return nil
	}
	prev := m.dir
	m.clearMarks(false)
	m.dir, m.entries, m.tree, m.treeFor, m.err = dir, nil, nil, "", ""
	m.setFilter("")
	// Back up a level: the folder just left is selected.
	m.pendingSelect = prev
	return m.list(dir)
}

// ── the list ─────────────────────────────────────────────────────────────────

// rows is what's listed: with no search, the folder's contents under a
// ".." row; with one, its matches here, then the folders under it that match.
func (m model) rows() []entry {
	q := strings.TrimSpace(m.filter.Value())
	if m.onRemote {
		return m.remoteRowsQ(q)
	}
	return m.localRowsQ(q)
}

// localRowsQ are this server's rows, searched for q.
func (m model) localRowsQ(q string) []entry {
	if q == "" {
		var out []entry
		if m.dir != "/" {
			out = append(out, entry{name: "..", path: filepath.Dir(m.dir), dir: true, up: true})
		}
		// What's on its way here, at the top, as it comes.
		for _, j := range m.jobs {
			if j.state == jobRunning && j.dest == m.dir && !j.out {
				out = append(out, entry{name: j.name, job: j})
			}
		}
		return append(out, m.entries...)
	}
	here := rank(m.entries, q)
	seen := map[string]bool{}
	for _, e := range here {
		seen[e.path] = true
	}
	var deeper []entry
	for _, e := range rank(m.tree, q) {
		if !seen[e.path] {
			deeper = append(deeper, e)
		}
	}
	return append(here, deeper...)
}

func (m model) selected() (entry, bool) {
	rs := m.rows()
	if m.cursor < 0 || m.cursor >= len(rs) {
		return entry{}, false
	}
	return rs[m.cursor], true
}

func (m *model) move(d int) {
	m.cursor += d
	m.clamp()
}

// clamp keeps the cursor on a row and in view.
func (m *model) clamp() {
	rs := m.rows()
	// Once the listing's in (more than ".."): the folder just left, coming
	// back up, or else the first thing in it, not "..".
	if m.pendingSelect != "" && (len(rs) > 1 || (len(rs) == 1 && !rs[0].up)) {
		m.cursor = 0
		if rs[0].up {
			m.cursor = 1
		}
		for i, e := range rs {
			if e.path == m.pendingSelect && !e.up {
				m.cursor = i
			}
		}
		m.pendingSelect = ""
	}
	m.cursor = max(0, min(m.cursor, len(rs)-1))
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if h > 0 && m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(rs)-h)))
}

// jobLines is how many transfers are shown under the list.
func (m model) jobLines() int {
	n := len(m.shownJobs())
	if n == 0 {
		return 0
	}
	return min(n, max(1, m.height/4)) + 1 // and a blank above
}

// shownJobs are the copies listed under the folder: done or failed, and
// those on their way to another folder (one coming here is in the list).
func (m model) shownJobs() []*job {
	var out []*job
	for _, j := range m.jobs {
		// Running into a folder that's on screen: its row there says it.
		here := !j.out && j.dest == m.dir && (m.panes() || !m.onRemote)
		there := j.out && j.dest == m.remote.dir && (m.panes() || m.onRemote)
		if j.state != jobRunning || !(here || there) {
			out = append(out, j)
		}
	}
	return out
}

// listHeight: the header (2), the filter and a blank (2), the transfers, the
// status line and the footer's rows.
func (m model) listHeight() int {
	return max(1, m.height-5-len(m.footerRows())-m.jobLines())
}

// footerRows are the keys in one row, or when that's too wide, two: what to
// do (green, orange, red) above, getting around (blue) and leaving below. A
// row still too wide says less, as one row always did.
func (m model) footerRows() [][]hint {
	hs := m.footer()
	if hintsWidth(hs) <= m.width {
		return [][]hint{hs}
	}
	var do, go_ []hint
	for _, h := range hs {
		switch h.kind {
		case hintView, hintQuiet:
			go_ = append(go_, h)
		default:
			do = append(do, h)
		}
	}
	if len(do) == 0 || len(go_) == 0 {
		return [][]hint{hs}
	}
	return [][]hint{do, go_}
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	return screenSafe(m.view())
}

func (m model) view() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewHeader() + "\n\n")
	if m.naming != nil {
		b.WriteString(m.naming.View() + "\n\n")
	} else {
		b.WriteString(m.filter.View() + "\n\n")
	}

	for _, l := range m.viewBody(m.listHeight()) {
		b.WriteString(l + "\n")
	}

	if n := m.jobLines(); n > 0 {
		b.WriteString("\n")
		shown := m.shownJobs()
		for _, j := range shown[len(shown)-(n-1):] {
			b.WriteString(m.viewJob(j) + "\n")
		}
	}
	b.WriteString(m.statusLine())
	b.WriteString(m.renderFooter())
	return b.String()
}

// viewHeader: the two machines as tabs, and the folder shown.
func (m model) viewHeader() string {
	if m.panes() {
		return m.viewPanesHeader()
	}
	return m.viewSides()
}

func (m model) viewEntry(e entry, sel bool, w int, marked bool) string {
	var mark, name, meta string
	switch {
	case e.job != nil:
		mark, name = m.spin.View(), styleLead.Render(e.name)
		meta = progressBar(e.job.bytes.Load(), e.job.total.Load(), min(20, max(6, w/5)))
	case e.up:
		mark, name = styleDim.Render("↑"), styleDim.Render("..")
	case e.dir:
		mark, name = styleTree.Render("▸"), styleTitle.UnsetBold().Render(e.name+"/")
	default:
		mark, name = styleDim.Render("·"), styleText.Render(e.name)
		if e.size >= 0 {
			meta = styleDim.Render(humanBytes(e.size))
		}
	}
	if m.landed[e.path] {
		meta = styleOK.Render("new") + "  " + meta
	}
	dot := " "
	if marked {
		dot = styleTree.Render("●")
		name = styleTree.Render(ansi.Strip(name))
	}
	line := " " + dot + " " + mark + " " + name
	if room := w - lipgloss.Width(line) - lipgloss.Width(meta) - 2; room < 0 {
		line = ansi.Truncate(line, max(4, w-lipgloss.Width(meta)-3), "…")
	}
	if meta != "" {
		if pad := w - lipgloss.Width(line) - lipgloss.Width(meta) - 1; pad > 0 {
			line += strings.Repeat(" ", pad) + meta
		}
	}
	if sel {
		return highlight(line, w)
	}
	return line
}

func (m model) viewJob(j *job) string {
	name := j.name
	if name == "" || j.state == jobDone {
		name = filepath.Base(j.dest) // what it's called now: an image herdr brought is renamed
	}
	var mark, tail string
	switch j.state {
	case jobRunning:
		mark = m.spin.View()
		tail = progressBar(j.bytes.Load(), j.total.Load(), 16) + styleDim.Render(" → "+m.jobWhere(j, j.dest))
	case jobDone:
		mark = styleOK.Render("✓")
		tail = styleDim.Render(fmt.Sprintf("%s in %s → ", humanBytes(j.bytes.Load()), j.took.Round(100*time.Millisecond))) +
			styleTree.Render(m.jobWhere(j, j.dest))
	case jobFailed:
		mark = styleErr.Render("✗")
		tail = styleErr.Render(j.err)
	}
	line := "  " + mark + " " + styleLead.Render(name) + "  " + tail
	return ansi.Truncate(line, m.width-1, "…")
}

// statusLine is the one line above the footer.
func (m model) statusLine() string {
	w := max(10, m.width-4)
	switch {
	case m.confirm != nil:
		return " " + styleWarn.Render(shorten(m.confirm.question(), w)) + "\n"
	case len(m.marks[side(m.onRemote)]) > 0:
		line, _ := m.markedStatus(w)
		return line
	case m.onRemote:
		if _, _, panel := m.setupShown(); !panel {
			return m.remoteStatus(w)
		}
	case m.leaving:
		return " " + styleWarn.Render("Uploads are still running: esc again stops them.") + "\n"
	case m.err != "":
		return " " + styleErr.Render(shorten(clean(m.err, false), w)) + "\n"
	case m.flash != "":
		return " " + styleOK.Render("✓ ") + shorten(clean(m.flash, false), w) + "\n"
	case !m.finding && len(m.senders) == 0 && os.Getenv("SSH_CONNECTION") == "":
		return styleDim.Render(" Dropped files are copied from this machine.") + "\n"
	case !m.finding && len(m.senders) == 0:
		return styleWarn.Render(shorten(` No ssh client found to fetch from: set "sender" in config.json.`, w)) + "\n"
	case len(m.senders) > 0:
		s := m.senders[0]
		switch r := m.probes[s.Host]; {
		case r != nil && r.running:
			return " " + m.spin.View() + styleDim.Render(shorten(" Checking ssh to "+s.Name+"…", w)) + "\n"
		case s.Mobile:
			return styleDim.Render(shorten(" Typing searches folders: what Taildrop brings lands in the one shown.", w)) + "\n"
		case r != nil && r.err != nil:
			if _, _, panel := m.setupShown(); !panel {
				return " " + styleErr.Render(shorten(m.probeAdvice(s, r.err), w)) + "\n"
			}
			return styleDim.Render(shorten(" Fix the connection back, or browse: typing searches folders.", w)) + "\n"
		}
	}
	if _, ok := m.remoteReady(); ok {
		return styleDim.Render(shorten(" Drop files here to copy them in. ^s sends what's selected to "+m.otherSide()+".", w)) + "\n"
	}
	return styleDim.Render(shorten(" Drop files or folders on this window to copy them into "+tildify(m.dir)+".", w)) + "\n"
}

func (m model) footer() []hint {
	if m.naming != nil {
		return []hint{{"enter create", "enter", hintAct}, {"esc cancel", "esc", hintQuiet}}
	}
	if m.confirm != nil {
		yes := "y move to trash"
		if !m.confirm.trash {
			yes = "y delete"
		}
		return []hint{{yes, "y", hintRisk}, {"esc cancel", "esc", hintQuiet}}
	}
	if s, r, ok := m.setupShown(); ok {
		return m.setupFooter(s, r)
	}
	return m.sideFooter()
}

func (m model) renderFooter() string {
	rows := m.footerRows()
	top := m.height - len(rows)
	lines := make([]string, len(rows))
	for i, hs := range rows {
		hot := ""
		if m.mouseY == top+i {
			hot = hintAt(hs, m.mouseX, m.width)
		}
		lines[i] = footerLine(hs, hot, m.width)
	}
	return strings.Join(lines, "\n")
}

// ── mouse ────────────────────────────────────────────────────────────────────

// listTop is the first list line: under the header, a blank, the filter and
// another blank.
const listTop = 4

func (m model) handleMouse(ev tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.mouseX, m.mouseY = ev.X, ev.Y
	switch {
	case ev.Button == tea.MouseButtonWheelUp && ev.Action == tea.MouseActionPress:
		m.move(-3)
	case ev.Button == tea.MouseButtonWheelDown && ev.Action == tea.MouseActionPress:
		m.move(3)
	case ev.Action == tea.MouseActionMotion:
		if m.paneAt(ev.X) != m.onRemote {
			return m, nil
		}
		if i, ok := m.rowAt(ev.Y); ok {
			m.cursor = i
		}
	case ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft:
		rows := m.footerRows()
		if top := m.height - len(rows); ev.Y >= top {
			if k := hintAt(rows[ev.Y-top], ev.X, m.width); k != "" {
				return m.handleKey(keyMsg(k))
			}
			return m, nil
		}
		if m.paneAt(ev.X) != m.onRemote && ev.Y >= listTop {
			next, cmd := m.switchSide()
			return next, cmd
		}
		if i, ok := m.rowAt(ev.Y); ok {
			m.cursor = i
			if e, _ := m.selected(); e.dir || e.up {
				if m.onRemote {
					return m, m.remoteEnter(e.path)
				}
				return m, m.enter(e.path)
			}
		}
	}
	return m, nil
}

func (m model) rowAt(y int) (int, bool) {
	i := m.offset + y - listTop
	if y < listTop || y >= listTop+m.listHeight() || i >= len(m.rows()) {
		return 0, false
	}
	return i, true
}

// ── run ──────────────────────────────────────────────────────────────────────

func runPicker(ctx context.Context, cfg config, dir string) error {
	// Ask the terminal for its background now: once the program owns stdin,
	// the reply would arrive as stray input.
	if cfg.Theme != "dark" && cfg.Theme != "light" {
		cfg.Theme = "light"
		if lipgloss.HasDarkBackground() {
			cfg.Theme = "dark"
		}
	}
	darkTerminal = cfg.Theme == "dark"
	useTheme(pickerTheme(darkTerminal))
	// WithContext: a SIGTERM (herdr closing the popup) ends it cleanly,
	// restoring the terminal, and stops any copy under way.
	m := newModel(ctx, cfg, dir)
	m.agentPane = os.Getenv(paneEnv)
	m.demo = os.Getenv(demoEnv) != ""
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithContext(ctx)).Run()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// probeResult is what checking a sender found.
type probeResult struct {
	running bool
	err     error
}

// probeSender checks the sender shown, once: a drop needn't be the first
// to find out ssh won't work.
func (m *model) probeSender() tea.Cmd {
	if len(m.senders) == 0 {
		return nil
	}
	s := m.senders[0]
	if m.probes[s.Host] != nil {
		return nil
	}
	if s.Mobile {
		m.probes[s.Host] = &probeResult{err: errMobile}
		return nil
	}
	m.probes[s.Host] = &probeResult{running: true}
	return m.probeCmd(s)
}

// probeAdvice says what the check found, and when it had to guess who to
// log in as, says so: the first drop from /Users/<you> tells it.
func (m model) probeAdvice(s sender, err error) string {
	msg := "Can't fetch from " + s.Name + ": " + err.Error()
	if !s.Fixed && m.cfg.User == "" && s.User == "" && strings.Contains(err.Error(), "refused") {
		msg += ". Tried as " + localUser() + `: if that's not you there, set "user" in config.json`
	}
	return msg
}

func localUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "you"
}

func existsHere(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// progressBar is a copy's progress: a bar and how far, or until the size is
// known, how much has come.
func progressBar(done, total int64, w int) string {
	if total <= 0 {
		return styleDim.Render(humanBytes(done))
	}
	frac := min(1, float64(done)/float64(total))
	filled := int(frac * float64(w))
	bar := styleTree.Render(strings.Repeat("━", filled))
	if filled < w {
		bar += styleDim.Render("╺" + strings.Repeat("━", w-filled-1))
	}
	return bar + " " + styleLead.Render(fmt.Sprintf("%3.0f%%", frac*100)) +
		styleDim.Render(fmt.Sprintf("  %s of %s", humanBytes(min(done, total)), humanBytes(total)))
}

// confirmDelete is a ^d waiting for its answer.
type confirmDelete struct {
	items []entry
	trash bool // to the trash; false: on another disk, so deleted for good
	files int  // in a lone folder, up to 10000; -1 otherwise
}

func (c confirmDelete) question() string {
	what := plural(len(c.items), "item")
	if len(c.items) == 1 {
		e := c.items[0]
		what = e.name
		if e.dir {
			what += "/"
			switch {
			case c.files >= 10000:
				what += " (10000+ files)"
			case c.files >= 0:
				what += " (" + plural(c.files, "file") + ")"
			}
		}
	}
	if c.trash {
		return "Move " + what + " to the trash? y or esc"
	}
	return "Delete " + what + " for good? Not all are on the trash's disk. y or esc"
}

type deletedMsg struct {
	path, done string
	err        error
}

// deletable: something in the list that's there, not ".." or a copy on its
// way.
func deletable(e entry) bool {
	return !e.up && e.job == nil && e.path != ""
}

func (m model) handleConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y", "enter":
		c := *m.confirm
		m.confirm = nil
		m.clearMarks(false)
		return m, func() tea.Msg {
			what := plural(len(c.items), "item")
			if len(c.items) == 1 {
				what = c.items[0].name
			}
			for _, e := range c.items {
				if err := toTrash(e.path, c.trash); err != nil {
					return deletedMsg{err: err}
				}
			}
			if c.trash {
				return deletedMsg{path: c.items[0].path, done: "Moved " + what + " to the trash"}
			}
			return deletedMsg{path: c.items[0].path, done: "Deleted " + what}
		}
	case "n", "N", "esc":
		m.confirm = nil
	}
	return m, nil
}

// recheckEvery is how often the setup panel checks the way back again.
const recheckEvery = 4 * time.Second

type recheckMsg struct{ host string }

// recheck checks the sender shown again, keeping what was found until
// there's news: the panel stays put meanwhile.
func (m *model) recheck() tea.Cmd {
	if len(m.senders) == 0 {
		return nil
	}
	s := m.senders[0]
	r := m.probes[s.Host]
	if s.Mobile || (r != nil && r.running) {
		return nil
	}
	if r == nil {
		return m.probeSender()
	}
	r.running = true
	return m.probeCmd(s)
}

// jobWhere is where a copy goes: a folder here, or there for one sent,
// named by its machine ("macbook-pro:~/Downloads").
func (m model) jobWhere(j *job, p string) string {
	if j.out {
		return j.from + ":" + remoteTilde(p, j.home)
	}
	return tildify(p)
}

// firstEntry, as pendingSelect, puts the cursor on a folder's first entry.
const firstEntry = "\x00first"

// copyPath puts the selected item's path on your clipboard: your
// terminal's, through herdr (OSC 52), whichever machine it's on.
func (m model) copyPath() (tea.Model, tea.Cmd) {
	items := m.chosen()
	if len(items) == 0 {
		return m, nil
	}
	text := items[0].path
	if len(items) > 1 {
		text = quotedPaths(items)
	}
	os.Stdout.WriteString(ansi.SetSystemClipboard(text))
	m.flash = "Copied " + text
	if len(items) > 1 {
		m.flash = "Copied " + plural(len(items), "path")
	}
	m.clearMarks(m.onRemote)
	return m, nil
}

// quotedPaths are paths as a shell (or an agent) reads them, one after the
// other.
func quotedPaths(items []entry) string {
	qs := make([]string, len(items))
	for i, e := range items {
		qs[i] = shellQuote(e.path)
	}
	return strings.Join(qs, " ")
}

// toAgent types the selected item's path into the agent the popup was
// opened from, without sending it, and closes the popup: the prompt is
// yours to finish.
func (m model) toAgent() (tea.Model, tea.Cmd) {
	items := m.chosen()
	if len(items) == 0 || m.agentPane == "" || m.onRemote {
		return m, nil
	}
	if err := herdrCall("pane.send_text", map[string]any{"pane_id": m.agentPane, "text": quotedPaths(items) + " "}, nil); err != nil {
		m.err = "Couldn't reach the agent: " + err.Error()
		return m, nil
	}
	return m, tea.Quit
}

// sendLabel is ^s's pill: how many when several, and with tabs, where to
// (with panes, the arrow over the divider says).
func (m model) sendLabel(n int, to string) string {
	label := "^s send"
	if n > 1 {
		label += " " + itoa(n)
	}
	if !m.panes() {
		label += " to " + shorten(to, 16)
	}
	return label
}

func itoa(n int) string { return strconv.Itoa(n) }

// paneAt says which side column x is on: with panes, the right is yours.
func (m model) paneAt(x int) bool {
	if !m.panes() {
		return m.onRemote
	}
	lw, _ := m.paneWidths()
	return x > lw
}

// sideFooter is the keys, the same on both sides and always in the same
// order, grouped by what they do: what to do with the chosen (green), make
// (orange), delete (red), the view (blue), leave (grey). A key that doesn't
// apply here or now stays in its place, greyed, so nothing moves under you.
func (m model) sideFooter() []hint {
	items := m.chosen()
	have := len(items) > 0
	on := func(h hint, ok bool) hint {
		if !ok {
			h.key = ""
		}
		return h
	}
	var hs []hint
	s, found := m.you()
	if found {
		to := s.Name
		if m.onRemote {
			to = serverName()
		}
		_, ready := m.remoteReady()
		hs = append(hs, on(hint{m.sendLabel(len(items), to), "ctrl+s", hintGo}, have && ready))
	}
	if m.agentPane != "" {
		hs = append(hs, on(hint{"^e agent", "ctrl+e", hintGo}, have && !m.onRemote))
	}
	hs = append(hs,
		hint{"^n folder", "ctrl+n", hintAct},
		on(hint{"^d delete", "ctrl+d", hintRisk}, have && !m.onRemote),
	)
	// The view's keys, the most needed first: a short popup drops them
	// from the right.
	if found {
		other := s.Name
		if m.onRemote {
			other = serverName()
		}
		hs = append(hs, hint{"tab " + other, "tab", hintView})
	}
	hs = append(hs,
		on(hint{"space mark", " ", hintView}, m.filter.Value() == "" && m.selectable()),
		on(hint{"^y copy path", "ctrl+y", hintView}, have),
		hint{"← up", "left", hintView},
	)
	if m.onRemote {
		hs = append(hs, hint{"^g Downloads", "ctrl+g", hintView})
	} else {
		hs = append(hs, hint{"^g agent folder", "ctrl+g", hintView})
	}
	esc := "esc close"
	if m.filter.Value() != "" {
		esc = "esc clear"
	}
	return append(hs, hint{esc, "esc", hintQuiet})
}

// selectable says whether the cursor is on something to mark: not "..",
// nor a copy still arriving.
func (m model) selectable() bool {
	e, ok := m.selected()
	return ok && deletable(e)
}

// markedStatus is the status line while things are marked: how many, and
// what to do with them.
func (m model) markedStatus(w int) (string, bool) {
	n := len(m.marks[side(m.onRemote)])
	if n == 0 {
		return "", false
	}
	line := " " + itoa(n) + " marked:"
	if _, ok := m.remoteReady(); ok {
		line += " ^s sends them to " + m.otherSide() + " ·"
	}
	line += " space unmarks · ^a marks all"
	return styleTree.Render(shorten(line, w)) + "\n", true
}

// probeCmd checks the way back to s; in the demo, it's never set up.
func (m model) probeCmd(s sender) tea.Cmd {
	if m.demo {
		return func() tea.Msg {
			time.Sleep(400 * time.Millisecond)
			return probedMsg{host: s.Host, err: &setupError{step: stepAuth, msg: "demo"}}
		}
	}
	ctx, target := m.ctx, s.target(m.cfg, "")
	return func() tea.Msg { return probedMsg{host: s.Host, err: probe(ctx, target)} }
}
