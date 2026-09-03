package sshx

import (
	"slices"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		host  store.Host
		extra []string
		want  []string
	}{
		{
			name: "default port is left implicit",
			host: store.Host{Addr: "example.com", Port: 22, User: "root"},
			want: []string{"root@example.com"},
		},
		{
			name: "zero port is treated as default",
			host: store.Host{Addr: "example.com", User: "root"},
			want: []string{"root@example.com"},
		},
		{
			name: "non-default port",
			host: store.Host{Addr: "example.com", Port: 2222, User: "root"},
			want: []string{"-p", "2222", "root@example.com"},
		},
		{
			name: "no user means no @ prefix",
			host: store.Host{Addr: "example.com"},
			want: []string{"example.com"},
		},
		{
			name: "identity and jump host",
			host: store.Host{Addr: "10.0.1.14", User: "admin", Identity: "~/.ssh/id_ed25519", ProxyJump: "bastion.corp"},
			want: []string{"-i", "~/.ssh/id_ed25519", "-J", "bastion.corp", "admin@10.0.1.14"},
		},
		{
			name:  "extra flags precede the target",
			host:  store.Host{Addr: "example.com", User: "root"},
			extra: []string{"-N", "-L", "5432:db:5432"},
			want:  []string{"-N", "-L", "5432:db:5432", "root@example.com"},
		},
		{
			// The whole point of tracking Source: OpenSSH already knows how to
			// reach these, and re-deriving a subset would override the rest.
			name:  "ssh_config hosts are addressed by alias alone",
			host:  store.Host{Name: "prod-web", Addr: "10.0.1.14", Port: 2222, User: "admin", Identity: "~/.ssh/k", ProxyJump: "bastion", Source: store.SourceSSHConfig},
			extra: []string{"-N"},
			want:  []string{"-N", "prod-web"},
		},
		{
			name: "ipv6 literal is passed through untouched",
			host: store.Host{Addr: "2001:db8::1", User: "root", Port: 2222},
			want: []string{"-p", "2222", "root@2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.host, tt.extra...)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Build must not retain or alias the caller's extra slice.
func TestBuildDoesNotAliasExtra(t *testing.T) {
	extra := []string{"-N", "-L", "1:2:3"}
	before := slices.Clone(extra)
	Build(store.Host{Addr: "h", User: "u"}, extra...)
	if !slices.Equal(extra, before) {
		t.Errorf("Build mutated extra: %q, want %q", extra, before)
	}
}

// -o settings must reach every connection path, including hosts addressed by
// their ssh_config alias.
func TestGlobalOptions(t *testing.T) {
	t.Cleanup(func() { SetGlobalOptions(nil) })
	SetGlobalOptions([]string{"ConnectTimeout=5", "BatchMode=yes"})

	got := Build(store.Host{Addr: "example.com", User: "root"})
	want := []string{"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "root@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("Build() = %q, want %q", got, want)
	}

	got = Build(store.Host{Name: "alias", Source: store.SourceSSHConfig})
	want = []string{"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "alias"}
	if !slices.Equal(got, want) {
		t.Errorf("config-host Build() = %q, want %q", got, want)
	}

	SetGlobalOptions(nil)
	if got := Build(store.Host{Addr: "h"}); !slices.Equal(got, []string{"h"}) {
		t.Errorf("after reset Build() = %q", got)
	}
}
