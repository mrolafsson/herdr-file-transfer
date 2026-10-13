// herdr-file-transfer: drop files from your desktop onto a herdr popup and they're
// copied over ssh into the agent's folder, or any folder you pick.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const usage = `herdr-file-transfer — drop files from your desktop into herdr

  action open      what the herdr action runs: opens the popup on the folder
                   of the agent you're in
  action demo      the popup on a made-up Mac that isn't set up yet: the
                   connection checklist, without ssh
  picker [DIR]     the popup itself, on DIR (or $HERDR_FILE_TRANSFER_DIR, or here)
  senders          the machines dropped files would be fetched from, likeliest
                   first, as the popup finds them
  check            whether each sender can be fetched from: ssh gets in with a
                   key, tar is there, and a Mac lets ssh read the Desktop
  fetch SRC DIR    copy SRC from the likeliest sender into DIR, as a drop does
  handoff JSON     (internal) open the popup files were picked for, once this
                   one has closed
`

// dirEnv is how the action tells the popup where to open; paneEnv, the
// pane it was opened from: the agent ^e types a path into.
const (
	dirEnv  = "HERDR_FILE_TRANSFER_DIR"
	paneEnv = "HERDR_FILE_TRANSFER_PANE"
	// demoEnv opens the popup on a made-up Mac whose way back isn't set
	// up: the connection checklist, to look at, without ssh.
	demoEnv = "HERDR_FILE_TRANSFER_DEMO"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-file-transfer:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		if args[0] == "action" {
			notify("File transfer", cfgErr.Error())
		}
		return cfgErr
	}

	switch args[0] {
	case "action":
		if len(args) < 2 || (args[1] != "open" && args[1] != "demo") {
			return errors.New("unknown action")
		}
		env := map[string]string{dirEnv: startDir(), paneEnv: invocationContext().FocusedPaneID}
		if args[1] == "demo" {
			env[demoEnv] = "1"
		}
		err := openPopup("picker", "80%", "75%", env)
		if isHerdrCode(err, "ui_busy") {
			notify("File transfer", "Another popup is open: close it (esc) and try again.")
		}
		return err
	case "picker":
		dir := os.Getenv(dirEnv)
		if len(args) > 1 {
			dir = args[1]
		}
		return runPicker(ctx, cfg, usableDir(dir))
	case "senders":
		for _, s := range findSenders(ctx, cfg) {
			fmt.Printf("%-24s %s\n", s.Name, s.target(cfg, ""))
		}
		return nil
	case "check":
		return runCheck(ctx, cfg)
	case "fetch":
		if len(args) != 3 {
			return errors.New("fetch SRC DIR")
		}
		target := ""
		if ss := findSenders(ctx, cfg); len(ss) > 0 {
			target = ss[0].target(cfg, args[1])
		}
		got, err := fetch(ctx, target, args[1], usableDir(args[2]), nil, nil)
		if err != nil {
			return err
		}
		fmt.Println(got)
		return nil
	case "handoff":
		if len(args) != 2 {
			return errors.New("handoff needs a popup")
		}
		return runHandoff(args[1])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// startDir is where the popup opens: the focused pane's folder (the agent's),
// else its space's.
func startDir() string {
	inv := invocationContext()
	for _, d := range []string{inv.FocusedPaneCwd, inv.WorkspaceCwd} {
		if d != "" {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// usableDir is dir made absolute, or the nearest folder above it that's
// there, or home.
func usableDir(dir string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, _ = filepath.Abs(expandHome(dir))
	for d := dir; ; d = filepath.Dir(d) {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// runCheck checks each sender, and says what to fix.
func runCheck(ctx context.Context, cfg config) error {
	senders := findSenders(ctx, cfg)
	if len(senders) == 0 {
		fmt.Println(`No sender found: no herdr client is attached over ssh or mosh. Set "sender" in config.json.`)
		return nil
	}
	failed := 0
	for _, s := range senders {
		target := s.target(cfg, "")
		if err := probe(ctx, target); err != nil {
			failed++
			fmt.Printf("✗ %s (%s): %s\n", s.Name, target, err)
			continue
		}
		fmt.Printf("✓ %s (%s): ready\n", s.Name, target)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d senders can't be fetched from", failed, len(senders))
	}
	return nil
}
