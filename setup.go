package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The way back: files are fetched by this server, over ssh, from the
// machine you're at. That takes, in the order ssh meets them:
//
//	a key here           to log in with
//	reach                the machine answers at all
//	its ssh server       on: a Mac's Remote Login
//	its host key         known here, so ssh trusts it's that machine
//	this key let in      in its ~/.ssh/authorized_keys
//	the Desktop          readable by ssh: a Mac keeps it from Remote Login
//	                     until that's given full disk access
//
// When the check fails, the popup lists these, ticks what works, and says how
// to do the first that doesn't, with a key for what can be done from here.

type setupStep int

const (
	stepKey setupStep = iota
	stepReach
	stepSSHD
	stepHostKey
	stepAuth
	stepDesktop
	stepDone
)

// setupError is a failure to fetch that a setup step would fix.
type setupError struct {
	step  setupStep
	msg   string
	noKey bool // and this server has no key to log in with
}

func (e *setupError) Error() string { return e.msg }

func stepOf(err error) (setupStep, bool) {
	var se *setupError
	if errors.As(err, &se) {
		return se.step, true
	}
	return 0, false
}

// hostname is os.Hostname, a seam for the screenshots.
var hostname = os.Hostname

// serverName is what this machine is called, short.
func serverName() string {
	h, _ := hostname()
	if h, _, _ = strings.Cut(h, "."); h == "" {
		return "this server"
	}
	return h
}

// ── keys ─────────────────────────────────────────────────────────────────────

// publicKey is this server's ssh public key: what the sender has to let in.
// ssh offers the first of these it finds.
func publicKey() (path string, key string) {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name+".pub")
		if data, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(data)) > 0 {
			return p, strings.TrimSpace(string(data))
		}
	}
	return "", ""
}

// hasKey: a key file, or one in an ssh agent this process can reach.
func hasKey() bool {
	if _, k := publicKey(); k != "" {
		return true
	}
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return false
	}
	return exec.Command("ssh-add", "-L").Run() == nil
}

// makeKey makes this server an ed25519 key, with no passphrase: the popup
// has nowhere to ask for one.
func makeKey() error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(p); err == nil {
		return errors.New("~/.ssh/id_ed25519 is already there")
	}
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", p,
		"-C", localUser()+"@"+serverName()).CombinedOutput()
	if err != nil {
		return errors.New(lastLine(string(out), err))
	}
	return nil
}

// trustHost adds the host keys host shows now to ~/.ssh/known_hosts: what
// answering yes to ssh's first question does.
func trustHost(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh-keyscan", "-T", "5", host).Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return fmt.Errorf("%s showed no host key: is its ssh on?", host)
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "known_hosts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(out)
	return err
}

// authorizeCommand is what to paste on the sender to let this server in.
func authorizeCommand(key string) string {
	return "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo " + shellQuote(key) +
		" >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
}

// ── the panel ────────────────────────────────────────────────────────────────

// setupStatus is how far the check got: the step it failed at (stepDone if
// none), and whether this server has a key at all.
type setupStatus struct {
	failed setupStep
	noKey  bool
	err    error
}

func statusOf(err error) setupStatus {
	st := setupStatus{failed: stepDone, err: err}
	if err == nil {
		return st
	}
	var se *setupError
	if errors.As(err, &se) {
		st.failed, st.noKey = se.step, se.noKey
	} else {
		st.failed = stepReach // something else: said as is, under the title
	}
	return st
}

type setupLine struct {
	step  setupStep
	label string
	where string   // "here", or "on <machine>": where it's done
	do    []string // the Next block: why, then what to press or click
}

// setupLines are the steps, for a sender of the given kind.
func setupLines(s sender, cfg config, server string) []setupLine {
	name, there := s.Name, "on "+s.Name
	sshdLabel := "ssh server running"
	var sshd []string
	switch s.Device {
	case "Mac":
		sshdLabel = "Remote Login on"
		sshd = []string{"So " + server + " can log in to it:", "System Settings › General › Sharing › Remote Login, on."}
	case "Windows PC":
		sshd = []string{"So " + server + " can log in to it:", "Settings › System › Optional features › OpenSSH Server,", "then start the sshd service."}
	case "linux", "Linux":
		sshd = []string{"So " + server + " can log in to it: install openssh-server, then", "sudo systemctl enable --now ssh"}
	default:
		sshd = []string{"So " + server + " can log in to it. On a Mac:", "System Settings › General › Sharing › Remote Login, on."}
	}
	auth := []string{
		name + " has to know " + server + "'s key before it lets it in.",
		"Press ^y to copy a command, and paste it in a terminal on " + name + ".",
	}
	if !s.Fixed && cfg.User == "" && s.User == "" {
		auth = append(auth, "", dimNote+`Logging in as `+localUser()+`. Someone else on `+name+`? Set "user" in config.json.`)
	}
	lines := []setupLine{
		{stepKey, "An ssh key for " + server, "here", []string{
			server + " needs a key of its own to log in to " + name + " with.",
			"Press ^k to make one.",
		}},
		{stepReach, name + " reachable", "", []string{
			server + " can't reach " + name + ". Is it awake, and on Tailscale?",
		}},
		{stepSSHD, sshdLabel, there, sshd},
		{stepHostKey, name + "'s host key trusted", "here", []string{
			server + " hasn't connected to " + name + " before, so it can't tell yet",
			"that it's really your machine. Press ^a to trust it.",
		}},
		{stepAuth, server + " let in", there, auth},
	}
	if s.Device == "Mac" || s.Device == "" {
		lines = append(lines, setupLine{stepDesktop, "Desktop readable over ssh", there, []string{
			"macOS keeps Desktop, Documents and Downloads from ssh until you allow it:",
			"System Settings › General › Sharing, the (i) next to Remote Login,",
			"then Allow full disk access for remote users, on.",
		}})
	}
	return lines
}

// stepState is a step's tick: done, the one to do, or not reached yet.
type stepState int

const (
	stepOK stepState = iota
	stepFailed
	stepPending
)

func (st setupStatus) of(step setupStep) stepState {
	switch {
	case step == stepKey && st.noKey, step != stepKey && step == st.failed:
		return stepFailed
	case step == stepKey, step < st.failed:
		return stepOK
	}
	return stepPending
}

// next is the step to do now: the first that failed.
func (st setupStatus) next(lines []setupLine) (setupLine, bool) {
	for _, l := range lines {
		if st.of(l.step) == stepFailed {
			return l, true
		}
	}
	return setupLine{}, false
}

// viewSetup is the panel shown in place of the list while the sender can't
// be fetched from: a short list of what the way back takes, ticked as far
// as it works, then the one thing to do next, where, and how.
func (m model) viewSetup(s sender, r *probeResult) []string {
	server := serverName()
	if s.Mobile {
		return m.viewTaildrop(s, server)
	}
	st := statusOf(r.err)
	lines := setupLines(s, m.cfg, server)
	done, labelW := 0, 0
	for _, l := range lines {
		if st.of(l.step) == stepOK {
			done++
		}
		labelW = max(labelW, lipgloss.Width(l.label))
	}
	out := []string{
		styleTitle.Render("No connection back to " + s.Name),
		styleText.Render(server+" fetches what you drop from "+s.Name+", over ssh. ") +
			styleDim.Render(fmt.Sprintf("%d of %d set up.", done, len(lines))),
		"",
	}
	for _, l := range lines {
		var mark, label string
		pad := strings.Repeat(" ", labelW-lipgloss.Width(l.label)+3)
		switch st.of(l.step) {
		case stepOK:
			mark, label = styleOK.Render("✓"), styleText.Render(l.label)
		case stepFailed:
			mark, label = styleErr.Render("✗"), styleLead.Render(l.label)
		default:
			mark, label = styleDim.Render("○"), styleDim.Render(l.label)
		}
		out = append(out, mark+" "+label+pad+styleDim.Render(l.where))
	}
	out = append(out, "")

	next, ok := st.next(lines)
	if _, known := stepOf(r.err); !known {
		// Not a step: say what happened, as it is.
		out = append(out, styleHeader.Render("Next"), styleErr.Render(clean(r.err.Error(), false)))
	} else if ok {
		where := ", here on " + server
		if next.where != "here" {
			where = ", on " + s.Name
		}
		if next.where == "" {
			where = ""
		}
		out = append(out, styleHeader.Render("Next")+styleDim.Render(where))
		for _, d := range next.do {
			if note, ok := strings.CutPrefix(d, dimNote); ok {
				out = append(out, styleDim.Render(note))
				continue
			}
			out = append(out, keyed(d))
		}
	}
	check := "Checking again every few seconds."
	if r.running {
		check = m.spin.View() + styleDim.Render(" Checking…")
	} else {
		check = styleDim.Render(check)
	}
	return append(out, "", check)
}

// dimNote marks a line of the Next block as an aside, drawn dim.
const dimNote = "\x00note:"

// keyed draws a line of instructions with its keys (^k, ^a, ^y) standing out.
func keyed(line string) string {
	var out strings.Builder
	words := strings.Split(line, " ")
	for i, w := range words {
		if i > 0 {
			out.WriteString(styleText.Render(" "))
		}
		if len(w) >= 2 && w[0] == '^' {
			out.WriteString(styleTree.Bold(true).Render(w))
		} else {
			out.WriteString(styleText.Render(w))
		}
	}
	return out.String()
}

// viewTaildrop is the panel for a phone or tablet: no ssh to fetch with,
// but Taildrop can send files here.
func (m model) viewTaildrop(s sender, server string) []string {
	dev := s.Device
	if dev == "" {
		dev = "device"
	}
	out := []string{
		styleTitle.Render("Sorry, dropping won't work from your " + dev), "",
		styleText.Render("Dropped files are fetched over ssh by " + server + ", and an " + dev + " can't take ssh."),
		styleText.Render("Pasting an image works, though: herdr brings that over itself."),
		styleText.Render("For anything else, Taildrop can bring it:"), "",
	}
	howTaildrop := []string{
		"On the " + dev + ", in Photos, Files or any app: Share, then Tailscale",
		"in the row of apps (not there? More, at the end of the row, and turn it on),",
		"then " + server + ". The Tailscale app has to be on.",
	}
	switch {
	case m.taildrop != nil && m.taildrop.err != "":
		out = append(out, styleErr.Render(m.taildrop.err))
		if m.taildrop.operator {
			out = append(out, styleText.Render("Run this once here, then ^t again:"),
				"  "+styleTree.Render("sudo tailscale set --operator="+localUser()))
		}
	case m.taildrop != nil:
		out = append(out, m.spin.View()+" "+styleLead.Render("Waiting for files…"), "")
		for _, h := range howTaildrop {
			out = append(out, styleText.Render(h))
		}
		out = append(out, styleText.Render("They land in ")+styleTree.Render(tildify(m.dir))+styleText.Render("."))
	default:
		out = append(out, keyed("Press ^t, and "+server+" waits for files. Then:"), "")
		for _, h := range howTaildrop {
			out = append(out, styleText.Render(h))
		}
		out = append(out, styleText.Render("They land in ")+styleTree.Render(tildify(m.dir))+styleText.Render(", the folder shown."))
	}
	return out
}

// ── Taildrop ─────────────────────────────────────────────────────────────────

// taildropState is files being received with Taildrop, while it waits.
type taildropState struct {
	cancel   context.CancelFunc
	err      string
	operator bool // the error is tailscale not letting this user take files
}

type taildropMsg struct {
	dir   string
	files []string // where each landed
	err   error
}

// receive waits for Taildrop files, then moves them into dir under free
// names.
func receive(ctx context.Context, dir string) tea.Cmd {
	return func() tea.Msg {
		tmp, err := os.MkdirTemp(dir, ".herdr-file-transfer-")
		if err != nil {
			return taildropMsg{dir: dir, err: err}
		}
		defer os.RemoveAll(tmp)
		out, err := exec.CommandContext(ctx, "tailscale", "file", "get", "--wait", "--conflict=rename", tmp).CombinedOutput()
		var landed []string
		des, _ := os.ReadDir(tmp)
		for _, de := range des {
			final := freeName(dir, de.Name())
			if os.Rename(filepath.Join(tmp, de.Name()), final) == nil {
				landed = append(landed, final)
			}
		}
		if err != nil && len(landed) == 0 {
			if ctx.Err() != nil {
				return taildropMsg{dir: dir, err: ctx.Err()}
			}
			return taildropMsg{dir: dir, err: errors.New(lastLine(string(out), err))}
		}
		return taildropMsg{dir: dir, files: landed}
	}
}

// operatorErr says whether tailscale refused because this user may not
// take files: only root or its operator can.
func operatorErr(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "access denied") || strings.Contains(low, "permission") || strings.Contains(low, "operator")
}

// ── in the popup ─────────────────────────────────────────────────────────────

// errMobile is a phone or tablet's "check": there's no ssh to check.
var errMobile = errors.New("a phone or tablet can't be fetched from")

type actionMsg struct {
	done string
	err  error
}

// setupShown is the sender the setup panel is about, when it's shown in
// place of the list: the check failed, and nothing's being searched or named.
func (m model) setupShown() (sender, *probeResult, bool) {
	return m.panelFor(m.onRemote)
}
func (m model) setupFooter(s sender, r *probeResult) []hint {
	var hs []hint
	if s.Mobile {
		if m.taildrop == nil || m.taildrop.err != "" {
			hs = append(hs, hint{"^t receive with Taildrop", "ctrl+t", hintGo})
		} else {
			hs = append(hs, hint{"^t stop", "ctrl+t", hintQuiet})
		}
	} else {
		// The next step's key, if it has one, as the thing to do.
		st := statusOf(r.err)
		if next, ok := st.next(setupLines(s, m.cfg, serverName())); ok {
			switch next.step {
			case stepKey:
				hs = append(hs, hint{"^k make key", "ctrl+k", hintGo})
			case stepHostKey:
				hs = append(hs, hint{"^a trust " + s.Name, "ctrl+a", hintGo})
			case stepAuth:
				hs = append(hs, hint{"^y copy command", "ctrl+y", hintGo})
			}
		}
		hs = append(hs, hint{"^r check now", "ctrl+r", hintView})
	}
	return append(hs, hint{"esc close", "esc", hintQuiet})
}

// setupKey does what a setup key says, for the sender shown.
func (m *model) setupKey(key string) (tea.Cmd, bool) {
	if len(m.senders) == 0 {
		return nil, false
	}
	s := m.senders[0]
	m.err, m.flash = "", ""
	if m.demo && key != "ctrl+t" {
		m.flash = "Demo: nothing was changed"
		return nil, true
	}
	switch key {
	case "ctrl+k":
		if hasKey() {
			return nil, false
		}
		return func() tea.Msg {
			if err := makeKey(); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{done: "Made " + serverName() + "'s key: ~/.ssh/id_ed25519"}
		}, true
	case "ctrl+a":
		if s.Mobile {
			return nil, false
		}
		ctx := m.ctx
		return func() tea.Msg {
			if err := trustHost(ctx, s.Host); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{done: "Trusted " + s.Name + "'s host key"}
		}, true
	case "ctrl+y":
		_, key := publicKey()
		if key == "" || s.Mobile {
			return nil, false
		}
		// Your terminal's clipboard, through herdr (OSC 52): the one on the
		// machine in front of you, not this server's.
		os.Stdout.WriteString(ansi.SetSystemClipboard(authorizeCommand(key)))
		m.flash = "Sent to your clipboard: paste it in a terminal on " + s.Name + ", then ^r"
		return nil, true
	case "ctrl+t":
		if !s.Mobile {
			return nil, false
		}
		if m.taildrop != nil && m.taildrop.err == "" {
			m.taildrop.cancel()
			m.taildrop = nil
			return nil, true
		}
		ctx, cancel := context.WithCancel(m.ctx)
		m.taildrop = &taildropState{cancel: cancel}
		return receive(ctx, m.dir), true
	}
	return nil, false
}

// received lists what Taildrop brought, and waits for more.
func (m model) received(msg taildropMsg) (tea.Model, tea.Cmd) {
	if m.taildrop == nil {
		return m, nil // stopped meanwhile
	}
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.taildrop.err = msg.err.Error()
		m.taildrop.operator = operatorErr(m.taildrop.err)
		if m.taildrop.operator {
			m.taildrop.err = serverName() + "'s Tailscale won't hand files to " + localUser() + "."
		}
		return m, nil
	}
	for _, f := range msg.files {
		m.nextID++
		j := &job{id: m.nextID, src: f, dest: f, from: "Taildrop", state: jobDone}
		if st, err := os.Stat(f); err == nil {
			j.bytes.Store(st.Size())
		}
		m.jobs = append(m.jobs, j)
		m.landed[f] = true
	}
	// Wait for the next, into the folder shown now.
	ctx, cancel := context.WithCancel(m.ctx)
	m.taildrop.cancel()
	m.taildrop.cancel = cancel
	return m, tea.Batch(m.list(m.dir), receive(ctx, m.dir))
}
