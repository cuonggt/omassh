package secrets

import (
	"errors"
	"testing"
)

func TestMemoryVaultRoundTrip(t *testing.T) {
	v := NewMemory()

	if _, err := v.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
	if err := v.Set("id1", "hunter2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := v.Get("id1")
	if err != nil || got != "hunter2" {
		t.Errorf("Get = %q, %v; want hunter2", got, err)
	}
	if err := v.Delete("id1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := v.Get("id1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
	}
	// Deleting what isn't there is not a failure.
	if err := v.Delete("id1"); err != nil {
		t.Errorf("second Delete = %v, want nil", err)
	}
}

func TestOpen(t *testing.T) {
	for _, kind := range []string{"", "keyring", "memory"} {
		v, err := Open(kind, "omassh-test")
		if err != nil || v == nil {
			t.Errorf("Open(%q) = %v, %v", kind, v, err)
		}
	}
	if _, err := Open("nope", "omassh-test"); err == nil {
		t.Error("Open(nope) should fail")
	}
}
