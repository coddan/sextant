package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BundleFileName is the file the providers read and write.
const BundleFileName = "sextant-sync.bundle"

// Provider stores a single opaque bundle. Fetch returns (nil, nil) when no
// bundle exists yet, which is what makes the first push on a fresh server work.
type Provider interface {
	// Fetch downloads the bundle, or returns nil when there is none.
	Fetch(ctx context.Context) ([]byte, error)
	// Push uploads the bundle, replacing any previous version.
	Push(ctx context.Context, data []byte) error
	// Describe is a human-readable location for messages.
	Describe() string
}

// LocalProvider keeps the bundle in a directory, which can itself be a synced
// folder (Dropbox, Syncthing, a network share, and so on).
type LocalProvider struct {
	Dir      string
	FileName string
}

// NewLocalProvider returns a provider writing into dir.
func NewLocalProvider(dir string) *LocalProvider {
	return &LocalProvider{Dir: dir}
}

func (p *LocalProvider) file() string {
	name := p.FileName
	if name == "" {
		name = BundleFileName
	}
	return filepath.Join(p.Dir, name)
}

// Fetch reads the bundle from disk, returning nil when it is absent.
func (p *LocalProvider) Fetch(_ context.Context) ([]byte, error) {
	data, err := os.ReadFile(p.file())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Push writes the bundle atomically with 0600 permissions.
func (p *LocalProvider) Push(_ context.Context, data []byte) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.Dir, ".sextant-sync-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, p.file())
}

// Describe names the destination for messages.
func (p *LocalProvider) Describe() string { return p.file() }

// WebDAVProvider reads and writes the bundle over WebDAV. That covers Nextcloud,
// ownCloud and most self-hosted file servers, which is what the "host your own
// sync" part of asks for.
type WebDAVProvider struct {
	// URL is the full URL of the bundle file.
	URL string
	// Username and Password are optional basic-auth credentials.
	Username string
	Password string
	// Client is optional; a sane default with a timeout is used otherwise.
	Client *http.Client
}

// NewWebDAVProvider validates the URL and returns a provider.
func NewWebDAVProvider(rawURL, username, password string) (*WebDAVProvider, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("sync: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("sync: WebDAV URL must be http or https, got %q", u.Scheme)
	}
	return &WebDAVProvider{URL: u.String(), Username: username, Password: password}, nil
}

func (p *WebDAVProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (p *WebDAVProvider) newRequest(ctx context.Context, method string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.URL, body)
	if err != nil {
		return nil, err
	}
	if p.Username != "" || p.Password != "" {
		req.SetBasicAuth(p.Username, p.Password)
	}
	return req, nil
}

// Fetch downloads the bundle. A 404 means "not uploaded yet" and is not an
// error, so the first sync from a second device does not fail.
func (p *WebDAVProvider) Fetch(ctx context.Context) ([]byte, error) {
	req, err := p.newRequest(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return io.ReadAll(resp.Body)
	default:
		return nil, fmt.Errorf("sync: GET %s: %s", p.URL, resp.Status)
	}
}

// Push uploads the bundle, creating the parent collection first if the server
// reports it is missing.
func (p *WebDAVProvider) Push(ctx context.Context, data []byte) error {
	req, err := p.newRequest(ctx, http.MethodPut, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound {
		if err := p.mkcol(ctx); err != nil {
			return err
		}
		// Retry once after creating the collection.
		req, err = p.newRequest(ctx, http.MethodPut, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err = p.client().Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sync: PUT %s: %s", p.URL, resp.Status)
	}
	return nil
}

// mkcol creates the collection (directory) that holds the bundle, tolerating
// servers that return 405 when it already exists.
func (p *WebDAVProvider) mkcol(ctx context.Context) error {
	u, err := url.Parse(p.URL)
	if err != nil {
		return err
	}
	u.Path = path.Dir(u.Path)
	req, err := http.NewRequestWithContext(ctx, "MKCOL", u.String(), nil)
	if err != nil {
		return err
	}
	if p.Username != "" || p.Password != "" {
		req.SetBasicAuth(p.Username, p.Password)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// 405 Method Not Allowed means the collection already exists.
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	return fmt.Errorf("sync: MKCOL %s: %s", u.Path, resp.Status)
}

// Describe names the destination for messages.
func (p *WebDAVProvider) Describe() string { return p.URL }
