# Omassh

A keyboard-driven SSH client for the terminal — Termius's data model, lazygit's
interaction model, running on top of real OpenSSH.

![Omassh](demo.gif)

## Status

Hosts and groups with attribute inheritance, embedded sessions, SFTP and port
forwarding, plus theming, rebindable keys, reachability probes, and a text
import/export for moving the list between machines or starting from
`~/.ssh/config`.

## Keys

| key | |
|---|---|
| `j`/`k`, `tab`, `1`/`2` | move and switch panel |
| click | select a group, host or file, or focus the session pane |
| double click | in sftp, enter the directory under the pointer |
| `enter` | connect — `ssh` takes the whole terminal, exit returns here |
| `/` | fuzzy search every host by name, address or tag |
| `n` / `e` / `d` | new / edit / delete, in a dialog over the list |
| paste | fills the focused field; works in search and in a session too |
| `↓` in a form | pick what you already use — a host's jump host, group or tags, a group's parent or jump host |
| `space` in the Tags picker | toggle a tag; the list stays open, `↵` finishes |
| `p` | probe reachability of the hosts in this group |
| `ctrl+l` | redraw, if the terminal cleared the screen underneath |
| `t` | connect in the main pane instead, keeping the host list |
| `s` | sftp: browse and transfer files |
| `f` | port forwarding: tunnels that outlive the window |
| `T` | pick a theme, previewing as you move |
| `r` | reload the store from disk |
| `?` | help, which names the running version |

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
the child gets the real stdin and stdout, and exiting drops you back into the
list. That path is emulation-free by construction — correct `SIGWINCH`, your
terminal's own scrollback, mouse and every escape sequence — and it is the
default for exactly that reason.

Its stderr is the one thing Omassh listens in on, keeping the last few lines so
that a session which fails can say why. Everything still reaches the terminal
unchanged; it is only copied on the way. Without it, ssh wrote the reason to a
screen the interface immediately painted over, and a connection refused, a key
rejected, a host key that had changed and a timeout all read alike — `exited
255`, with the reason visible only after quitting. The exit code is still
reported, since that is the certain part; the last line of stderr follows it as
context rather than as a claim about the cause. Passphrase and host-key prompts
are unaffected: ssh puts those on `/dev/tty`, not stderr.

`t` opens the session in the **main pane** instead, keeping the host list
beside it. There is one such session at a time, so connecting somewhere else
replaces it rather than leaving a connection nothing in the interface can
reach; a green `●` marks the host it is on.

While that pane has focus every key goes to the remote, so `ctrl+\ w` hands
the keyboard back to the list — the session stays connected and visible —
`ctrl+\ d` detaches and `ctrl+\ X` ends it for good.

The prefix works from the host list as well. Detaching and ending are facts
about the session rather than about whichever panel holds the keyboard, and
reaching them used to mean going back into the pane first — where the prefix
was silently ignored on the way, so `ctrl+\ d` on the list opened the delete
confirmation for the highlighted host. `t` on the host already connected
returns to its pane rather than building a second session over the top.

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
— is passed through as a plain `-J`, since `ssh` already understands it.

Chains work: a jump host may sit behind another, and each hop is told the
address of the next one rather than being left to work it out. `ssh` expands
`%h` and `%p` across a whole `ProxyCommand`, nested levels included, so writing
them would hand every hop in a chain the *final* destination — the first hop
would dial it directly and the hosts in between would never be contacted.
Omassh knows every address in the chain, so it writes each one.

SFTP needs no SSH client of its own. Omassh runs `ssh -s <host> sftp` and
speaks the SFTP protocol over that child's stdio, so OpenSSH performs the
connection exactly as it would for an interactive session — `ProxyJump`
chains, `ProxyCommand`, certificates, `Match` blocks, `IdentityAgent` and
`known_hosts` all apply, with no second implementation to keep in step.
`BatchMode` is forced on, because the child's stdin carries the protocol and
there is nowhere to prompt; `ssh-add` the key first if it has a passphrase.

## Port forwarding

`f` lists the tunnels belonging to a host, and `↵` starts or stops the
highlighted one. Local (`-L`), remote (`-R`) and dynamic (`-D`) are all here,
written the way ssh writes them — `5432` and `db.internal:5432` are the two
halves of `-L 5432:db.internal:5432`.

A tunnel runs as a detached session on the same private tmux server the
interactive sessions use, so it **outlives the window that started it**. That
is the whole point: a forward has nothing to look at, and the value of it is
that it keeps running. Quitting Omassh leaves your database tunnel up; opening
Omassh again shows it as up, because it is.

Everything about reaching the host is `sshx.Build`'s answer, the same as for a
session, so a tunnel to a host behind a bastion goes through the bastion and
one whose group supplies a user connects as that user. On top of that a forward
adds only what makes a connection a tunnel: `-N`, `ExitOnForwardFailure` so a
port that cannot be bound is a failure rather than a connection carrying
nothing, and `ServerAliveInterval` so a tunnel whose network went away is
reported as stopped instead of holding its port and reading as up.

`BatchMode` and `ExitOnForwardFailure` are forced on, ahead of anything `-o`
passes in, because nobody is watching: a passphrase or host-key prompt in a
detached session waits for an answer that is never coming, and a connection
that could not bind its port carries nothing. Either way "up" would mean
something other than a tunnel — with `-o BatchMode=no` on omassh's own command
line, a forward to a host that refused the key sat at a password prompt while
the interface reported it running. They are not preferences competing with
yours; they are what makes `▶` mean anything. `ssh-add` the key first, and
connect once interactively to accept an unknown host key. The keepalives are a
preference, and stay yours to tune.

The local port is bound here before ssh is asked to bind it, so a port already
in use is a sentence naming the port rather than a tunnel that vanishes a
moment after starting. A tunnel that stops for any other reason keeps its
session, dead, so it can still say what ssh said — `✖` in the list, with the
reason beneath it. Without that the failure would be indistinguishable from a
tunnel nobody had started.

Editing a rule reaches nothing already running — the tunnel is named by the
rule's id, so it goes on carrying the route it was started with. It used to go
on being reported as up against the new route as well, which is the shape of
mistake where repointing a tunnel at staging leaves every connection landing on
production and the screen agreeing with you. Each tunnel now records the whole
invocation it was started with, so one whose rule *or host* has changed
underneath it shows `▷` rather than `▶` — editing the host's address moves a
tunnel just as surely as editing the rule — and `↵` restarts it on what they
say now.

A green `▶` beside a host in the list means one of its tunnels is up.

## Moving between machines

The store is bbolt — one binary file, with ids minted locally. Copying it over
another machine's replaces that machine's list rather than joining it, and two
machines that each gained a host cannot be reconciled by copying in either
direction. The text form is the way across:

```sh
omassh export > hosts.yaml                # or -o hosts.yaml, written 0600
omassh import hosts.yaml                  # stdin when given no file
omassh export | ssh other-machine omassh import
```

Records match **by name**, never by id. That is what makes the same list
mergeable at both ends, and it means a host keeps the session history hanging
off it across an import — the host you have connected to forty times is still
that host afterwards. A field the document leaves out keeps the value already
stored, so importing fills in and corrects but never blanks; clearing a field
is the interface's job. `-n` reports what an import would do and writes
nothing.

A key the format does not have is named with its line rather than skipped
past. An import reports the same `2 added, 0 updated` whether or not it
understood every line, so `jump_host` where the field is `jump` would
otherwise leave that host showing `via —` and nothing on screen to say a line
was dropped — the mistake and the success read identically. More than one YAML
document in the file is refused on the same grounds, since only the first
would be imported; a file that merely opens with `---` is still one document.

Forwarding rules travel with their host, nested under it, because a rule says
how you work with a machine — "the database is on 5432 through there" — which
is as true on a laptop as on a desktop. A rule has no name, so the whole of it
is its identity: kind, what it binds, where it comes out. Nothing is updated
in place and nothing is removed, which is what lets two rules bind the same
port for different destinations and still both arrive.

Session history itself stays behind. "Last connected two hours ago" is a fact
about the machine that connected, and carrying it across would let a laptop's
history overwrite a desktop's on every import. A running tunnel stays behind
for the same reason: the rule crosses, the process does not.

The file holds no secrets — an identity is a path to a key, never the key — so
it belongs in a dotfiles repo as comfortably as anything else there.
`config.yaml` is already text and travels the same way, on its own.

## Starting from ~/.ssh/config

```sh
omassh import-ssh-config                  # ~/.ssh/config, or name a file
omassh import-ssh-config -group Work      # ... all into one group
```

Only what Omassh needs to list, probe and reach a machine is taken: the alias,
the address behind it, and the user, port, key and jump host. Everything else
in that file keeps working without being copied, because Omassh runs the real
ssh, which reads the file itself. Values resolve the way ssh resolves them —
settings under `Host *` reach every alias, first match wins — so an imported
host carries what `ssh -G` reports for it.

Wildcard and `Match` blocks are settings rather than machines, and are not
imported as hosts. `Include`d files are followed, which is the whole story for
a config that is one line pointing somewhere else.

`HostName %h.internal` — one block standing in for a whole estate — is
expanded per alias as ssh expands it, since ssh does that to the *setting* and
never to the destination it is finally handed. Imported literally, those hosts
arrived with a `%h` in the address and could not resolve anything.

## Reaching your hosts from everything else

```sh
omassh export-ssh-config                  # writes ~/.ssh/config, or -o FILE
omassh export-ssh-config -n               # what it would change, writing nothing
```

A host kept only in Omassh is reachable by Omassh. `scp`, `rsync`, `git`,
Ansible and every editor's remote mode read `~/.ssh/config` and know nothing
about a database, so this writes the list there — after which
`scp file prod-web:` and `git clone prod-web:repo` reach the same machines by
the same names, through the same bastions.

Only between its own markers. Everything outside them comes back byte for
byte, comments and blank lines included, and the file is replaced by a rename
rather than truncated, since a half-written `~/.ssh/config` is every machine
at once. A symlinked config — the usual way a dotfiles repository keeps one —
is followed rather than replaced. Running it twice changes nothing.

Markers that do not pair up stop the whole thing, naming the line. A start
marker whose end had been deleted would otherwise read as if the rest of the
file were Omassh's, and there is no getting a `~/.ssh/config` back from that.

The block goes at the **top**, because ssh keeps the first value it finds for
each setting: below a `Host *` of yours, every exported host would quietly
take that block's user instead of its own.

An alias your config already declares — in the file or in anything it
`Include`s — is left exactly as it is and reported, never written over: import
treats your config as a read-only source, and this keeps that promise from the
other side. So is a host whose jump host will not be in the file, since writing
it without one would dial a machine meant to sit behind a bastion. A host whose name ssh could not use as a destination is reported
the same way rather than written; a name with a space in it is refused by ssh
however it is quoted, and one holding `*` or `?` would be a pattern governing
hosts Omassh knows nothing about.

Groups are flattened on the way out, since ssh config has no such thing: what
a host inherits is written onto the host.

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

The database sits beside it, as `omassh.db`. More than one Omassh can use it
at once: the file is held for the length of an operation rather than the length
of the program, which matters because `enter` hands the whole terminal to ssh
and leaves no interface to look the next host up in. Each window reads the
store when something happens to it, so a host added in one appears in another
on its next reload — `r` at any time.

Themes (`tokyonight`, `gruvbox`, `nord`, `mono`, or your own palette), key
bindings and ssh options all live there. A malformed config is reported at
startup rather than ignored, because settings that silently do nothing are
worse than an error that says why — and so is a key that is not a setting.
`ssh_option` without its `s`, or a palette with `selected` where it means
`selected_bg`, is named with its line rather than skipped past, since skipping
looks exactly like the file not being read at all. More than one YAML document
in the file is refused for the same reason, since only the first would take
effect. Arrow keys and `ctrl+c` are reserved and always work, so no config can
trap you in the program.

`T` opens a theme picker that recolours the interface as you move through it,
since a palette is something you judge by looking at it. Keeping one writes
`theme:` into the config file above, creating it if there is none — the one
line, leaving comments, custom palettes and every other setting exactly as
they were. So there is one place a theme comes from: what you pick and what
you write by hand are the same setting, and neither quietly outranks the
other. A palette defined under `themes:` is offered alongside the built-ins.

Colours degrade automatically: on a terminal without truecolor the palettes
render in 256 colours, and `mono` exists for terminals with less than that.

## If the screen goes blank

Some terminals clear the screen without telling the application — iTerm2's
`cmd+K` is the common one. Omassh's renderer still believes its last frame is
on screen and writes only the differences, so nothing reappears on its own.
`ctrl+l` forces a full repaint; from inside a session, where `ctrl+l` belongs
to the remote shell, use `ctrl+\ r`.

## Reachability

A group holds the hosts beneath it as well as its own, so selecting one shows
everything in it and the number beside it says the same. A group whose machines
all live in its children used to read as empty, which is the shape most people
nest for.

`p` probes the hosts in the current group with a plain TCP connection —
`●` up, `✖` down. Hosts behind a jump host or a `ProxyCommand` show `◌` and are
skipped rather than guessed at: their address means something only from the far
side of the proxy, so dialling it from here would report on a different machine
entirely.

## License

[MIT](LICENSE) © Cuong Giang
