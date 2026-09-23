# SFTP

`s` opens a two-pane file browser on the selected host: your machine on the
left, the host on the right.

| key | |
|---|---|
| `tab` | switch panes; `h`/`l` and `←`/`→` do too |
| `j` / `k` | move |
| `↵` / `-` | enter a directory / go up |
| `c` | copy what is highlighted to the other pane — a file, or a directory and everything under it |
| `m` / `r` / `M` / `d` | make a directory / rename / chmod / delete |
| `g` | re-read the pane you are in |
| `esc` | stop a copy that is running, or leave if none is |
| `q` | leave |

A click selects a file and focuses its pane, and a double click enters a
directory.

The local pane opens where Omassh was started rather than at your home
directory, since the directory you were just working in is more often the one
you want to send from. The remote pane opens where a session would land, and
the keyboard starts there — you came here to look at the host.

## How it connects

SFTP needs no SSH client of its own. Omassh runs `ssh -s <host> sftp` and
speaks the SFTP protocol over that child's input and output, so OpenSSH makes
the connection exactly as it would for an interactive session: jump hosts,
`ProxyCommand`, certificates, `Match` blocks, `IdentityAgent` and
`known_hosts` all apply, with no second implementation to keep in step.

`BatchMode` is forced on, because the child's input carries the protocol and
there is nowhere to prompt. `ssh-add` a key that has a passphrase first, and
connect once in a session to accept a host key ssh has not seen before. A
[password credential](credentials.md#passwords) still works, since its
password comes from the keychain rather than from a prompt.

## Copying

A transfer is written beside its destination and renamed onto it at the end,
so the destination is only ever the old file or the new one. A copy that fails
halfway leaves what was already there. The size reported is counted from what
actually crossed rather than read off the row: a symlink's row gives the
length of the path it holds, while the copy follows it to the file.

Copying onto a name already taken asks first. A copy over a file destroys it
as surely as deleting it does, and deleting asks. It asks rather than refuses
because replacing what is there is very often the point, and refusing would
mean deleting first, which leaves a moment with neither file.

`c` on a directory takes the whole tree. Onto a directory already there it
merges: the names that clash are overwritten and everything else in there is
left alone. That is why it asks to merge rather than to replace — replace
would promise that whatever is not in the copy goes away. Onto a *file* of the
same name it is refused rather than asked about, since no answer to that
question keeps both.

The walk reads each directory's listing rather than following the names in
it, which is what keeps it finite: a listing reports a symlink as the link and
not as what it points at, so only real directories are descended, and a real
directory cannot contain itself.

Nothing here can write a symlink, so a link is copied the way copying its own
row is — followed, and the file it names moved. A link to a *directory* has no
bytes to move, so it is counted and stepped over rather than failing a tree
that is otherwise fine; one link is a poor reason to abandon ten thousand
files. The count is given at the end, because a copy that quietly left
something behind is the one nobody checks until it matters.

Each file lands atomically; the tree as a whole does not, and cannot. A copy
that fails partway names the file it stopped on and keeps what had already
arrived. Progress names each file by its place in the tree —
`project/src/deep/blob.bin` — and the end is counted in files with the size
beside it, since "copied 400 files" alone does not say whether the wait moved
a manual or a film archive.

## Stopping a copy

`esc` stops a copy that is running, and leaves the browser when none is. The
progress row says so, because the one moment that key matters is the moment
the progress has taken the key list's place. A file that was interrupted goes
back to how it was: the part file is removed and the destination never
touched. A tree keeps what had already crossed and says how much that was,
since nothing puts those files back. `q` calls off a copy on the way out
rather than pulling the connection from under it.

## Deleting

A symlink is a name, so deleting one removes the name and leaves what it
points at alone — inside a directory being deleted, too. Following links would
reach files nobody selected: deleting a link to a directory would empty that
directory, and deleting a directory that merely *contains* one would destroy
everything on the far side of it.

## Names and errors

A filename is not a message. Remote names are stripped of escape sequences and
control characters before anything draws them, so a name cannot recolour the
interface or move the cursor in the middle of a redraw.

What goes wrong is said in words. When the server's reply does not say why,
Omassh checks the usual causes itself — the name already being taken, or the
directory meant to hold it missing — and otherwise says the server refused,
rather than showing a protocol constant to someone who has just typed a
directory name.
