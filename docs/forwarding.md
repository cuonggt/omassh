# Port forwarding

`f` lists the tunnels belonging to the selected host, and `↵` starts or stops
the one highlighted. `n`, `e` and `d` make, change and remove a rule, and `r`
asks tmux again what is still up.

All three kinds are here, written the way ssh writes them — `5432` and
`db.internal:5432` are the two halves of `-L 5432:db.internal:5432`:

- **local** (`-L`) binds a port here and carries it out through the host
- **remote** (`-R`) binds a port on the host and carries it back here
- **dynamic** (`-D`) binds a SOCKS proxy here

| mark | |
|---|---|
| `▶` | running |
| `■` | stopped |
| `✖` | stopped because it failed; the line beneath says what ssh said |
| `▷` | running, but on a route that has since changed — `↵` restarts it |

A green `▶` beside a host in the list means one of its tunnels is up.

## Tunnels outlive the window

A tunnel runs as a detached session on the same tmux server the
[pane sessions](sessions.md#sessions-that-outlive-the-window) use, so it
outlives the window that started it. That is the whole point: a forward has
nothing to look at, and its value is that it keeps running. Quitting Omassh
leaves your database tunnel up; opening Omassh again shows it as up, because
it is. Nothing about a tunnel is held in Omassh's memory — its state is read
back out of tmux — so the list cannot drift from what is actually running.

That is also why port forwarding needs tmux: a tunnel that died with Omassh
would still look like one that was up.

## What a tunnel adds

Everything about reaching the host is the same as for a session, so a tunnel
to a host behind a jump host goes through the jump host, and one whose group
supplies a user connects as that user. On top of that, a forward adds only
what makes a connection a tunnel:

- `-N`, since there is no remote command to run
- `ExitOnForwardFailure`, so a port that cannot be bound is a failure rather
  than a connection carrying nothing
- `ServerAliveInterval`, so a tunnel whose network went away is reported as
  stopped instead of holding its port and reading as up

`BatchMode` and `ExitOnForwardFailure` are forced on, ahead of anything `-o`
passes in, because nobody is watching: a passphrase or host-key prompt in a
detached session waits for an answer that is never coming, and a connection
that could not bind its port carries nothing. Either way "up" would mean
something other than a tunnel. They are not preferences competing with yours;
they are what makes `▶` mean anything. `ssh-add` the key first, and connect
once in a session to accept a host key ssh has not seen before. The keepalives
are a preference, and stay yours to tune.

## When a tunnel fails

The local port is bound here before ssh is asked to bind it, so a port already
in use is a sentence naming the port rather than a tunnel that vanishes a
moment after starting. A tunnel that stops for any other reason keeps its
session, dead, so it can still say what ssh said — `✖` in the list, with the
reason beneath it. Without that, a failure would look exactly like a tunnel
nobody had started.

## Editing a running tunnel

Editing a rule reaches nothing already running: a tunnel goes on carrying the
route it was started with. So each tunnel records the whole invocation it was
started with, and one whose rule *or host* has changed underneath it shows `▷`
rather than `▶` — editing the host's address moves a tunnel just as surely as
editing the rule. `↵` restarts it on what they say now. Repointing a tunnel at
staging must never leave every connection landing on production while the
screen agrees with you.
