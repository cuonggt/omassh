package portable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	sshcfg "github.com/kevinburke/ssh_config"

	"github.com/cuonggt/omassh/internal/store"
)

// Omassh writes its hosts into an OpenSSH client config between these markers
// and touches nothing else in the file.
//
// The point is that a host kept in Omassh is reachable by everything else on
// the machine: scp, rsync, git, Ansible and every editor's remote mode read
// ~/.ssh/config and know nothing about a database. Import is the other
// direction, and stays read-only — this is the only thing that writes there.
const (
	blockStart = "# >>> omassh >>>"
	blockEnd   = "# <<< omassh <<<"
)

// blockHeader explains the block to whoever opens the file next, including
// why it is where it is.
const blockHeader = `# Written by ` + "`omassh export-ssh-config`" + `. Keep your hosts in omassh:
# anything changed between these markers is replaced the next time it runs.
#
# The block goes first because ssh keeps the first value it finds for each
# setting, so a Host * above it would quietly outrank everything here.`

// SSHConfigPlan is what an export would do to a config file.
type SSHConfigPlan struct {
	// Content is the whole file as it would be written.
	Content []byte
	// Block is only what omassh contributes, for a dry run to show.
	Block string
	// Written names the hosts that made it in; Left names the ones that did
	// not, each with the reason.
	Written []string
	Left    []LeftOut
}

// LeftOut is a host the export did not write, and why.
type LeftOut struct {
	Name string
	Why  string
}

// ExportSSHConfig folds a host list into an OpenSSH client config.
//
// declared is every alias the file already defines outside omassh's block,
// Include'd files and all. A host whose name is among them is left out and
// reported: the file's own entry is what ssh has been using, and the block
// goes above it, so writing one would silently take that host over. Import
// treats a config as a read-only source; this keeps the same promise from the
// other side.
func ExportSSHConfig(existing []byte, declared map[string]bool, hosts []store.Host) (SSHConfigPlan, error) {
	body, err := stripBlock(string(existing))
	if err != nil {
		return SSHConfigPlan{}, err
	}

	sorted := append([]store.Host(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return less(sorted[i].Name, sorted[j].Name) })

	var p SSHConfigPlan
	keep := make([]store.Host, 0, len(sorted))
	for _, h := range sorted {
		switch {
		case !usableAlias(h.Name):
			p.Left = append(p.Left, LeftOut{h.Name, "cannot be an ssh alias"})
		case declared[key(h.Name)]:
			p.Left = append(p.Left, LeftOut{h.Name, "your own config already defines it"})
		default:
			keep = append(keep, h)
		}
	}
	keep, p.Left = withReachableJumps(keep, declared, hosts, p.Left)

	var entries []string
	for _, h := range keep {
		entries = append(entries, sshEntry(h))
		p.Written = append(p.Written, h.Name)
	}

	p.Block = blockStart + "\n" + blockHeader + "\n\n" +
		strings.Join(entries, "\n") + "\n" + blockEnd + "\n"

	out := p.Block
	if body = strings.TrimLeft(body, "\n"); body != "" {
		out += "\n" + body
	}
	p.Content = []byte(out)
	return p, nil
}

// withReachableJumps drops hosts whose jump host will not be in the file.
//
// A ProxyJump naming one of your hosts is only worth writing if that host is
// written too. One left out for a name ssh cannot use took its dependants
// with it into a config that referred to an alias nothing declared — and the
// alternative, writing the host without its ProxyJump, is far worse: it would
// dial the address directly, which for a machine behind a bastion is either
// nothing at all or, on the wrong network, something else entirely.
//
// A jump host that is none of yours — `ops@edge.example.com` — is left where
// it is, exactly as import leaves it, because it is ssh's to make sense of.
// So is one your own config declares.
//
// Repeated until it settles, since dropping a host can strand the hosts that
// jump through it.
func withReachableJumps(keep []store.Host, declared map[string]bool, all []store.Host, left []LeftOut) ([]store.Host, []LeftOut) {
	ours := make(map[string]bool, len(all))
	for _, h := range all {
		ours[key(h.Name)] = true
	}
	for {
		here := make(map[string]bool, len(keep))
		for _, h := range keep {
			here[key(h.Name)] = true
		}
		var out []store.Host
		dropped := false
		for _, h := range keep {
			j := key(strings.TrimSpace(h.ProxyJump))
			if j == "" || !ours[j] || here[j] || declared[j] {
				out = append(out, h)
				continue
			}
			left = append(left, LeftOut{h.Name, "its jump host " + strconv.Quote(h.ProxyJump) + " is not in the file"})
			dropped = true
		}
		keep = out
		if !dropped {
			return keep, left
		}
	}
}

// refusedBySSH is what ssh will not have in a destination, whatever the config
// says about it.
//
// Read off ssh itself rather than guessed at: each of these was written into a
// Host block and handed back to `ssh -G`, which answered "hostname contains
// invalid characters" for exactly this set. Most are shell metacharacters, on
// the reasoning that a destination reaches a ProxyCommand eventually.
const refusedBySSH = "\"$&'(),;<>\\`{|}"

// usableAlias reports whether a host's name can be an ssh alias at all.
//
// ssh refuses a destination holding any of the above — "hostname contains
// invalid characters" — however the config quotes it, so an entry under such a
// name is one nothing could ever use. Whitespace is refused for the same
// reason, with a second one behind it: written unquoted, a name with a space
// declares two aliases out of the pieces.
//
// A name holding *, ? or ! is worse than unusable: it is a pattern rather than
// a machine, and ssh would apply this host's settings to every alias it
// happened to match, including hosts omassh knows nothing about.
//
// The rest are names ssh takes and then reads as something other than a name,
// which is the quietest failure of the three — no complaint from anybody, and
// a connection to the wrong place or to nowhere:
//
//   - @ separates a user from a host, so "we@b" is the host b with the user
//     we, and the stanza written for it matches nothing. A leading one is a
//     destination with an empty user, which ssh answers with its usage.
//   - A leading - is a flag.
//   - A leading # opens a comment, so the Host line declaring the alias is not
//     a Host line at all and the settings under it belong to whatever came
//     before.
//   - A leading = is the separator in ssh's own Key=Value form, so "Host
//     =front" declares the alias front.
//
// Getting this wrong is quiet in every direction. The name is written into the
// file, the run reports it as one of the hosts exported, and it is only the
// first `ssh that-host` that says otherwise — so the check is against what ssh
// does, and the test that keeps it honest asks ssh rather than this list.
func usableAlias(name string) bool {
	switch {
	case !concrete(name),
		strings.ContainsAny(name, refusedBySSH),
		strings.Contains(name, "@"),
		strings.HasPrefix(name, "-"),
		strings.HasPrefix(name, "#"),
		strings.HasPrefix(name, "="):
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

// sshEntry is one host as an ssh config stanza.
func sshEntry(h store.Host) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Host %s\n", h.Name)
	fmt.Fprintf(&b, "    HostName %s\n", sshValue(h.Addr))
	// Port 22 is what ssh does anyway, and saying so is noise.
	if h.Port != 0 && h.Port != 22 {
		fmt.Fprintf(&b, "    Port %s\n", strconv.Itoa(h.Port))
	}
	for _, f := range []struct{ key, val string }{
		{"User", h.User},
		{"IdentityFile", h.Identity},
		// By name, not spelled out as a ProxyCommand the way the command line
		// needs. Inside a config file ssh resolves the name against the file
		// itself, so the hop is reached with its own port and key — which is
		// the very thing -J on a command line cannot do.
		{"ProxyJump", h.ProxyJump},
	} {
		if strings.TrimSpace(f.val) != "" {
			fmt.Fprintf(&b, "    %s %s\n", f.key, sshValue(f.val))
		}
	}
	return b.String()
}

// sshValue quotes an argument that would otherwise be read as several.
//
// ssh splits a stanza's arguments on whitespace, so a host named "web one" or
// a key under a path with a space in it would silently become two aliases and
// two filenames.
func sshValue(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// stripBlock removes omassh's block, leaving the rest of the file untouched.
//
// Line by line rather than by parsing: everything outside the markers has to
// come back byte for byte, comments, blank lines, indentation and all, and a
// config file is one of the few things on a machine that is worse to reformat
// than to leave alone.
//
// Markers that do not pair up are refused rather than interpreted. A start
// whose end had been deleted — by hand, by a truncation, by a merge going
// wrong — meant everything after it read as omassh's, so the whole of the
// rest of the file was dropped and the export reported a clean write. There
// is no recovering a ~/.ssh/config from that, so it is not guessed at.
func stripBlock(s string) (string, error) {
	lines := strings.Split(s, "\n")
	var out []string
	start := 0
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == blockStart:
			if start != 0 {
				return "", fmt.Errorf("line %d: a second %s before the first one ends", i+1, blockStart)
			}
			start = i + 1
		case t == blockEnd:
			if start == 0 {
				return "", fmt.Errorf("line %d: %s with no %s above it", i+1, blockEnd, blockStart)
			}
			start = 0
		case start == 0:
			out = append(out, l)
		}
	}
	if start != 0 {
		return "", fmt.Errorf("line %d: %s is never closed by %s", start, blockStart, blockEnd)
	}
	return strings.Join(out, "\n"), nil
}

// DeclaredAliases lists the concrete aliases a config file already defines for
// itself, following Include as ssh does and ignoring omassh's own block.
//
// A missing file declares nothing, which is what a first export meets.
func DeclaredAliases(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	// The block is stripped first so a second export does not see its own
	// hosts as the file's and refuse to write any of them.
	stripped, err := stripBlock(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	expanded, err := expandIncludesIn([]byte(stripped), filepath.Dir(path), 0)
	if err != nil {
		return nil, err
	}
	cfg, err := sshcfg.DecodeBytes(expanded)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	out := map[string]bool{}
	for _, h := range cfg.Hosts {
		if isMatchBlock(h) {
			continue
		}
		for _, pat := range h.Patterns {
			if a := pat.String(); concrete(a) {
				out[key(a)] = true
			}
		}
	}
	return out, nil
}
