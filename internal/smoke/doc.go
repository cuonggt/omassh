// Package smoke drives the real omassh binary against a real SSH server.
//
// Everything else in the tree tests a package. This tests the program: the
// binary as it ships, running in a terminal, with tmux behind it, talking to
// something that actually answers.
//
// It exists because two bugs reached a release without a single test failing.
// security(1) asks for a password on /dev/tty when it can find one, so the
// keychain write hung the moment it ran somewhere with a terminal — which the
// unit tests, calling the function directly, never had. And the environment a
// password credential needs was set on an exec.Cmd that the tmux path then
// replaced, four lines later. Both are invisible to a test that calls the
// functions and looks at what they return; both are obvious the first time
// somebody presses the key.
//
// The tests here are slow and need tmux, and they skip where it is absent, as
// the term tests do. They are not a substitute for those: what they cover is
// the wiring between packages, which is exactly what nothing else sees.
package smoke
