// Package keys lists the private keys someone has, so a form can offer them
// instead of asking for a path to be typed from memory.
package keys

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// head is how much of a file is read to decide what it is. The longest thing
// this looks for — "-----BEGIN ENCRYPTED PRIVATE KEY-----" — is thirty-seven
// bytes, and nothing in the directory is read further than this: some of what
// is in there is not a key, and need not be small.
const head = 64

// In lists the private keys in dir, by path and in order.
//
// Nothing for a directory that is not there or cannot be read: a machine with
// no ~/.ssh has no keys to offer, which is not a failure to report. Nothing
// for an empty dir either, which is how the tests say "look nowhere" — a
// suite that offered its runner's own keys would assert on whatever they
// happen to keep.
func In(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		// A .pub is the public half, and skipping it by name saves opening a
		// file whose first line would rule it out anyway.
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		// Only regular files are opened. ~/.ssh is also where ssh is usually
		// told to keep its control sockets, and opening one of those waits
		// for whoever is listening — which here would be nobody, with the
		// interface stopped until the open gave up.
		if info, err := e.Info(); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if p := filepath.Join(dir, e.Name()); isPrivate(p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// isPrivate reads a file's first line, which is where every private key ssh
// takes says what it is.
//
// The name is not asked: id_ed25519, id_rsa and deploy.pem are all real
// names, and so is a file called `config`, which is in that directory on
// every machine. What the line rules out is the rest of what lives there —
// a public key begins with its algorithm, known_hosts with a hostname, and a
// certificate says CERTIFICATE where a key says PRIVATE KEY.
//
// A key that cannot be opened is not offered. Somebody else's key, readable
// only by them, is not one this could use either.
func isPrivate(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, head)
	n, _ := f.Read(buf)
	line := string(buf[:n])
	return strings.HasPrefix(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY")
}
