// Command omassh is a keyboard-driven SSH client for the terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/secrets"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui"
)

// keyringService namespaces Omassh's entries in the OS credential store.
const keyringService = "omassh"

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	// ssh-add executes this binary as its SSH_ASKPASS helper. That mode must
	// be handled before anything else: no flags, no database, no UI.
	if secrets.ServeAskpass() {
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "omassh:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultPath, err := store.DefaultPath()
	if err != nil {
		return err
	}
	var sshOpts multiFlag
	flag.Var(&sshOpts, "o", "ssh option applied to every connection, as in ssh -o (repeatable)")
	dbPath := flag.String("db", defaultPath, "path to the omassh database")
	secretStore := flag.String("secrets", "keyring",
		"where identity secrets live: keyring (OS credential store) or memory (this run only)")
	flag.Parse()
	sshx.SetGlobalOptions(sshOpts)

	vault, err := secrets.Open(*secretStore, keyringService)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Tunnels are children of this process. Stopping them on the way out is
	// what makes that honest rather than a leak.
	sup := forward.New(nil)
	defer sup.StopAll()

	final, err := tea.NewProgram(ui.New(st, vault, sup)).Run()
	// An open SFTP session owns an ssh child of its own; close it explicitly
	// rather than relying on process exit to reap it.
	if m, ok := final.(ui.Model); ok {
		m.CloseSFTP()
	}
	return err
}
