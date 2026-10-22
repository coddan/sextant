package sshclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Prompts collects the callbacks a frontend must provide. Any callback may be
// nil; Sextant then falls back to stored credentials, or fails the attempt.
type Prompts struct {
	// Password asks for a login password. attempt starts at 1.
	Password func(hostName string, attempt int) (string, error)
	// Passphrase asks for a private-key passphrase. It is asked once per key
	// and kept in memory until the app quits, never written to disk.
	Passphrase func(keyPath string) (string, error)
	// HostKey asks whether to trust a new host key. A nil callback trusts new
	// keys (TOFU) when AskHostKeys is true.
	HostKey func(host, fingerprint, keyType string) (bool, error)
	// KeyboardInteractive handles 2FA and other challenge questions.
	KeyboardInteractive func(hostName, name, instruction string, questions []string, echos []bool) ([]string, error)
}

// AuthCache keeps passphrases and passwords in memory only.
type AuthCache struct {
	mu          sync.Mutex
	passphrases map[string]string
	passwords   map[string]string
}

// NewAuthCache returns an empty cache.
func NewAuthCache() *AuthCache {
	return &AuthCache{passphrases: map[string]string{}, passwords: map[string]string{}}
}

// Passphrase returns a cached passphrase.
func (c *AuthCache) Passphrase(path string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.passphrases[path]
	return v, ok
}

// SetPassphrase caches a passphrase.
func (c *AuthCache) SetPassphrase(path, value string) {
	if c == nil || value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passphrases[path] = value
}

// Password returns a cached password for a host ID.
func (c *AuthCache) Password(hostID string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.passwords[hostID]
	return v, ok
}

// SetPassword caches a password for a host ID.
func (c *AuthCache) SetPassword(hostID, value string) {
	if c == nil || value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passwords[hostID] = value
}

// Clear forgets every cached credential.
func (c *AuthCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passphrases = map[string]string{}
	c.passwords = map[string]string{}
}

// authBuilder assembles authentication methods for one host and tracks agent
// connections that must be closed after the handshake.
type authBuilder struct {
	dialer   *Dialer
	hostName string
	hostID   string
	stored   string
	methods  []ssh.AuthMethod
	cleanup  []func()
}

func (a *authBuilder) close() {
	for _, fn := range a.cleanup {
		fn()
	}
}

func (d *Dialer) authMethods(host hostForAuth) (*authBuilder, error) {
	b := &authBuilder{dialer: d, hostName: host.Name, hostID: host.ID}
	b.stored = host.Password

	if !host.IdentitiesOnly {
		if conn, err := dialAgent(); err == nil {
			ag := agent.NewClient(conn)
			if signers, err := ag.Signers(); err == nil && len(signers) > 0 {
				b.methods = append(b.methods, ssh.PublicKeys(signers...))
			}
			b.cleanup = append(b.cleanup, func() { _ = conn.Close() })
		}
	}

	keys := host.IdentityFiles
	if len(keys) == 0 && !host.IdentitiesOnly {
		keys = defaultIdentityFiles()
	}
	for _, path := range keys {
		path = expandUser(path)
		method := b.keyMethod(path, host.CertificateFile)
		if method != nil {
			b.methods = append(b.methods, method)
		}
	}

	b.methods = append(b.methods, ssh.PasswordCallback(func() (string, error) {
		if b.stored != "" {
			return b.stored, nil
		}
		if d.Cache != nil {
			if v, ok := d.Cache.Password(host.ID); ok {
				return v, nil
			}
		}
		if d.Prompts.Password != nil {
			value, err := d.Prompts.Password(host.Name, 1)
			if err != nil {
				return "", err
			}
			if d.Cache != nil {
				d.Cache.SetPassword(host.ID, value)
			}
			return value, nil
		}
		return "", fmt.Errorf("ssh: password authentication required for %s", host.Name)
	}))

	b.methods = append(b.methods, ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i, q := range questions {
			lower := strings.ToLower(q)
			switch {
			case strings.Contains(lower, "password"):
				if b.stored != "" {
					answers[i] = b.stored
					continue
				}
				if d.Cache != nil {
					if v, ok := d.Cache.Password(host.ID); ok {
						answers[i] = v
						continue
					}
				}
			case strings.Contains(lower, "code"), strings.Contains(lower, "otp"),
				strings.Contains(lower, "verification"), strings.Contains(lower, "token"):
				// Fall through to the prompt for the second factor.
			}
			if d.Prompts.KeyboardInteractive == nil {
				if strings.Contains(lower, "password") && b.stored != "" {
					answers[i] = b.stored
					continue
				}
				return nil, fmt.Errorf("ssh: %s asks %q but no prompt handler is available", host.Name, q)
			}
			values, err := d.Prompts.KeyboardInteractive(host.Name, name, instruction, questions, echos)
			if err != nil {
				return nil, err
			}
			return values, nil
		}
		return answers, nil
	}))
	return b, nil
}

// keyMethod parses one key, deferring the passphrase prompt until the server
// actually asks for that key.
func (a *authBuilder) keyMethod(path, certFile string) ssh.AuthMethod {
	signer, err := loadSigner(path)
	if err == nil {
		return ssh.PublicKeys(a.withCertificate(signer, path, certFile))
	}
	if !isPassphraseError(err) {
		return nil
	}
	// Deferred: parse again with the passphrase on first use.
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		if a.dialer.Cache != nil {
			if pass, ok := a.dialer.Cache.Passphrase(path); ok {
				if signer, err := parseSignerWithPassphrase(path, pass); err == nil {
					return []ssh.Signer{a.withCertificate(signer, path, certFile)}, nil
				}
			}
		}
		if a.dialer.Prompts.Passphrase == nil {
			return nil, fmt.Errorf("ssh: key %s needs a passphrase", path)
		}
		pass, err := a.dialer.Prompts.Passphrase(path)
		if err != nil {
			return nil, err
		}
		a.dialer.Cache.SetPassphrase(path, pass)
		signer, err := parseSignerWithPassphrase(path, pass)
		if err != nil {
			return nil, err
		}
		return []ssh.Signer{a.withCertificate(signer, path, certFile)}, nil
	})
}

// withCertificate attaches an OpenSSH user certificate. Without this
// the server sees only the plain key and rejects certificate-only accounts.
func (a *authBuilder) withCertificate(signer ssh.Signer, keyPath, certFile string) ssh.Signer {
	paths := []string{}
	if certFile != "" {
		paths = append(paths, expandUser(certFile))
	}
	paths = append(paths, keyPath+"-cert.pub")
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			continue
		}
		cert, ok := pub.(*ssh.Certificate)
		if !ok {
			continue
		}
		certSigner, err := ssh.NewCertSigner(cert, signer)
		if err != nil {
			continue
		}
		return certSigner
	}
	return signer
}

func defaultIdentityFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa", "id_dsa"}
	out := make([]string, 0, len(names))
	for _, n := range names {
		p := filepath.Join(home, ".ssh", n)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

func isPassphraseError(err error) bool {
	if _, ok := err.(*ssh.PassphraseMissingError); ok {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "passphrase") || strings.Contains(s, "encrypted") || strings.Contains(s, "decrypt")
}
