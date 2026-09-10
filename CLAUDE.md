# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Omassh is a keyboard-driven SSH client TUI (Go + Bubble Tea v2) that drives the
real OpenSSH binary rather than reimplementing SSH. `README.md` is the
user-facing document and explains *why* most of the visible behaviour is what it
is; read the relevant part of it before changing anything a user can see.

## Commands

```bash
go run ./cmd/omassh                                    # against the real database
go run ./cmd/omassh -db /tmp/x.db -config /tmp/x.yaml  # against a scratch one
go run ./cmd/omassh -print-config                      # example config; line 1 is the resolved path
go build -o omassh ./cmd/omassh
```

Manual verification should always use `-db`/`-config`. The default database is
the user's real host list, and the default config is the file the theme picker
writes back into.

```bash
go test ./...                                     # ~13s, of which internal/ui is ~12s
go test ./internal/ui -run TestPanelFocusCycles   # one test
go test -race ./internal/term ./internal/ui       # the packages with goroutines (~21s)
go vet ./...
```

There is no linter configuration: `gofmt` and `go vet` are the whole of it.

```bash
goreleaser check                       # validate .goreleaser.yaml
goreleaser release --snapshot --clean  # archives, checksums and cask locally, publishing nothing
./hack/demo.sh                         # re-record demo.gif (needs vhs)
```

### What the tests need from the machine

- **tmux.** `internal/term` and `internal/ui` open real tmux-backed panes. Those
  tests skip cleanly where tmux is absent, so such a machine still gets a green
  run while covering less.
- **Isolation is mandatory.** Both packages' `TestMain` point
  `OMASSH_TMUX_SOCKET` and `TMUX_TMPDIR` at per-pid values under `/tmp`, because
  the suite kills servers wholesale and once did it on the socket real sessions
  live on. `TestTheSuiteLeavesNothingBehind` fails if that ever stops happening.
  Any new package that shells out to tmux needs the same `TestMain`.
- **No Docker and no external hosts.** Anything needing a server runs
  `gliderlabs/ssh` in-process on loopback — `internal/sftpx/session_test.go`,
  `internal/term/pane_test.go`, `internal/term/forward_test.go`.
- UI tests drive the Bubble Tea `Model` value directly via
  `internal/ui/harness_test.go`; `Update`/`View` are pure, so there is no fake
  terminal. Assertions read text back out of `View()`.

## Architecture

**`internal/sshx.Build` is the chokepoint.** Every ssh invocation Omassh makes —
full-screen handoff, embedded pane, port forward, reachability probe, SFTP
subsystem — builds its argv there, so connection behaviour cannot drift between
paths. A new way to connect goes through `Build`. `SetGlobalOptions` is
process-wide and set once in `main` before anything connects.

**Two ways to run a session, deliberately.** `internal/sshx/session.go` hands the
whole terminal to a real ssh through `tea.Exec` (`enter`) — emulation-free by
construction. `internal/term` runs one inside the layout (`t`) with `x/xpty` and
`x/vt`, at the cost of owning every emulation detail. Both exist; do not collapse
them into one.

**tmux is the persistence layer and the source of truth.** Sessions *and* port
forwards are tmux sessions on Omassh's own server socket (`-L omassh`, overridden
by `OMASSH_TMUX_SOCKET`), which is what lets both outlive the window. Nothing
about a tunnel is held in process memory: `internal/term/forward.go` reads state
back out of tmux (`list-panes -a` with `#{pane_dead}`, `#{pane_dead_status}`,
`#{pane_dead_signal}`) and records the invocation's fingerprint in the pane
option `@omassh-args` — which is how a tunnel whose rule or host changed
underneath it is shown as stale rather than as carrying the new route.
`remain-on-exit` keeps a dead tunnel present so it can still say why it died.

Session names are the namespace: `omassh-<name>-<statkey>` for a host,
`omassh--fwd-<id>` for a tunnel. The second dash is load-bearing — `sanitize`
trims dashes off a host name, so nothing it produces can collide with a forward.

**The store is opened per operation, never held.** `internal/store` is bbolt,
which takes the file exclusively; holding it open for the life of the program
meant a second Omassh could not start, which the full-screen handoff makes
routine. `Open` creates whatever buckets are missing (`groups`, `hosts`,
`stats`, `forwards`), so a database written by an older build upgrades without a
version check anywhere. Ids are minted locally and mean nothing off this machine.

**Group inheritance lives in `store/resolve.go`, not in the UI.**
`Resolver.Resolve` fills a host's empty attributes from the nearest ancestor
group, records which group each inherited value came from, and turns a jump host
named as one of your own hosts into the destination ssh actually needs. The
detail pane only displays what it says.

**Import/export is plan-then-apply.** `internal/portable` parses the YAML
document; `Merge` returns a `Plan` — records with ids already assigned, plus one
line per change. `-n` prints that plan, and without it the same plan is written
in a single transaction, so a dry run is the real thing rather than a second
implementation predicting it. Records match by **name**; unknown keys and
multi-document files are refused rather than skipped past.

**The UI is one model.** `internal/ui.Model` carries `focus panel` and
`mode mode`; every screen is a mode, and `data` (`data.go`) is one snapshot of
store plus tmux plus `~/.ssh/config`, rebuilt by `load()`. `view.go` draws.

**Config errors are reported, never ignored.** `internal/config` refuses a
malformed file at startup — an unknown key, a bad palette colour name, a second
YAML document — because a setting that silently does nothing is worse than an
error saying why. `theme.PaletteKeys()` reads the valid colour names off the
struct by reflection, so the complaint about a wrong one cannot drift from the
struct. The theme picker writes back into the same file through
`config.SetTheme`, so a theme picked and a theme typed are one setting.

## Conventions

- **Comments say why, and usually name the bug.** The prose is long by design and
  records what went wrong before the code looked like this. Match the register; a
  change with no explanation reads as unmotivated here.
- **Test names are sentences about behaviour** —
  `TestKeepsAHostsMarksWhenItsNameIsTooLongForTheRow`. They assert what a user
  would see, mostly by rendering and reading the text back.
- **Commit subjects are one line, imperative, in the user's terms** — "Keep the
  way out of a session when the name will not fit beside it". No
  conventional-commit prefixes; the log has none.
- **User-facing text never leaks implementation.** No Go type names, no raw
  errno, no bare exit codes. An error says what to do about it — "ssh-add the key
  first".
- **Anything drawn into a fixed width goes through
  `github.com/charmbracelet/x/ansi`** — `StringWidth`, `Truncate`, `Wrap` — never
  `len()` or byte slicing. `ansi.Wrap` hard-breaks a long word; `ansi.Wordwrap`
  lets it overflow the box.
- **Nothing secret is stored.** A host names a key *path*; passphrases are
  ssh-agent's job. Export files are meant to live in dotfiles repos, so keep it
  that way.
