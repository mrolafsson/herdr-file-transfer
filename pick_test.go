package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestPick(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Folder"), 0o755)
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.pdf")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("bb"), 0o644)
	t.Cleanup(func() { pendingSwap = nil })

	back := popupRef{PluginID: "herdr-linear", Entrypoint: "picker", Env: map[string]string{"HERDR_LINEAR_DRAFT": "1"}}
	open := func() model {
		m := newTestModel(t, dir)
		m.pick, m.back = "attach", &back
		pendingSwap = nil
		return m
	}

	// What lands is marked, ready to go: enter hands both back.
	m := open()
	m.markPicked(a)
	m.markPicked(b)
	m.markPicked(filepath.Join(dir, "Folder")) // not a folder
	view := ansi.Strip(m.View())
	for _, want := range []string{"2 marked", "enter to attach", "enter attach 2"} {
		if !strings.Contains(view, want) {
			t.Errorf("picking lacks %q:\n%s", want, view)
		}
	}
	m.cursor = 3 // b.pdf
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if pendingSwap == nil || pendingSwap.PluginID != "herdr-linear" {
		t.Fatalf("enter didn't go back: %+v", pendingSwap)
	}
	var got []string
	json.Unmarshal([]byte(pendingSwap.Env[pickedEnv]), &got)
	if len(got) != 2 || pendingSwap.Env["HERDR_LINEAR_DRAFT"] != "1" {
		t.Errorf("went back with %v, env %v", got, pendingSwap.Env)
	}
	if back.Env[pickedEnv] != "" {
		t.Error("the popup to go back to was changed in place")
	}

	// Marks stay from folder to folder: enter on a folder opens it, and on
	// a file there, hands back what's marked.
	m = open()
	m.markPicked(a)
	m.cursor = 1 // Folder
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if pendingSwap != nil || m.dir != filepath.Join(dir, "Folder") || len(m.marks[0]) != 1 {
		t.Fatalf("enter on a folder: dir %s, marks %v, swap %+v", m.dir, m.marks[0], pendingSwap)
	}
	c := filepath.Join(dir, "Folder", "c.txt")
	os.WriteFile(c, []byte("c"), 0o644)
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // back into Folder (it's selected)
	m.cursor = 1                                    // c.txt
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m.cursor = 1
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	got = nil
	if pendingSwap != nil {
		json.Unmarshal([]byte(pendingSwap.Env[pickedEnv]), &got)
	}
	if len(got) != 2 {
		t.Errorf("marks from two folders: %v", got)
	}

	// Nothing marked: the selected file.
	m = open()
	m.cursor = 2 // a.png
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if pendingSwap == nil || !strings.Contains(pendingSwap.Env[pickedEnv], "a.png") {
		t.Errorf("enter on a file didn't pick it: %+v", pendingSwap)
	}

	// esc goes back with nothing.
	m = open()
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if pendingSwap == nil || pendingSwap.Env[pickedEnv] != "" {
		t.Errorf("esc didn't go back empty-handed: %+v", pendingSwap)
	}

	// Not picking: enter on a file does nothing, and lands aren't marked.
	m = newTestModel(t, dir)
	pendingSwap = nil
	m.markPicked(a)
	m.cursor = 2
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if pendingSwap != nil || len(m.marks[0]) != 0 {
		t.Error("picked without being asked to")
	}
}

func TestPickVerb(t *testing.T) {
	for in, want := range map[string]string{"attach": "attach", " add ": "add", "x\x1b[31m": "attach", strings.Repeat("a", 40): "attach"} {
		if got := pickVerb(in); got != want {
			t.Errorf("pickVerb(%q) = %q", in, got)
		}
	}
}

func TestSwapEnv(t *testing.T) {
	env := []string{"PATH=/bin", "HERDR_PLUGIN_ID=herdr-file-transfer", "HERDR_FILE_TRANSFER_PICK=attach", returnEnv + "={}",
		"HERDR_PLUGIN_STATE_DIR=/s/herdr-file-transfer", "HERDR_SOCKET_PATH=/sock"}
	got := strings.Join(swapEnv(env, "herdr-file-transfer", popupRef{PluginID: "herdr-linear", Entrypoint: "picker", Env: map[string]string{pickedEnv: "[]"}}, "/r"), " ")
	for _, want := range []string{"PATH=/bin", "HERDR_SOCKET_PATH=/sock", "HERDR_PLUGIN_ID=herdr-linear", "HERDR_PLUGIN_STATE_DIR=/s/herdr-linear", pickedEnv + "=[]"} {
		if !strings.Contains(got, want) {
			t.Errorf("swapEnv lacks %q: %s", want, got)
		}
	}
	for _, not := range []string{"HERDR_FILE_TRANSFER_PICK=", returnEnv, "HERDR_PLUGIN_ID=herdr-file-transfer"} {
		if strings.Contains(got, not) {
			t.Errorf("swapEnv kept %q: %s", not, got)
		}
	}
}
