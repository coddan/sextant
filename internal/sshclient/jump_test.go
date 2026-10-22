package sshclient

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func lineFor(host string, key ssh.PublicKey) string {
	return host + " " + key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal()) + "\n"
}

func TestKnownHostsPlainChangedAndWildcard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testKey(t)
	other := testKey(t)
	content := lineFor("192.0.2.1", key) +
		lineFor("[192.0.2.2]:2222", key) +
		lineFor("*.example.com", key) +
		"@revoked 192.0.2.9 " + other.Type() + " " + base64.StdEncoding.EncodeToString(other.Marshal()) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := k.Lookup("192.0.2.1", 22, key); v != KeyKnown {
		t.Fatalf("plain: %v", v)
	}
	if v, _ := k.Lookup("192.0.2.2", 2222, key); v != KeyKnown {
		t.Fatalf("bracket: %v", v)
	}
	if v, _ := k.Lookup("a.example.com", 22, key); v != KeyKnown {
		t.Fatalf("wildcard: %v", v)
	}
	if v, _ := k.Lookup("192.0.2.1", 22, other); v != KeyChanged {
		t.Fatalf("changed: %v", v)
	}
	if v, _ := k.Lookup("192.0.2.3", 22, key); v != KeyUnknown {
		t.Fatalf("unknown: %v", v)
	}
	if v, _ := k.Lookup("192.0.2.9", 22, other); v != KeyRevoked {
		t.Fatalf("revoked: %v", v)
	}
}

func TestKnownHostsHashed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testKey(t)
	salt := []byte("0123456789abcdef")
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte("secret.example"))
	entry := "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	content := entry + " " + key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal()) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	k, _ := LoadKnownHosts(path)
	if v, _ := k.Lookup("secret.example", 22, key); v != KeyKnown {
		t.Fatalf("hashed lookup = %v", v)
	}
	if v, _ := k.Lookup("other.example", 22, key); v != KeyUnknown {
		t.Fatalf("hashed false positive = %v", v)
	}
}

func TestKnownHostsAddAndFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	k, err := LoadKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	if err := k.Add("host.example", 2222, key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[host.example]:2222 "; len(data) < len(want) || string(data[:len(want)]) != want {
		t.Fatalf("written line = %q", data)
	}
	reloaded, _ := LoadKnownHosts(path)
	if v, _ := reloaded.Lookup("host.example", 2222, key); v != KeyKnown {
		t.Fatalf("reload = %v", v)
	}
	fp := Fingerprint(key)
	sum := sha256.Sum256(key.Marshal())
	if fp != "SHA256:"+base64.RawStdEncoding.EncodeToString(sum[:]) {
		t.Fatalf("fingerprint = %q", fp)
	}
}

func TestJumpChainInheritsAliasAndOverrides(t *testing.T) {
	jump := model.NewHost("bastion", "jump.example.com")
	jump.User = "ops"
	jump.Port = 2200
	jump.IdentityFiles = []string{"~/.ssh/jump"}
	inner := model.NewHost("inner", "10.0.0.5")
	inner.ProxyJump = "bastion"
	target := model.NewHost("web", "10.0.0.9")
	target.ProxyJump = "inner,root@other:2222"

	chain, err := ResolveChain(target, []model.Host{jump, inner})
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain = %v", chain)
	}
	if chain[0].Name != "bastion" || chain[0].Port != 2200 || chain[0].User != "ops" {
		t.Fatalf("bastion = %+v", chain[0])
	}
	if chain[1].Name != "inner" {
		t.Fatalf("inner = %+v", chain[1])
	}
	if chain[2].Hostname != "other" || chain[2].User != "root" || chain[2].Port != 2222 {
		t.Fatalf("literal hop = %+v", chain[2])
	}
	for _, h := range chain {
		if h.ProxyJump != "" {
			t.Fatalf("hop still has ProxyJump: %+v", h)
		}
	}
}

func TestJumpCycleAndNone(t *testing.T) {
	a := model.NewHost("a", "1.1.1.1")
	b := model.NewHost("b", "2.2.2.2")
	a.ProxyJump = "b"
	b.ProxyJump = "a"
	if _, err := ResolveChain(a, []model.Host{a, b}); err == nil {
		t.Fatal("expected cycle error")
	}
	c := model.NewHost("c", "3.3.3.3")
	c.ProxyJump = "none"
	if chain, err := ResolveChain(c, nil); err != nil || len(chain) != 0 {
		t.Fatalf("none = %v %v", chain, err)
	}
	d := model.NewHost("d", "4.4.4.4")
	d.ProxyJump = "u@[2001:db8::1]:2201"
	chain, err := ResolveChain(d, nil)
	if err != nil || len(chain) != 1 || chain[0].Hostname != "2001:db8::1" || chain[0].Port != 2201 || chain[0].User != "u" {
		t.Fatalf("ipv6 hop = %+v %v", chain, err)
	}
}
