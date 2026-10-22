package putty

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The keys in testdata were generated with puttygen and match the encryption
// and KDF variants shipped by PuTTY itself: v2 with AES-256-CBC, v3 without
// encryption, and v3 with the Argon2id key derivation. The passphrase used for
// the encrypted ones is "testkey".

func TestIsPPK(t *testing.T) {
	dir := t.TempDir()

	renamed := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(renamed, []byte("PuTTY-User-Key-File-3: ssh-ed25519\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsPPK(renamed) {
		t.Error("content should identify a PPK regardless of extension")
	}

	byExt := filepath.Join(dir, "key.ppk")
	if err := os.WriteFile(byExt, []byte("not a putty key at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsPPK(byExt) {
		t.Error(".ppk extension should be recognised")
	}

	plain := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(plain, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsPPK(plain) {
		t.Error("an OpenSSH key is not a PPK")
	}
}

func TestInspectVariants(t *testing.T) {
	cases := []struct {
		file      string
		version   int
		encrypted bool
		kdf       string
	}{
		{"rsa-v2-aes256.ppk", 2, true, ""},
		{"rsa-v3-none.ppk", 3, false, ""},
		{"rsa-v3-argon2.ppk", 3, true, "Argon2id"},
	}
	for _, tc := range cases {
		info, err := Inspect(filepath.Join("testdata", tc.file))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if info.Version != tc.version || info.Encrypted != tc.encrypted || info.KDF != tc.kdf {
			t.Errorf("%s: got %+v", tc.file, info)
		}
		if info.Comment != "a@b" {
			t.Errorf("%s: comment = %q", tc.file, info.Comment)
		}
	}
}

func TestConvertUnencrypted(t *testing.T) {
	res, err := Convert(filepath.Join("testdata", "rsa-v3-none.ppk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.PrivateKey), "BEGIN OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected private key encoding:\n%s", res.PrivateKey)
	}
	if !strings.HasPrefix(res.PublicKey, "ssh-rsa ") || !strings.HasSuffix(res.PublicKey, "a@b") {
		t.Fatalf("public key = %q", res.PublicKey)
	}
	if res.Comment != "a@b" {
		t.Fatalf("comment = %q", res.Comment)
	}
}

func TestConvertEncryptedVariants(t *testing.T) {
	for _, file := range []string{"rsa-v2-aes256.ppk", "rsa-v3-argon2.ppk"} {
		res, err := Convert(filepath.Join("testdata", file), []byte("testkey"))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !strings.Contains(string(res.PrivateKey), "BEGIN OPENSSH PRIVATE KEY") {
			t.Fatalf("%s: unexpected private key encoding", file)
		}
		if !strings.HasPrefix(res.PublicKey, "ssh-rsa ") {
			t.Fatalf("%s: public key = %q", file, res.PublicKey)
		}
	}
}

func TestConvertWrongPassphrase(t *testing.T) {
	for _, file := range []string{"rsa-v2-aes256.ppk", "rsa-v3-argon2.ppk"} {
		_, err := Convert(filepath.Join("testdata", file), []byte("not-the-passphrase"))
		if !errors.Is(err, ErrWrongPassphrase) {
			t.Errorf("%s: got %v, want ErrWrongPassphrase", file, err)
		}
	}
}

func TestConvertMissingPassphrase(t *testing.T) {
	_, err := Convert(filepath.Join("testdata", "rsa-v2-aes256.ppk"), nil)
	if err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("got %v", err)
	}
}

func TestConvertIdentityRoundTrip(t *testing.T) {
	// A converted key must load with the same parser the SSH client uses.
	res, err := Convert(filepath.Join("testdata", "rsa-v3-argon2.ppk"), []byte("testkey"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(res.PrivateKey)
	if err != nil {
		t.Fatalf("converted key does not parse: %v", err)
	}
	got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	// Convert appends the PPK comment; compare the algorithm and key blob.
	gotFields := strings.Fields(got)
	resFields := strings.Fields(res.PublicKey)
	if len(resFields) < 2 || gotFields[0] != resFields[0] || gotFields[1] != resFields[1] {
		t.Fatalf("public key mismatch:\n got %q\nwant %q", got, res.PublicKey)
	}
}

func TestConvertToHostUsesConvertedKey(t *testing.T) {
	dir := t.TempDir()
	// Copy the fixture so the source key path is realistic.
	src := filepath.Join(dir, "id_rsa.ppk")
	data, err := os.ReadFile(filepath.Join("testdata", "rsa-v3-none.ppk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}

	sessions := []Session{{
		Name:   "web",
		String: map[string]string{"HostName": "web.example.com", "UserName": "alice", "Protocol": "ssh", "PublicKeyFile": src},
		DWord:  map[string]uint32{"PortNumber": 2222},
	}}
	hosts := ImportSessions(sessions, ImportOptions{ConvertKeys: true, KeyDir: filepath.Join(dir, "keys")})
	if len(hosts.Warnings) != 0 {
		t.Fatalf("warnings = %v", hosts.Warnings)
	}
	if len(hosts.Hosts) != 1 {
		t.Fatalf("hosts = %+v", hosts.Hosts)
	}
	h := hosts.Hosts[0]
	if h.Hostname != "web.example.com" || h.User != "alice" || h.Port != 2222 {
		t.Fatalf("host = %+v", h)
	}
	if len(h.IdentityFiles) != 1 || IsPPK(h.IdentityFiles[0]) {
		t.Fatalf("identity files = %v (should be a converted OpenSSH key)", h.IdentityFiles)
	}
	if _, err := os.Stat(h.IdentityFiles[0]); err != nil {
		t.Fatalf("converted key missing: %v", err)
	}
}
