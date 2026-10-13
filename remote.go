package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The other way: a file or folder here, sent to the machine you're at, into
// a folder of its you pick by browsing it over ssh. The same tar stream as a
// drop, the other way round: `tar -c` here, over ssh, into `tar -x` there,
// in a hidden folder next to where it's going, moved into place once whole
// under a name nothing there has yet.

// sshArgs are the options every ssh to a sender runs with. One connection
// per machine is shared, kept a couple of minutes after the last use, so
// browsing its folders doesn't log in again at every step.
func sshArgs(target string) []string {
	return []string{
		"-o", "BatchMode=yes", // no password prompt inside the popup: a key, or a clear failure
		"-o", "ConnectTimeout=8",
		"-o", "ServerAliveInterval=15",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(os.TempDir(), "herdr-ft-"+strconv.Itoa(os.Getuid())+"-%C"),
		"-o", "ControlPersist=120",
		"-T", target, "--",
	}
}

func sshCmd(ctx context.Context, target, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh", append(sshArgs(target), script)...)
}

// remoteListing is a folder on the sender: where it is, its home (to show
// paths under it as ~/…), and what's in it.
type remoteListing struct {
	dir, home string
	entries   []entry
}

// listRemote lists dir on target: "~/Downloads", or a path.
func listRemote(ctx context.Context, target, dir string, hidden bool) (remoteListing, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// -p marks folders with a slash; -A leaves out . and ..
	script := "cd " + remoteDir(dir) + ` && printf '%s\n%s\n' "$PWD" "$HOME" && ls -1Ap`
	var stderr bytes.Buffer
	cmd := sshCmd(ctx, target, script)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return remoteListing{}, fmt.Errorf("can't reach %s: no answer", hostOf(target))
		}
		return remoteListing{}, explain(target, stderr.String(), err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var l remoteListing
	for i := 0; sc.Scan(); i++ {
		line := sc.Text()
		switch i {
		case 0:
			l.dir = line
			continue
		case 1:
			l.home = line
			continue
		}
		name, isDir := strings.CutSuffix(line, "/")
		if name == "" || strings.HasPrefix(name, ".herdr-file-transfer-") || (!hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		// No size: ls -1 doesn't say, and a wrong one is worse than none.
		l.entries = append(l.entries, entry{name: name, path: path.Join(l.dir, name), dir: isDir, size: -1})
	}
	if l.dir == "" {
		return l, errors.New("no answer from " + hostOf(target))
	}
	sortEntries(l.entries)
	return l, nil
}

// mkdirRemote makes a folder (and any between) in dir on target.
func mkdirRemote(ctx context.Context, target, dir, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := sshCmd(ctx, target, "cd "+remoteDir(dir)+" && mkdir -p "+shellQuote("./"+name)+" && cd "+shellQuote("./"+name)+` && printf '%s\n' "$PWD"`)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", explain(target, stderr.String(), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// sendTo copies src here into destDir on target, reporting its size and
// bytes as they go. It returns where it landed there.
func sendTo(ctx context.Context, target, src, destDir string, onTotal, progress func(int64)) (string, error) {
	src = strings.TrimRight(src, "/")
	base := filepath.Base(src)
	if base == "/" || base == "." || base == "" {
		return "", errors.New("can't send a whole disk")
	}
	if onTotal != nil {
		onTotal(diskSize(src))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pack := exec.CommandContext(ctx, "tar", "-C", filepath.Dir(src), "-cf", "-", "./"+base)
	var packErr, sshErr bytes.Buffer
	pack.Stderr = &packErr
	out, err := pack.StdoutPipe()
	if err != nil {
		return "", err
	}
	// There: unpack into a hidden folder, then move it in under a free name
	// ("hero 2.png" next to a hero.png), and say where.
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem, ext = base, ""
	}
	script := "set -e; cd " + remoteDir(destDir) + "; " +
		`t=$(mktemp -d ./.herdr-file-transfer-XXXXXX); trap 'rm -rf "$t"' EXIT; ` +
		`tar -xf - -C "$t"; ` +
		"f=" + shellQuote(base) + "; i=2; " +
		`while [ -e "$f" ] || [ -L "$f" ]; do f=` + shellQuote(stem) + `" $i"` + shellQuote(ext) + `; i=$((i+1)); done; ` +
		`mv "$t"/` + shellQuote(base) + ` "$f"; printf '%s/%s\n' "$PWD" "$f"`
	unpack := sshCmd(ctx, target, script)
	unpack.Stdin = &counter{r: out, report: progress}
	unpack.Stderr = &sshErr
	var landed bytes.Buffer
	unpack.Stdout = &landed

	if err := pack.Start(); err != nil {
		return "", err
	}
	if err := unpack.Start(); err != nil {
		cancel()
		_ = pack.Wait()
		return "", err
	}
	unpackDone := unpack.Wait()
	if unpackDone != nil {
		cancel() // don't leave tar blocked on a pipe nobody reads
	}
	packDone := pack.Wait()
	switch {
	case unpackDone != nil:
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) && packDone == nil {
			return "", ctx.Err()
		}
		return "", explain(target, sshErr.String(), unpackDone)
	case packDone != nil:
		return "", fmt.Errorf("packing: %s", lastLine(packErr.String(), packDone))
	}
	return strings.TrimSpace(landed.String()), nil
}

// diskSize is how many bytes src holds: a file's size, a folder's files'.
func diskSize(src string) int64 {
	var n int64
	_ = filepath.WalkDir(src, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// remoteTilde shows a path under home as ~/….
func remoteTilde(p, home string) string {
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
