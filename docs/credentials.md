# Credentials

`C` lists the ways you log in. A host or a group can name one of them instead
of repeating a user and a key on every machine it applies to; `n`, `e` and `d`
make, change and remove them.

There are three kinds:

- **key** — a user and a path to a private key, which becomes `ssh -i`
- **agent** — a user, with the key left to ssh-agent
- **password** — a user, for a machine that takes no key at all

A credential fills in the same fields a group does, by the same rule: the
nearest thing that sets a value wins. A host's own user beats its credential,
which beats its group's user, which beats its group's credential, and so on up
the tree; [hosts.md](hosts.md#groups-and-inheritance) has the whole order. The
detail pane marks a value a credential supplied the way it marks one from a
group — `← Prod deploy` beside the key, where a group's would read
`← Production`.

Deleting a credential takes it off everything that named it, and says how many
will lose it before it does. A host left pointing at a credential that had
gone would resolve to nothing, with no sign why.

## Passwords

Only a password credential changes how Omassh connects. It carries no key, so
ssh is told to go straight to the password rather than walking the agent's
keys first: on a host offering several, ssh can spend `MaxAuthTries` on keys
it was never going to be let in with, and be refused before it reaches the
password at all.

The password itself is not in Omassh's files. The store is an ordinary file
and the export is meant for a dotfiles repository, so a password goes where
the operating system already keeps such things — the login keychain on macOS,
the Secret Service through libsecret on Linux — and Omassh keeps a name for
it. On Linux that needs `secret-tool`, usually packaged as `libsecret-tools`.
A machine with neither says so while you are typing the password, rather than
accepting one it cannot store. It is the same argument Omassh makes about
ssh-agent: there is a facility for this already, and reimplementing it would
be worse than using it.

It reaches ssh through `SSH_ASKPASS`, OpenSSH's own way of being answered by a
program rather than a person — no `sshpass`, and nothing pretending to be a
terminal. ssh runs Omassh, Omassh reads the keychain, and the answer comes
back on a pipe between those two processes. The password is never in an
argument list, which every process on the machine can read, and never in an
environment variable, which every child would inherit; only the credential's
id travels that way. This needs OpenSSH 8.4 or newer.

Because a program answers, a password host works where nobody is watching —
the file browser, tunnels and snippet runs — as well as where you are sitting.
Those connections normally run with `BatchMode=yes`, so that nothing can stop
and wait for an answer that is never coming, and `BatchMode` would refuse the
askpass helper along with every other prompt. For a password credential it
becomes `NumberOfPasswordPrompts=1` instead: ssh asks once, the helper
answers, and a wrong password fails at once rather than looping on a prompt
nobody can see. What must never happen there is a connection that waits, and
neither of those waits.

A question is not answered with the password. ssh puts every prompt of the
connection to the helper, and the one that matters besides the password is
whether to trust a host key it has not seen before. Connecting with `enter` or
in the pane, that question comes to you on the terminal, as ssh on its own
would ask it. Where nobody is watching, the answer is no, and the connection
stops with "Host key verification failed" — as it would for a host with a key,
and put right the same way, by connecting once with `enter` and answering.

## On another machine

`omassh export` carries a credential's name, kind, user and key path, and
never the password. A list moved to another machine arrives knowing who it
logs in as and wanting the password typed into that machine's own keychain —
the same thing it would want for a key that machine does not have yet.
