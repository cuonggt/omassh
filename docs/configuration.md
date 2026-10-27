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
    muted: 8

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
colour and the sixteen its scheme sets. So Omassh looks like the rest of the
terminal, reads as well on a light background as on a dark one, and follows
the terminal when its scheme changes — under Omarchy, that means it follows
the Omarchy theme without being told about it.

`omarchy` follows it on purpose rather than by side effect. It draws in the
theme Omarchy is actually set to, read from Omarchy's own current theme at
`~/.local/state/omarchy/current/theme/colors.toml` — a symlink Omarchy
repoints as the theme changes, which is what makes the name mean "whatever is
set now" rather than one particular palette. Where `terminal` picks up only
what the terminal's own scheme carries, this gets the whole palette: the
accent, the selection, the muted border. Start Omassh again after switching
themes and it follows. On a machine without Omarchy, naming it is a startup
error saying so rather than a quietly different palette, and the theme picker
does not offer it there at all.

A theme is allowed to leave roles out, and Omarchy has a documented cascade so
that every program reading the file lands on the same palette — `fg` for
`foreground`, `color8` for `muted`, `purple` for `magenta`, and so on. Omassh
follows that cascade rather than guessing, so the host list cannot disagree
with the bar at the top of the screen about what the theme is. Values Omassh
cannot draw — the gradients and `rgba()` that other consumers use — count as
roles the theme did not set, and anything still unset falls back to `terminal`.

Omarchy's light themes are excluded from the built-in list but not from this:
if Omarchy is set to one, that is what you get, and it is right, because the
terminal it is drawn in went light at the same moment.

The rest are Omarchy's own themes, each colour taken from the theme's
`colors.toml`, so the list `T` offers is the list `omarchy-theme-set` offers
and a machine running one of them can be asked for the same name here:
`catppuccin`, `everforest`, `gruvbox`, `kanagawa`, `matte-black`, `nord`,
`ristretto`, `tokyo-night` and the others. They are hex, and made for a dark
background; on a terminal without truecolor they render in 256 colours, and
`mono` exists for terminals with less than that. Omarchy's light themes are
not among them: Omassh paints no background of its own, so a light foreground
would land on whatever the terminal already had behind it.

Some of those themes are nearly monochrome — `vantablack` and `solitude` give
`red` and `green` as greys. Nothing breaks, because no colour here is ever the
only thing saying what it means: a failure is red *and* `✖`, a tunnel is green
*and* `▶`.

## Palettes of your own

The colours are the roles an Omarchy theme names, so a palette can be copied
straight out of a `colors.toml`. One sets any of `accent`, `selection`,
`muted`, `foreground`, `dark_foreground`, `bright_foreground`, `red`,
`yellow`, `green` and `magenta`, each to one of:

- `"#rrggbb"`, in quotes, because a bare `#` starts a YAML comment
- a number from the terminal's palette: `4` is its blue, `8` its grey, and
  anything up to `255` works
- `default`, the terminal's own text colour — which, as `selection`, means no
  fill at all, leaving the row under the cursor marked by its edge

These used to be named `text`, `text_dim`, `text_bright`, `border` and
`selected_bg`. A config file written against those still loads: the names
changed and the colours they stand for did not.

Omarchy's surface roles — `background` and its lighter and darker kin — are
not here, because Omassh paints no background. The terminal's own is the
surface, which is what lets the default palette read on a light terminal as
well as a dark one.

Colours a palette leaves out come from `terminal`, so `accent: "#ff8800"` on
its own is the terminal's colours with an orange accent. That is also the way
out for a scheme whose grey is its background, as the original Solarized
Dark's is: `terminal` draws dim text and borders in the grey, colour `8`, as
most terminal programs do, so under that scheme they vanish, and
`dark_foreground: 10` with `muted: 10` brings them back.

## How it is drawn

The chrome follows Omarchy's design language, so that one rule answers what
used to be decided screen by screen:

- **Square corners and one-cell borders.** Omarchy rounds nothing by default,
  and a terminal has no half-cell to round with.
- **A blank cell between panels**, the way Hyprland puts `gaps_in` between
  tiled windows, rather than two borders touching and drawing a divider twice
  as thick as the hairline intended. Nothing is spent at the frame's edge —
  the terminal window already has `gaps_out` around it. On a terminal too
  short for the gap it gives way rather than pushing the layout off the
  bottom.
- **Focus is the accent border and a bold title.** Only one panel has it, and
  colour is never the only thing saying so.
- **The cursor is a bar at the row's edge**, `▌` in the accent, with the
  palette's `selection` behind the text where it names one. Omarchy carries
  emphasis on an edge rather than in a solid fill, and the bar is the one mark
  every palette can draw. Its column is held open on every row, so arriving at
  a row never shifts it.
- **A cursor and which panel has focus are different things.** Tab away from a
  list and it keeps showing where you were, in `muted` instead of the accent.
  Where a list also has a persistent choice — the tags on a form, the hosts a
  snippet will run on, the theme actually configured — that is marked
  separately, with `✓`, `☑` or `●`.
- **Colour means something or is not used.** `red` is a failure, `green` a
  success or something running, `yellow` a state that is not the resting one —
  a tunnel carrying what it was started with, a transfer still going, the
  session prefix armed — and `magenta` is code, the `ssh …` line you could
  type yourself. Body text, headings and keycaps are weight and brightness
  rather than hue, and `accent` is kept for focus and the cursor.

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
`select` where it means `selection`, is named with its line rather than
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
