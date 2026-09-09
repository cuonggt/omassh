package portable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sshcfg "github.com/kevinburke/ssh_config"
)

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
// Import is for discovery, not for taking over OpenSSH's configuration.
func FromSSHConfig(path string) (Document, error) {
	raw, err := expandIncludes(path, filepath.Dir(path), 0)
	if err != nil {
		return Document{}, err
	}
	cfg, err := sshcfg.DecodeBytes(raw)
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
		h := Host{
			Name:     a,
			Addr:     get("HostName"),
			User:     get("User"),
			Identity: get("IdentityFile"),
			Jump:     get("ProxyJump"),
		}
		// `Host myserver` with no HostName means connect to the literal name,
		// which is how a great many entries are written.
		if h.Addr == "" {
			h.Addr = a
		}
		// Port 22 is stored as unset: it is what ssh does anyway, and leaving
		// it out keeps an exported document free of noise.
		if n, err := strconv.Atoi(get("Port")); err == nil && n != 22 && n > 0 {
			h.Port = n
		}
		if strings.EqualFold(h.Jump, "none") {
			h.Jump = ""
		}
		d.Hosts = append(d.Hosts, h)
	}
	return d, nil
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

	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		args, ok := includeDirective(line)
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

// includeDirective reports the paths on an Include line, if it is one.
func includeDirective(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if i := strings.IndexAny(s, "="); i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), "include") {
		s = "Include " + s[i+1:]
	}
	fields := strings.Fields(s)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "include") {
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
