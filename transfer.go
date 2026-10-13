package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A file or folder is copied as a tar stream: `tar -c` on the sender, over
// ssh, into `tar -x` here. Unlike scp it takes folders, odd names and
// macOS's paths as they are, and the bytes can be counted as they come.
// It lands in a hidden folder next to where it's going, and is moved into
// place only once whole, under a name nothing there has yet.

// fetchCmd is what produces the tar stream of src: ssh to the sender, or
// with no sender this machine's own tar.
func fetchCmd(ctx context.Context, target, src string) *exec.Cmd {
	dir, base := path.Split(strings.TrimRight(src, "/"))
	if dir == "" {
		dir = "/"
	}
	// Its size first, on stderr, for the progress bar: du's, in KB, near
	// enough to what tar sends. COPYFILE_DISABLE: a Mac's tar would
	// otherwise add ._ files for extended attributes. "./": a name that
	// starts with a dash isn't an option.
	name := shellQuote("./" + base)
	script := "cd " + remoteDir(strings.TrimRight(dir, "/")+"/") + " || exit 1; " +
		`echo "` + sizeMark + `$(du -sk ` + name + ` 2>/dev/null | cut -f1)" >&2; ` +
		"COPYFILE_DISABLE=1 tar -cf - " + name
	if target == "" {
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	return sshCmd(ctx, target, script)
}

// extractArgs are the flags for `tar -x` here. GNU tar warns about the
// headers a Mac's tar adds; they're nothing to show.
var extractArgs = sync.OnceValue(func() []string {
	args := []string{"-xf", "-"}
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("tar", "--version").Output(); err == nil && bytes.Contains(out, []byte("GNU tar")) {
			args = append(args, "--warning=no-unknown-keyword", "--no-same-owner")
		}
	}
	return args
})

// fetch copies src (on target, or here when target is "") into destDir,
// reporting its size once known, and bytes as they arrive. It returns where
// it put it.
func fetch(ctx context.Context, target, src, destDir string, onTotal, progress func(int64)) (string, error) {
	base := path.Base(strings.TrimRight(src, "/"))
	name := base
	if target == "" && fromHerdr(src) {
		name = herdrImageName(src)
	}
	if base == "/" || base == "." || base == "" || base == ".." {
		return "", errors.New("can't copy a whole disk")
	}
	tmp, err := os.MkdirTemp(destDir, ".herdr-file-transfer-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	send := fetchCmd(ctx, target, src)
	var recvErr bytes.Buffer
	sendErr := &sizeSniffer{onTotal: onTotal}
	send.Stderr = sendErr
	out, err := send.StdoutPipe()
	if err != nil {
		return "", err
	}
	recv := exec.CommandContext(ctx, "tar", append(extractArgs(), "-C", tmp)...)
	recv.Stdin = &counter{r: out, report: progress}
	recv.Stderr = &recvErr
	if err := send.Start(); err != nil {
		return "", err
	}
	if err := recv.Start(); err != nil {
		cancel()
		_ = send.Wait()
		return "", err
	}
	recvDone := recv.Wait()
	if recvDone != nil {
		cancel() // don't leave ssh blocked on a pipe nobody reads
	}
	sendDone := send.Wait()
	switch {
	case ctx.Err() != nil && recvDone == nil && sendDone != nil:
		return "", ctx.Err()
	case sendDone != nil:
		sendErr.buf.Write(sendErr.partial)
		return "", explain(target, sendErr.buf.String(), sendDone)
	case recvDone != nil:
		return "", fmt.Errorf("unpacking: %s", lastLine(recvErr.String(), recvDone))
	}

	got := filepath.Join(tmp, base)
	if _, err := os.Lstat(got); err != nil {
		return "", errors.New("nothing arrived")
	}
	final := freeName(destDir, name)
	if err := os.Rename(got, final); err != nil {
		return "", err
	}
	return final, nil
}

// freeName is dir/name, or "name 2.ext", "name 3.ext"… if that's taken.
func freeName(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" { // a dotfile: ".env" is all stem
		stem, ext = name, ""
	}
	for i := 2; ; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s %d%s", stem, i, ext))
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}

// explain turns ssh's or the sender's tar's complaint into what to do. One
// about the connection is a *setupError, naming the setup step it's about.
func explain(target, stderr string, err error) error {
	low := strings.ToLower(stderr)
	host := hostOf(target)
	switch {
	case strings.Contains(low, "permission denied (") || strings.Contains(low, "too many authentication failures"):
		return &setupError{step: stepAuth, msg: fmt.Sprintf("%s refused this server's ssh key (add it to that machine's ~/.ssh/authorized_keys)", target)}
	case strings.Contains(low, "connection refused"):
		return &setupError{step: stepSSHD, msg: fmt.Sprintf("%s isn't taking ssh (on a Mac: System Settings › Sharing › Remote Login)", host)}
	case strings.Contains(low, "timed out") || strings.Contains(low, "no route to host") || strings.Contains(low, "could not resolve"):
		return &setupError{step: stepReach, msg: fmt.Sprintf("can't reach %s", host)}
	case strings.Contains(low, "host key verification failed"):
		return &setupError{step: stepHostKey, msg: fmt.Sprintf("%s's host key isn't known here: ssh %s once by hand", host, target)}
	case strings.Contains(low, "no such file") || strings.Contains(low, "cannot stat") || strings.Contains(low, "couldn't visit") || strings.Contains(low, "not found in archive"):
		if host == "" {
			return errors.New("no such file here")
		}
		return fmt.Errorf("no such file on %s", host)
	case strings.Contains(low, "could not chdir") || strings.Contains(low, "cannot change") || strings.Contains(low, "can't cd"):
		if host == "" {
			return errors.New("no such folder here")
		}
		return fmt.Errorf("no such folder on %s", host)
	case strings.Contains(low, "operation not permitted"):
		// macOS keeps Desktop, Documents and Downloads from sshd until it's
		// given Full Disk Access.
		return &setupError{step: stepDesktop, msg: fmt.Sprintf("%s won't let ssh read it (on a Mac: give Remote Login Full Disk Access)", host)}
	}
	return errors.New(lastLine(stderr, err))
}

func lastLine(s string, err error) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// counter reports how many bytes have passed through it so far.
type counter struct {
	r      io.Reader
	n      atomic.Int64
	report func(int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.report != nil {
		c.report(c.n.Add(int64(n)))
	}
	return n, err
}

// herdr brings an image you drop or paste over itself: its client sends it,
// and the server saves it in a folder of its own and pastes that path. Such
// a file is here already, whoever's attached: no ssh to fetch it with.
func herdrImageDir() string {
	return filepath.Join(os.TempDir(), "herdr-clipboard-images-"+strconv.Itoa(os.Getuid()))
}

// fromHerdr says whether src is an image herdr has brought here.
func fromHerdr(src string) bool {
	dir := herdrImageDir()
	if filepath.Dir(src) != dir && filepath.Dir(src) != filepath.Join("/tmp", filepath.Base(dir)) {
		return false
	}
	_, err := os.Stat(src)
	return err == nil
}

// herdrImageName is a name for an image herdr brought, whose own
// ("client-146-clipboard-1791910811413384621-0.png") says nothing: when it
// came.
func herdrImageName(src string) string {
	when := time.Now()
	if st, err := os.Stat(src); err == nil {
		when = st.ModTime()
	}
	ext := filepath.Ext(src)
	if ext == "" {
		ext = ".png"
	}
	return "image-" + when.Format("2006-01-02-150405") + ext
}

// sizeMark starts the line the sending side says its size on.
const sizeMark = "herdr-size "

// sizeSniffer is the sending side's stderr: the size line goes to onTotal,
// the rest is kept to explain a failure with.
type sizeSniffer struct {
	buf     bytes.Buffer
	partial []byte
	onTotal func(int64)
}

func (s *sizeSniffer) Write(p []byte) (int, error) {
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		line := s.partial[:i+1]
		if kb, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte(sizeMark)); ok {
			if n, err := strconv.ParseInt(string(kb), 10, 64); err == nil && s.onTotal != nil {
				s.onTotal(n * 1024)
			}
		} else {
			s.buf.Write(line)
		}
		s.partial = s.partial[i+1:]
	}
	return len(p), nil
}
