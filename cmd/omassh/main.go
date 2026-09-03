// Command omassh is a keyboard-driven SSH client for the terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/config"
	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/secrets"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// keyringService namespaces Omassh's entries in the OS credential store.
const keyringService = "omassh"

// Build information, set by the linker at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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
	defaultDB, err := store.DefaultPath()
	if err != nil {
		return err
	}
	defaultCfg, err := config.DefaultPath()
	if err != nil {
		return err
	}

	var sshOpts multiFlag
	flag.Var(&sshOpts, "o", "ssh option applied to every connection, as in ssh -o (repeatable)")
	dbPath := flag.String("db", defaultDB, "path to the omassh database")
	cfgPath := flag.String("config", defaultCfg, "path to the config file")
	printCfg := flag.Bool("print-config", false, "write a documented example config to stdout and exit")
	showVer := flag.Bool("version", false, "print version information and exit")
	secretStore := flag.String("secrets", "keyring",
		"where identity secrets live: keyring (OS credential store) or memory (this run only)")
	flag.Parse()

	if *showVer {
		fmt.Printf("omassh %s (%s, built %s)\n", version, commit, date)
		return nil
	}
	if *printCfg {
		fmt.Print(config.Example)
		return nil
	}

	// A broken config is reported rather than ignored: settings that silently
	// do nothing are worse than a startup error that says why.
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	palette, err := cfg.Palette()
	if err != nil {
		return err
	}
	theme.Apply(palette)
	km, err := cfg.Keymap()
	if err != nil {
		return err
	}
	probeTimeout, err := cfg.ProbeDuration()
	if err != nil {
		return err
	}
	// Command-line options come last so they win over the config file.
	sshx.SetGlobalOptions(append(append([]string{}, cfg.SSHOptions...), sshOpts...))

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

	opts := ui.Options{Keys: km, Fanout: cfg.Fanout, ProbeTimeout: probeTimeout}
	final, err := tea.NewProgram(ui.New(st, vault, sup, opts)).Run()
	// An SFTP session or an embedded pane owns an ssh child of its own; close
	// them explicitly rather than relying on process exit to reap them.
	if m, ok := final.(ui.Model); ok {
		m.Close()
	}
	return err
}
