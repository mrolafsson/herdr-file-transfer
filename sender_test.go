package main

import (
	"strings"
	"testing"
	"time"
)

func TestSSHClientIP(t *testing.T) {
	for in, want := range map[string]string{
		"100.110.188.60 59696 100.114.185.60 22": "100.110.188.60",
		"127.0.0.1 5000 127.0.0.1 22":            "",
		"10.0.0.1 5000 10.0.0.1 22":              "", // this machine to itself
		"":                                       "",
		"garbage":                                "",
	} {
		if got := sshClientIP(in); got != want {
			t.Errorf("sshClientIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSenderTarget(t *testing.T) {
	found := sender{Host: "100.1.2.3", Name: "macbook-pro"}
	if got := found.target(config{}, "/Users/olaf/Desktop/x.png"); got != "olaf@100.1.2.3" {
		t.Errorf("owner from path: %s", got)
	}
	if got := found.target(config{User: "me"}, "/Users/olaf/x.png"); got != "me@100.1.2.3" {
		t.Errorf("configured user: %s", got)
	}
	if got := found.target(config{}, "/tmp/x.png"); got != "100.1.2.3" {
		t.Errorf("no user: %s", got)
	}
	fixed := sender{Host: "laptop", Fixed: true}
	if got := fixed.target(config{User: "me"}, "/Users/olaf/x.png"); got != "laptop" {
		t.Errorf("configured sender: %s", got)
	}
}

func TestTailscaleMobile(t *testing.T) {
	if !(tailscalePeer{OS: "iOS"}).mobile() || (tailscalePeer{OS: "macOS"}).mobile() {
		t.Error("mobile")
	}
	if n := (tailscalePeer{DNSName: "macbook-pro.tail.ts.net.", HostName: "Olaf's MacBook Pro"}).name(); n != "macbook-pro" {
		t.Errorf("name %q", n)
	}
}

func TestLikeliest(t *testing.T) {
	now := time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC)
	addrs := []clientAddr{
		{IP: "ipad-idle", Started: now.Add(-24 * time.Hour), Typed: now.Add(-10 * time.Minute)},
		{IP: "mac-bridge", Started: now.Add(-time.Hour)},
		{IP: "ipad-typing", Started: now.Add(-48 * time.Hour), Typed: now.Add(-5 * time.Second)},
		{IP: "old-bridge", Started: now.Add(-3 * time.Hour)},
	}
	likeliest(addrs, now)
	var got []string
	for _, a := range addrs {
		got = append(got, a.IP)
	}
	if want := "ipad-typing mac-bridge old-bridge ipad-idle"; strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s", got, want)
	}
}
