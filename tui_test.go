package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// drive runs a message through the model and any command it returns, one
// level deep: enough for a listing or a copy to come back.
func drive(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(model)
	for _, c := range flatten(cmd) {
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case out := <-done:
			switch out.(type) {
			case listedMsg, searchedMsg, jobDoneMsg, sendersMsg:
				next, _ = m.Update(out)
				m = next.(model)
			}
		case <-time.After(300 * time.Millisecond):
			// A tick or the progress channel: not waited on.
		}
	}
	return m
}

func flatten(cmd tea.Cmd) []tea.Cmd {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Cmd
			for _, c := range b {
				out = append(out, flatten(c)...)
			}
			return out
		}
		return []tea.Cmd{func() tea.Msg { return msg }}
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func newTestModel(t *testing.T, dir string) model {
	m := newModel(context.Background(), withDefaults(config{}), dir)
	m.finding = false
	m = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	next, _ := m.Update(m.list(dir)())
	return next.(model)
}

func typeText(t *testing.T, m model, s string) model {
	for _, r := range s {
		m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestBrowseAndSearch(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src/components/ui", "docs", "assets/icons", "node_modules/ui"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644)
	m := newTestModel(t, root)

	var names []string
	for _, e := range m.rows() {
		names = append(names, e.name)
	}
	if got := strings.Join(names, ","); got != "..,assets,docs,node_modules,src,README.md" {
		t.Errorf("rows %s", got)
	}

	// Search finds a folder deep down, and skips node_modules.
	m = typeText(t, m, "ui")
	rows := m.rows()
	if len(rows) != 1 || rows[0].name != filepath.Join("src", "components", "ui") {
		t.Fatalf("search rows %+v", rows)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dir != filepath.Join(root, "src", "components", "ui") || m.filter.Value() != "" {
		t.Errorf("entered %s, filter %q", m.dir, m.filter.Value())
	}

	// Up a level: the folder just left is selected.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if e, _ := m.selected(); m.dir != filepath.Join(root, "src", "components") || e.name != "ui" {
		t.Errorf("up to %s, on %q", m.dir, e.name)
	}
	// Back to where it opened.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.dir != root {
		t.Errorf("ctrl+g to %s", m.dir)
	}
}

func TestNewFolder(t *testing.T) {
	root := t.TempDir()
	m := newTestModel(t, root)
	m = typeText(t, m, "uploads")
	if len(m.rows()) != 0 {
		t.Fatal("matched something")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dir != filepath.Join(root, "uploads") {
		t.Errorf("in %s", m.dir)
	}
}

func TestDropCopiesIntoShownFolder(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "shot 1.png"), []byte("png"), 0o644)
	os.Mkdir(filepath.Join(root, "assets"), 0o755)
	m := newTestModel(t, root)
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyDown}) // past ".."
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	paste := strings.ReplaceAll(filepath.Join(src, "shot 1.png"), " ", `\ `)
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})
	if len(m.jobs) != 1 {
		t.Fatalf("%d jobs", len(m.jobs))
	}
	deadline := time.Now().Add(3 * time.Second)
	for m.jobs[0].state == jobRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	j := m.jobs[0]
	if j.state != jobDone {
		t.Fatalf("job %+v", j)
	}
	if _, err := os.Stat(filepath.Join(root, "assets", "shot 1.png")); err != nil {
		t.Error(err)
	}
	if m.filter.Value() != "" {
		t.Errorf("the drop went into the search: %q", m.filter.Value())
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"shot 1.png", "✓"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestTypedPasteGoesToSearch(t *testing.T) {
	m := newTestModel(t, t.TempDir())
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("assets"), Paste: true})
	if m.filter.Value() != "assets" || len(m.jobs) != 0 {
		t.Errorf("filter %q, jobs %d", m.filter.Value(), len(m.jobs))
	}
}

func TestViewFitsNarrow(t *testing.T) {
	m := newTestModel(t, t.TempDir())
	m.senders = []sender{{Host: "100.1.2.3", Name: "a-very-long-machine-name"}, {Host: "x", Name: "y"}}
	for _, w := range []int{30, 60, 120} {
		m = drive(t, m, tea.WindowSizeMsg{Width: w, Height: 20})
		for i, line := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(line) > w {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, ansi.StringWidth(line), ansi.Strip(line))
			}
		}
	}
}

func TestNewFolderButton(t *testing.T) {
	root := t.TempDir()
	m := newTestModel(t, root)
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.naming == nil {
		t.Fatal("^n didn't ask for a name")
	}
	if !strings.Contains(ansi.Strip(m.View()), "enter create") {
		t.Error("footer doesn't offer create")
	}
	m = typeText(t, m, "shots/today")
	if m.filter.Value() != "" {
		t.Errorf("the name went into the search: %q", m.filter.Value())
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.naming != nil || m.dir != filepath.Join(root, "shots", "today") {
		t.Errorf("in %s, naming %v", m.dir, m.naming != nil)
	}

	// esc leaves it, making nothing; a name that climbs out is refused.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.naming != nil {
		t.Error("esc didn't cancel")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	m = typeText(t, m, "../../escape")
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(root, "escape")); err == nil {
		t.Error("made a folder outside the one shown")
	}
}

func TestDropHerdrImage(t *testing.T) {
	tmp, root := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := herdrImageDir()
	os.MkdirAll(dir, 0o700)
	src := filepath.Join(dir, "client-146-clipboard-1791910811413384621-0.png")
	os.WriteFile(src, []byte("png"), 0o600)
	when := time.Date(2026, 10, 13, 19, 0, 34, 0, time.Local)
	os.Chtimes(src, when, when)

	m := newTestModel(t, root)
	m.senders = []sender{{Host: "nowhere.invalid", Name: "macbook"}}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(src + " "), Paste: true})
	deadline := time.Now().Add(3 * time.Second)
	for m.jobs[0].state == jobRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	j := m.jobs[0]
	if j.state != jobDone || j.target != "" {
		t.Fatalf("job %+v", j)
	}
	if filepath.Base(j.dest) != "image-2026-10-13-190034.png" {
		t.Errorf("named %s", filepath.Base(j.dest))
	}
	if !strings.Contains(ansi.Strip(m.View()), "image-2026-10-13-190034.png") {
		t.Error("the row doesn't show the new name")
	}
}

func TestProgressRows(t *testing.T) {
	root := t.TempDir()
	m := newTestModel(t, root)
	j := &job{id: 1, src: "/Users/sam/Movies/walkthrough.mov", name: "walkthrough.mov", dest: root, state: jobRunning}
	j.bytes.Store(48 << 20)
	j.total.Store(96 << 20)
	m.jobs = []*job{j}
	rows := m.rows()
	if len(rows) < 2 || rows[1].job != j {
		t.Fatalf("the copy isn't in its folder's list: %+v", rows)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"walkthrough.mov", "━", "50%", "48.0 MB of 96.0 MB"} {
		if !strings.Contains(view, want) {
			t.Errorf("lacks %q:\n%s", want, view)
		}
	}
	// Elsewhere, it's under the list instead, with where it's going.
	j.dest = filepath.Join(root, "other")
	if rows := m.rows(); len(rows) > 1 && rows[1].job != nil {
		t.Error("listed in a folder it isn't going to")
	}
	if !strings.Contains(ansi.Strip(m.View()), "→ ") {
		t.Error("no row under the list")
	}
}

func TestDeleteToTrash(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("HOME", data) // a Mac's trash is ~/.Trash
	os.WriteFile(filepath.Join(root, "old.png"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "drafts", "a"), 0o755)
	os.WriteFile(filepath.Join(root, "drafts", "a", "1.txt"), nil, 0o644)
	m := newTestModel(t, root)
	if e, _ := m.selected(); e.name != "drafts" {
		t.Fatalf("opens on %q, not the first entry", e.name)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.confirm == nil || !strings.Contains(m.confirm.question(), "drafts/ (1 file)") {
		t.Fatalf("confirm %+v", m.confirm)
	}
	// Anything but yes leaves it.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm != nil {
		t.Fatal("esc didn't cancel")
	}
	if _, err := os.Stat(filepath.Join(root, "drafts")); err != nil {
		t.Fatal("cancel deleted it")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyDown}) // old.png
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if !strings.Contains(ansi.Strip(m.View()), "Move old.png to the trash?") {
		t.Fatalf("no question:\n%s", ansi.Strip(m.View()))
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if _, err := os.Stat(filepath.Join(root, "old.png")); !os.IsNotExist(err) {
		t.Fatal("still there")
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(filepath.Join(data, ".Trash", "old.png")); err != nil {
			t.Error("not in the trash:", err)
		}
	} else {
		if _, err := os.Stat(filepath.Join(data, "Trash", "files", "old.png")); err != nil {
			t.Error("not in the trash:", err)
		}
		info, _ := os.ReadFile(filepath.Join(data, "Trash", "info", "old.png.trashinfo"))
		if !strings.Contains(string(info), "Path="+filepath.Join(root, "old.png")) {
			t.Errorf("trashinfo %q", info)
		}
	}
	next, _ := m.Update(m.list(root)())
	m = next.(model)
	for _, e := range m.rows() {
		if e.name == "old.png" {
			t.Error("still listed")
		}
	}
	// ".." can't be.
	m.cursor = 0
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.confirm != nil {
		t.Error("offered to delete ..")
	}
}

func TestSetupPanel(t *testing.T) {
	// On an iPad: this server's folders, and beside them, sorry, and Taildrop.
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Invoices"), 0o755)
	m := newTestModel(t, dir)
	m.senders = []sender{{Host: "ipad-pro", Name: "ipad-pro", Device: "iPad", Mobile: true}}
	m.probes["ipad-pro"] = &probeResult{err: errMobile}
	m = drive(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
	view := ansi.Strip(m.View())
	for _, want := range []string{"Invoices/", "Sorry, dropping won't work from your iPad", "then Tailscale", serverName() + " is waiting for files", "They land in the folder on the left."} {
		if !strings.Contains(view, want) {
			t.Errorf("iPad panes lack %q:\n%s", want, view)
		}
	}
	// The folders take the keys: into one, and Taildrop lands there.
	m.cursor = 1
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dir != filepath.Join(dir, "Invoices") {
		t.Errorf("enter didn't open Invoices: %s", m.dir)
	}
	// Waiting for Taildrop, a new folder moves the wait there (the command
	// isn't run: it would be the real tailscale).
	stopped := false
	m.taildrop = &taildropState{cancel: func() { stopped = true }}
	if cmd := m.enter(dir); cmd == nil || !stopped || m.taildrop == nil {
		t.Errorf("going up didn't move the Taildrop wait (stopped %v)", stopped)
	}
	m.taildrop.cancel()
	m.taildrop = nil

	// Narrower, they're tabs: the folders first, the iPad's a tab away.
	m = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if view = ansi.Strip(m.View()); strings.Contains(view, "Sorry") {
		t.Errorf("the server's tab shows the iPad panel:\n%s", view)
	}
	// Waiting for Taildrop shows on the folders' tab too: a spinner, and where.
	m.taildrop = &taildropState{cancel: func() {}}
	if view = ansi.Strip(m.View()); !strings.Contains(view, "Waiting for Taildrop files") || !strings.Contains(view, "ipad-pro "+ansi.Strip(m.spin.View())) {
		t.Errorf("the folders' tab doesn't say Taildrop's waiting:\n%s", view)
	}
	m.taildrop = nil
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if view = ansi.Strip(m.View()); !strings.Contains(view, "Sorry, dropping won't work from your iPad") || !strings.Contains(view, "More, at the end of the row") {
		t.Errorf("the iPad's tab lacks its panel:\n%s", view)
	}

	// Tailscale won't hand it files: ^y copies the command that lets it,
	// from either side.
	m.taildrop = &taildropState{cancel: func() {}, err: "no", operator: true}
	view = ansi.Strip(m.View())
	for _, want := range []string{operatorCommand(), "^y copies it", "^y copy command"} {
		if !strings.Contains(view, want) {
			t.Errorf("operator panel lacks %q:\n%s", want, view)
		}
	}
	for _, tab := range []bool{false, true} {
		if tab {
			m = drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
		}
		m.flash = ""
		m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlY})
		if !strings.Contains(m.flash, "clipboard") {
			t.Errorf("^y (onRemote %v) didn't copy the operator command: %q", m.onRemote, m.flash)
		}
	}

	// On a Mac the way back to isn't set up yet.
	m = newTestModel(t, t.TempDir())
	m.senders = []sender{{Host: "macbook-pro", Name: "macbook-pro", Device: "Mac"}}
	m.probes["macbook-pro"] = &probeResult{err: &setupError{step: stepHostKey, msg: "x", noKey: true}}
	view = ansi.Strip(m.View())
	server := serverName()
	for _, want := range []string{"No connection back to macbook-pro", "✗ An ssh key for " + server, "✓ Remote Login on", "○ " + server + " let in", "Next, here on " + server, "Press ^k to make one.", "^k make key", "Checking again"} {
		if !strings.Contains(view, want) {
			t.Errorf("Mac panel lacks %q:\n%s", want, view)
		}
	}
	// Typing searches: the list is back.
	m = typeText(t, m, "x")
	if strings.Contains(ansi.Strip(m.View()), "No connection back") {
		t.Error("the panel hides the search")
	}
}

func TestSides(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "report.pdf"), []byte("x"), 0o644)
	m := newTestModel(t, root)
	m.senders = []sender{{Host: "macbook-pro", Name: "macbook-pro", Device: "Mac"}}
	m.probes["macbook-pro"] = &probeResult{}
	server := serverName()
	view := ansi.Strip(m.View())
	for _, want := range []string{server, "macbook-pro ✓", "^s send to macbook-pro", "macbook-pro:~/Downloads", "tab macbook-pro"} {
		if !strings.Contains(view, want) {
			t.Errorf("this side lacks %q:\n%s", want, view)
		}
	}

	// tab: your machine's side, listed from ~/Downloads.
	next, cmd := m.switchSide()
	m = next.(model)
	if !m.onRemote || cmd == nil || m.remote.dir != "" {
		t.Fatalf("onRemote %v, remote %+v", m.onRemote, m.remote)
	}
	next, _ = m.Update(remoteListedMsg{asked: "~/Downloads", l: remoteListing{
		dir: "/Users/sam/Downloads", home: "/Users/sam",
		entries: []entry{{name: "Invoices", path: "/Users/sam/Downloads/Invoices", dir: true}, {name: "old.zip", path: "/Users/sam/Downloads/old.zip"}},
	}})
	m = next.(model)
	view = ansi.Strip(m.View())
	for _, want := range []string{"~/Downloads", "Invoices/", "old.zip", "^s send", server + ":", "tab " + server, "^g Downloads"} {
		if !strings.Contains(view, want) {
			t.Errorf("your side lacks %q:\n%s", want, view)
		}
	}
	if e, _ := m.selected(); e.name != "Invoices" {
		t.Errorf("opens on %q", e.name)
	}
	// Each side keeps its folder.
	next, _ = m.switchSide()
	m = next.(model)
	if m.onRemote || m.dir != root || m.remote.dir != "/Users/sam/Downloads" {
		t.Errorf("back: onRemote %v dir %s remote %s", m.onRemote, m.dir, m.remote.dir)
	}
	// ^s here: a job to the folder shown there.
	m.cursor = 1
	next, _ = m.copyAcross()
	m = next.(model)
	if len(m.jobs) != 1 || !m.jobs[0].out || m.jobs[0].dest != "/Users/sam/Downloads" {
		t.Fatalf("jobs %+v", m.jobs)
	}
	for _, j := range m.jobs {
		j.state = jobFailed // it went nowhere: there's no macbook-pro here
	}

	// Not to an iPad.
	m.senders = []sender{{Host: "ipad-pro", Name: "ipad-pro", Mobile: true, Device: "iPad"}}
	if _, ok := m.remoteReady(); ok {
		t.Error("ready to send to an iPad")
	}
}

func TestToAgent(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "shot 1.png"), []byte("x"), 0o644)
	m := newTestModel(t, root)
	m.agentPane = "w1:p3"
	if !strings.Contains(ansi.Strip(m.View()), "^e agent") {
		t.Fatalf("no ^e:\n%s", ansi.Strip(m.View()))
	}
	var sent map[string]any
	old := herdrCallFn
	herdrCallFn = func(method string, params, out any) error {
		if method == "pane.send_text" {
			sent = params.(map[string]any)
		}
		return nil
	}
	t.Cleanup(func() { herdrCallFn = old })
	next, cmd := m.toAgent()
	m = next.(model)
	if sent["pane_id"] != "w1:p3" || sent["text"] != "'"+filepath.Join(root, "shot 1.png")+"' " {
		t.Errorf("sent %v", sent)
	}
	if cmd == nil {
		t.Error("the popup stays open")
	}
}

// wideWithMac is the popup wide enough for panes, your Mac's side listed.
func wideWithMac(t *testing.T, root string) model {
	m := newTestModel(t, root)
	m.senders = []sender{{Host: "macbook-pro", Name: "macbook-pro", Device: "Mac"}}
	m.probes["macbook-pro"] = &probeResult{}
	m = drive(t, m, tea.WindowSizeMsg{Width: 140, Height: 24})
	next, _ := m.Update(remoteListedMsg{asked: "~/Downloads", l: remoteListing{
		dir: "/Users/sam/Downloads", home: "/Users/sam",
		entries: []entry{{name: "Invoices", path: "/Users/sam/Downloads/Invoices", dir: true}, {name: "old.zip", path: "/Users/sam/Downloads/old.zip"}},
	}})
	return next.(model)
}

func TestPanes(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.png"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "b.png"), []byte("x"), 0o644)
	m := wideWithMac(t, root)
	if !m.panes() {
		t.Fatal("no panes at 140 wide")
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{serverName(), "→  macbook-pro ✓", "~/Downloads", "a.png", "Invoices/", "old.zip", "│", "^s send", "^y copy path"} {
		if !strings.Contains(view, want) {
			t.Errorf("panes lack %q:\n%s", want, view)
		}
	}
	for i, line := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(line) > 140 {
			t.Errorf("line %d is %d wide", i, ansi.StringWidth(line))
		}
	}
	// tab: the keys go right, each side keeping its cursor.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.png
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if e, _ := m.selected(); !m.onRemote || e.name != "Invoices" {
		t.Errorf("right pane on %q (onRemote %v)", e.name, m.onRemote)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if e, _ := m.selected(); m.onRemote || e.name != "b.png" {
		t.Errorf("back on %q", e.name)
	}
	// A click in the right pane moves there.
	m = drive(t, m, tea.MouseMsg{X: 120, Y: listTop + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m.onRemote {
		t.Error("a click on the right pane didn't move there")
	}
	// Narrow: tabs again.
	m = drive(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	if m.panes() || !strings.Contains(ansi.Strip(m.View()), "old.zip") {
		t.Error("not tabs when narrow")
	}
}

func TestMarks(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	for _, n := range []string{"a.png", "b.png", "c.png"} {
		os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644)
	}
	m := wideWithMac(t, root)
	if !strings.Contains(ansi.Strip(m.View()), "space mark") {
		t.Error("nothing says space marks")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeySpace}) // a, on to b
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyDown})  // c
	m = drive(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.chosen()); got != 2 {
		t.Fatalf("%d chosen", got)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "● · a.png") || !strings.Contains(view, "^s send 2") ||
		!strings.Contains(view, "2 marked: ^s sends them to macbook-pro:~/Downloads · space unmarks · ^a marks all") {
		t.Errorf("marks not shown:\n%s", view)
	}
	// ^d: both, asked once.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.confirm == nil || !strings.Contains(m.confirm.question(), "Move 2 items to the trash?") {
		t.Fatalf("confirm %+v", m.confirm)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	// ^a: all three; again: none.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if got := len(m.chosen()); got != 3 {
		t.Errorf("^a chose %d", got)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if got := len(m.marks[0]); got != 0 {
		t.Errorf("^a again left %d marked", got)
	}
	// ^s with two marked: two copies to the folder shown there.
	m.cursor = 1
	m = drive(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = drive(t, m, tea.KeyMsg{Type: tea.KeySpace})
	next, _ := m.copyAcross()
	m = next.(model)
	if len(m.jobs) != 2 || m.jobs[0].dest != "/Users/sam/Downloads" || !m.jobs[1].out {
		t.Errorf("jobs %+v", m.jobs)
	}
	if len(m.marks[0]) != 0 {
		t.Error("marks kept after sending")
	}
	for _, j := range m.jobs {
		j.state = jobFailed
	}
	// ^e with marks: every path, quoted.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m.agentPane = "p"
	var text string
	old := herdrCallFn
	herdrCallFn = func(method string, params, out any) error {
		text, _ = params.(map[string]any)["text"].(string)
		return nil
	}
	t.Cleanup(func() { herdrCallFn = old })
	m.toAgent()
	if strings.Count(text, root) != 3 {
		t.Errorf("sent %q", text)
	}
}

func TestArrowFollowsFocus(t *testing.T) {
	m := wideWithMac(t, t.TempDir())
	if head := ansi.Strip(strings.Split(m.View(), "\n")[0]); !strings.Contains(head, "→") || strings.Contains(head, "Upload") {
		t.Errorf("header %q", head)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if head := ansi.Strip(strings.Split(m.View(), "\n")[0]); !strings.Contains(head, "←") {
		t.Errorf("header after tab %q", head)
	}
}

// Not set up yet: no panes, the checklist has the popup to itself.
func TestSetupFillsPopup(t *testing.T) {
	m := wideWithMac(t, t.TempDir())
	m.probes["macbook-pro"] = &probeResult{err: &setupError{step: stepAuth, msg: "x"}}
	view := ansi.Strip(m.View())
	if m.panes() || strings.Contains(view, "│") {
		t.Errorf("still panes:\n%s", view)
	}
	for _, want := range []string{"No connection back to macbook-pro", "^y copy command", "^r check now"} {
		if !strings.Contains(view, want) {
			t.Errorf("lacks %q:\n%s", want, view)
		}
	}
	m.probes["macbook-pro"] = &probeResult{}
	if !m.panes() {
		t.Error("no panes once it works")
	}
}
