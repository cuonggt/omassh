# Configuration

Everything is optional — Omassh runs with no configuration at all.

## Where it lives

The config file follows the platform convention:

- macOS: `~/Library/Application Support/omassh/config.yaml`
- Linux: `~/.config/omassh/config.yaml`, or under `$XDG_CONFIG_HOME`

`omassh -h` prints the resolved path, and so does the first line of
`omassh -print-config`, which writes a documented example of every setting.
Writing to the wrong file is silent, since a missing config is not an error,
so let the shell work the path out:

```sh
cfg=$(omassh -print-config | head -1 | cut -c3-)
mkdir -p "$(dirname "$cfg")" && omassh -print-config > "$cfg"
```

`-config FILE` reads a different one.

## What is in it

```yaml
theme: terminal

themes:
  mine:
    accent: "#ff8800"
    border: 8

keys:
  connect: o
  search: f

ssh_options:
  - ConnectTimeout=10
  - ServerAliveInterval=30

probe_timeout: 2s
```

| setting | |
|---|---|
| `theme` | the palette to draw in — a built-in, or one of yours |
| `themes` | palettes of your own, by name |
| `keys` | rebindings, from action to key |
| `ssh_options` | passed to every ssh Omassh runs, as `ssh -o` would pass them |
| `probe_timeout` | how long a [reachability probe](hosts.md#reachability) waits before calling a host down |

## Themes

`T` opens a theme picker that recolours the interface as you move through it,
since a palette is something you judge by looking at it. Keeping one writes
`theme:` into the config file, creating it if there is none — that one line,
leaving comments, custom palettes and every other setting exactly as they
were. So there is one place a theme comes from: what you pick and what you
write by hand are the same setting, and neither quietly outranks the other. A
palette defined under `themes:` is offered alongside the built-ins.

The default, `terminal`, draws in your terminal's own colours: its text
colour, the sixteen its scheme sets, and its own reverse video for the
selection. So Omassh looks like the rest of the terminal, reads as well on a
light background as on a dark one, and follows the terminal when its scheme
changes. The other built-ins — `tokyonight`, `gruvbox` and `nord` — are
colours of their own, in hex, made for a dark background. On a terminal
without truecolor they render in 256 colours, and `mono` exists for terminals
with less than that.

## Palettes of your own

A palette sets any of `text`, `text_dim`, `text_bright`, `accent`, `green`,
`yellow`, `red`, `magenta`, `border` and `selected_bg`, each to one of:

- `"#rrggbb"`, in quotes, because a bare `#` starts a YAML comment
- a number from the terminal's palette: `4` is its blue, `8` its grey, and
  anything up to `255` works
- `default`, the terminal's own text colour — which, as `selected_bg`, means
  its own highlight

Colours a palette leaves out come from `terminal`, so `accent: "#ff8800"` on
its own is the terminal's colours with an orange accent. That is also the way
out for a scheme whose grey is its background, as the original Solarized
Dark's is: `terminal` draws dim text and borders in the grey, colour `8`, as
most terminal programs do, so under that scheme they vanish, and
`text_dim: 10` with `border: 10` brings them back.

## Keys

Every action in the main view can be rebound. Keys are written the way the
help screen shows them — `ctrl+l`, `shift+tab`, `enter`.

| action | default |
|---|---|
| `up` / `down` | `k` / `j` |
| `next-panel` / `prev-panel` | `tab` / `shift+tab` |
| `panel-groups` / `panel-hosts` | `1` / `2` |
| `search` | `/` |
| `connect` | `enter` |
| `pane` | `t` |
| `new` / `edit` / `delete` | `n` / `e` / `d` |
| `probe` | `p` |
| `sftp` | `s` |
| `forward` | `f` |
| `credentials` | `C` |
| `snippets` | `S` |
| `theme` | `T` |
| `reload` | `r` |
| `redraw` | `ctrl+l` |
| `help` | `?` |
| `quit` | `q` |

Two actions wanting one key stop Omassh at startup, and the error says which
of the two came from you: a new built-in binding landing on a key you had
already claimed then names the line to change, rather than one of them
silently doing nothing. The arrow keys and `ctrl+c` are reserved and always
work, so no config can trap you in the program.

## ssh options

`ssh_options` go to every ssh Omassh runs, as `ssh -o` would pass them, and so
does `-o` on the command line, which can be given more than once:

```sh
omassh -o ConnectTimeout=5
```

ssh keeps the first value it is given for an option, so order is precedence:
`-o` on the command line outranks `ssh_options`, and both outrank your
`~/.ssh/config`. Ahead of all of them come the few settings Omassh fixes for
connections nobody is watching, because those are what make a connection safe
to leave alone; [forwarding](forwarding.md#what-a-tunnel-adds) says which. An
option with no value, `BatchMode` on its own, is refused at startup rather
than carried to every connection to fail there.

## Mistakes are errors

A malformed config is reported at startup rather than ignored, because a
setting that silently does nothing is worse than an error that says why. So is
a key that is not a setting: `ssh_option` without its `s`, or a palette with
`selected` where it means `selected_bg`, is named with its line rather than
skipped past, since skipping looks exactly like the file not being read at
all. More than one YAML document in the file is refused for the same reason,
since only the first would take effect. So is a hex colour without its quotes,
which YAML reads as a comment: `accent: #ff8800` is an accent with nothing
after it, and the complaint gives the colour back as `"#ff8800"`. Every
palette under `themes:` is checked, not only the one in use, since the picker
offers them all.

## The database

The host list sits beside the config, as `omassh.db`; `-db FILE` opens a
different one. More than one Omassh can use it at once: the file is held for
the length of an operation rather than the length of the program, which
matters because `enter` hands the whole terminal to ssh and leaves no
interface to look the next host up in. Each window reads the store when
something happens in it, so a host added in one appears in another on its
next reload — `r`, at any time.

Nothing secret is in it. Keys are paths, and
[passwords](credentials.md#passwords) live in the keychain.
