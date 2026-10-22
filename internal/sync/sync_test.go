package sync

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/model"
)

func testStore(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{
		Dir:       dir,
		Settings:  filepath.Join(dir, "settings.json"),
		Hosts:     filepath.Join(dir, "hosts.json"),
		Snippets:  filepath.Join(dir, "snippets.json"),
		Secrets:   filepath.Join(dir, "secrets.json"),
		SecretKey: filepath.Join(dir, "secrets.key"),
		State:     filepath.Join(dir, "state.json"),
	}
	store, err := config.OpenAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sampleStore(t *testing.T) *config.Store {
	store := testStore(t)
	host := model.NewHost("web", "web.example.com")
	host.User = "alice"
	host.Port = 2222
	if err := store.HostPassword(&host, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	store.UpsertHost(host)
	store.AddGroup("prod")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSealOpenRoundTrip(t *testing.T) {
	bundle := Bundle{
		Settings: config.DefaultSettings(),
		Hosts:    []model.Host{model.NewHost("a", "a.example.com")},
		Secrets:  map[string]string{"host:x:password": "hunter2"},
	}
	data, err := Seal(bundle, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"kdf": "argon2id"`) {
		t.Fatalf("envelope missing kdf: %s", data)
	}
	got, err := Open(data, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Hostname != "a.example.com" {
		t.Fatalf("hosts = %+v", got.Hosts)
	}
	if got.Secrets["host:x:password"] != "hunter2" {
		t.Fatalf("secrets = %+v", got.Secrets)
	}
	if got.Version != BundleVersion || got.App != "sextant" {
		t.Fatalf("header = %+v", got)
	}
}

func TestOpenWrongPassphrase(t *testing.T) {
	data, err := Seal(Bundle{}, "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(data, "wrong"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRejectsForeignData(t *testing.T) {
	if _, err := Open([]byte("not a bundle"), "x"); err == nil {
		t.Fatal("expected an error for non-bundle data")
	}
	if _, err := Open([]byte(`{"magic":"something-else"}`), "x"); err == nil {
		t.Fatal("expected an error for a foreign magic")
	}
}

func TestLocalProviderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider(dir)
	ctx := context.Background()

	if data, err := p.Fetch(ctx); err != nil || data != nil {
		t.Fatalf("empty fetch = %v, %v", data, err)
	}
	if err := p.Push(ctx, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := p.Fetch(ctx)
	if err != nil || string(data) != "hello" {
		t.Fatalf("fetch = %q, %v", data, err)
	}
	if !strings.Contains(p.Describe(), dir) {
		t.Fatalf("describe = %q", p.Describe())
	}
}

func TestFullLocalRoundTrip(t *testing.T) {
	src := sampleStore(t)
	dir := t.TempDir()
	provider := NewLocalProvider(dir)
	ctx := context.Background()

	if err := Push(ctx, provider, src, "passphrase"); err != nil {
		t.Fatal(err)
	}

	dst := testStore(t)
	if err := Pull(ctx, provider, dst, "passphrase"); err != nil {
		t.Fatal(err)
	}
	if len(dst.Hosts) != 1 || dst.Hosts[0].Hostname != "web.example.com" {
		t.Fatalf("imported hosts = %+v", dst.Hosts)
	}
	if pw, ok := dst.Password(dst.Hosts[0]); !ok || pw != "s3cr3t" {
		t.Fatalf("imported password = %q ok=%v", pw, ok)
	}
	if _, ok := dst.GroupByName("prod"); !ok {
		t.Fatal("group was not imported")
	}
}

func TestSnapshotLocked(t *testing.T) {
	store := sampleStore(t)
	// Only master-password mode can be locked; the key-file mode is always open.
	if err := store.EnableMasterPassword("password123"); err != nil {
		t.Fatal(err)
	}
	store.Secrets.Lock()
	if !store.Secrets.Locked() {
		t.Fatal("store should be locked")
	}
	if _, err := Snapshot(store); !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
	if err := Apply(Bundle{}, store); !errors.Is(err, ErrLocked) {
		t.Fatalf("apply locked: got %v, want ErrLocked", err)
	}
}

func TestApplyReplacesSecrets(t *testing.T) {
	store := sampleStore(t)
	bundle := Bundle{
		Settings: config.DefaultSettings(),
		Hosts:    []model.Host{model.NewHost("new", "new.example.com")},
		Secrets:  map[string]string{"host:new:password": "fresh"},
	}
	if err := Apply(bundle, store); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Secrets.Get("host:new:password"); !ok {
		t.Fatal("new secret missing")
	}
	// The old host's password must be gone, since the host is gone.
	if ids := store.Secrets.IDs(); len(ids) != 1 {
		t.Fatalf("secrets = %v", ids)
	}
}

func TestWebDAVProviderRoundTrip(t *testing.T) {
	var mu sync.Mutex
	files := map[string][]byte{}
	dirs := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			data, ok := files[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		case http.MethodPut:
			// Require the parent collection to exist, like a real WebDAV
			// server, so the MKCOL retry path is exercised.
			if !dirs[path.Dir(r.URL.Path)] {
				w.WriteHeader(http.StatusConflict)
				return
			}
			data, _ := io.ReadAll(r.Body)
			files[r.URL.Path] = data
			w.WriteHeader(http.StatusCreated)
		case "MKCOL":
			dirs[r.URL.Path] = true
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	provider, err := NewWebDAVProvider(srv.URL+"/remote.php/dav/sextant/sextant-sync.bundle", "user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if data, err := provider.Fetch(ctx); err != nil || data != nil {
		t.Fatalf("missing fetch = %q, %v", data, err)
	}
	if err := provider.Push(ctx, []byte("bundle-bytes")); err != nil {
		t.Fatal(err)
	}
	data, err := provider.Fetch(ctx)
	if err != nil || string(data) != "bundle-bytes" {
		t.Fatalf("fetch = %q, %v", data, err)
	}
	if !dirs["/remote.php/dav/sextant"] {
		t.Fatal("expected the parent collection to be created if needed")
	}
}

func TestWebDAVProviderRejectsBadURL(t *testing.T) {
	if _, err := NewWebDAVProvider("ftp://example.com/x", "", ""); err == nil {
		t.Fatal("expected a scheme error")
	}
	if _, err := NewWebDAVProvider("://nope", "", ""); err == nil {
		t.Fatal("expected a parse error")
	}
}
