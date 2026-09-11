// Command omassh is a keyboard-driven SSH client for the terminal.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/config"
	"github.com/cuonggt/omassh/internal/portable"
	"github.com/cuonggt/omassh/internal/safefile"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// Build information, set by the linker at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `omassh — a keyboard-driven SSH client for the terminal.

  omassh                                browse and connect
  omassh export [-o FILE]               write the host list as YAML
  omassh import [-n] [FILE]             merge a YAML host list, stdin if no file
  omassh import-ssh-config [-n] [FILE]  merge ~/.ssh/config
  omassh export-ssh-config [-n]         write the hosts into ~/.ssh/config

Records match by name, not by the ids the database mints locally, so a list
exported on one machine merges into another; importing the same list twice
changes nothing the second time; and a host keeps its session history across
an import. A field the document leaves out keeps the value already stored.

Flags:
`

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "omassh:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// A subcommand is a bare word. Anything starting with a dash is a flag to
	// the browser, which is what omassh does when asked for nothing else.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		run, ok := commands[args[0]]
		if !ok {
			return fmt.Errorf("unknown command %q — see omassh -h", args[0])
		}
		return run(args[1:])
	}
	return browse(args)
}

// commands are the subcommands, each of which takes flags of its own. Both
// dispatch and the complaint about one written after the flags read this, so
// neither can learn of a command the other has not.
var commands = map[string]func([]string) error{
	"export":            runExport,
	"import":            runImport,
	"import-ssh-config": runImportSSHConfig,
	"export-ssh-config": runExportSSHConfig,
}

func browse(args []string) error {
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
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		return err
	}
	// Anything left over was meant to do something, and nothing here reads it.
	//
	// flag stops at the first bare word, so `omassh -db other.db import
	// hosts.yaml` took the -db, dropped the rest and opened the browser — on
	// the database that had been asked for, which is exactly what made it look
	// like it had worked, while the import silently never happened.
	if flag.NArg() > 0 {
		arg := flag.Arg(0)
		if _, ok := commands[arg]; ok {
			return fmt.Errorf("%s is a command and comes first: omassh %s [flags], not omassh [flags] %s", arg, arg, arg)
		}
		return fmt.Errorf("nothing here takes %q — see omassh -h", arg)
	}

	if *showVer {
		fmt.Printf("omassh %s (%s, built %s)\n", version, commit, date)
		return nil
	}
	if *printCfg {
		fmt.Print(config.Example(*cfgPath))
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
	sshx.SetGlobalOptions(globalSSHOptions(sshOpts, cfg.SSHOptions))

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	opts := ui.Options{
		Keys: km, ProbeTimeout: probeTimeout, Version: version,
		Theme: cfg.ThemeName(), Themes: cfg.Themes,
		// The picker writes to the same file Load just read, so a theme
		// chosen in the interface and one written by hand are one setting.
		SaveTheme: func(name string) error { return config.SetTheme(*cfgPath, name) },
	}
	final, err := tea.NewProgram(ui.New(st, opts)).Run()
	// An SFTP session or an embedded pane owns an ssh child of its own; close
	// them explicitly rather than relying on process exit to reap them.
	if m, ok := final.(ui.Model); ok {
		m.Close()
	}
	return err
}

// globalSSHOptions is what every connection carries, in the order that decides
// which value wins.
//
// ssh keeps the first value it is given for an option and ignores the rest, so
// order here is precedence. The command line goes first: `-o ConnectTimeout=5`
// has to outrank a config file asking for 30, and putting it after meant the
// file quietly won — while the command omassh displayed carried both, reading
// as though the last one applied.
func globalSSHOptions(cmdLine, fromConfig []string) []string {
	return append(append([]string{}, cmdLine...), fromConfig...)
}

// --- export and import -------------------------------------------------

// subcommand builds a flag set carrying the flags every one of them takes.
func subcommand(name, summary string) (*flag.FlagSet, *string, error) {
	defaultDB, err := store.DefaultPath()
	if err != nil {
		return nil, nil, err
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	db := fs.String("db", defaultDB, "path to the omassh database")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\n%s\n\nFlags:\n", summary, name)
		fs.PrintDefaults()
	}
	return fs, db, nil
}

func runExport(args []string) error {
	fs, db, err := subcommand("omassh export [-o FILE]",
		"Write the host list as YAML, for a dotfiles repo or another machine.")
	if err != nil {
		return err
	}
	// Written by omassh rather than by the shell so the mode is 0600: the
	// file is an inventory of your infrastructure, even though the only
	// secret in it is a path to a key.
	out := fs.String("o", "", "write to this file, mode 0600, instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	// A record the store cannot decode is skipped, not fatal: refusing to
	// export would mean the readable records could not be rescued either. The
	// complaint goes to stderr, so it is seen even when the list is redirected
	// into a file or piped into another machine.
	groups, gerr := st.Groups()
	hosts, herr := st.Hosts()
	forwards, ferr := st.Forwards()
	for _, e := range []error{gerr, herr, ferr} {
		if e != nil {
			fmt.Fprintln(os.Stderr, "omassh: "+e.Error())
		}
	}

	raw, err := portable.Export(groups, hosts, forwards).YAML()
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	if err := os.WriteFile(*out, raw, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d hosts and %d groups to %s\n", len(hosts), len(groups), *out)
	return nil
}

func runImport(args []string) error {
	fs, db, err := subcommand("omassh import [-n] [FILE]",
		"Merge a host list written by omassh export. Reads stdin if given no file.")
	if err != nil {
		return err
	}
	dry := fs.Bool("n", false, "report what would change, and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := readDocument(fs.Arg(0))
	if err != nil {
		return err
	}
	d, err := portable.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", source(fs.Arg(0)), err)
	}
	// Valid YAML that is not a host list parses into nothing at all, and
	// would otherwise report a tidy no-op for what is really the wrong file.
	if len(d.Hosts) == 0 && len(d.Groups) == 0 {
		return fmt.Errorf("%s holds no hosts or groups — is it an omassh export?", source(fs.Arg(0)))
	}
	return apply(*db, d, *dry)
}

func runImportSSHConfig(args []string) error {
	fs, db, err := subcommand("omassh import-ssh-config [-n] [-group NAME] [FILE]",
		"Merge the machines named in an OpenSSH client config, ~/.ssh/config by default.")
	if err != nil {
		return err
	}
	dry := fs.Bool("n", false, "report what would change, and write nothing")
	group := fs.String("group", "", "put every imported host in this group, creating it if needed")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := fs.Arg(0)
	if path == "" {
		if path, err = portable.DefaultSSHConfig(); err != nil {
			return err
		}
	}
	d, err := portable.FromSSHConfig(path)
	if err != nil {
		return err
	}
	if *group != "" {
		for i := range d.Hosts {
			if d.Hosts[i].Group == "" {
				d.Hosts[i].Group = *group
			}
		}
	}
	if len(d.Hosts) == 0 {
		fmt.Printf("%s names no hosts to import\n", path)
		return nil
	}
	return apply(*db, d, *dry)
}

// runExportSSHConfig writes the host list into an OpenSSH client config.
//
// A host kept only in Omassh is reachable by Omassh and nothing else: scp,
// rsync, git, Ansible and every editor's remote mode read ~/.ssh/config. This
// is the one thing that writes there, and it writes only between its own
// markers.
func runExportSSHConfig(args []string) error {
	fs, db, err := subcommand("omassh export-ssh-config [-n] [-o FILE]",
		"Write the host list into an OpenSSH client config, so scp, git and rsync reach the same machines.")
	if err != nil {
		return err
	}
	dry := fs.Bool("n", false, "report what would change, and write nothing")
	out := fs.String("o", "", "write to this file instead of ~/.ssh/config")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *out
	if path == "" {
		if path, err = portable.DefaultSSHConfig(); err != nil {
			return err
		}
	}

	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	// As with export: a record that will not decode is named rather than
	// fatal, so the rest can still be written.
	groups, gerr := st.Groups()
	hosts, herr := st.Hosts()
	for _, e := range []error{gerr, herr} {
		if e != nil {
			fmt.Fprintln(os.Stderr, "omassh: "+e.Error())
		}
	}

	// Written out resolved: ssh config has no notion of a group, so what a
	// host inherits has to be spelled out on the host itself.
	r := store.NewResolver(groups, hosts)
	resolved := make([]store.Host, 0, len(hosts))
	for _, h := range hosts {
		resolved = append(resolved, r.Resolve(h).Host)
	}

	declared, err := portable.DeclaredAliases(path)
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	plan, err := portable.ExportSSHConfig(existing, declared, resolved)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, n := range plan.Written {
		fmt.Println("  write  " + n)
	}
	for _, l := range plan.Left {
		fmt.Printf("  keep   %s  — %s\n", l.Name, l.Why)
	}
	summary := fmt.Sprintf("%d written, %d left alone", len(plan.Written), len(plan.Left))
	if *dry {
		fmt.Println(summary + " — nothing written, drop -n to apply")
		return nil
	}
	if err := safefile.Replace(path, plan.Content, 0o600); err != nil {
		return err
	}
	fmt.Printf("%s — %s\n", summary, path)
	return nil
}

// source names where a document came from, for an error that has to say.
func source(path string) string {
	if path == "" || path == "-" {
		return "stdin"
	}
	return path
}

// readDocument reads the named file, or stdin for "-" or nothing, so an
// export can be piped straight into an import over ssh.
func readDocument(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// apply merges a document into the store, reporting every record it touches.
//
// The plan is worked out in full before anything is written, so a document
// that cannot be applied is refused whole rather than halfway.
func apply(db string, d portable.Document, dry bool) error {
	st, err := store.Open(db)
	if err != nil {
		return err
	}
	defer st.Close()

	// As with export: what cannot be decoded is named rather than fatal. A
	// record that will not read is also one no incoming record can match by
	// name, so it may end up duplicated — saying so is the honest part.
	groups, gerr := st.Groups()
	hosts, herr := st.Hosts()
	forwards, ferr := st.Forwards()
	for _, e := range []error{gerr, herr, ferr} {
		if e != nil {
			fmt.Fprintln(os.Stderr, "omassh: "+e.Error())
		}
	}

	plan, err := portable.Merge(d, groups, hosts, forwards)
	if err != nil {
		return err
	}

	for _, c := range plan.Changes {
		fmt.Println(" ", c)
	}
	added, updated := plan.Counts()
	summary := fmt.Sprintf("%d added, %d updated, %d already matched", added, updated, plan.Unchanged)
	if dry {
		fmt.Println(summary + " — nothing written, drop -n to apply")
		return nil
	}
	// One transaction for the lot: a record at a time meant a trip to the disk
	// each, which took the better part of a minute for a few thousand of them
	// and left part of a list behind if the disk filled up on the way.
	if err := st.PutAll(plan.Groups, plan.Hosts, plan.Forwards); err != nil {
		return err
	}
	fmt.Println(summary)
	return nil
}
