package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Picking files for another plugin: herdr-linear's "attach", say. It opens
// this popup in its place with pickEnv set to what's done with them (the
// verb, "attach"), returnEnv to its own popup, and dirEnv to where to start
// (the agent's folder). Any file on this server can be picked; one on the
// machine you're at is brought over first (dropped, sent with Taildrop or
// ^s) into the folder shown, and marked as it lands. Marks stay from folder
// to folder. enter on a file hands back the marked files (or that one): the
// other popup opens again with their paths, as a JSON list, in pickedEnv.
// esc goes back with none.
//
// herdr shows one popup at a time, so going back means this program becoming
// the other plugin's popup (exec), in the popup that's open, as herdr-linear,
// herdr-recap and herdr-github go from one to another.

const (
	pickEnv   = "HERDR_FILE_TRANSFER_PICK"
	pickedEnv = "HERDR_FILE_TRANSFER_PICKED"
	returnEnv = "HERDR_POPUP_RETURN"
)

// popupRef is a plugin's popup, as plugin.pane.open takes it.
type popupRef struct {
	PluginID   string            `json:"plugin_id"`
	Entrypoint string            `json:"entrypoint"`
	Width      string            `json:"width,omitempty"`
	Height     string            `json:"height,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// A plugin's or an entrypoint's ID, as herdr allows them: what a popupRef
// read from the environment is held to.
var refID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (p popupRef) valid() bool {
	return refID.MatchString(p.PluginID) && refID.MatchString(p.Entrypoint)
}

// cameFrom is the popup this one was opened from, if it was.
func cameFrom() *popupRef {
	var p popupRef
	if json.Unmarshal([]byte(os.Getenv(returnEnv)), &p) != nil || !p.valid() {
		return nil
	}
	return &p
}

// pendingSwap is the popup to become once the terminal is the shell's again.
var pendingSwap *popupRef

// goBack has the popup this one was opened from open again in its place,
// with paths when there are some.
func goBack(back popupRef, paths []string) {
	env := map[string]string{}
	for k, v := range back.Env {
		env[k] = v
	}
	if len(paths) > 0 {
		data, _ := json.Marshal(paths)
		env[pickedEnv] = string(data)
	}
	back.Env = env
	pendingSwap = &back
}

// paneCommand is where herdr has a plugin's popup: its folder and command.
func paneCommand(p popupRef) (root string, command []string, err error) {
	var res struct {
		Plugins []struct {
			PluginID string `json:"plugin_id"`
			Enabled  bool   `json:"enabled"`
			Root     string `json:"plugin_root"`
			Panes    []struct {
				ID      string   `json:"id"`
				Command []string `json:"command"`
			} `json:"panes"`
		} `json:"plugins"`
	}
	if err := herdrCall("plugin.list", nil, &res); err != nil {
		return "", nil, err
	}
	for _, pl := range res.Plugins {
		if pl.PluginID != p.PluginID || !pl.Enabled {
			continue
		}
		for _, pane := range pl.Panes {
			if pane.ID == p.Entrypoint && len(pane.Command) > 0 && pl.Root != "" {
				return pl.Root, pane.Command, nil
			}
		}
	}
	return "", nil, errors.New(p.PluginID + " has no popup called " + p.Entrypoint)
}

// swapEnv is this process's environment made the other plugin's: herdr's
// variables for a plugin renamed for it, this popup's own dropped, and the
// ones the other is opened with set.
func swapEnv(env []string, own string, p popupRef, root string) []string {
	var out []string
	dirs := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "HERDR_PLUGIN_CONFIG_DIR" || k == "HERDR_PLUGIN_STATE_DIR":
			// herdr keeps each plugin's under its ID, side by side.
			if filepath.Base(v) == own {
				dirs[k] = filepath.Join(filepath.Dir(v), p.PluginID)
			}
		case strings.HasPrefix(k, "HERDR_PLUGIN_"), k == returnEnv, strings.HasPrefix(k, "HERDR_FILE_TRANSFER_"):
		default:
			out = append(out, kv)
		}
	}
	out = append(out, "HERDR_PLUGIN_ID="+p.PluginID, "HERDR_PLUGIN_ROOT="+root, "HERDR_PLUGIN_ENTRYPOINT_ID="+p.Entrypoint)
	for k, v := range dirs {
		out = append(out, k+"="+v)
	}
	for k, v := range p.Env {
		out = append(out, k+"="+v)
	}
	return out
}

// swapInto becomes the other plugin's popup. It only returns if it couldn't.
func swapInto(p popupRef) error {
	if !p.valid() {
		return errors.New("not a popup: " + p.PluginID + "/" + p.Entrypoint)
	}
	root, command, err := paneCommand(p)
	if err != nil {
		return err
	}
	path := command[0]
	if strings.Contains(path, "/") {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
	} else if path, err = exec.LookPath(path); err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	return syscall.Exec(path, command, swapEnv(os.Environ(), pluginID(), p, root))
}

// finishSwap is the last thing the popup does: become the one it goes back
// to, or failing that, have it opened once this one has gone.
func finishSwap() error {
	p := pendingSwap
	if p == nil {
		return nil
	}
	if err := swapInto(*p); err != nil {
		return handOverDetached(*p)
	}
	return nil
}

// handoffWait is how long a popup is given to close before the next one's
// open is given up on.
const handoffWait = 4 * time.Second

// handOverDetached opens another popup once this one has closed: by this
// program again, detached, since the popup's own process ends with it.
func handOverDetached(p popupRef) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "handoff", string(data))
	// Its own session, so it outlives the popup it opens the next one for.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runHandoff is the detached half of handOverDetached: it opens the popup
// as soon as herdr has room for it.
func runHandoff(arg string) error {
	var p popupRef
	if err := json.Unmarshal([]byte(arg), &p); err != nil {
		return err
	}
	if !p.valid() {
		return errors.New("not a popup: " + p.PluginID + "/" + p.Entrypoint)
	}
	params := map[string]any{
		"plugin_id": p.PluginID, "entrypoint": p.Entrypoint, "placement": "popup", "focus": true, "env": p.Env,
	}
	if p.Width != "" && p.Height != "" {
		params["width"], params["height"] = p.Width, p.Height
	}
	var err error
	for deadline := time.Now().Add(handoffWait); ; time.Sleep(60 * time.Millisecond) {
		if err = herdrCall("plugin.pane.open", params, nil); err == nil || !isHerdrCode(err, "ui_busy") || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		notify("File transfer", "Couldn't open "+p.PluginID+": "+err.Error())
	}
	return err
}

// pickVerb is pickEnv's verb, held to a word or two: it's shown as is.
func pickVerb(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 24 || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' }) {
		return "attach"
	}
	return v
}

// picking says whether this popup picks files for another's.
func (m model) picking() bool { return m.pick != "" && m.back != nil }

// markPicked marks a file that's just landed in the folder shown, so enter
// takes it along with the rest.
func (m *model) markPicked(p string) {
	if !m.picking() || filepath.Dir(p) != m.dir {
		return
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		m.marks[0][p] = entry{name: filepath.Base(p), path: p, size: st.Size()}
	}
}

// pickable is what enter would hand back: the marked or selected files on
// this server's side. Folders aren't: they can't be attached.
func (m model) pickable() []string {
	if m.onRemote {
		return nil
	}
	var out []string
	for _, e := range m.chosen() {
		if !e.dir && !e.up && e.job == nil && e.path != "" {
			out = append(out, e.path)
		}
	}
	return out
}

func (m model) canPick() bool { return len(m.pickable()) > 0 }

// pickLabel is enter's pill: "enter attach", "enter attach 3".
func (m model) pickLabel() string {
	label := "enter " + m.pick
	if n := len(m.pickable()); n > 1 {
		label += " " + itoa(n)
	}
	return label
}

// pickDone hands the files back to the popup that asked for them.
func (m model) pickDone() (tea.Model, tea.Cmd) {
	paths := m.pickable()
	if len(paths) == 0 {
		m.err = "Choose a file to " + m.pick + ": a folder can't be"
		return m, nil
	}
	goBack(*m.back, paths)
	return m, tea.Quit
}

// pickStatus is the status line while picking, before anything's marked.
func (m model) pickStatus(w int) string {
	line := " Choose files here, or drop them to bring them here first; enter to " + m.pick + "."
	if m.onRemote {
		line = " ^s brings what's selected here, then enter to " + m.pick + " it."
	}
	return styleDim.Render(shorten(line, w)) + "\n"
}
