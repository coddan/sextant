//go:build !windows

package sshclient

import (
	"errors"
	"net"
	"os"
)

// ErrNoAgent means no SSH agent socket is available.
var ErrNoAgent = errors.New("ssh: no SSH agent available")

func dialAgent() (net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, ErrNoAgent
	}
	return net.Dial("unix", sock)
}
