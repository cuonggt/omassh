# Hosts, groups and jump hosts

A host is a name and an address. Everything else is optional: a port, a user,
a key, a jump host, a credential, a group and any number of tags. `n` adds a
host, `e` edits the one highlighted and `d` deletes it; with the group list
focused, the same keys work on groups.

## Groups and inheritance

Groups nest, and a host fills anything it leaves empty from the nearest group
above it that sets it. Three things are inherited — the user, the key and the
jump host — and a [credential](credentials.md) counts as setting whatever it
carries. The nearest thing that sets a value wins, which, most specific first,
means:

1. the host itself
2. the host's credential
3. its group
4. its group's credential
5. the parent group, then the parent's credential, and so on up the tree

The detail pane says where each inherited value came from: `← Production`
beside a user the Production group supplied, `← Prod deploy` beside one a
credential did. A host connecting as someone unexpected says why on the screen
you are already looking at.

The address and the port are never inherited. They are what makes a host that
host.

A group holds the hosts beneath it as well as its own, so selecting one lists
everything in it, and the number beside it counts the same. Nesting is how
machines usually get grouped, and a group whose machines all live in its
children would otherwise look empty.

## Finding a host

`/` searches every host at once, fuzzily, by name, address and tag; `esc`
clears it.

In a form, `↓` offers what you already use: the jump hosts, groups and tags
for a host, and the parents and jump hosts for a group. Tags are a set —
`space` toggles one, `✓` marks those chosen and `↵` finishes. The jump host
and group fields stay free text: any ssh destination works as a jump host, and
a group name that does not exist yet is created.

## How a host is reached

Every connection Omassh makes — a full-screen session, the pane, SFTP, a
tunnel, a snippet run — is built in one place from the host's resolved
settings. A host that connects in a session connects the same way everywhere
else: through the same jump host, as the same user, with the same key.

A key is a path, which becomes `ssh -i`. The key itself stays where it is, and
its passphrase is ssh-agent's business: `ssh-add` it, or set
`AddKeysToAgent yes` in `~/.ssh/config`. Omassh never asks for one.

Whatever Omassh does not set is left to OpenSSH, which reads your
`~/.ssh/config` as it always does — `Match` blocks, certificates,
`IdentityAgent`, `known_hosts` and the rest.

## Jump hosts

A jump host is named by picking one of your hosts, and the connection to it is
spelled out as a `ProxyCommand` carrying that host's own port, user and key.
`ssh -J` would hand the hop only `-l`, `-p` and `-v`, so the jump host's key
would be silently ignored; Omassh writes the command ssh would otherwise build
internally, with nothing left out. A jump host Omassh does not know —
`ops@edge.example.com` — is passed through as a plain `-J`, since ssh already
understands it.

Chains work: a jump host may sit behind another, and each hop is told the
address of the next rather than being left to work it out. ssh expands `%h`
and `%p` across a whole `ProxyCommand`, nested levels included, so writing
them would hand every hop the *final* destination, and the hosts in between
would never be contacted. Omassh knows every address in the chain, so it
writes each one.

## Reachability

`p` probes the hosts in the selected group: `●` up, `✖` down. A probe is a TCP
connection to the host's port, opened and closed. That is what makes it quick
enough to sweep a whole group at once, and it is why a probe says a port is
answering and nothing about whether ssh would let you in. It waits
`probe_timeout` for an answer — two seconds, unless the
[config](configuration.md) says otherwise.

Hosts behind a jump host show `◌` and are skipped rather than guessed at:
their address means something only from the far side of the jump, so dialling
it from here would report on a different machine entirely. A `ProxyCommand`
set in `~/.ssh/config` is out of Omassh's sight, so a host that relies on one
is dialled directly like any other.
