// Package putty imports keys and sessions from PuTTY, so moving off PuTTY does
// not mean re-typing a hundred hosts.
//
// It reads PuTTY private key (PPK) files in both the old v2 format and the
// current v3 format, including the AES-256-CBC encrypted variants and the
// Argon2id key derivation introduced with v3. Sessions are read from a
// registry export (.reg), and on Windows directly from the registry.
package putty

import (
	"crypto"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kayrus/putty"
	"golang.org/x/crypto/ssh"
)

// ErrWrongPassphrase is returned when a PPK file cannot be decrypted. PuTTY
// itself only reports a MAC mismatch, which reads like file corruption, so the
// friendlier message matters for the import UI.
var ErrWrongPassphrase = errors.New("putty: wrong passphrase for this key")

// KeyInfo describes a PPK file without decrypting the private half.
type KeyInfo struct {
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	Comment   string `json:"comment"`
	Encrypted bool   `json:"encrypted"`
	// KDF is "Argon2id" for v3 Argon2 keys and empty otherwise.
	KDF string `json:"kdf,omitempty"`
}

// IsPPK reports whether path looks like a PuTTY private key, by extension and
// by content. Content wins, because some users rename the file.
func IsPPK(path string) bool {
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		buf := make([]byte, 32)
		n, _ := f.Read(buf)
		if strings.HasPrefix(string(buf[:n]), "PuTTY-User-Key-File-") {
			return true
		}
	}
	return strings.HasSuffix(strings.ToLower(path), ".ppk")
}

// Inspect loads the PPK header so the caller can ask for a passphrase only when
// one is actually needed.
func Inspect(path string) (KeyInfo, error) {
	key, err := putty.NewFromFile(path)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("putty: read %s: %w", path, err)
	}
	return KeyInfo{
		Version:   key.Version,
		Algorithm: key.Algo,
		Comment:   key.Comment,
		Encrypted: key.Encryption != "none" && key.Encryption != "",
		KDF:       key.KeyDerivation,
	}, nil
}

// ConvertResult is an OpenSSH key pair derived from a PPK file.
type ConvertResult struct {
	// PrivateKey is the OpenSSH PEM encoding of the private key.
	PrivateKey []byte
	// PublicKey is one authorized_keys line, without a trailing newline.
	PublicKey string
	Comment   string
	Algorithm string
}

// Convert decrypts a PPK file and re-encodes it in OpenSSH format, so the rest
// of the application only ever deals with one private key format.
//
// The passphrase may be nil for unencrypted keys. The raw private key is never
// written anywhere by this function; the caller decides what to do with the
// returned bytes.
func Convert(path string, passphrase []byte) (ConvertResult, error) {
	key, err := putty.NewFromFile(path)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("putty: read %s: %w", path, err)
	}
	encrypted := key.Encryption != "none" && key.Encryption != ""
	if encrypted && len(passphrase) == 0 {
		return ConvertResult{}, fmt.Errorf("putty: %s is encrypted, a passphrase is required", path)
	}

	raw, err := key.ParseRawPrivateKey(passphrase)
	if err != nil {
		if encrypted {
			return ConvertResult{}, ErrWrongPassphrase
		}
		return ConvertResult{}, fmt.Errorf("putty: parse %s: %w", path, err)
	}
	priv, ok := raw.(crypto.PrivateKey)
	if !ok {
		return ConvertResult{}, fmt.Errorf("putty: %s produced an unusable key", path)
	}

	block, err := ssh.MarshalPrivateKey(priv, key.Comment)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("putty: %s: %w", path, err)
	}

	result := ConvertResult{
		PrivateKey: pem.EncodeToMemory(block),
		Comment:    key.Comment,
		Algorithm:  key.Algo,
	}

	rawPub, err := key.ParseRawPublicKey()
	if err == nil {
		if pub, ok := rawPub.(crypto.PublicKey); ok {
			if sshPub, err := ssh.NewPublicKey(pub); err == nil {
				line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
				if key.Comment != "" {
					line += " " + key.Comment
				}
				result.PublicKey = line
			}
		}
	}
	if result.PublicKey == "" {
		return ConvertResult{}, fmt.Errorf("putty: %s: could not derive a public key", path)
	}
	return result, nil
}
