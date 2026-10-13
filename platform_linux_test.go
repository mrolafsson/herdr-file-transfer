package main

import "testing"

func TestIsClientCmdline(t *testing.T) {
	split := func(args ...string) [][]byte {
		out := make([][]byte, len(args))
		for i, a := range args {
			out[i] = []byte(a)
		}
		return out
	}
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"herdr"}, true},
		{[]string{"herdr", "--session", "default"}, true},
		{[]string{"/home/x/.local/bin/herdr", "remote-client-bridge", "--idle-timeout-v1"}, true},
		{[]string{"herdr", "session", "attach", "work"}, true},
		{[]string{"herdr", "server"}, false},
		{[]string{"herdr", "workspace", "focus", "wF"}, false},
		{[]string{"herdr", "session", "list"}, false},
	} {
		if got := isClientCmdline(split(tc.args...)); got != tc.want {
			t.Errorf("isClientCmdline(%q) = %v", tc.args, got)
		}
	}
}
