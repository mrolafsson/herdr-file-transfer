package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// herdrError is a refusal from the herdr server, as opposed to a transport failure.
type herdrError struct {
	Code    string
	Message string
}

func (e *herdrError) Error() string { return e.Code + ": " + e.Message }

func socketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "herdr", "herdr.sock")
}

// herdrCallFn is the herdr socket, a seam for tests.
var herdrCallFn = herdrSocketCall

func herdrCall(method string, params any, out any) error { return herdrCallFn(method, params, out) }

// herdrSocketCall sends one request and decodes `result` into out. The
// server answers a single line per connection and hangs up.
func herdrSocketCall(method string, params any, out any) error {
	conn, err := net.DialTimeout("unix", socketPath(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if params == nil {
		params = map[string]any{}
	}
	req, err := json.Marshal(map[string]any{"id": "herdr-file-transfer", "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReaderSize(conn, 1<<20).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return fmt.Errorf("herdr %s: bad reply: %w", method, err)
	}
	if reply.Error != nil {
		return &herdrError{reply.Error.Code, reply.Error.Message}
	}
	if out != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, out)
	}
	return nil
}

func isHerdrCode(err error, code string) bool {
	var he *herdrError
	return errors.As(err, &he) && he.Code == code
}

func notify(title, body string) {
	_ = herdrCall("notification.show", map[string]any{"title": title, "body": body}, nil)
}

// invocation is what herdr tells an action about where it was invoked from.
type invocation struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceCwd   string `json:"workspace_cwd"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCwd string `json:"focused_pane_cwd"`
}

func invocationContext() invocation {
	var inv invocation
	_ = json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &inv)
	return inv
}

// openPopup opens one of this plugin's pane entrypoints as a popup. The menu the
// action was picked from may still be closing, so ui_busy is retried briefly.
func openPopup(entrypoint, width, height string, env map[string]string) error {
	params := map[string]any{
		"plugin_id": pluginID(), "entrypoint": entrypoint, "placement": "popup",
		"focus": true, "width": width, "height": height, "env": env,
	}
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = herdrCall("plugin.pane.open", params, nil); err == nil || !isHerdrCode(err, "ui_busy") {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
	return err
}
