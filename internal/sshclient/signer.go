package sshclient

import (
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// loadSigner parses an unencrypted private key.
func loadSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(expandUser(path))
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// parseSignerWithPassphrase parses an encrypted private key.
func parseSignerWithPassphrase(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(expandUser(path))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "incorrect") {
		return nil, err
	}
	return signer, err
}
