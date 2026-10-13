package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// probeScript checks, on the sender, everything a drop will need: a shell to
// log in to, tar, and (on a Mac) that ssh may read the Desktop, which macOS
// keeps from it without Full Disk Access.
const probeScript = `command -v tar >/dev/null 2>&1 || { echo "tar: not found on this machine" >&2; exit 3; }
if [ -d "$HOME/Desktop" ]; then ls "$HOME/Desktop" >/dev/null || exit 4; fi
echo ok`

// probe checks that target can be fetched from: ssh gets in with a key, and
// what a drop needs is there. nil means a drop will work.
func probe(ctx context.Context, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := sshCmd(ctx, target, probeScript)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return &setupError{step: stepReach, msg: fmt.Sprintf("can't reach %s: no answer", hostOf(target))}
	}
	if err != nil {
		err = explain(target, stderr.String(), err)
		// With no key here, getting in was never going to work: say that
		// first.
		var se *setupError
		if errors.As(err, &se) && !hasKey() {
			se.noKey = true
		}
		return err
	}
	if !bytes.Contains(out, []byte("ok")) {
		return errors.New("unexpected answer from " + hostOf(target))
	}
	return nil
}

func hostOf(target string) string {
	if _, h, ok := strings.Cut(target, "@"); ok {
		return h
	}
	return target
}

func userOf(target string) string {
	if u, _, ok := strings.Cut(target, "@"); ok {
		return u
	}
	return ""
}

// The user a drop got in as, by sender address: who to log in as when there
// is no dropped path to tell (the check, a drop from outside /Users), and
// why the check can pass before the first drop.

func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgDir("XDG_STATE_HOME", ".local/state"), "herdr", "plugins", pluginID())
}

func usersFile() string { return filepath.Join(stateDir(), "users.json") }

func rememberedUsers() map[string]string {
	users := map[string]string{}
	if data, err := os.ReadFile(usersFile()); err == nil {
		_ = json.Unmarshal(data, &users)
	}
	return users
}

// rememberUser notes that target's user got in at its host.
func rememberUser(target string) {
	user, host := userOf(target), hostOf(target)
	if user == "" {
		return
	}
	users := rememberedUsers()
	if users[host] == user {
		return
	}
	users[host] = user
	data, _ := json.MarshalIndent(users, "", "  ")
	if os.MkdirAll(stateDir(), 0o700) == nil {
		tmp := usersFile() + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, usersFile())
		}
	}
}
