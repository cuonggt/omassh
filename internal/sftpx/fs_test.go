package sftpx

import "testing"

// The shapes a status error can arrive in, which a server cannot be made to
// produce on demand. The message is unexported and printed as `sftp: "…"
// (CODE)`, so the text is the only way to it — and the only risk in reading
// the text is that a future format leaves nothing to find.
func TestWhatIsTakenFromAStatusError(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{{
		"the operation and the path lead, as they do in an os error",
		`sftp: "open /srv/app/a.sock: operation not supported on socket" (SSH_FX_FAILURE)`,
		"operation not supported on socket",
	}, {
		"a bare sentence is kept whole",
		`sftp: "Permission denied" (SSH_FX_FAILURE)`,
		"Permission denied",
	}, {
		"a full disk, which is the one people meet",
		`sftp: "write /srv/app/big.bin: no space left on device" (SSH_FX_FAILURE)`,
		"no space left on device",
	}, {
		"a path with a colon of its own, where only the last one is the reason",
		`sftp: "open /srv/weird: name/file.txt: permission denied" (SSH_FX_FAILURE)`,
		"permission denied",
	}, {
		// What OpenSSH's own sftp-server sends: the code's name, spelled as a
		// word, which says nothing the code did not.
		"a message that is only the code's name",
		`sftp: "Failure" (SSH_FX_FAILURE)`,
		"the server refused it, without saying why",
	}, {
		"and the same for any other code named that way",
		`sftp: "No such file" (SSH_FX_NO_SUCH_FILE)`,
		"the server refused it, without saying why",
	}, {
		"a server that says nothing",
		`sftp: "" (SSH_FX_FAILURE)`,
		"the server refused it, without saying why",
	}, {
		"a format this cannot read",
		`something else entirely`,
		"the server refused it, without saying why",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := saidBy(tc.raw); got != tc.want {
				t.Errorf("saidBy(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
