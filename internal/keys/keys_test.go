package keys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateUnencrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_test")

	info, err := Generate(path, "omassh-test", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if info.Type != "ED25519" || info.Bits != 256 {
		t.Errorf("got %s/%d, want ED25519/256", info.Type, info.Bits)
	}
	if !strings.HasPrefix(info.Fingerprint, "SHA256:") {
		t.Errorf("Fingerprint = %q", info.Fingerprint)
	}
	if info.Comment != "omassh-test" {
		t.Errorf("Comment = %q, want omassh-test", info.Comment)
	}
	if info.Encrypted {
		t.Error("Encrypted = true for a key made with an empty passphrase")
	}
	if _, err := os.Stat(path + ".pub"); err != nil {
		t.Errorf("public half missing: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key mode = %o, want 600", perm)
	}
}

func TestGenerateEncryptedIsDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_locked")

	info, err := Generate(path, "locked", "correct horse")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !info.Encrypted {
		t.Error("Encrypted = false for a passphrase-protected key")
	}
}

// Overwriting a private key destroys access to everything trusting it.
func TestGenerateRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_test")
	if _, err := Generate(path, "first", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(path, "second", ""); err == nil {
		t.Fatal("Generate overwrote an existing key")
	}
	info, err := Inspect(path)
	if err != nil || info.Comment != "first" {
		t.Errorf("original key was modified: %+v, %v", info, err)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	if _, err := Generate(filepath.Join(dir, "id_a"), "a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(filepath.Join(dir, "id_b"), "b", "pw"); err != nil {
		t.Fatal(err)
	}
	// Noise that must not be listed: a lone public key, and other ssh files.
	os.WriteFile(filepath.Join(dir, "orphan.pub"), []byte("ssh-ed25519 AAAA x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("host key\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "config"), []byte("Host x\n"), 0o600)

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d keys, want 2: %+v", len(got), got)
	}
	byComment := map[string]Info{}
	for _, k := range got {
		byComment[k.Comment] = k
	}
	if !byComment["b"].Encrypted || byComment["a"].Encrypted {
		t.Errorf("encryption flags wrong: a=%v b=%v", byComment["a"].Encrypted, byComment["b"].Encrypted)
	}
}

func TestListMissingDirIsEmpty(t *testing.T) {
	got, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil || got != nil {
		t.Errorf("got %v, %v; want nil, nil", got, err)
	}
}

func TestParseKeyLine(t *testing.T) {
	tests := []struct {
		line    string
		bits    int
		typ     string
		comment string
	}{
		{"256 SHA256:abc plain (ED25519)", 256, "ED25519", "plain"},
		{"3072 SHA256:xyz user@host (RSA)", 3072, "RSA", "user@host"},
		{"256 SHA256:abc a comment with spaces (ED25519)", 256, "ED25519", "a comment with spaces"},
		{"256 SHA256:abc no comment (ED25519)", 256, "ED25519", ""},
	}
	for _, tt := range tests {
		got, err := ParseKeyLine(tt.line)
		if err != nil {
			t.Errorf("ParseKeyLine(%q): %v", tt.line, err)
			continue
		}
		if got.Bits != tt.bits || got.Type != tt.typ || got.Comment != tt.comment {
			t.Errorf("ParseKeyLine(%q) = %d/%s/%q, want %d/%s/%q",
				tt.line, got.Bits, got.Type, got.Comment, tt.bits, tt.typ, tt.comment)
		}
	}
	if _, err := ParseKeyLine("garbage"); err == nil {
		t.Error("ParseKeyLine(garbage) should fail")
	}
}
