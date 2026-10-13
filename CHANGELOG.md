# Changelog

## 0.1.0 - 2026-10-13

- **Drop files on a popup** and they're copied, over ssh, from the machine
  you're at into the folder it shows: the agent's, to start. Files and
  folders, several at once; a taken name gets the next free one.
- **Browse and search folders**: `enter` in, `←` out, type to search every
  folder under the one shown, `^n` for a new one.
- **Machines found by themselves** from the herdr clients attached over ssh
  or mosh, by their Tailscale names, the one you're at first; `tab`
  switches, `"sender"` in config.json overrides.
- **The way back, checked.** When the server can't ssh back to your
  machine, the popup lists what that takes, ticks what works, and says how
  to do the rest, with keys for a key (`^k`), the host key (`^a`) and the
  command to run on your machine (`^y`). `herdr-file-transfer check` does
  it from the command line.
- **iPad and phone**: says dropping can't work from them, and receives
  Taildrop files instead (`^t`).
- **Progress bars**: a dropped file is listed in its folder at once, with a
  spinner and a bar (its size is asked for first).
- **Images dropped or pasted on herdr** (which brings them over itself) are
  copied without ssh, named `image-<when>.png`.
- **`^d` moves to the trash**, after a `y`: the freedesktop trash on Linux,
  `~/.Trash` on a Mac; on another disk, it says it'll delete for good.
- **Send to your machine**: `^s` on a file or folder here lists your
  machine's folders over ssh (from `~/Downloads`, or where you last sent);
  `^s` again sends it there, with a progress row. `^n` makes a folder there.
- `tab` names the machine it switches to, and ssh shares one connection per
  machine, so browsing it is quick.
- **A tab per machine**: the server's folders and your machine's; `^s`
  sends what's selected to the folder shown on the other. `tab` no longer
  cycles through machines: it's the one you're at.
- **`^e` hands a path to the agent** you opened the popup from (typed, not
  sent), and `^y` copies it. A finished drop is selected, ready for `^e`.
- **Two panes** in a popup 110 columns wide or more: the server's folders
  and your machine's, side by side; `tab` moves between them, `^s` sends
  to the other. Narrower, they're tabs.
- **Mark several** with `space` (`^a` all): `^s send 3 to macbook-pro`,
  `^e`, `^y` and `^d` take them all.
- Until the way back works, the checklist has the whole popup; the panes
  come once it does. *File transfer: demo (connection setup)* shows it on a
  made-up Mac.

