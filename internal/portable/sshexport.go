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
func ExportSSHConfig(existing []byte, declared map[string]bool, hosts []store.Host) SSHConfigPlan {
	sorted := append([]store.Host(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return less(sorted[i].Name, sorted[j].Name) })

	var p SSHConfigPlan
	var entries []string
	for _, h := range sorted {
		switch {
		case !usableAlias(h.Name):
			p.Left = append(p.Left, LeftOut{h.Name, "cannot be an ssh alias"})
		case declared[key(h.Name)]:
			p.Left = append(p.Left, LeftOut{h.Name, "your own config already defines it"})
		default:
			entries = append(entries, sshEntry(h))
			p.Written = append(p.Written, h.Name)
		}
	}

	p.Block = blockStart + "\n" + blockHeader + "\n\n" +
		strings.Join(entries, "\n") + "\n" + blockEnd + "\n"

	body := strings.TrimLeft(stripBlock(string(existing)), "\n")
	out := p.Block
	if body != "" {
		out += "\n" + body
	}
	p.Content = []byte(out)
	return p
}

// usableAlias reports whether a host's name can be an ssh alias at all.
//
// ssh refuses a destination with whitespace in it — "hostname contains
// invalid characters" — however the config quotes it, so an entry under such
// a name is one nothing could ever use, and writing it unquoted would declare
// two aliases out of the pieces. A name holding *, ? or ! is worse: it is a
// pattern rather than a machine, and ssh would apply this host's settings to
// every alias it happened to match, including hosts omassh knows nothing
// about.
func usableAlias(name string) bool {
	if !concrete(name) || strings.ContainsAny(name, `"'\`) {
		return false
	}
	return strings.IndexFunc(name, unicode.IsSpace) < 0
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
func stripBlock(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	inside := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == blockStart:
			inside = true
		case t == blockEnd:
			inside = false
		case !inside:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
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
	stripped := stripBlock(string(raw))

	tmp, err := os.CreateTemp(filepath.Dir(path), ".omassh-scan-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(stripped); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	expanded, err := expandIncludes(tmp.Name(), filepath.Dir(path), 0)
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
