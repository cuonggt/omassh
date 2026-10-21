package keys_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cuonggt/omassh/internal/keys"
)

// write puts a file in dir and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A real key, made the way anyone makes one, so the header this recognises is
// the header ssh-keygen writes rather than the one this test believes it
// writes.
func TestARealKeyIsOffered(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed; the keys it writes are what this reads")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "test", "-f", path, "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}

	if got := keys.In(dir); !slices.Equal(got, []string{path}) {
		t.Errorf("In() = %q, want just the key — the .pub beside it is the public half", got)
	}
}

// What else lives in that directory is not a key, and saying it was would put
// a path on a host that ssh will refuse.
func TestWhatIsNotAKeyIsNotOffered(t *testing.T) {
	dir := t.TempDir()
	key := write(t, dir, "id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaA\n-----END OPENSSH PRIVATE KEY-----\n")
	write(t, dir, "id_ed25519.pub", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 test\n")
	write(t, dir, "known_hosts", "example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5\n")
	write(t, dir, "config", "Host prod\n    HostName 10.0.0.5\n")
	write(t, dir, "server.crt", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	write(t, dir, "notes.txt", "the key is in the drawer\n")
	if err := os.Mkdir(filepath.Join(dir, "backup"), 0o700); err != nil {
		t.Fatal(err)
	}

	if got := keys.In(dir); !slices.Equal(got, []string{key}) {
		t.Errorf("In() = %q, want just %q", got, key)
	}
}

// Every private key format ssh takes, including one with a passphrase, says
// PRIVATE KEY on its first line.
func TestEveryKindOfKeyIsOffered(t *testing.T) {
	dir := t.TempDir()
	var want []string
	for name, header := range map[string]string{
		"openssh":   "-----BEGIN OPENSSH PRIVATE KEY-----",
		"rsa":       "-----BEGIN RSA PRIVATE KEY-----",
		"ec":        "-----BEGIN EC PRIVATE KEY-----",
		"pkcs8":     "-----BEGIN PRIVATE KEY-----",
		"encrypted": "-----BEGIN ENCRYPTED PRIVATE KEY-----",
	} {
		want = append(want, write(t, dir, name, header+"\nAAAA\n"))
	}
	slices.Sort(want)

	if got := keys.In(dir); !slices.Equal(got, want) {
		t.Errorf("In() = %q, want %q", got, want)
	}
}

// A directory that is not there has no keys to offer, and an empty name is
// how the tests say to look nowhere at all.
func TestNowhereToLookIsNoKeys(t *testing.T) {
	for what, dir := range map[string]string{
		"no directory at all": filepath.Join(t.TempDir(), "nothing-here"),
		"nowhere named":       "",
	} {
		if got := keys.In(dir); got != nil {
			t.Errorf("%s: In() = %q, want nothing", what, got)
		}
	}
}
