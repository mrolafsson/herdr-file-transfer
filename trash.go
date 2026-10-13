package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// ^d moves a file or folder to the trash, where a mistake can be taken back:
// on Linux the freedesktop one (~/.local/share/Trash, which file managers and
// `gio trash` share), on a Mac ~/.Trash. Something on another disk than the
// trash would have to be copied there, so it's deleted instead, and the
// question says so before you answer it.

// trashHome is the trash's folder: where trashed things go.
func trashHome() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash")
	}
	return filepath.Join(xdgDir("XDG_DATA_HOME", ".local/share"), "Trash")
}

// canTrash says whether p can be moved to the trash: they're on one disk.
func canTrash(p string) bool {
	st, err := os.Lstat(p)
	if err != nil {
		return false
	}
	// The trash, or the nearest folder above it that's there yet.
	t := trashHome()
	for {
		if tst, err := os.Stat(t); err == nil {
			return sameDevice(st, tst)
		}
		if t == filepath.Dir(t) {
			return false
		}
		t = filepath.Dir(t)
	}
}

func sameDevice(a, b fs.FileInfo) bool {
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Dev == sb.Dev
}

// toTrash moves p to the trash, or with trash false, deletes it.
func toTrash(p string, trash bool) error {
	if !trash {
		return os.RemoveAll(p)
	}
	if runtime.GOOS == "darwin" {
		dir := trashHome()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return os.Rename(p, freeName(dir, filepath.Base(p)))
	}
	// freedesktop: the thing in files/, and in info/ where it came from and
	// when, so it can be put back.
	files, info := filepath.Join(trashHome(), "files"), filepath.Join(trashHome(), "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	name := filepath.Base(p)
	for i := 2; ; i++ {
		_, errF := os.Lstat(filepath.Join(files, name))
		_, errI := os.Lstat(filepath.Join(info, name+".trashinfo"))
		if errors.Is(errF, os.ErrNotExist) && errors.Is(errI, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s.%d", filepath.Base(p), i)
	}
	record := filepath.Join(info, name+".trashinfo")
	abs, _ := filepath.Abs(p)
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: abs}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	// O_EXCL: the name is ours, not one another trashing just took.
	f, err := os.OpenFile(record, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(p, filepath.Join(files, name))
	}
	if err != nil {
		os.Remove(record)
	}
	return err
}

// countFiles is how many files are in a folder, up to limit.
func countFiles(dir string, limit int) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		if n >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return n
}
