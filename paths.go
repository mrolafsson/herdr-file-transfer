package main

import (
	"net/url"
	"strings"
)

// droppedPaths reads what a terminal pastes when files are dropped on it:
// one path or several, each quoted ('/a b.png'), backslash-escaped
// (/a\ b.png), bare, or as file:// URIs, one per line or space-separated.
// It returns nil when the text isn't paths: then it was typed, or pasted
// for the search.
func droppedPaths(text string) []string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r", "\n"))
	if text == "" {
		return nil
	}
	if strings.Contains(text, "file://") {
		var out []string
		for _, f := range strings.Fields(text) {
			u, err := url.Parse(f)
			if err != nil || u.Scheme != "file" || u.Path == "" {
				return nil
			}
			out = append(out, u.Path)
		}
		return out
	}
	if words, ok := shellWords(text); ok && allPaths(words) {
		return words
	}
	// A path with spaces, pasted as is: each line is one path.
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if allPaths(out) {
		return out
	}
	return nil
}

func allPaths(ps []string) bool {
	if len(ps) == 0 {
		return false
	}
	for _, p := range ps {
		if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") {
			return false
		}
		if strings.ContainsRune(p, 0) || strings.ContainsAny(p, "\n") {
			return false
		}
	}
	return true
}

// shellWords splits text as a POSIX shell does words: quotes and backslashes,
// no expansions. ok is false for an unclosed quote.
func shellWords(text string) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case r == '\\':
			inWord = true
			if i+1 < len(rs) {
				i++
				if rs[i] != '\n' {
					cur.WriteRune(rs[i])
				}
			}
		case r == '\'':
			inWord = true
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j == len(rs) {
				return nil, false
			}
			cur.WriteString(string(rs[i+1 : j]))
			i = j
		case r == '"':
			inWord = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && strings.ContainsRune("\"\\$`\n", rs[j+1]) {
					j++
				}
				cur.WriteRune(rs[j])
			}
			if j == len(rs) {
				return nil, false
			}
			i = j
		default:
			inWord = true
			cur.WriteRune(r)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./+@:,=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// remoteDir is a folder for the sender's shell: quoted, with a leading ~
// left to mean its home.
func remoteDir(dir string) string {
	if dir == "~" {
		return `"$HOME"`
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		return `"$HOME"/` + shellQuote(rest)
	}
	return shellQuote(dir)
}

// pathOwner is the user a dropped path belongs to, when it says:
// /Users/olaf/Desktop/x.png is olaf's.
func pathOwner(p string) string {
	for _, root := range []string{"/Users/", "/home/"} {
		if rest, ok := strings.CutPrefix(p, root); ok {
			if name, _, ok := strings.Cut(rest, "/"); ok && name != "" && name != "Shared" {
				return name
			}
		}
	}
	return ""
}
