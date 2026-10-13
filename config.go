package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	// Sender is the ssh target files are fetched from ("me@laptop", or a
	// Host from ~/.ssh/config). Empty = the machine of each herdr client
	// attached over ssh or mosh, newest first; tab in the popup switches.
	Sender string `json:"sender"`
	// User is who to log in as on a found sender. Empty = the owner of the
	// dropped path when it says (/Users/<name>/…, /home/<name>/…), else
	// ssh's own default.
	User string `json:"user"`
	// ShowHidden lists dotfiles and dot-folders too.
	ShowHidden bool `json:"show_hidden"`
	// SearchDepth is how many folders deep search looks under the one shown.
	SearchDepth int `json:"search_depth"`
	// Theme: "dark", "light", or empty to ask the terminal.
	Theme string `json:"theme"`
}

func pluginID() string {
	if id := os.Getenv("HERDR_PLUGIN_ID"); id != "" {
		return id
	}
	return "herdr-file-transfer"
}

func xdgDir(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func configDir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdr", "plugins", "config", pluginID())
}

// loadConfig reads config.json. On error it still returns the defaults.
func loadConfig() (config, error) {
	cfg := config{}
	data, err := os.ReadFile(filepath.Join(configDir(), "config.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return withDefaults(cfg), err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return withDefaults(config{}), errors.New("config.json: " + err.Error())
		}
	}
	return withDefaults(cfg), nil
}

func withDefaults(cfg config) config {
	cfg.Sender = strings.TrimSpace(cfg.Sender)
	cfg.User = strings.TrimSpace(cfg.User)
	if cfg.SearchDepth <= 0 {
		cfg.SearchDepth = 6
	}
	return cfg
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// tildify shows a path under the home folder as ~/….
func tildify(p string) string {
	home, _ := os.UserHomeDir()
	if home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+"/"); ok {
		return "~/" + rest
	}
	return p
}
