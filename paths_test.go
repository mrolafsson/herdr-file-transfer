package main

import (
	"reflect"
	"testing"
)

func TestDroppedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []string
	}{
		{"bare", "/Users/olaf/Desktop/shot.png", []string{"/Users/olaf/Desktop/shot.png"}},
		{"trailing space (iTerm2, Ghostty)", "/Users/olaf/a.png ", []string{"/Users/olaf/a.png"}},
		{"backslash escapes", `/Users/olaf/Desktop/Screen\ Shot\ 1.png`, []string{"/Users/olaf/Desktop/Screen Shot 1.png"}},
		{"single quotes", `'/Users/olaf/My File.png' '/Users/olaf/b.txt'`, []string{"/Users/olaf/My File.png", "/Users/olaf/b.txt"}},
		{"quote in a name", `'/tmp/it'\''s.png'`, []string{"/tmp/it's.png"}},
		{"double quotes", `"/tmp/a b.png"`, []string{"/tmp/a b.png"}},
		{"several escaped", `/tmp/a\ 1.png /tmp/b\ 2.png`, []string{"/tmp/a 1.png", "/tmp/b 2.png"}},
		{"file URIs", "file:///Users/olaf/My%20File.png\nfile:///tmp/b.png", []string{"/Users/olaf/My File.png", "/tmp/b.png"}},
		{"file URI with host", "file://macbook/Users/olaf/x.png", []string{"/Users/olaf/x.png"}},
		{"unescaped spaces, one per line", "/Users/olaf/My File.png\n/tmp/Other One.txt", []string{"/Users/olaf/My File.png", "/tmp/Other One.txt"}},
		{"home", "~/Desktop/x.png", []string{"~/Desktop/x.png"}},
		{"a search, not a path", "assets", nil},
		{"words", "src components", nil},
		{"relative", "./x.png", nil},
		{"empty", "  \n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := droppedPaths(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("droppedPaths(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRemoteDirAndQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/olaf/":      "/Users/olaf/",
		"/Users/o laf/":     `'/Users/o laf/'`,
		"~/":                `"$HOME"/''`,
		"~/Desk top/":       `"$HOME"/'Desk top/'`,
		"/tmp/it's/":        `'/tmp/it'\''s/'`,
		"/tmp/$(rm -rf ~)/": `'/tmp/$(rm -rf ~)/'`,
		"/tmp/`whoami`/x/":  "'/tmp/`whoami`/x/'",
	} {
		if got := remoteDir(in); got != want {
			t.Errorf("remoteDir(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPathOwner(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/olaf/Desktop/x.png": "olaf",
		"/home/jo/x":                "jo",
		"/Users/Shared/x":           "",
		"/tmp/x":                    "",
		"~/x":                       "",
	} {
		if got := pathOwner(in); got != want {
			t.Errorf("pathOwner(%q) = %q, want %q", in, got, want)
		}
	}
}
