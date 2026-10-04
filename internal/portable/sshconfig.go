package portable

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sshcfg "github.com/kevinburke/ssh_config"

	"github.com/cuonggt/omassh/internal/store"
)

// hostName is the address ssh resolves HostName to for one alias.
//
// ssh expands two tokens there: %h, the name being connected to, and %% for a
// literal percent. `HostName %h.internal` is how a whole estate of machines
// gets one block instead of thirty, so it is common — and taken literally it
// became an address with a %h still in it, which nothing can resolve. ssh
// expands tokens in the HostName *setting*, never in the destination it is
// finally handed, so those hosts imported cleanly and then could not connect
// to anything.
//
// Only those two, because they are the only two ssh accepts here. Anything
// else makes ssh itself refuse the file ("percent_expand: failed"), and
// deciding what it ought to have meant would put an address in the list that
// ssh would never have used.
func hostName(raw, alias string) string {
	if !strings.Contains(raw, "%") {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '%' || i+1 == len(raw) {
			b.WriteByte(raw[i])
			continue
		}
		switch raw[i+1] {
		case 'h':
			b.WriteString(alias)
			i++
		case '%':
			b.WriteByte('%')
			i++
		default:
			b.WriteByte(raw[i])
		}
	}
	return b.String()
}

// DefaultSSHConfig is where OpenSSH keeps a user's client configuration.
func DefaultSSHConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// FromSSHConfig reads an OpenSSH client config and returns the machines named
// in it as a document, ready to merge.
//
// Only what Omassh needs to list, probe and reach a host is taken: the alias,
// the address behind it, and the user, port, key and jump host. Everything
// else in that file keeps working without being copied, because Omassh runs
// the real ssh, which reads the file itself — ControlMaster, certificates and
// Match blocks are honoured whether or not anything here knows about them.
// That needs ssh to be handed the alias rather than the address, or the block
// written under it is passed over; Aliases is what makes it so. Import is for
// discovery, not for taking over OpenSSH's configuration.
func FromSSHConfig(path string) (Document, error) {
	raw, err := expandIncludes(path, filepath.Dir(path), 0)
	if err != nil {
		return Document{}, err
	}
	cfg, err := decode(raw)
	if err != nil {
		return Document{}, fmt.Errorf("%s: %w", path, err)
	}

	// One alias may be declared more than once, its settings merging; Get
	// already accounts for that, so each alias is collected only once.
	var aliases []string
	seen := map[string]bool{}
	for _, h := range cfg.Hosts {
		if isMatchBlock(h) {
			continue
		}
		for _, p := range h.Patterns {
			a := p.String()
			if !concrete(a) || seen[key(a)] {
				continue
			}
			seen[key(a)] = true
			aliases = append(aliases, a)
		}
	}
	sort.Slice(aliases, func(i, j int) bool { return less(aliases[i], aliases[j]) })

	d := Document{Version: Version}
	for _, a := range aliases {
		get := func(k string) string {
			v, err := cfg.Get(a, k)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(v)
		}
		addr, port := reaches(cfg, a)
		h := Host{
			Name:     a,
			Addr:     addr,
			User:     get("User"),
			Identity: identityFor(cfg, a, get("IdentityFile")),
			Jump:     get("ProxyJump"),
		}
		// Port 22 is stored as unset: it is what ssh does anyway, and leaving
		// it out keeps an exported document free of noise.
		if port != 22 {
			h.Port = port
		}
		if strings.EqualFold(h.Jump, "none") {
			h.Jump = ""
		}
		d.Hosts = append(d.Hosts, h)
	}
	return d, nil
}

// reaches is where ssh goes for an alias: the address its HostName gives, with
// %h expanded, or the alias itself where nothing gives one — `Host myserver`
// with no HostName means connect to the literal name, which is how a great
// many entries are written — and the port, 22 where nothing says otherwise.
//
// Import stores what this says, and connecting compares a host against it, so
// the two cannot disagree about where an alias goes.
func reaches(cfg *sshcfg.Config, alias string) (string, int) {
	get := func(k string) string {
		v, err := cfg.Get(alias, k)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	addr := hostName(get("HostName"), alias)
	if addr == "" {
		addr = alias
	}
	port := 22
	if n, err := strconv.Atoi(get("Port")); err == nil && n > 0 {
		port = n
	}
	return addr, port
}

// Aliases lists the machines a config file names for itself, and where ssh
// takes each name, following Include as ssh does and ignoring omassh's own
// block.
//
// It is what lets a host the file names be reached by that name. ssh chooses
// the Host blocks that apply by the name on its command line, so a host
// reached by its address passed over everything written under its alias —
// see store.Host.Alias. Only names ssh would take as a destination are listed,
// since anything else is no way to reach a machine.
//
// omassh's own block is left out because everything in it came from omassh in
// the first place: reaching a host by the alias written there would add
// nothing but whatever has gone stale in it since the last export.
//
// A missing file names nothing.
func Aliases(path string) ([]store.ConfigAlias, error) {
	cfg, err := ownConfig(path)
	if err != nil || cfg == nil {
		return nil, err
	}
	var out []store.ConfigAlias
	seen := map[string]bool{}
	for _, h := range cfg.Hosts {
		if isMatchBlock(h) {
			continue
		}
		for _, p := range h.Patterns {
			a := p.String()
			if !usableAlias(a) || seen[key(a)] {
				continue
			}
			seen[key(a)] = true
			addr, port := reaches(cfg, a)
			out = append(out, store.ConfigAlias{Name: a, Addr: addr, Port: port})
		}
	}
	return out, nil
}

// ownConfig is a config file as ssh reads it, minus omassh's own block: what
// the file says for itself. Nil, and no error, for a file that is not there.
func ownConfig(path string) (*sshcfg.Config, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// The block is stripped first so that what omassh wrote is never read
	// back as the file's own.
	stripped, err := stripBlock(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	expanded, err := expandIncludesIn([]byte(stripped), filepath.Dir(path), 0)
	if err != nil {
		return nil, err
	}
	cfg, err := decode(expanded)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// decode parses a config once its Includes are spliced in.
//
// The parser ends each complaint with a newline of its own, which printed a
// blank line under it, so that is trimmed off.
func decode(raw []byte) (*sshcfg.Config, error) {
	cfg, err := sshcfg.DecodeBytes(inertMatches(raw))
	if err != nil {
		return nil, errors.New(strings.TrimSpace(err.Error()))
	}
	return cfg, nil
}

// inertMatches turns each Match block the parser cannot evaluate into one that
// applies to nothing.
//
// The parser understands `Match all` and `Match host`, and refuses the whole
// file at any other criterion: exec, user, originalhost, final, canonical,
// localnetwork, tagged. Every one of those is ordinary OpenSSH — `Match exec`
// is how a gpg agent is told which terminal it is on, and `Match final` is
// how a canonicalised name gets its settings — so a config ssh reads every day
// could not be imported at all, for a reason that had nothing to do with any
// of the machines in it.
//
// Most of them cannot be answered from here. exec runs a command, user and
// localuser depend on who is connecting, localnetwork on where this machine
// is, and final and canonical on what ssh makes of a name as it connects. So
// a block guarded by one is read as one whose condition is false: none of its
// settings are taken for any alias, and nothing in it is a machine. That is
// the cautious reading, since it never copies a setting written for some
// connections onto all of them, and costs nothing at connection time, when
// ssh reads the file itself and decides.
//
// originalhost can be answered: it is the name as typed, which for an alias is
// the alias — what the parser's own host criterion compares against. A block
// under it that set a HostName ahead of the alias's own block decides where
// ssh goes, so it is read rather than dropped.
//
// A Match on the host alone is left to the parser, which does answer it. One
// that adds another criterion is not, because the parser would read the
// criterion and its argument as two more host patterns, and apply the block
// to a host called user.
//
// The line is replaced rather than removed, so the settings beneath it stay
// inside a block of their own instead of joining the one above, and every
// later line keeps its number for the parser's own complaints.
func inertMatches(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		args, ok := directive(l, "match")
		if !ok {
			continue
		}
		for j, a := range args {
			// ssh reads the rest of a line from a word starting with # as a
			// comment, and so does the parser.
			if strings.HasPrefix(a, "#") {
				args = args[:j]
				break
			}
		}
		switch {
		case len(args) == 1 && strings.EqualFold(args[0], "all"),
			len(args) == 2 && strings.EqualFold(args[0], "host"):
			// The parser answers these as ssh does.
		case len(args) == 2 && strings.EqualFold(args[0], "originalhost"):
			lines[i] = "Match host " + args[1]
		default:
			lines[i] = "Match host !*"
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// identityFor is the key this host should be reached with.
//
// IdentityFile is one of the few settings ssh accumulates rather than decides.
// Every other one here is first-wins, which is ssh's rule and what `get`
// implements — but ask ssh about a host declared under both `Host *` and its
// own name and it lists both keys, and tries them in turn until one is
// accepted.
//
// Omassh keeps one key per host, so it has to choose, and taking the first was
// wrong in the layout people actually write: with `Host *` at the top of the
// file, every host came in holding the catch-all key and the key written under
// its own name was dropped. ssh would have offered that one second and got in
// with it; omassh passes what it stored, so a host the config reaches perfectly
// well became one that answers "Permission denied (publickey)".
//
// So a key written under the host's own name wins over one from a pattern that
// merely covers it, and first-wins decides between equals. Nothing is lost the
// other way round: -i adds to what ssh would try anyway, so the catch-all key
// is still offered if the agent or the default names hold it.
func identityFor(cfg *sshcfg.Config, alias, firstWins string) string {
	for _, h := range cfg.Hosts {
		if isMatchBlock(h) || !namesOutright(h, alias) {
			continue
		}
		for _, n := range h.Nodes {
			if kv, ok := n.(*sshcfg.KV); ok && strings.EqualFold(kv.Key, "IdentityFile") {
				if v := strings.TrimSpace(kv.Value); v != "" {
					return v
				}
			}
		}
	}
	return firstWins
}

// namesOutright reports whether a block names this host rather than matching it
// as one of a class. `Host web1 web2` names both; `Host web*` names neither.
//
// Case-insensitively, which ssh is not — `ssh upper` does not match `Host
// UPPER` — but which the list above already is: aliases are collected through
// key(), so two blocks differing only in case are one host here before this is
// ever asked, and the two of them disagreeing about a key is not a question
// worth having a second answer for.
func namesOutright(h *sshcfg.Host, alias string) bool {
	for _, p := range h.Patterns {
		if s := p.String(); concrete(s) && key(s) == key(alias) {
			return true
		}
	}
	return false
}

// isMatchBlock reports whether a block came from a Match directive rather than
// a Host one. The parser folds both into the same type, and a `Match host
// bastion` would otherwise be read as a host called bastion — a machine that
// does not exist, added to the list from a rule about one that does. String
// round-trips the original keyword, which is the only signal the package
// exposes.
func isMatchBlock(h *sshcfg.Host) bool {
	return strings.HasPrefix(strings.TrimSpace(h.String()), "Match ")
}

// concrete reports whether a pattern names one machine rather than a class of
// them. A wildcard block still contributes its settings, since Get applies it
// to every alias it matches, but there is no single host to add for `Host *`.
func concrete(p string) bool {
	return p != "" && !strings.ContainsAny(p, "*?!")
}

// maxIncludeDepth matches OpenSSH's own limit on nested Include directives.
const maxIncludeDepth = 5

// expandIncludes splices Include'd files into the text before it is parsed.
//
// The parser resolves an Include well enough to answer a question about an
// alias you already know, but it does not expose the aliases inside one, and
// enumerating them is the whole of this job — a config that is nothing but
// `Include ~/.orbstack/ssh/config` would otherwise import as empty. Since
// Include is defined as textual inclusion at that point in the file, splicing
// preserves the first-match-wins order the parser then relies on.
//
// An Include written inside a Host or Match block is conditional in OpenSSH
// and unconditional here. That costs nothing for enumeration, which wants
// every alias the file can reach.
func expandIncludes(path, base string, depth int) ([]byte, error) {
	if depth > maxIncludeDepth {
		return nil, fmt.Errorf("%s: Include nested more than %d deep", path, maxIncludeDepth)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return expandIncludesIn(raw, base, depth)
}

// expandIncludesIn is the same splicing for text already in hand.
//
// Scanning a config for the aliases it declares does not want a copy of it
// written next to the original: ~/.ssh is not somewhere to leave scratch
// files, and a dry run that promises to write nothing has to mean it.
func expandIncludesIn(raw []byte, base string, depth int) ([]byte, error) {
	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		args, ok := directive(line, "include")
		if !ok {
			out.WriteString(line + "\n")
			continue
		}
		for _, arg := range args {
			matches, err := resolveInclude(arg, base)
			if err != nil {
				return nil, err
			}
			for _, m := range matches {
				// A file listed but missing is skipped, as ssh skips it.
				sub, err := expandIncludes(m, base, depth+1)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return nil, err
				}
				out.Write(sub)
				out.WriteString("\n")
			}
		}
	}
	return []byte(out.String()), nil
}

// directive reports the arguments on a line that is the given keyword, if it
// is one — `Include a b`, or ssh's own `Include=a` form.
func directive(line, keyword string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if i := strings.IndexAny(s, "="); i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), keyword) {
		s = keyword + " " + s[i+1:]
	}
	fields := strings.Fields(s)
	if len(fields) < 2 || !strings.EqualFold(fields[0], keyword) {
		return nil, false
	}
	return fields[1:], true
}

// resolveInclude turns one Include argument into the files it names. A
// relative path is relative to the directory of the config being read, which
// for ~/.ssh/config is the ~/.ssh that OpenSSH specifies.
func resolveInclude(arg, base string) ([]string, error) {
	arg = strings.Trim(arg, `"`)
	if strings.HasPrefix(arg, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		arg = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(arg, "~"), "/"))
	}
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(base, arg)
	}
	matches, err := filepath.Glob(arg)
	if err != nil {
		return nil, fmt.Errorf("Include %s: %w", arg, err)
	}
	sort.Strings(matches)
	return matches, nil
}
