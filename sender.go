package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// sender is a machine dropped files are fetched from: one a herdr client is
// attached from, or the one config.json names.
type sender struct {
	Host  string // what ssh connects to: an address, or the configured target
	Name  string // what the popup calls it
	Fixed bool   // the configured target, user and all
	User  string // who a drop last got in as there, for want of a better clue
	// Device is what Tailscale says it is ("iPad", "macOS"), when it says.
	Device string
	// Mobile: a phone or tablet, which runs no ssh server to fetch from.
	Mobile bool
}

// target is the ssh destination for fetching src: a found sender's address,
// with the user from config, or else the one src's path is owned by, or else
// the one a drop last got in as.
func (s sender) target(cfg config, src string) string {
	if s.Fixed {
		return s.Host
	}
	user := cfg.User
	if user == "" {
		user = pathOwner(src)
	}
	if user == "" {
		user = s.User
	}
	if user != "" {
		return user + "@" + s.Host
	}
	return s.Host
}

// clientAddr is the address a herdr client's ssh (or mosh) session came from.
type clientAddr struct {
	IP      string
	Started time.Time
	Typed   time.Time // its terminal's last input; zero when it isn't one
}

// justTyped is how recently a client's terminal must have had a key pressed
// to be the one that opened the popup: the key that did is that recent,
// give or take the terminal's few seconds of rounding.
const justTyped = 30 * time.Second

// likeliest orders clients by how likely you are at each now: one that just
// had a key pressed (the one that opened the popup), then one that can't
// say (a pipe: `herdr --remote` from a laptop), newest first, then the idle
// ones, the least idle first.
func likeliest(addrs []clientAddr, now time.Time) {
	rank := func(a clientAddr) int {
		switch {
		case !a.Typed.IsZero() && now.Sub(a.Typed) < justTyped:
			return 0
		case a.Typed.IsZero():
			return 1
		}
		return 2
	}
	sort.SliceStable(addrs, func(i, j int) bool {
		ri, rj := rank(addrs[i]), rank(addrs[j])
		switch {
		case ri != rj:
			return ri < rj
		case ri == 1:
			return addrs[i].Started.After(addrs[j].Started)
		}
		return addrs[i].Typed.After(addrs[j].Typed)
	})
}

// findSenders lists who files may come from, the likeliest first: the
// configured sender, then each machine a herdr client is attached from, the
// one you're at first. A phone or tablet is listed, as one that can't be
// fetched from, so the popup can say so when you're on it.
func findSenders(ctx context.Context, cfg config) []sender {
	var out []sender
	if cfg.Sender != "" {
		out = append(out, sender{Host: cfg.Sender, Name: cfg.Sender, Fixed: true})
	}
	addrs := clientAddrs()
	likeliest(addrs, time.Now())
	// This process's own: the server's, from whichever ssh session started
	// it. Possibly long gone, so last.
	if ip := sshClientIP(os.Getenv("SSH_CONNECTION")); ip != "" {
		addrs = append(addrs, clientAddr{IP: ip})
	}
	peers := tailscalePeers(ctx)
	users := rememberedUsers()
	seen := map[string]bool{}
	for _, a := range addrs {
		if seen[a.IP] {
			continue
		}
		seen[a.IP] = true
		s := sender{Host: a.IP, Name: a.IP}
		if p, ok := peers[a.IP]; ok {
			s.Name, s.Device, s.Mobile = p.name(), p.device(), p.mobile()
			// ssh to the name when MagicDNS has it: what ssh says, and
			// known_hosts keeps, reads as the machine, not a number.
			if resolvesTo(ctx, s.Name, a.IP) {
				s.Host = s.Name
			}
		}
		s.User = users[s.Host]
		out = append(out, s)
	}
	return out
}

// sshClientIP is the client's address in an SSH_CONNECTION value
// ("client port server port"), unless it's this machine.
func sshClientIP(v string) string {
	f := strings.Fields(v)
	if len(f) != 4 || f[0] == f[2] || f[0] == "127.0.0.1" || f[0] == "::1" {
		return ""
	}
	return f[0]
}

type tailscalePeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	OS           string   `json:"OS"`
	TailscaleIPs []string `json:"TailscaleIPs"`
}

// name is the peer's MagicDNS name ("macbook-pro"), or its host name.
func (p tailscalePeer) name() string {
	if n, _, _ := strings.Cut(p.DNSName, "."); n != "" {
		return n
	}
	return p.HostName
}

// device is what kind of machine the peer is, as you'd call it.
func (p tailscalePeer) device() string {
	n := strings.ToLower(p.DNSName + " " + p.HostName)
	switch {
	case strings.Contains(n, "ipad"):
		return "iPad"
	case strings.Contains(n, "iphone"):
		return "iPhone"
	case strings.EqualFold(p.OS, "ios"):
		return "iPhone or iPad"
	case strings.EqualFold(p.OS, "android"):
		return "Android device"
	case strings.EqualFold(p.OS, "macos"):
		return "Mac"
	case strings.EqualFold(p.OS, "windows"):
		return "Windows PC"
	}
	return p.OS
}

func (p tailscalePeer) mobile() bool {
	switch strings.ToLower(p.OS) {
	case "ios", "android":
		return true
	}
	return false
}

// tailscalePeers maps each tailnet address to its machine, so a sender has a
// name. Without tailscale, an empty map.
func tailscalePeers(ctx context.Context) map[string]tailscalePeer {
	out := map[string]tailscalePeer{}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return out
	}
	var st struct {
		Peer map[string]tailscalePeer `json:"Peer"`
	}
	if json.Unmarshal(data, &st) != nil {
		return out
	}
	for _, p := range st.Peer {
		for _, ip := range p.TailscaleIPs {
			out[ip] = p
		}
	}
	return out
}

// resolvesTo says whether name resolves, here, to ip.
func resolvesTo(ctx context.Context, name, ip string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, name)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a == ip {
			return true
		}
	}
	return false
}
