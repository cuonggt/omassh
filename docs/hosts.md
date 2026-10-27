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
clears it. Each result says where in the tree it came from, as a trail —
`web-01  Production › edge` — since a search that spans everything is the one
place the list no longer tells you that by where the row is.

In a form, `↓` offers what you already use: the key paths, jump hosts, groups
and tags for a host, and the parents and jump hosts for a group. Tags are a
set — `space` toggles one, `✓` marks those chosen and `↵` finishes. The key
path, jump host and group fields stay free text: a key kept anywhere works,
any ssh destination works as a jump host, and a group name that does not exist
yet is created.

The keys offered are the private keys in `~/.ssh`, and then the paths your
other hosts and credentials already name — so a key kept somewhere else is
typed once and offered from then on. A key is recognised by reading its first
line rather than by its name, since `id_ed25519`, `id_rsa` and `deploy.pem`
are all real names for one and `config` is in that directory on every machine.
The public half beside a key, `known_hosts` and a certificate are not offered,
and nothing in there is read further than that first line.

`ctrl+g` on that field makes a key, by handing the terminal to `ssh-keygen`
the way `enter` hands it to ssh. ssh-keygen asks for the passphrase itself,
and that is the point rather than a convenience: it takes one interactively or
in its argument list, an argument list is readable by everything running on
the machine, and Omassh holds no secrets of its own. What it makes is an
`ed25519` key named after the host — `~/.ssh/prod-web` for a host called
prod-web — or one at the path already in the field, and the field is filled in
with it afterwards. Whether to overwrite a key already there is ssh-keygen's
question too, as is refusing to.

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

## Your own ssh config

ssh decides which `Host` blocks of `~/.ssh/config` apply by the name it is
handed. So when your config names one of your hosts — the same name, reaching
the same address and port — Omassh hands ssh that name and pins the address
beside it:

```sh
ssh -o HostName=10.0.0.5 deploy@prod-web
```

Everything under `Host prod-web` then applies, as it does to `ssh prod-web`:
`ForwardAgent`, `IdentitiesOnly`, `SetEnv`, the `HostKeyAlgorithms` an old
switch needs before it will talk at all. Handed the address instead, ssh would
pass over the whole block. The address is still Omassh's, so the name decides
which settings apply and never which machine is reached, and the user, port
and jump host Omassh sets still win: ssh keeps the first value it is given,
and the command line comes first.

The machine has to be the same, not only the name. A `Host web` in the file
that points somewhere else is a different machine that happens to share a
name, and its settings — a forwarded agent among them — are not this host's to
take. The detail pane says which it is: `config  Host prod-web  ←
~/.ssh/config` when the block is in use, and where the file puts that name
when it is not. An address changed in one place and not the other is the
usual reason.

Hosts your config does not name are handed to ssh by their address, so a block
written for an address or a domain — `Host *.corp.example.com` — still applies
to them. Handing ssh every host's name would trade those blocks for these.

The file is read for the names it gives machines and for what it sends them
through, which [reachability](#reachability) needs, and nothing else — along
with whatever it `Include`s, every time the list is read, so after an edit,
`r`. Omassh's own block from
[`export-ssh-config`](moving.md#reaching-your-hosts-from-everything-else) does
not count, since everything in it came from Omassh in the first place. A file
that cannot be read says so in the status bar, and until it reads, every host
is reached by its address.

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

`p` probes the hosts in the selected group: `●` up, `✖` down, `◌` not asked.
It waits `probe_timeout` for an answer — two seconds, unless the
[config](configuration.md) says otherwise.

A host Omassh dials itself gets a TCP connection to its port, opened and
closed. That is what makes it quick enough to sweep a whole group at once, and
it is why a probe says a port is answering and nothing about whether ssh would
let you in.

A host behind a jump host cannot be dialled from here at all: its address
means something only from the far side of the jump, and dialling it from here
would report on a different machine entirely — quite possibly something else
on your own network. So it is asked of the jump host, which is the one place
the address does mean something. Omassh asks for a stdio forward to the host —
`ssh -W`, the same thing ssh's own `ProxyJump` opens — and the jump host makes
the connection. Nothing logs in to the host itself, nothing is written to
`known_hosts`, and the host is never sent a byte.

`●` is then the host having answered: sshd greets whoever connects before it
is spoken to, so that greeting arriving back through the forward is the port
answering. `✖` is the jump host saying it could not connect. A port that
answers and then waits to be addressed first cannot be told from one that is
not answering at all, and reads as `◌`.

Any jump host can be asked, however it was named: one of your own hosts,
written down with its port and its key; a destination you typed, such as
`ops@edge.example.com:2222`; or a `ProxyJump` in your `~/.ssh/config`. A chain
is asked of its last hop — the one that can reach the host — with ssh carrying
the connection through the hops in front of it. A hop Omassh knows only as a
destination has no key written down for it, so that comes from ssh's own
config and your agent, exactly as it would if you typed the `ssh` yourself.

Asking costs a connection to the jump host for each host behind it, where a
dial costs a packet, so a sweep makes four at a time to any one jump host —
fifty hosts behind one bastion is fifty logins to it, thirteen rounds of four.
An sshd turns away connections that have yet to log in once ten of them are
waiting, and one it turned away would arrive here as `◌` against a host that
is perfectly fine.

`◌` is a host nobody managed to ask about, and it is never read as a host that
is down: too much of what goes wrong on the way has nothing to do with the
machine. It is what you get when

- the jump host would not let the probe in. It connects with nobody in front
  of it, so `ssh-add` a key that has a passphrase, and `enter` the jump host
  once to accept a host key ssh has not seen before.
- the route is a `ProxyCommand` — an `aws ssm` session, say, whose host is an
  instance id rather than an address at all. That is a program only ssh knows
  how to run, and a program cannot be asked for a connection the way a jump
  host can.
- the hop is written in a way Omassh cannot read as a destination, which is
  `[user@]host[:port]`, what `ssh -J` takes.
- the host is one the jump host can reach but whose packets are dropped rather
  than refused. The jump host waits on its own connection far longer than the
  probe waits to hear back.

The file is read as ssh reads it for the name it is handed: the host's name
where the file gives it one, and its address otherwise, which is what patterns
such as `Host 10.0.1.*` match. The first `ProxyJump` or `ProxyCommand` met
decides, `none` included, and which of the two it was decides whether the
probe can ask it. The detail pane shows the route on the `via` line with
`← ~/.ssh/config` beside it. A jump host Omassh gives the host goes on ssh's
command line, ahead of the file, so that is the one ssh takes, the one shown,
and the one asked.

A route set under a `Match` that holds only at the moment of connecting —
`exec`, `user` and the rest — is still out of Omassh's sight, so a host that
relies on one is dialled directly like any other.
