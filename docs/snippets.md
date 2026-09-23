# Snippets

`S` lists the scripts worth keeping, so a command you run often is typed once
rather than remembered. `n`, `e` and `d` make, change and remove them. The row
beside a name shows the command itself where there is one line of it, and the
size where there is more.

## Writing one

A one-line script is typed into the form. Anything longer goes to your editor
on `ctrl+e` — `$VISUAL` first, then `$EDITOR`, then `vi` — and comes back with
whatever you saved. Omassh does not grow a text area for this, for the reason
it does not implement SSH: there is an editor on the machine already, and it
is better than the one that would be written here. A script too long for its
field reads `4 lines — ctrl+e to edit`, and the field stops taking keystrokes,
so a description typed in the wrong place can never be saved as the script.

## Running one

`↵` on a snippet opens a screen showing the whole script and the ways of
saying where to run it:

- **this host** — the one selected in the host list
- **this group** — every host the list is showing; under a search it offers
  the matches instead, and calls them that
- **pick hosts…** — any set at all, ticked with `space`, where `a` chooses all
  or none

Nothing runs until you confirm on that screen, with `y` or a second `↵`. That
step is not ceremony: a long snippet is listed by its size, so a single `↵`
would be running something you cannot see on machines you have to be right
about.

Results fill in as they arrive, one row per host, each carrying the last line
that host said, and `↵` on a row opens what it said in full. `esc` stops the
run and keeps what has already come back, because the hosts that did finish
are worth reading, and closing the screen would take them away in the same
moment. A second `esc` leaves without waiting, for a run whose results are not
coming: killing ssh does not close a pipe that a background process on the
host is still holding.

Four hosts run at a time. A probe is a TCP connection and nothing else, but a
run is a whole ssh session plus whatever the script does at the far end, and
thirty simultaneous package upgrades is not a thing to start by accident.
Hosts past the limit are shown as queued rather than running, because they
are.

**A run has no terminal.** It goes through the same unattended path SFTP
does, so `sudo` asking for a password fails at once instead of waiting on a
keyboard that is not there — which is the right answer, since waiting is the
one thing a connection nobody is watching must never do. A
[password credential](credentials.md#passwords) still works, because the
keychain answers rather than a person. What a run cannot do is anything
interactive, and that is what pasting is for.

## Pasting one

`p` pastes the snippet into the session in the
[pane](sessions.md#in-the-pane-t) instead, and stops there. It arrives as a
paste rather than as typing, which a shell that understands bracketed paste
holds in its edit buffer for you to read, change and run yourself. One that
does not understand it — macOS ships a bash whose readline predates the idea —
runs each line as it arrives. Which of those happened cannot be known from
here: the mode belongs to the shell at the far end, and with tmux in between,
it is tmux's own that reaches Omassh. So nothing is claimed. A one-line
snippet always waits, because what would run it is the trailing newline
Omassh takes off; a longer one says to go and look, next to the pane it has
just put in front of you.

## In an export

`omassh export` carries a snippet's script exactly as written, which is also
the warning: a password belongs in a credential, where the export does not
carry it, and never in a script, where it does.
