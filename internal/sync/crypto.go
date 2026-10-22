// Package sync exports and imports the whole Sextant configuration as a single
// encrypted bundle, so it can be moved between machines through a shared folder
// or a WebDAV server.
//
// The bundle contains hosts, groups, snippets, settings and — because they are
// useless without them — the decrypted credentials. That is why the bundle is
// always encrypted with a passphrase the user chooses: there is no unencrypted
// export.
package sync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/model"
)

// BundleVersion is the schema version of the exported payload.
const BundleVersion = 1

// magic identifies an Sextant bundle before any decryption is attempted.
const magic = "sextant-sync"

// ErrWrongPassphrase is returned when a bundle cannot be decrypted. AES-GCM
// cannot tell a wrong key from a corrupt file, so the message covers both.
var ErrWrongPassphrase = errors.New("sync: could not decrypt the bundle: wrong passphrase or damaged file")

// Bundle is the portable snapshot of a configuration.
type Bundle struct {
	Version    int               `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	App        string            `json:"app,omitempty"`
	Settings   config.Settings   `json:"settings"`
	Hosts      []model.Host      `json:"hosts"`
	Groups     []model.Group     `json:"groups,omitempty"`
	Snippets   []model.Snippet   `json:"snippets,omitempty"`
	Secrets    map[string]string `json:"secrets,omitempty"`
}

// Argon2id parameters. They are deliberately expensive: the bundle holds
// plaintext-equivalent credentials, so a weak machine is a small price.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
)

// envelope is the on-disk/on-wire format. Keeping the KDF parameters next to the
// ciphertext means a bundle stays readable when the defaults change.
type envelope struct {
	Magic      string `json:"magic"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Memory     uint32 `json:"memory"`
	Time       uint32 `json:"time"`
	Threads    uint8  `json:"threads"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// Seal serialises and encrypts a bundle with the given passphrase.
func Seal(bundle Bundle, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("sync: a passphrase is required to export")
	}
	bundle.Version = BundleVersion
	bundle.App = "sextant"
	if bundle.ExportedAt.IsZero() {
		bundle.ExportedAt = time.Now().UTC()
	}
	plaintext, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(magic))

	env := envelope{
		Magic:      magic,
		Version:    BundleVersion,
		KDF:        "argon2id",
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Memory:     argonMemory,
		Time:       argonTime,
		Threads:    argonThreads,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	return json.MarshalIndent(env, "", "  ")
}

// Open decrypts a bundle that was produced by Seal.
func Open(data []byte, passphrase string) (Bundle, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Bundle{}, fmt.Errorf("sync: not an Sextant bundle: %w", err)
	}
	if env.Magic != magic {
		return Bundle{}, errors.New("sync: not an Sextant bundle")
	}
	if env.KDF != "" && env.KDF != "argon2id" {
		return Bundle{}, fmt.Errorf("sync: unsupported key derivation %q", env.KDF)
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return Bundle{}, ErrWrongPassphrase
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return Bundle{}, ErrWrongPassphrase
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return Bundle{}, ErrWrongPassphrase
	}

	// Older bundles may omit the parameters; fall back to the defaults.
	memory, passes, threads := env.Memory, env.Time, env.Threads
	if memory == 0 {
		memory = argonMemory
	}
	if passes == 0 {
		passes = argonTime
	}
	if threads == 0 {
		threads = argonThreads
	}

	key := argon2.IDKey([]byte(passphrase), salt, passes, memory, threads, argonKeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Bundle{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Bundle{}, err
	}
	if len(nonce) != gcm.NonceSize() {
		return Bundle{}, ErrWrongPassphrase
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(magic))
	if err != nil {
		return Bundle{}, ErrWrongPassphrase
	}
	var bundle Bundle
	if err := json.Unmarshal(plaintext, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("sync: bundle content is not valid: %w", err)
	}
	return bundle, nil
}
