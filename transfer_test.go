package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With no sender, a drop is fetched from this machine: the same tar pipe,
// without ssh.

func TestFetchFile(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	name := "it's a file.txt"
	if err := os.WriteFile(filepath.Join(src, name), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var seen, total int64
	got, err := fetch(context.Background(), "", filepath.Join(src, name), dest, func(n int64) { total = n }, func(n int64) { seen = n })
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dest, name) {
		t.Errorf("landed at %s", got)
	}
	if b, _ := os.ReadFile(got); string(b) != "hello" {
		t.Errorf("content %q", b)
	}
	if seen == 0 {
		t.Error("no progress reported")
	}
	if total <= 0 {
		t.Error("no size reported")
	}
	// Nothing left behind but the file.
	if des, _ := os.ReadDir(dest); len(des) != 1 {
		t.Errorf("dest holds %d entries", len(des))
	}

	// Again: it's kept, and the new one gets the next free name.
	got, err = fetch(context.Background(), "", filepath.Join(src, name), dest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "it's a file 2.txt" {
		t.Errorf("second copy is %s", filepath.Base(got))
	}
}

func TestFetchFolder(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "pics", "sub dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "pics", "sub dir", "a.png"), []byte("x"), 0o644)
	got, err := fetch(context.Background(), "", filepath.Join(src, "pics")+"/", dest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, "sub dir", "a.png")); err != nil {
		t.Error(err)
	}
}

func TestFetchMissing(t *testing.T) {
	dest := t.TempDir()
	_, err := fetch(context.Background(), "", "/nonexistent/x.png", dest, nil, nil)
	if err == nil {
		t.Fatal("no error")
	}
	if des, _ := os.ReadDir(dest); len(des) != 0 {
		t.Errorf("left %d entries behind", len(des))
	}
}

func TestFetchRefusesRoot(t *testing.T) {
	if _, err := fetch(context.Background(), "", "/", t.TempDir(), nil, nil); err == nil {
		t.Fatal("copied /")
	}
}

func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.png", "a 2.png", ".env", "Makefile"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	for in, want := range map[string]string{"a.png": "a 3.png", ".env": ".env 2", "Makefile": "Makefile 2", "b.png": "b.png"} {
		if got := filepath.Base(freeName(dir, in)); got != want {
			t.Errorf("freeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExplain(t *testing.T) {
	for stderr, want := range map[string]string{
		"olaf@100.1.2.3: Permission denied (publickey,password).":     "refused this server's ssh key",
		"ssh: connect to host 100.1.2.3 port 22: Connection refused":  "Remote Login",
		"Host key verification failed.":                               "ssh olaf@100.1.2.3 once by hand",
		"tar: x.png: Cannot stat: No such file or directory":          "no such file on 100.1.2.3",
		"tar: Desktop/x.png: Couldn't visit: Operation not permitted": "no such file on", // the visit failure names the file first
		"tar: x: Operation not permitted":                             "Full Disk Access",
	} {
		got := explain("olaf@100.1.2.3", stderr, errors.New("exit status 1")).Error()
		if !strings.Contains(got, want) {
			t.Errorf("explain(%q) = %q, want it to say %q", stderr, got, want)
		}
	}
}
