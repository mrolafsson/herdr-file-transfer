package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// clientAddrs finds the herdr clients attached over ssh or mosh, by the
// SSH_CONNECTION each inherited, and when each last had a key pressed: its
// terminal's access time, which reading input updates (to within a few
// seconds, as `w` shows idle times). A client on a pipe (`herdr --remote`'s
// bridge) has none to tell. A mosh client has its
// bootstrap ssh's, which names the same machine. The server and the
// one-shot CLI commands are skipped: their env says nothing about who's
// attached now.
func clientAddrs() []clientAddr {
	procs, _ := filepath.Glob("/proc/[0-9]*")
	uid := uint32(os.Getuid())
	var out []clientAddr
	for _, dir := range procs {
		st, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); !ok || sys.Uid != uid {
			continue
		}
		comm, _ := os.ReadFile(dir + "/comm")
		if strings.TrimSpace(string(comm)) != "herdr" {
			continue
		}
		args, _ := os.ReadFile(dir + "/cmdline")
		if !isClientCmdline(bytes.Split(bytes.TrimRight(args, "\x00"), []byte{0})) {
			continue
		}
		env, _ := os.ReadFile(dir + "/environ")
		ip := ""
		for _, kv := range bytes.Split(env, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, []byte("SSH_CONNECTION=")); ok {
				ip = sshClientIP(string(v))
			}
		}
		if ip != "" {
			out = append(out, clientAddr{IP: ip, Started: procStart(dir), Typed: ttyTyped(dir)})
		}
	}
	return out
}

// ttyTyped is when a process's terminal last had input, or zero when its
// input isn't a terminal.
func ttyTyped(dir string) time.Time {
	in, err := os.Readlink(dir + "/fd/0")
	if err != nil || !strings.HasPrefix(in, "/dev/") {
		return time.Time{}
	}
	st, err := os.Stat(in)
	if err != nil {
		return time.Time{}
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return time.Unix(sys.Atim.Sec, sys.Atim.Nsec)
	}
	return time.Time{}
}

// isClientCmdline: `herdr`, `herdr --session x`, `herdr session attach x`,
// or the bridge `herdr --remote` runs.
func isClientCmdline(args [][]byte) bool {
	if len(args) < 2 {
		return true
	}
	switch a := string(args[1]); {
	case a == "remote-client-bridge", strings.HasPrefix(a, "-"):
		return true
	case a == "session":
		return len(args) > 2 && string(args[2]) == "attach"
	}
	return false
}

// procStart is when a process started: field 22 of its stat, in clock ticks
// since boot. Only the order matters, so ticks stand in for time.
func procStart(dir string) time.Time {
	data, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return time.Time{}
	}
	// The command name (field 2) can hold spaces and parens: count from
	// after its closing one.
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return time.Time{}
	}
	f := strings.Fields(string(data[i+1:]))
	if len(f) < 20 {
		return time.Time{}
	}
	ticks, _ := strconv.ParseInt(f[19], 10, 64)
	return time.Unix(ticks, 0)
}
