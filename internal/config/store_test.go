package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coddan/sextant/internal/model"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		Dir:       dir,
		Settings:  filepath.Join(dir, "settings.json"),
		Hosts:     filepath.Join(dir, "hosts.json"),
		Snippets:  filepath.Join(dir, "snippets.json"),
		Secrets:   filepath.Join(dir, "secrets.json"),
		SecretKey: filepath.Join(dir, "secrets.key"),
		State:     filepath.Join(dir, "state.json"),
	}
}

func TestStoreRoundTripAndPasswordEncryption(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	h := model.NewHost("web", "192.0.2.10")
	store.UpsertHost(h)
	if err := store.HostPassword(&h, "hunter2"); err != nil {
		t.Fatal(err)
	}
	store.UpsertHost(h)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	// hosts.json must never contain the plaintext password.
	raw, err := os.ReadFile(paths.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("empty hosts file")
	}
	for _, needle := range []string{"hunter2", `"password":`} {
		if containsFold(string(raw), needle) {
			t.Fatalf("hosts.json leaked %q: %s", needle, raw)
		}
	}
	secretsFile, _ := os.ReadFile(paths.Secrets)
	if containsFold(string(secretsFile), "hunter2") {
		t.Fatal("secrets.json contains plaintext password")
	}

	reopened, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reopened.Host(h.ID)
	if !ok {
		t.Fatal("host did not round-trip")
	}
	if got, ok := reopened.Password(loaded); !ok || got != "hunter2" {
		t.Fatalf("password = %q, %v", got, ok)
	}
}

func TestMasterPassword(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Secrets.Set("token", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnableMasterPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if !store.HasMasterPassword() {
		t.Fatal("expected master password mode")
	}
	if _, err := os.Stat(paths.SecretKey); !os.IsNotExist(err) {
		t.Fatalf("key file still present: %v", err)
	}

	reopened, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Locked() {
		t.Fatal("store should start locked")
	}
	if _, ok := reopened.Secrets.Get("token"); ok {
		t.Fatal("locked store returned a value")
	}
	if err := reopened.Unlock("wrong"); err == nil {
		t.Fatal("unlock with wrong password succeeded")
	}
	if err := reopened.Unlock("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if v, ok := reopened.Secrets.Get("token"); !ok || v != "s3cret" {
		t.Fatalf("token = %q, %v", v, ok)
	}
}

func TestDisableMasterPassword(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Secrets.Set("a", "b")
	if err := store.EnableMasterPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if !store.Settings.Security.MasterPassword {
		t.Fatal("settings did not record master password mode")
	}
	if err := store.DisableMasterPassword("password123"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Locked() {
		t.Fatal("key file mode should unlock automatically")
	}
	if v, _ := reopened.Secrets.Get("a"); v != "b" {
		t.Fatalf("got %q", v)
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := lower(s), lower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
