# Moving between machines

The store is one binary file, with ids minted on the machine that made them.
Copying it over another machine's replaces that machine's list rather than
joining it, and two machines that each gained a host cannot be reconciled by
copying in either direction. The text form is the way across.

## Export and import

```sh
omassh export > hosts.yaml            # or -o hosts.yaml, written 0600
omassh import hosts.yaml              # stdin when given no file
omassh import -n hosts.yaml           # what it would change, writing nothing
omassh export | ssh other-machine omassh import
```

The file is the whole list, readable and writable by hand:

```yaml
version: 3
credentials:
  - name: Prod deploy
    kind: key
    user: deploy
    identity: ~/.ssh/prod_ed25519
groups:
  - name: Production
    jump: bastion
    credential: Prod deploy
  - name: Web
    parent: Production
hosts:
  - name: bastion
    addr: bastion.example.com
    user: ops
  - name: db-01
    addr: 10.0.1.20
    group: Production
    forwards:
      - kind: local
        listen: "5432"
        dest: localhost:5432
  - name: web-01
    addr: 10.0.1.11
    group: Web
    tags: [web, eu-west]
snippets:
  - name: disk free
    script: df -h / | tail -1
```

Records match **by name**, never by id. That is what makes the same list
mergeable at both ends, and it means a host keeps the history hanging off it
across an import — the host you have connected to forty times is still that
host afterwards. A field the document leaves out keeps the value already
stored, so importing fills in and corrects but never blanks; clearing a field
is the interface's job. Importing the same file twice changes nothing the
second time.

`-n` prints the plan and writes nothing. Without it, the same plan is written
in a single transaction, so a dry run is the real thing rather than a
prediction of it.

A key the format does not have is refused, named with its line and in the
words of the file rather than of the program reading it:
`"jump_host" is not something a host has — it takes name, addr, port, …`,
with the keys read off the format, so the list offered is the list accepted.
An import reports the same `2 added, 0 updated, 0 already matched` whether or
not it understood every line, so a key skipped past would make a mistake read
exactly like a success. More than one YAML document in the file is refused on
the same grounds, since only the first would be imported; a file that merely
opens with `---` is still one document.

Forwarding rules travel with their host, nested under it, because a rule says
how you work with a machine — "the database is on 5432 through there" — which
is as true on a laptop as on a desktop. A rule has no name, so the whole of it
is its identity: kind, what it binds, where it comes out. Nothing is updated
in place and nothing is removed, which is what lets two rules bind the same
port for different destinations and still both arrive.

Session history stays behind. "Last connected two hours ago" is a fact about
the machine that connected, and carrying it across would let a laptop's
history overwrite a desktop's on every import. A running tunnel stays behind
for the same reason: the rule crosses, the process does not.

The file holds no secrets — a key is a path, never the key, and a
[password](credentials.md#passwords) stays in the keychain — so it belongs in
a dotfiles repository as comfortably as anything else there. A snippet's
script crosses exactly as written, which is the one reason to look before
committing one. `config.yaml` is already text, and travels the same way on its
own.

## Starting from ~/.ssh/config

```sh
omassh import-ssh-config              # ~/.ssh/config, or name a file
omassh import-ssh-config -group Work  # ... all into one group
omassh import-ssh-config -n           # what it would add, writing nothing
```

Only what Omassh needs to list, probe and reach a machine is taken: the alias,
the address behind it, and the user, port, key and jump host. Everything else
in the file keeps working without being copied, because Omassh runs the real
ssh, which reads the file itself. Values resolve the way ssh resolves them —
settings under `Host *` reach every alias, and the first match wins — so an
imported host carries what `ssh -G` reports for it.

Wildcard and `Match` blocks are settings rather than machines, and are not
imported as hosts. `Include`d files are followed, which is the whole story for
a config that is one line pointing somewhere else.

`HostName %h.internal` — one block standing in for a whole estate — is
expanded per alias as ssh expands it, since ssh does that to the setting and
never to the destination it is finally handed. Taken literally, those hosts
would arrive with a `%h` in the address and resolve nothing.

This is the only way `~/.ssh/config` reaches the host list. The list never
reads the file behind your back; ssh reads it at every connection, as it
always does.

## Reaching your hosts from everything else

```sh
omassh export-ssh-config              # writes ~/.ssh/config, or -o FILE
omassh export-ssh-config -n           # what it would change, writing nothing
```

A host kept only in Omassh is reachable only by Omassh. `scp`, `rsync`, `git`,
Ansible and every editor's remote mode read `~/.ssh/config` and know nothing
about a database, so this writes the list there — after which
`scp file prod-web:` and `git clone prod-web:repo` reach the same machines by
the same names, through the same jump hosts.

It writes only between its own markers. Everything outside them comes back
byte for byte, comments and blank lines included, and the file is replaced by
a rename rather than truncated, since a half-written `~/.ssh/config` breaks
every connection at once. A symlinked config — the usual way a dotfiles
repository keeps one — is followed rather than replaced. Running it twice
changes nothing.

Markers that do not pair up stop the whole thing, naming the line. A start
marker whose end had been deleted would otherwise read as if the rest of the
file were Omassh's, and there is no getting a `~/.ssh/config` back from that.

The block goes at the **top**, because ssh keeps the first value it finds for
each setting: below a `Host *` of yours, every exported host would quietly
take that block's user instead of its own.

An alias your config already declares — in the file or in anything it
`Include`s — is left exactly as it is and reported, never written over: import
treats your config as a read-only source, and this keeps that promise from the
other side. So is a host whose jump host will not be in the file, since
writing it without one would dial a machine meant to sit behind a bastion. A
host whose name ssh could not use as a destination is reported the same way
rather than written: a name with a space in it is refused by ssh however it is
quoted, and one holding `*` or `?` would be a pattern governing hosts Omassh
knows nothing about.

Groups are flattened on the way out, since ssh config has no such thing: what
a host inherits is written onto the host.

That matters if the file ever comes back. Importing a config Omassh wrote
turns everything those hosts inherited into settings of their own, and a host
carrying its own user has stopped following its group: change the group to
`ubuntu` afterwards and that host goes on connecting as `deploy`. No value is
wrong, and the only sign on screen is a quiet one — the `← Production` beside
the value in the detail pane is simply not there. `omassh export` is the round
trip that keeps the list's shape, because YAML has groups to keep.
`~/.ssh/config` is somewhere to write the list, not somewhere to keep it.
