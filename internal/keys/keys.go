// Package keys generates and inspects SSH private keys.
//
// Every operation shells out to ssh-keygen rather than using Go's crypto
// packages, for the same reason sessions shell out to ssh: the files produced
// are then exactly what OpenSSH expects, including its own private-key format,
// and Omassh inherits any future changes for free.
package keys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Info describes one key pair.
type Info struct {
	Path        string
	Type        string
	Bits        int
	Fingerprint string
	Comment     string
	Encrypted   bool
}

func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ssh"
	}
	return filepath.Join(home, ".ssh")
}

// Generate writes a new ed25519 key pair at path. An empty passphrase creates
// an unencrypted key, which is what ssh-keygen does too.
func Generate(path, comment, passphrase string) (Info, error) {
	if path == "" {
		return Info{}, fmt.Errorf("a key needs a path")
	}
	if _, err := os.Stat(path); err == nil {
		// Overwriting a private key destroys access to everything trusting it.
		return Info{}, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Info{}, err
	}

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", passphrase, "-C", comment, "-f", path, "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		return Info{}, fmt.Errorf("ssh-keygen: %s", strings.TrimSpace(string(out)))
	}
	return Inspect(path)
}

// Inspect reads a key's metadata, including whether it is passphrase-protected.
func Inspect(path string) (Info, error) {
	out, err := exec.Command("ssh-keygen", "-l", "-f", path).Output()
	if err != nil {
		return Info{}, fmt.Errorf("not a readable key: %s", filepath.Base(path))
	}
	info, err := parseKeyLine(strings.TrimSpace(string(out)))
	if err != nil {
		return Info{}, err
	}
	info.Path = path
	info.Encrypted = isEncrypted(path)
	return info, nil
}

// isEncrypted reports whether path needs a passphrase, by asking ssh-keygen to
// derive the public key with an empty one.
func isEncrypted(path string) bool {
	return exec.Command("ssh-keygen", "-y", "-P", "", "-f", path).Run() != nil
}

// List returns every key pair in dir, identified by having a .pub sibling.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		priv := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".pub"))
		if _, err := os.Stat(priv); err != nil {
			continue // a .pub with no private half is not usable here
		}
		if info, err := Inspect(priv); err == nil {
			out = append(out, info)
		}
	}
	return out, nil
}

// parseKeyLine reads ssh-keygen -l output: "256 SHA256:abc… comment (ED25519)".
func parseKeyLine(line string) (Info, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return Info{}, fmt.Errorf("unrecognised ssh-keygen output: %q", line)
	}
	info := Info{Fingerprint: fields[1]}
	info.Bits, _ = strconv.Atoi(fields[0])

	rest := fields[2:]
	if last := rest[len(rest)-1]; strings.HasPrefix(last, "(") && strings.HasSuffix(last, ")") {
		info.Type = strings.Trim(last, "()")
		rest = rest[:len(rest)-1]
	}
	info.Comment = strings.Join(rest, " ")
	if info.Comment == "no comment" {
		info.Comment = ""
	}
	return info, nil
}

// ParseKeyLine is the shared parser for ssh-keygen and ssh-add listings.
func ParseKeyLine(line string) (Info, error) { return parseKeyLine(line) }
