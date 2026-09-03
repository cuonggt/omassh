package store

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func byName(hs []Host) map[string]Host {
	m := map[string]Host{}
	for _, h := range hs {
		m[h.Name] = h
	}
	return m
}

func TestLoadSSHConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "conf.d/extra.conf", `
Host included-host
  HostName 192.168.50.5
  User alice
  Port 2200
`)
	path := writeConfig(t, dir, "config", `
Include conf.d/*.conf

Host direct
  HostName 10.0.0.5
  Port 2222
  User bob
  IdentityFile ~/.ssh/id_direct
  ProxyJump bastion

Host alpha beta
  HostName multi.example

Host wild-*
  HostName never.listed

Host tunnelled
  HostName 127.0.0.1
  ProxyCommand /usr/bin/something --fd-pass

Match host somewhere
  User matched

Host *
  User fallback
`)

	hosts, err := LoadSSHConfig(path)
	if err != nil {
		t.Fatalf("LoadSSHConfig: %v", err)
	}
	got := byName(hosts)

	t.Run("enumerates concrete aliases only", func(t *testing.T) {
		want := []string{"alpha", "beta", "direct", "included-host", "tunnelled"}
		if len(hosts) != len(want) {
			t.Fatalf("got %d hosts %v, want %d %v", len(hosts), keys(got), len(want), want)
		}
		for _, w := range want {
			if _, ok := got[w]; !ok {
				t.Errorf("missing alias %q (got %v)", w, keys(got))
			}
		}
	})

	t.Run("wildcard and Match blocks are not hosts", func(t *testing.T) {
		for _, bad := range []string{"wild-*", "*", "somewhere"} {
			if _, ok := got[bad]; ok {
				t.Errorf("alias %q should not be listed", bad)
			}
		}
	})

	t.Run("resolves values through includes", func(t *testing.T) {
		h := got["included-host"]
		if h.Addr != "192.168.50.5" || h.User != "alice" || h.Port != 2200 {
			t.Errorf("included-host = %+v, want 192.168.50.5 alice:2200", h)
		}
	})

	t.Run("reads directives from the host block", func(t *testing.T) {
		h := got["direct"]
		if h.Addr != "10.0.0.5" || h.Port != 2222 || h.User != "bob" {
			t.Errorf("direct = %+v", h)
		}
		if h.Identity != "~/.ssh/id_direct" || h.ProxyJump != "bastion" {
			t.Errorf("direct identity/jump = %q / %q", h.Identity, h.ProxyJump)
		}
	})

	t.Run("falls back to Host * defaults", func(t *testing.T) {
		if h := got["alpha"]; h.User != "fallback" {
			t.Errorf("alpha User = %q, want fallback from Host *", h.User)
		}
	})

	t.Run("multiple patterns on one line each become a host", func(t *testing.T) {
		if got["alpha"].Addr != "multi.example" || got["beta"].Addr != "multi.example" {
			t.Errorf("alpha/beta addrs = %q / %q", got["alpha"].Addr, got["beta"].Addr)
		}
	})

	t.Run("flags ProxyCommand rather than reinterpreting it", func(t *testing.T) {
		if h := got["tunnelled"]; h.Note == "" {
			t.Errorf("tunnelled Note = %q, want a ProxyCommand note", h.Note)
		}
	})

	t.Run("all hosts are marked as config-sourced", func(t *testing.T) {
		for _, h := range hosts {
			if h.Source != SourceSSHConfig || h.GroupID != SSHConfigGroupID {
				t.Errorf("%s: source = %v, group = %q", h.Name, h.Source, h.GroupID)
			}
		}
	})
}

func keys(m map[string]Host) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLoadSSHConfigMissingFileIsNotAnError(t *testing.T) {
	hosts, err := LoadSSHConfig(filepath.Join(t.TempDir(), "nope"))
	if err != nil || hosts != nil {
		t.Errorf("got %v, %v; want nil, nil", hosts, err)
	}
}

func TestLoadSSHConfigSkipsUnreadableInclude(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "config", "Include /definitely/not/here/*\n\nHost still-here\n  HostName 1.1.1.1\n")

	hosts, err := LoadSSHConfig(path)
	if err != nil {
		t.Fatalf("a broken include should not fail the load: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "still-here" {
		t.Errorf("got %v, want the host that follows the bad include", hosts)
	}
}

func TestLoadSSHConfigStopsAtIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "b.conf", "Include a.conf\nHost from-b\n  HostName 2.2.2.2\n")
	path := writeConfig(t, dir, "a.conf", "Include b.conf\nHost from-a\n  HostName 1.1.1.1\n")

	done := make(chan []Host, 1)
	go func() {
		hosts, _ := LoadSSHConfig(path)
		done <- hosts
	}()
	select {
	case hosts := <-done:
		if len(hosts) != 2 {
			t.Errorf("got %d hosts, want 2 from the mutually-including files", len(hosts))
		}
	case <-timeout():
		t.Fatal("LoadSSHConfig did not terminate on an include cycle")
	}
}
