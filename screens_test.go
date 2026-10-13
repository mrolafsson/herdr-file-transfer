package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestWriteDemoScreens saves each README screen, as the popup draws it, on a
// made-up project and machines. It only runs for scripts/screenshots.sh,
// which sets SCREENS_DIR and turns the files into PNGs with freeze.
func TestWriteDemoScreens(t *testing.T) {
	dir := os.Getenv("SCREENS_DIR")
	if dir == "" {
		t.Skip("set SCREENS_DIR to write the README screenshots")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI256) })
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(name string, m model) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(m.View()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// herdr's default theme, which freeze's background matches.
	catppuccin := herdrPalettes["catppuccin"]
	useTheme(&catppuccin)
	t.Cleanup(func() { useTheme(nil) })

	// A home of its own, so paths read ~/src/acme-web, and a server name.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "sam")
	old := hostname
	hostname = func() (string, error) { return "devbox", nil }
	t.Cleanup(func() { hostname = old })
	project := filepath.Join(home, "src", "acme-web")
	for _, d := range []string{"public/images/screenshots", "public/fonts", "src/components/ui", "src/routes", "docs/design", "tests/fixtures"} {
		os.MkdirAll(filepath.Join(project, d), 0o755)
	}
	for name, size := range map[string]int{"README.md": 2400, "package.json": 1200, "tsconfig.json": 600, "vite.config.ts": 900} {
		os.WriteFile(filepath.Join(project, name), make([]byte, size), 0o644)
	}
	mac := sender{Host: "macbook", Name: "macbook", Device: "Mac"}
	ipad := sender{Host: "ipad", Name: "ipad", Device: "iPad", Mobile: true}
	screen := func(at string) model {
		m := newModel(t.Context(), withDefaults(config{}), at)
		m.finding = false
		next, _ := m.Update(tea.WindowSizeMsg{Width: 92, Height: 24})
		m = next.(model)
		next, _ = m.Update(m.list(at)())
		return next.(model)
	}

	// Files dropped on the agent's folder: two in, one on its way, one that
	// wasn't there.
	images := filepath.Join(project, "public", "images")
	drop := screen(images)
	drop.senders, drop.probes = []sender{mac}, map[string]*probeResult{"macbook": {}}
	for _, n := range []string{"hero.png", "pricing-table.png"} {
		p := filepath.Join(images, n)
		os.WriteFile(p, make([]byte, 312_000), 0o644)
		drop.landed[p] = true
	}
	next, _ := drop.Update(drop.list(images)())
	drop = next.(model)
	mkjob := func(src, dest string, state jobState, n int64, took time.Duration, err string) *job {
		j := &job{src: src, dest: dest, state: state, took: took, err: err, name: filepath.Base(src)}
		j.bytes.Store(n)
		j.total.Store(96_400_000)
		return j
	}
	drop.jobs = []*job{
		mkjob("/Users/sam/Desktop/hero.png", filepath.Join(images, "hero.png"), jobDone, 312_000, 600*time.Millisecond, ""),
		mkjob("/Users/sam/Desktop/pricing-table.png", filepath.Join(images, "pricing-table.png"), jobDone, 312_000, 500*time.Millisecond, ""),
		mkjob("/Users/sam/Movies/walkthrough.mov", images, jobRunning, 61_300_000, 0, ""),
	}
	drop.cursor = 4
	drop.agentPane = "w1:p1"
	save("drop", drop)

	// Search: every folder under the one shown.
	search := screen(project)
	search.senders, search.probes = []sender{mac}, map[string]*probeResult{"macbook": {}}
	for _, r := range "shots" {
		next, _ = search.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		search = next.(model)
	}
	next, _ = search.Update(search.search(project, "shots")())
	search = next.(model)
	save("search", search)

	// The way back, not set up yet.
	setup := screen(project)
	setup.senders = []sender{mac}
	setup.probes = map[string]*probeResult{"macbook": {err: &setupError{step: stepAuth, msg: "x"}}}
	save("setup", setup)

	// Wide: both machines side by side, two files marked here, one already
	// on its way there.
	panes := screen(images)
	panes.senders, panes.probes = []sender{mac}, map[string]*probeResult{"macbook": {}}
	panes.agentPane = "w1:p1"
	next, _ = panes.Update(tea.WindowSizeMsg{Width: 132, Height: 22})
	panes = next.(model)
	next, _ = panes.Update(remoteListedMsg{asked: "~/Downloads", l: remoteListing{
		dir: "/Users/sam/Downloads", home: "/Users/sam",
		entries: []entry{
			{name: "Invoices", path: "/Users/sam/Downloads/Invoices", dir: true},
			{name: "brand-kit", path: "/Users/sam/Downloads/brand-kit", dir: true},
			{name: "contract-v3.pdf", path: "/Users/sam/Downloads/contract-v3.pdf", size: -1},
			{name: "team-offsite.jpg", path: "/Users/sam/Downloads/team-offsite.jpg", size: -1},
		},
	}})
	panes = next.(model)
	sending := mkjob(filepath.Join(images, "hero.png"), "/Users/sam/Downloads", jobRunning, 61_300_000, 0, "")
	sending.out, sending.home, sending.name, sending.from = true, "/Users/sam", "launch-video.mov", "macbook"
	panes.jobs = []*job{sending}
	panes.cursor = 2
	for _, n := range []string{"hero.png", "pricing-table.png"} {
		p := filepath.Join(images, n)
		panes.marks[0][p] = entry{name: n, path: p}
	}
	save("panes", panes)

	// On an iPad.
	tablet := screen(project)
	tablet.senders = []sender{ipad, mac}
	tablet.probes = map[string]*probeResult{"ipad": {err: errMobile}}
	save("ipad", tablet)
}
