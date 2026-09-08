# Omassh

A keyboard-driven SSH client for the terminal — Termius's data model, lazygit's
interaction model, running on top of real OpenSSH.

![Omassh](demo.gif)

## Status

Hosts and groups with attribute inheritance, embedded sessions and SFTP, plus
theming, rebindable keys and reachability probes.

## Keys

| key | |
|---|---|
| `j`/`k`, `tab`, `1`/`2` | move and switch panel |
| click | select a group or host, or focus the session pane |
| `enter` | connect — `ssh` takes the whole terminal, exit returns here |
| `/` | fuzzy search every host by name, address or tag |
| `n` / `e` / `d` | new / edit / delete, in a dialog over the list |
| paste | fills the focused field; works in search and in a session too |
| `↓` in a form | on Jump host, Group or Tags, pick what you already use |
| `space` in the Tags picker | toggle a tag; the list stays open, `↵` finishes |
| `p` | probe reachability of the hosts in this group |
| `ctrl+l` | redraw, if the terminal cleared the screen underneath |
| `t` | connect in the main pane instead, keeping the host list |
| `s` | sftp: browse and transfer files |
| `r` | reload the store from disk |
| `?` | help |

## Design

Omassh does not reimplement SSH. Interactive sessions are genuine OpenSSH
processes handed the real terminal via `tea.Exec`, so scrollback, `SIGWINCH`,
mouse reporting and full-screen remote programs behave exactly as they would
without Omassh in the picture. `ProxyJump`, certificates, `Match` blocks and
agent config are honoured because OpenSSH itself is honouring them.

`internal/sshx.Build` is the single place any ssh invocation is constructed —
sessions, probes and the native SFTP dialer all funnel through
it, so connection behaviour cannot drift between them.

Omassh stores no secrets and never asks for a passphrase. A host names a key
path, which becomes `ssh -i`; a passphrase-protected key is unlocked the usual
way, with `ssh-add` or `AddKeysToAgent yes` in your `~/.ssh/config`. Nothing
here duplicates what ssh-agent already does.

`enter` **hands the whole terminal to `ssh`**. Omassh releases the terminal,
the child gets the real stdin, stdout and stderr, and exiting drops you back
into the list. That path is emulation-free by construction — correct
`SIGWINCH`, your terminal's own scrollback, mouse and every escape sequence —
and it is the default for exactly that reason.

`t` opens the session in the **main pane** instead, keeping the host list
beside it. There is one such session at a time, so connecting somewhere else
replaces it rather than leaving a connection nothing in the interface can
reach; a green `●` marks the host it is on.

While that pane has focus every key goes to the remote, so `ctrl+\ w` hands
the keyboard back to the list — the session stays connected and visible —
`ctrl+\ d` detaches and `ctrl+\ X` ends it for good.

Sessions are **persistent** where tmux is installed. A pty whose master belongs
to Omassh dies with it, so each session is instead run as
`tmux new-session -A -s omassh-<host> ssh …` on a private tmux server — closing
Omassh detaches rather than disconnects, and reconnecting reattaches with the
screen and shell state intact. A yellow `●` beside a host means a session is
waiting — press `t` to reattach. Without tmux, sessions are ephemeral as
before. Those sessions live on their own server socket, so `tmux ls` in your
shell is unaffected.

Scrollback is `ctrl+\ k` and `ctrl+\ j` to page, `ctrl+\ G` to return live;
typing anything snaps back on its own, since a terminal that stayed scrolled
while you typed would hide your own output. For persistent sessions the history
belongs to tmux — 10000 lines, surviving restarts — and those keys drive its
copy mode. Without tmux the emulator keeps 2000 lines itself.

A pty runs `ssh`, its output feeds a VT emulator, and keys go back the other
way, so the remote gets a real terminal the size of the pane, `SIGWINCH` and
all.

Once a session has focus every keystroke belongs to the remote, `ctrl+c`
included — which is why the session commands sit behind a `ctrl+\` prefix, the
way tmux uses `ctrl+b`. Press the prefix twice to send a literal one through.

A jump host is named by picking one of your hosts, and the connection to it is
spelled out rather than left to `ssh -J`. `-J` hands the hop only `-l`, `-p`
and `-v`, so the jump host's own key would be silently ignored; Omassh emits
the `ProxyCommand` that `ssh` would build internally, with that host's port and
identity in it. A jump host that Omassh does not know — `ops@edge.example.com`
— is passed through as a plain `-J`, since `ssh` already understands it. Chains
work: a jump host may sit behind another.

SFTP needs no SSH client of its own. Omassh runs `ssh -s <host> sftp` and
speaks the SFTP protocol over that child's stdio, so OpenSSH performs the
connection exactly as it would for an interactive session — `ProxyJump`
chains, `ProxyCommand`, certificates, `Match` blocks, `IdentityAgent` and
`known_hosts` all apply, with no second implementation to keep in step.
`BatchMode` is forced on, because the child's stdin carries the protocol and
there is nowhere to prompt; `ssh-add` the key first if it has a passphrase.

## Install

```sh
brew install --cask cuonggt/tap/omassh
```

macOS and Linux, amd64 and arm64. Or from source:

```sh
go install github.com/cuonggt/omassh/cmd/omassh@latest
```

Releases are built with [GoReleaser](https://goreleaser.com) for macOS and
Linux on both amd64 and arm64. `goreleaser release --snapshot --clean` produces
the archives, checksums and a Homebrew cask locally without publishing.

The demo above is recorded with [VHS](https://github.com/charmbracelet/vhs):

```sh
./hack/demo.sh
```

That starts a throwaway SSH server on loopback so the session pane shows real
shells, seeds a database with sample infrastructure, and drives the UI.

## Run

```sh
go run ./cmd/omassh
```

`enter` connects. `?` lists keys. `q` quits.

`-o` passes an ssh option through to every connection, as `ssh -o` does:

```sh
go run ./cmd/omassh -o ConnectTimeout=5
```

## Configuration

Everything is optional — Omassh runs with no configuration at all. The config
file follows the platform convention, so it is
`~/Library/Application Support/omassh/config.yaml` on macOS and
`~/.config/omassh/config.yaml` on Linux — `omassh -h` prints the resolved path,
and so does the first line of `-print-config`. Writing to the wrong one is
silent, since a missing config is not an error, so let the shell work it out:

```sh
cfg=$(omassh -print-config | head -1 | cut -c3-)
mkdir -p "$(dirname "$cfg")" && omassh -print-config > "$cfg"
```

The database sits beside it, as `omassh.db`.

Themes (`tokyonight`, `gruvbox`, `nord`, `mono`, or your own palette), key
bindings and ssh options all live there. A malformed config
is reported at startup rather than ignored, because settings that silently do
nothing are worse than an error that says why. Arrow keys and `ctrl+c` are
reserved and always work, so no config can trap you in the program.

Colours degrade automatically: on a terminal without truecolor the palettes
render in 256 colours, and `mono` exists for terminals with less than that.

## If the screen goes blank

Some terminals clear the screen without telling the application — iTerm2's
`cmd+K` is the common one. Omassh's renderer still believes its last frame is
on screen and writes only the differences, so nothing reappears on its own.
`ctrl+l` forces a full repaint; from inside a session, where `ctrl+l` belongs
to the remote shell, use `ctrl+\ r`.

## Reachability

`p` probes the hosts in the current group with a plain TCP connection —
`●` up, `✖` down. Hosts behind a jump host or a `ProxyCommand` show `◌` and are
skipped rather than guessed at: their address means something only from the far
side of the proxy, so dialling it from here would report on a different machine
entirely.

## License

[MIT](LICENSE) © Cuong Giang
