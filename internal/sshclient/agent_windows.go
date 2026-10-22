//go:build windows

package sshclient

import (
	"errors"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

// ErrNoAgent means no SSH agent pipe is available.
var ErrNoAgent = errors.New("ssh: no SSH agent available")

// dialAgent connects to the OpenSSH for Windows agent pipe. Windows users
// commonly store keys in a password manager that exposes this pipe; Sextant
// uses it for authentication just like the OpenSSH client does.
func dialAgent() (net.Conn, error) {
	pipe := os.Getenv("SSH_AUTH_SOCK")
	if pipe == "" {
		pipe = `\\.\pipe\openssh-ssh-agent`
	}
	conn, err := winio.DialPipe(pipe, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
