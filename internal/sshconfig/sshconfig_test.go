package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWildcardInheritanceAndFirstMatchWins(t *testing.T) {
	cfg := `# shared defaults for a fleet
Host app-web-prod*
  HostName 192.0.2.10
  User deploy
  IdentityFile ~/.ssh/id_ed25519
  Port 2222

Host app-web-prod-01
  Port 22

Host app-web-prod-02
  User override
  Port 2200

Host *
  User should-not-win
  Port 9999
`
	parsed, err := Parse(cfg, "/home/x/.ssh")
	if err != nil {
		t.Fatal(err)
	}
	got := parsed.Apply("app-web-prod-01")
	if got.HostName != "192.0.2.10" || got.User != "deploy" || got.Port != 22 {
		t.Fatalf("01 resolved wrong: %s", got.Debug())
	}
	if len(got.IdentityFiles) != 1 {
		t.Fatalf("identity not inherited: %v", got.IdentityFiles)
	}
	got = parsed.Apply("app-web-prod-02")
	if got.Port != 2200 || got.User != "override" {
		t.Fatalf("02 resolved wrong: %s", got.Debug())
	}
	// Host * comes last, so it cannot override earlier values.
	if got.HostName != "192.0.2.10" {
		t.Fatalf("first-match-wins broken: %s", got.Debug())
	}
}

func TestNegatedAndMultiplePatterns(t *testing.T) {
	cfg := `Host web1 web2 !web2
  User shared
Host db*
  User dbu
`
	parsed, _ := Parse(cfg, "")
	if got := parsed.Apply("web1"); got.User != "shared" {
		t.Fatalf("web1: %s", got.Debug())
	}
	if got := parsed.Apply("web2"); got.User != "" {
		t.Fatalf("web2 should be excluded: %s", got.Debug())
	}
	if got := parsed.Apply("db9"); got.User != "dbu" {
		t.Fatalf("db9: %s", got.Debug())
	}
}

func TestMatchBlockSkipped(t *testing.T) {
	cfg := `Host a
  User alice
Match host b
  User mallory
`
	parsed, _ := Parse(cfg, "")
	if got := parsed.Apply("b"); got.User != "" {
		t.Fatalf("Match block leaked: %s", got.Debug())
	}
	if got := parsed.Apply("a"); got.User != "alice" {
		t.Fatalf("a: %s", got.Debug())
	}
}

func TestIncludeAndAliases(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	inc := filepath.Join(sub, "web.conf")
	if err := os.WriteFile(inc, []byte("Host included\n  HostName 203.0.113.9\n  CertificateFile ~/.ssh/id_ed25519-cert.pub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := "Include conf.d/*.conf\nHost local\n  HostName 127.0.0.1\n"
	parsed, err := Parse(main, dir)
	if err != nil {
		t.Fatal(err)
	}
	aliases := parsed.Aliases()
	if len(aliases) != 2 || aliases[0] != "included" || aliases[1] != "local" {
		t.Fatalf("aliases = %v", aliases)
	}
	got := parsed.Apply("included")
	if got.HostName != "203.0.113.9" {
		t.Fatalf("include not applied: %s", got.Debug())
	}
	if got.CertificateFile == "" {
		t.Fatal("certificate file not parsed")
	}
}

func TestLocalForwardFormsAndTokens(t *testing.T) {
	cfg := `Host bastion
  HostName %h.example.com
  LocalForward 8080 localhost:80
  LocalForward 127.0.0.1:5432 db.internal:5432
  LocalForward [::1]:9000 [2001:db8::1]:9000
  ProxyJump ops@jump:2222
  ForwardAgent yes
`
	parsed, _ := Parse(cfg, "")
	got := parsed.Apply("bastion")
	if got.HostName != "bastion.example.com" {
		t.Fatalf("hostname token: %s", got.Debug())
	}
	if len(got.LocalForwards) != 3 {
		t.Fatalf("forwards = %v", got.LocalForwards)
	}
	if got.ProxyJump != "ops@jump:2222" || !got.ForwardAgent {
		t.Fatalf("options: %s", got.Debug())
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"web*", "web-1", true},
		{"web?", "web1", true},
		{"web?", "web12", false},
		{"*prod*", "app-prod-1", true},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "example.com", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
	}
	for _, c := range cases {
		if got := matchPattern(c.pattern, c.name); got != c.want {
			t.Errorf("matchPattern(%q,%q)=%v want %v", c.pattern, c.name, got, c.want)
		}
	}
}
