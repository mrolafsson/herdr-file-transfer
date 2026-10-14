# Changelog

## 0.4.1 - 2026-10-14

- A file sent to an iPad is said to land where it does: iCloud Drive ›
  Downloads, not Files › Tailscale.

## 0.4.0 - 2026-10-14

- **`^s` sends to an iPad or phone**, with Taildrop: what's marked on the
  server goes to Files › Tailscale there, with progress as it goes. Files
  only; a folder says so. The `→` between the panes is back for it.

## 0.3.0 - 2026-10-13

- **Picks files for the other herdr plugins**, starting with herdr-linear's
  attachments: opened by another plugin, the popup marks what you bring
  over, keeps marks from folder to folder, and `enter` hands the files back
  to that plugin's popup (`esc`, none). The convention is in the README.
- The `→` between the panes is gone for an iPad: nothing's sent to one.
- Taildrop's waiting is said once: the iPad's pane says it side by side, the
  status line only with tabs.

## 0.2.0 - 2026-10-13

- **iPad and phone, beside the folders**: this server's folders on one
  side, Taildrop on the other (a tab away when narrow), so you choose where
  what's shared lands.
- **Taildrop waits by itself** from the moment the popup opens, into the
  folder shown, and follows you to another. No more `^t` to start and stop:
  it's only there to try again after an error. A spinner says it's waiting,
  on the iPad's tab and in the status line.
- **`^y` copies the Tailscale operator command** when the server's
  Tailscale won't hand files over.
- A copied command's "sent to your clipboard" now shows on your machine's
  side too.

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

