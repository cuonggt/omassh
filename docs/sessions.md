# Sessions

There are two ways to connect, and both exist on purpose: `enter` gives ssh
the whole terminal, and `t` runs it in a pane beside the host list.

## Full screen: `enter`

`enter` hands the whole terminal to `ssh`. Omassh lets go of it, ssh gets the
real input and output, and leaving the shell drops you back into the list.
Nothing stands between you and the remote, so resizing, your terminal's own
scrollback, the mouse and every escape sequence behave exactly as they would
without Omassh in the picture. That is why it is the default.

ssh's error output is the one thing Omassh listens to on the way, keeping its
last few lines so that a session that fails can say why. Everything still
reaches the terminal unchanged; it is only copied. A failure is reported with
the exit status, because that is the certain part, and ssh's last line after
it as context rather than as a claim about the cause. Without that line, a
refused connection, a rejected key, a changed host key and a timeout would all
read alike. Passphrase and host-key prompts are unaffected: ssh puts those on
the terminal itself.

## In the pane: `t`

`t` opens the session in the main pane instead, keeping the host list beside
it. ssh runs in a pseudo-terminal whose output Omassh draws, so the remote
gets a real terminal the size of the pane and hears about every resize. A
green `●` marks the host the pane is connected to.

One session is on screen at a time. Connecting somewhere else takes the pane;
with tmux, the session you left keeps running, marked by a yellow `●`, and `t`
on that host reattaches it. `t` on the host already in the pane goes back to
it rather than opening a second session over the top.

### The prefix

While the pane has focus every key goes to the remote, `ctrl+c` included, so
Omassh's own commands sit behind a prefix — `ctrl+\`, the way tmux's sit
behind `ctrl+b`:

| keys | |
|---|---|
| `ctrl+\ w` | back to the host list; the session stays connected and visible |
| `ctrl+\ d` | detach; the session keeps running, and `t` reattaches |
| `ctrl+\ X` | end the session for good |
| `ctrl+\ k` / `ctrl+\ j` | scroll back / forward a page |
| `ctrl+\ G` | return to the live view |
| `ctrl+\ r` | redraw the screen |
| `ctrl+\ ctrl+\` | send a literal `ctrl+\` to the remote |

The prefix works from the host list as well. Detaching and ending are facts
about the session, not about whichever panel has the keyboard, so neither
needs a trip back into the pane first. Once a session has ended, `esc` returns
to the list.

`ctrl+b` goes to the remote like any other key. The tmux keeping the session
has no prefix of its own, so `ctrl+b` reaches readline, vim or a tmux on the
far side, and nothing on this side can detach the pane from under you.

### Scrolling

`ctrl+\ k` and `ctrl+\ j` page through the output, the wheel scrolls three
lines at a time, and `ctrl+\ G` returns to the live view. Typing anything
returns on its own, since a terminal that stayed scrolled back while you typed
would hide your own output. With tmux the history belongs to it — 10,000
lines, surviving restarts — and these keys drive its copy mode; without tmux
the pane keeps 2,000 lines itself.

### The mouse

Omassh takes the mouse while it is on screen, so the wheel scrolls whichever
list or session is under the pointer. Left to itself, a terminal turns the
wheel into arrow keys on the alternate screen, and in a shell that means
stepping through the commands you last ran. Selecting text with the mouse
therefore takes your terminal's own modifier — `shift` in most of them — as it
does under tmux.

## Sessions that outlive the window

Where tmux is installed, a session in the pane runs inside tmux, on a server
of Omassh's own. A pseudo-terminal whose other end belongs to Omassh would die
with it; tmux keeps the session instead, so quitting Omassh detaches rather
than disconnects, and reconnecting finds the screen and the shell as you left
them. A yellow `●` beside a host means a session is waiting there. Full-screen
sessions are ssh itself, and end when it does.

That server has a socket of its own, so `tmux ls` in your shell is unaffected.
To see what Omassh has running:

```sh
tmux -L omassh ls
```

Without tmux, a session in the pane ends when Omassh does, and
[port forwarding](forwarding.md) is unavailable.

A session opened from two windows at once shows the same screen in both, which
is what reattaching means. tmux sizes it for whichever window was typed in
last, so the other draws it short of its pane or clipped by it; the status bar
says so when you connect.
