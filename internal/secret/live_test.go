package secret

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// EnvLive is what turns the test below on.
const EnvLive = "OMASSH_KEYCHAIN_TEST"

// The real keychain, exercised only when asked for.
//
// Off by default, and deliberately so: this is the one test in the suite that
// cannot be isolated. security(1) takes a named keychain only as a trailing
// argument, where it is read as the value of -w — so writing to anywhere but
// the default keychain means putting the password in the argument list, which
// is the thing the whole backend exists to avoid. The choice is between a test
// that writes into the keychain of whoever runs it and a test that has to be
// asked for, and asking is the smaller cost.
//
//	OMASSH_KEYCHAIN_TEST=1 go test ./internal/secret -run Live
func TestLiveKeychainRoundTrip(t *testing.T) {
	if os.Getenv(EnvLive) == "" {
		t.Skipf("set %s=1 to run this against the real keychain", EnvLive)
	}
	st, err := Open()
	if err != nil {
		t.Skipf("no store on this machine: %v", err)
	}

	// Named so that anything left behind by a crashed run is obvious, and
	// removed again however this ends.
	id := fmt.Sprintf("omassh-live-test-%d", os.Getpid())
	t.Cleanup(func() { st.Delete(id) })

	if _, err := st.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("something is already filed under %q: %v", id, err)
	}

	const want = "correct horse battery staple  "
	if err := st.Set(id, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q — trailing spaces included", got, want)
	}

	if err := st.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Get(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
	}
}
