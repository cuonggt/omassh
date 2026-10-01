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

`ctrl+\ n` and `ctrl+\ p` step to the next and previous host with a session
running, without going back to the list — every such host, in the list's
order and round again at the end, the way tmux's own `n` and `p` step between
windows. The session you step away from keeps running, and the status says
which of them you are on: `session 2 of 3`. Without tmux there is never
another one to step to, since a session ends when the pane leaves it.

### The prefix

While the pane has focus every key goes to the remote, `ctrl+c` included, so
Omassh's own commands sit behind a prefix — `ctrl+\`, the way tmux's sit
behind `ctrl+b`:

| keys | |
|---|---|
| `ctrl+\ w` | back to the host list; the session stays connected and visible |
| `ctrl+\ n` / `ctrl+\ p` | the next / previous host with a session running |
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
stepping through the commands you last ran.

Over a session the wheel goes where a terminal would send it. In a shell it
goes back through the output. A program that has taken the whole screen —
less, man, vim — gets the arrow keys a terminal sends in its place, so the
wheel moves through the file rather than through whatever the shell said
before the program started. A program that asked for the mouse — vim with
`mouse=a`, htop — gets the wheel itself. Once you have scrolled back, the wheel
stays in the history until you return to the live view.

### Selecting and copying

Drag across a session to select text; letting go copies it. The status bar
says what was copied — the text itself when it is a line, how many lines when
it is more — since a clipboard cannot be looked at to check. While the button
is down the session holds still, so output arriving meanwhile cannot slide
under the selection and be copied in place of what you chose.

With tmux the selection is tmux's own. It joins a line the pane wrapped back
into one, so a long command or a URL comes out whole rather than broken where
the pane happened to be narrow. A double click takes a word, a triple click a
line, and dragging to the top or bottom edge carries on into the history.
Without tmux, or once a session has ended, Omassh selects what the pane is
showing itself, a row to a line, and those are not there.

A program that asks for the mouse itself — vim with `mouse=a`, htop — gets the
drag instead, as it would in a terminal. Your terminal's own selection is still
behind its modifier, `shift` in most, but it runs the whole width of the window
and takes the host list along with it.

The copy goes to this machine's clipboard: `pbcopy` on macOS, and `wl-copy`,
`xclip` or `xsel` on Linux, whichever the display has. With none of those, or
when Omassh is itself running over ssh — where this machine's clipboard is not
the one at your keyboard — it asks the terminal to copy instead, with OSC 52,
and says it asked rather than that it copied. A terminal may refuse, and
nothing comes back to say so: iTerm2 does until "Applications in terminal may
access clipboard" is on, and a tmux of your own in between needs
`set-clipboard on` to pass the request along.

Only what you select reaches the clipboard. A program on the far side can
write OSC 52 too, and Omassh's tmux refuses it from them — otherwise any host
you connected to could put what it liked on your clipboard, ready to be pasted
into a shell.

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
