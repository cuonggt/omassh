package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
)

const maxIncludeDepth = 8

// LoadSSHConfig reads an OpenSSH client config and returns the concrete host
// aliases it defines, including those reached through Include directives.
//
// Includes are expanded in place, preserving OpenSSH's first-obtained-value-
// wins ordering: a directive in an included file beats a later `Host *` in the
// including file, exactly as ssh(1) would apply it. This is why Omassh walks
// the config itself rather than leaning on ssh_config's own Get, which
// resolves include paths relative to ~/.ssh at decode time.
//
// A missing file is not an error: plenty of people have no ~/.ssh/config.
func LoadSSHConfig(path string) ([]Host, error) {
	blocks, err := flatten(path, 0, map[string]bool{})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var hosts []Host
	seen := map[string]bool{}
	for _, b := range blocks {
		for _, p := range b.Patterns {
			alias := p.String()
			if !isConcreteAlias(alias) || seen[alias] {
				continue
			}
			seen[alias] = true
			hosts = append(hosts, hostFromAlias(blocks, alias))
		}
	}
	SortHosts(hosts)
	return hosts, nil
}

// flatten returns every non-Match host block reachable from path, in the order
// ssh(1) would consider them, with Include directives spliced in at the point
// they appear.
func flatten(path string, depth int, visited map[string]bool) ([]*ssh_config.Host, error) {
	if depth > maxIncludeDepth {
		return nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if visited[abs] {
		return nil, nil // mutually-including files
	}
	visited[abs] = true

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, err
	}

	base := filepath.Dir(abs)
	var out []*ssh_config.Host
	for _, block := range cfg.Hosts {
		// Match blocks are conditional on runtime state (originalhost, user,
		// exec results). Omassh cannot evaluate them, so it neither lists nor
		// consults them rather than guessing wrong.
		if isMatchBlock(block) {
			continue
		}
		out = append(out, block)
		for _, node := range block.Nodes {
			inc, ok := node.(*ssh_config.Include)
			if !ok {
				continue
			}
			for _, p := range expandInclude(inc.String(), base) {
				sub, err := flatten(p, depth+1, visited)
				if err != nil {
					continue // an unreadable include is skipped, not fatal
				}
				out = append(out, sub...)
			}
		}
	}
	return out, nil
}

func hostFromAlias(blocks []*ssh_config.Host, alias string) Host {
	get := func(key string) string {
		return strings.Trim(lookup(blocks, alias, key), `"`)
	}

	h := Host{
		Name:      alias,
		Addr:      alias,
		User:      get("User"),
		Identity:  get("IdentityFile"),
		ProxyJump: get("ProxyJump"),
		GroupID:   SSHConfigGroupID,
		Source:    SourceSSHConfig,
	}
	if hn := get("HostName"); hn != "" {
		h.Addr = hn
	}
	if p, err := strconv.Atoi(get("Port")); err == nil {
		h.Port = p
	}
	// A ProxyCommand is arbitrary shell. Omassh notes that one is in play and
	// otherwise stays out of the way — OpenSSH runs it either way.
	if h.ProxyJump == "" && get("ProxyCommand") != "" {
		h.Note = "reached via ProxyCommand"
	}
	return h
}

// lookup returns the first value for key among blocks matching alias, which is
// how ssh(1) resolves a directive.
func lookup(blocks []*ssh_config.Host, alias, key string) string {
	for _, b := range blocks {
		if !b.Matches(alias) {
			continue
		}
		for _, node := range b.Nodes {
			kv, ok := node.(*ssh_config.KV)
			if ok && strings.EqualFold(kv.Key, key) {
				return strings.TrimSpace(kv.Value)
			}
		}
	}
	return ""
}

func isMatchBlock(h *ssh_config.Host) bool {
	return strings.HasPrefix(strings.TrimSpace(h.String()), "Match")
}

// expandInclude turns an Include directive line into concrete file paths.
// Relative paths resolve against the including file's directory, which for the
// standard ~/.ssh/config is exactly the ~/.ssh that ssh_config(5) specifies.
func expandInclude(line, base string) []string {
	s := strings.TrimSpace(line)
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "Include"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "="))

	var paths []string
	for _, raw := range strings.Fields(s) {
		p := expandTilde(strings.Trim(raw, `"`))
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		matches, err := filepath.Glob(p)
		if err != nil || len(matches) == 0 {
			continue
		}
		paths = append(paths, matches...)
	}
	return paths
}

func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// isConcreteAlias reports whether a Host pattern names one real host rather
// than a wildcard or negated match.
func isConcreteAlias(a string) bool {
	return a != "" && !strings.ContainsAny(a, "*?!")
}
