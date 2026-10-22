package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Secret store errors.
var (
	ErrLocked         = errors.New("secrets: store is locked")
	ErrNotLocked      = errors.New("secrets: store is not locked")
	ErrWrongPassword  = errors.New("secrets: wrong master password")
	ErrKeyFileMissing = errors.New("secrets: key file is missing")
	ErrUnknownBackend = errors.New("secrets: unknown secret backend")
)

// Envelope is the on-disk form of the secret store. Credentials are encrypted
// with AES-256-GCM, so hosts.json never contains a password and the
// store can be unlocked by a master password.
type Envelope struct {
	Version    int    `json:"version"`
	Mode       string `json:"mode"` // "keyfile" or "master"
	Salt       string `json:"salt,omitempty"`
	Time       uint32 `json:"time,omitempty"`
	Memory     uint32 `json:"memory,omitempty"`
	Threads    uint8  `json:"threads,omitempty"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

const (
	modeKeyFile = "keyfile"
	modeMaster  = "master"

	saltLen  = 16
	keyLen   = 32
	nonceLen = 12

	defaultTime    = 3
	defaultMemory  = 64 * 1024
	defaultThreads = 4
)

// SecretStore persists credential values encrypted at rest.
type SecretStore struct {
	mu      sync.RWMutex
	path    string
	keyPath string
	env     Envelope
	key     []byte
	values  map[string]string
	loaded  bool
}

func openSecretStore(paths Paths) (*SecretStore, error) {
	s := &SecretStore{
		path:    paths.Secrets,
		keyPath: paths.SecretKey,
		values:  map[string]string{},
	}
	data, err := os.ReadFile(paths.Secrets)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("secrets: read: %w", err)
		}
		// First run: create the local key and an empty store. The key file is
		// 0600; users who want a master password switch modes explicitly.
		if err := s.initKeyFile(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := json.Unmarshal(data, &s.env); err != nil {
		return nil, fmt.Errorf("secrets: parse %s: %w", paths.Secrets, err)
	}
	if s.env.Version == 0 {
		s.env.Version = 1
	}
	switch s.env.Mode {
	case modeMaster:
		// Locked until Unlock is called.
		return s, nil
	case modeKeyFile, "":
		key, err := os.ReadFile(paths.SecretKey)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrKeyFileMissing
			}
			return nil, fmt.Errorf("secrets: read key: %w", err)
		}
		if len(key) != keyLen {
			return nil, fmt.Errorf("secrets: key file has %d bytes, want %d", len(key), keyLen)
		}
		s.key = key
		if err := s.decryptInto(); err != nil {
			return nil, err
		}
		return s, nil
	default:
		return nil, ErrUnknownBackend
	}
}

func (s *SecretStore) initKeyFile() error {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("secrets: generate key: %w", err)
	}
	if err := writeFileAtomic(s.keyPath, key, 0o600); err != nil {
		return fmt.Errorf("secrets: write key: %w", err)
	}
	s.key = key
	s.env = Envelope{Version: 1, Mode: modeKeyFile}
	s.loaded = true
	return s.saveLocked()
}

// RequiresPassword reports whether unlocking with a password is necessary.
func (s *SecretStore) RequiresPassword() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.env.Mode == modeMaster
}

// Locked reports whether values are inaccessible right now.
func (s *SecretStore) Locked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key == nil
}

// Unlock derives the key from the master password and decrypts the store.
func (s *SecretStore) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Mode != modeMaster {
		return ErrNotLocked
	}
	if s.key != nil {
		return nil
	}
	key, err := deriveKey(password, s.env)
	if err != nil {
		return err
	}
	s.key = key
	values, err := s.open()
	if err != nil {
		s.key = nil
		if errors.Is(err, errAuth) {
			return ErrWrongPassword
		}
		return err
	}
	s.values = values
	s.loaded = true
	return nil
}

// Lock forgets the derived key and decrypted values.
func (s *SecretStore) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Mode != modeMaster {
		return
	}
	s.key = nil
	s.values = map[string]string{}
	s.loaded = false
}

// Set stores a secret.
func (s *SecretStore) Set(id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrLocked
	}
	s.values[id] = value
	return s.saveLocked()
}

// Get returns a secret.
func (s *SecretStore) Get(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[id]
	return v, ok
}

// Delete removes a secret.
func (s *SecretStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[id]; !ok {
		return
	}
	delete(s.values, id)
	_ = s.saveLocked()
}

// DeletePrefix removes every secret whose ID starts with prefix.
func (s *SecretStore) DeletePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id := range s.values {
		if strings.HasPrefix(id, prefix) {
			delete(s.values, id)
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
}

// IDs lists secret IDs without revealing values.
func (s *SecretStore) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.values))
	for id := range s.values {
		out = append(out, id)
	}
	return out
}

// Save encrypts and writes the store.
func (s *SecretStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrLocked
	}
	return s.saveLocked()
}

// EnableMasterPassword re-encrypts with a key derived from password and
// removes the local key file.
func (s *SecretStore) EnableMasterPassword(password string) error {
	if len(password) < 8 {
		return errors.New("secrets: master password must be at least 8 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrLocked
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	next := Envelope{
		Version: 1,
		Mode:    modeMaster,
		Salt:    b64(salt),
		Time:    defaultTime,
		Memory:  defaultMemory,
		Threads: defaultThreads,
	}
	key := argon2.IDKey([]byte(password), salt, defaultTime, defaultMemory, defaultThreads, keyLen)
	s.env = next
	s.key = key
	if err := s.saveLocked(); err != nil {
		return err
	}
	if err := os.Remove(s.keyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("secrets: remove key file: %w", err)
	}
	return nil
}

// DisableMasterPassword returns to key-file mode.
func (s *SecretStore) DisableMasterPassword(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Mode != modeMaster {
		return ErrNotLocked
	}
	if s.key == nil {
		if err := s.unlockLocked(password); err != nil {
			return err
		}
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	if err := writeFileAtomic(s.keyPath, key, 0o600); err != nil {
		return err
	}
	s.key = key
	s.env = Envelope{Version: 1, Mode: modeKeyFile}
	return s.saveLocked()
}

// ChangePassword re-encrypts with a new master password.
func (s *SecretStore) ChangePassword(old, next string) error {
	if len(next) < 8 {
		return errors.New("secrets: master password must be at least 8 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Mode != modeMaster {
		return ErrNotLocked
	}
	if s.key == nil {
		if err := s.unlockLocked(old); err != nil {
			return err
		}
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s.env.Salt = b64(salt)
	s.env.Time = defaultTime
	s.env.Memory = defaultMemory
	s.env.Threads = defaultThreads
	s.key = argon2.IDKey([]byte(next), salt, defaultTime, defaultMemory, defaultThreads, keyLen)
	return s.saveLocked()
}

func (s *SecretStore) unlockLocked(password string) error {
	key, err := deriveKey(password, s.env)
	if err != nil {
		return err
	}
	s.key = key
	values, err := s.open()
	if err != nil {
		s.key = nil
		if errors.Is(err, errAuth) {
			return ErrWrongPassword
		}
		return err
	}
	s.values = values
	s.loaded = true
	return nil
}

var errAuth = errors.New("secrets: authentication failed")

func (s *SecretStore) open() (map[string]string, error) {
	nonce, err := unb64(s.env.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := unb64(s.env.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("secrets: nonce is %d bytes, want %d", len(nonce), gcm.NonceSize())
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errAuth
	}
	values := map[string]string{}
	if len(plain) > 0 {
		if err := json.Unmarshal(plain, &values); err != nil {
			return nil, fmt.Errorf("secrets: decode payload: %w", err)
		}
	}
	return values, nil
}

func (s *SecretStore) decryptInto() error {
	values, err := s.open()
	if err != nil {
		return err
	}
	s.values = values
	s.loaded = true
	return nil
}

func (s *SecretStore) saveLocked() error {
	if s.key == nil {
		return ErrLocked
	}
	plain, err := json.Marshal(s.values)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	s.env.Version = 1
	s.env.Nonce = b64(nonce)
	s.env.Ciphertext = b64(gcm.Seal(nil, nonce, plain, nil))
	data, err := json.MarshalIndent(s.env, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(s.path, data, 0o600)
}

func deriveKey(password string, env Envelope) ([]byte, error) {
	salt, err := unb64(env.Salt)
	if err != nil {
		return nil, err
	}
	t, m, p := env.Time, env.Memory, env.Threads
	if t == 0 {
		t = defaultTime
	}
	if m == 0 {
		m = defaultMemory
	}
	if p == 0 {
		p = defaultThreads
	}
	return argon2.IDKey([]byte(password), salt, t, m, p, keyLen), nil
}

// ConstantTimeEqual compares two secrets without leaking length-independent
// timing information about the contents. Used by the key setup verifier.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
