package store

import "testing"

func TestForwardSpec(t *testing.T) {
	tests := []struct {
		name string
		fwd  Forward
		flag string
		spec string
	}{
		{
			name: "local",
			fwd:  Forward{Kind: ForwardLocal, ListenPort: 5432, TargetHost: "db.internal", TargetPort: 5432},
			flag: "-L", spec: "5432:db.internal:5432",
		},
		{
			name: "local with bind address",
			fwd:  Forward{Kind: ForwardLocal, BindAddr: "127.0.0.1", ListenPort: 8080, TargetHost: "web", TargetPort: 80},
			flag: "-L", spec: "127.0.0.1:8080:web:80",
		},
		{
			name: "remote",
			fwd:  Forward{Kind: ForwardRemote, ListenPort: 9000, TargetHost: "localhost", TargetPort: 3000},
			flag: "-R", spec: "9000:localhost:3000",
		},
		{
			name: "dynamic needs no target",
			fwd:  Forward{Kind: ForwardDynamic, ListenPort: 1080},
			flag: "-D", spec: "1080",
		},
		{
			name: "dynamic with bind address",
			fwd:  Forward{Kind: ForwardDynamic, BindAddr: "0.0.0.0", ListenPort: 1080},
			flag: "-D", spec: "0.0.0.0:1080",
		},
		{
			// ssh's forward grammar is colon-separated, so a bare IPv6 literal
			// is ambiguous and has to be bracketed.
			name: "ipv6 target is bracketed",
			fwd:  Forward{Kind: ForwardLocal, ListenPort: 5432, TargetHost: "2001:db8::1", TargetPort: 5432},
			flag: "-L", spec: "5432:[2001:db8::1]:5432",
		},
		{
			name: "ipv6 bind address is bracketed",
			fwd:  Forward{Kind: ForwardDynamic, BindAddr: "::1", ListenPort: 1080},
			flag: "-D", spec: "[::1]:1080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fwd.Kind.Flag(); got != tt.flag {
				t.Errorf("Flag() = %q, want %q", got, tt.flag)
			}
			if got := tt.fwd.Spec(); got != tt.spec {
				t.Errorf("Spec() = %q, want %q", got, tt.spec)
			}
		})
	}
}

func TestForwardValidate(t *testing.T) {
	tests := []struct {
		name    string
		fwd     Forward
		wantErr bool
	}{
		{"good local", Forward{Kind: ForwardLocal, ListenPort: 1, TargetHost: "h", TargetPort: 2}, false},
		{"good dynamic", Forward{Kind: ForwardDynamic, ListenPort: 1080}, false},
		{"zero listen port", Forward{Kind: ForwardDynamic}, true},
		{"listen port out of range", Forward{Kind: ForwardDynamic, ListenPort: 70000}, true},
		{"local without target host", Forward{Kind: ForwardLocal, ListenPort: 1, TargetPort: 2}, true},
		{"local without target port", Forward{Kind: ForwardLocal, ListenPort: 1, TargetHost: "h"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fwd.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBindsLocally(t *testing.T) {
	if !(Forward{Kind: ForwardLocal}).BindsLocally() || !(Forward{Kind: ForwardDynamic}).BindsLocally() {
		t.Error("local and dynamic forwards bind a local port")
	}
	if (Forward{Kind: ForwardRemote}).BindsLocally() {
		t.Error("a remote forward binds on the server, not here")
	}
}

func TestParseForwardKind(t *testing.T) {
	for _, in := range []string{"", "local", "LOCAL", "-L"} {
		if k, err := ParseForwardKind(in); err != nil || k != ForwardLocal {
			t.Errorf("ParseForwardKind(%q) = %v, %v", in, k, err)
		}
	}
	if k, _ := ParseForwardKind("socks"); k != ForwardDynamic {
		t.Errorf("socks should parse as dynamic")
	}
	if _, err := ParseForwardKind("sideways"); err == nil {
		t.Error("ParseForwardKind(sideways) should fail")
	}
}

func TestForwardStoreCRUDAndCascade(t *testing.T) {
	s := openTest(t)
	h, _ := s.PutHost(Host{Name: "db", Addr: "10.0.0.1"})

	f, err := s.PutForward(Forward{HostKey: h.StatKey(), Kind: ForwardLocal,
		ListenPort: 5432, TargetHost: "localhost", TargetPort: 5432})
	if err != nil || f.ID == "" {
		t.Fatalf("PutForward = %+v, %v", f, err)
	}
	// Rules can also hang off a config-sourced host, which has no id.
	if _, err := s.PutForward(Forward{HostKey: "cfg:orb", Kind: ForwardDynamic, ListenPort: 1080}); err != nil {
		t.Fatalf("PutForward for a config host: %v", err)
	}

	mine, _ := s.ForwardsFor(h.StatKey())
	if len(mine) != 1 || mine[0].ListenPort != 5432 {
		t.Fatalf("ForwardsFor = %+v", mine)
	}

	if _, err := s.PutForward(Forward{HostKey: h.StatKey(), Kind: ForwardLocal, ListenPort: 0}); err == nil {
		t.Error("PutForward accepted an invalid rule")
	}

	// Deleting the host takes its rules with it, but leaves other hosts' alone.
	if err := s.DeleteHost(h.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Forwards()
	if len(all) != 1 || all[0].HostKey != "cfg:orb" {
		t.Errorf("after deleting the host, forwards = %+v", all)
	}
}
