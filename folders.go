package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// sortEntries puts folders first, then by name, case aside.
func sortEntries(es []entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].dir != es[j].dir {
			return es[i].dir
		}
		return strings.ToLower(es[i].name) < strings.ToLower(es[j].name)
	})
}

// skipDirs are never searched into: big, and never where a file is dropped.
var skipDirs = map[string]bool{"node_modules": true, ".git": true, "__pycache__": true, ".venv": true, "target": true}

// maxWalk bounds a search's walk, for a folder with a world under it.
const maxWalk = 20000

// walkDirs lists the folders under root, depth levels down, each named by
// its path from root.
func walkDirs(root string, depth int, hidden bool) []entry {
	out := []entry{}
	level := []string{root}
	for d := 0; d < depth && len(level) > 0 && len(out) < maxWalk; d++ {
		var next []string
		for _, dir := range level {
			des, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, de := range des {
				name := de.Name()
				if !de.IsDir() || skipDirs[name] || strings.HasPrefix(name, ".herdr-file-transfer-") || (!hidden && strings.HasPrefix(name, ".")) {
					continue
				}
				p := filepath.Join(dir, name)
				rel, _ := filepath.Rel(root, p)
				out = append(out, entry{name: rel, path: p, dir: true})
				next = append(next, p)
				if len(out) >= maxWalk {
					return out
				}
			}
		}
		level = next
	}
	return out
}

// rank keeps the entries whose name matches q and orders them best first:
// a name that is q, then starts with it, then holds it, then holds its
// letters in order. A path is matched by its last part first, so "ui" finds
// src/components/ui before src/build.
func rank(es []entry, q string) []entry {
	q = strings.ToLower(q)
	type scored struct {
		e     entry
		score int
	}
	var out []scored
	for _, e := range es {
		if s := matchScore(strings.ToLower(e.name), q); s > 0 {
			out = append(out, scored{e, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		// Shallower first: nearer to where you are.
		return strings.Count(out[i].e.name, "/") < strings.Count(out[j].e.name, "/")
	})
	res := make([]entry, len(out))
	for i, s := range out {
		res[i] = s.e
	}
	return res
}

func matchScore(name, q string) int {
	base := name[strings.LastIndex(name, "/")+1:]
	switch {
	case base == q:
		return 100
	case strings.HasPrefix(base, q):
		return 80
	case strings.Contains(base, q):
		return 60
	case strings.Contains(name, q):
		return 40
	case subsequence(name, q):
		return 10
	}
	return 0
}

func subsequence(s, q string) bool {
	for _, r := range s {
		if len(q) == 0 {
			break
		}
		if strings.HasPrefix(q, string(r)) {
			q = q[len(string(r)):]
		}
	}
	return q == ""
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
	return fmt.Sprintf("%.2f GB", float64(n)/1024/1024/1024)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func shorten(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return strings.TrimRight(ansi.Truncate(s, n-1, ""), " ") + "…"
}

// shortenLeft keeps a path's end, where it's going: "…/assets/icons".
func shortenLeft(s string, n int) string {
	if n <= 1 || ansi.StringWidth(s) <= n {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && ansi.StringWidth(string(rs))+1 > n {
		rs = rs[1:]
	}
	return "…" + string(rs)
}

// highlight gives a whole line the selection background, w cells wide. The
// line's own coloured pieces each end in a style reset, which would also end
// the background, so it's switched back on after every one.
func highlight(line string, w int) string {
	on := styleSelected.Render("x")
	on = on[:strings.Index(on, "x")] // the escape that turns the background on
	if on != "" {
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	}
	return styleSelected.Width(w).Render(line)
}
