package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

// Errors returned by the dialer.
var (
	ErrHostKeyChanged = errors.New("ssh: host key changed")
	ErrUnknownHostKey = errors.New("ssh: unknown host key")
)

// PasswordResolver returns a stored password for a host. Jump hosts may have
// their own credentials, so every hop gets a lookup.
type PasswordResolver func(h model.Host) (string, bool)

// Dialer connects to hosts. It is safe for concurrent use.
type Dialer struct {
	Prompts Prompts
	Cache   *AuthCache
	Timeout time.Duration
	// KnownHostsPath defaults to ~/.ssh/known_hosts.
	KnownHostsPath string
	// AcceptNewHostKeys records an unknown key on first use. Frontends that
	// want a confirmation set AskHostKeys and provide Prompts.HostKey.
	AcceptNewHostKeys bool
	AskHostKeys       bool

	mu    sync.Mutex
	known *KnownHosts
}

func (d *Dialer) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 10 * time.Second
}

// knownHosts lazily loads the known_hosts file shared with OpenSSH.
func (d *Dialer) knownHosts() (*KnownHosts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.known != nil {
		return d.known, nil
	}
	path := d.KnownHostsPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = home + string(os.PathSeparator) + ".ssh" + string(os.PathSeparator) + "known_hosts"
	}
	k, err := LoadKnownHosts(path)
	if err != nil {
		return nil, err
	}
	d.known = k
	return k, nil
}

// IsKnownHost reports whether host:port already has a pinned key.
func (d *Dialer) IsKnownHost(host string, port int) bool {
	k, err := d.knownHosts()
	if err != nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	candidates := []string{host}
	if port != 0 && port != 22 {
		candidates = append(candidates, fmt.Sprintf("[%s]:%d", host, port))
	}
	for i := range k.entries {
		if k.entries[i].matches(candidates) {
			return true
		}
	}
	return false
}

// hostKeyCallback verifies one hop's key.
func (d *Dialer) hostKeyCallback(host model.Host) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		k, err := d.knownHosts()
		if err != nil {
			return fmt.Errorf("ssh: read known_hosts: %w", err)
		}
		port := host.Port
		if port == 0 {
			port = 22
		}
		lookupHost := hostname
		if host.Hostname != "" {
			lookupHost = host.Hostname
		}
		verdict, entry := k.Lookup(lookupHost, port, key)
		switch verdict {
		case KeyKnown:
			return nil
		case KeyRevoked:
			return fmt.Errorf("ssh: host key for %s is revoked in %s", hostname, k.Path())
		case KeyChanged:
			pinned := "another key type"
			if entry != nil {
				pinned = entry.Key.Type() + " " + Fingerprint(entry.Key)
			}
			return fmt.Errorf("%w: %s offers %s %s, pinned %s; remove the old line from %s only if this change is expected",
				ErrHostKeyChanged, hostname, key.Type(), Fingerprint(key), pinned, k.Path())
		case KeyUnknown:
			trust := d.AcceptNewHostKeys && !d.AskHostKeys
			if d.AskHostKeys && d.Prompts.HostKey != nil {
				ok, err := d.Prompts.HostKey(hostname, Fingerprint(key), key.Type())
				if err != nil {
					return err
				}
				trust = ok
			}
			if !trust {
				return fmt.Errorf("%w: %s offers %s %s", ErrUnknownHostKey, hostname, key.Type(), Fingerprint(key))
			}
			if err := k.Add(host.Hostname, port, key); err != nil {
				// A read-only known_hosts must not silently weaken security,
				// but refusing the connection would be worse for a read-only
				// home. The key was verified by TOFU; report it and continue.
				if d.Prompts.HostKey != nil {
					_, _ = d.Prompts.HostKey(hostname, Fingerprint(key), key.Type())
				}
			}
			return nil
		}
		return nil
	}
}

type hostForAuth struct {
	ID              string
	Name            string
	Hostname        string
	User            string
	Port            int
	IdentityFiles   []string
	CertificateFile string
	IdentitiesOnly  bool
	Password        string
}

func toAuthHost(h model.Host, password string) hostForAuth {
	return hostForAuth{
		ID:              h.ID,
		Name:            h.Name,
		Hostname:        h.Hostname,
		User:            h.EffectiveUser(),
		Port:            h.Port,
		IdentityFiles:   h.IdentityFiles,
		CertificateFile: h.CertificateFile,
		IdentitiesOnly:  h.IdentitiesOnly,
		Password:        password,
	}
}

// Client is a connected host, possibly through jump hosts.
type Client struct {
	Host  model.Host
	conn  *ssh.Client
	jumps []*ssh.Client
}

// SSH exposes the underlying client for SFTP and forwarding.
func (c *Client) SSH() *ssh.Client { return c.conn }

// Close closes the target and every jump connection.
func (c *Client) Close() error {
	var first error
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			first = err
		}
	}
	for _, j := range c.jumps {
		_ = j.Close()
	}
	return first
}

// Dial connects to target, resolving a ProxyJump chain if present.
func (d *Dialer) Dial(ctx context.Context, target model.Host, known []model.Host, passwords PasswordResolver) (*Client, error) {
	if target.Hostname == "" {
		return nil, fmt.Errorf("ssh: host %q has no hostname", target.Name)
	}
	if d.Cache == nil {
		d.Cache = NewAuthCache()
	}
	chain, err := ResolveChain(target, known)
	if err != nil {
		return nil, err
	}
	hops := append(chain, target)
	var parents []*ssh.Client
	var current *ssh.Client
	for i, hop := range hops {
		password := ""
		if passwords != nil {
			if p, ok := passwords(hop); ok {
				password = p
			}
		}
		addr := hop.Address()
		var raw net.Conn
		if current == nil {
			dialer := &net.Dialer{Timeout: d.timeout()}
			raw, err = dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				closeAll(parents)
				return nil, fmt.Errorf("ssh: connect to %s: %w", addr, friendlyDialError(err))
			}
		} else {
			raw, err = current.Dial("tcp", addr)
			if err != nil {
				closeAll(parents)
				return nil, fmt.Errorf("ssh: connect to %s through jump host: %w", addr, err)
			}
		}
		_ = raw.SetDeadline(time.Now().Add(d.timeout()))
		cfg, builder, err := d.clientConfig(hop, password)
		if err != nil {
			raw.Close()
			closeAll(parents)
			return nil, err
		}
		conn, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
		builder.close()
		if err != nil {
			raw.Close()
			closeAll(parents)
			if i < len(hops)-1 {
				return nil, fmt.Errorf("ssh: authenticate to jump host %s (%s): %w", hop.Name, addr, err)
			}
			return nil, fmt.Errorf("ssh: connect to %s as %s: %w", addr, hop.EffectiveUser(), err)
		}
		_ = raw.SetDeadline(time.Time{})
		current = ssh.NewClient(conn, chans, reqs)
		parents = append(parents, current)
	}
	client := &Client{Host: target, conn: current}
	if len(parents) > 0 {
		client.jumps = parents[:len(parents)-1]
	}
	if target.ForwardAgent {
		setupAgentForwarding(current)
	}
	return client, nil
}

func closeAll(clients []*ssh.Client) {
	for _, c := range clients {
		_ = c.Close()
	}
}

func friendlyDialError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("timed out")
	}
	// macOS local-network privacy produces a bare "no route to host"; name
	// the likely cause so the message is actionable.
	msg := err.Error()
	if strings.Contains(msg, "no route to host") {
		return fmt.Errorf("%s (macOS may be blocking Local Network access; allow Sextant in System Settings > Privacy & Security > Local Network)", msg)
	}
	return err
}

func (d *Dialer) clientConfig(hop model.Host, password string) (*ssh.ClientConfig, *authBuilder, error) {
	builder, err := d.authMethods(toAuthHost(hop, password))
	if err != nil {
		return nil, nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            hop.EffectiveUser(),
		Auth:            builder.methods,
		HostKeyCallback: d.hostKeyCallback(hop),
		Timeout:         d.timeout(),
		ClientVersion:   "SSH-2.0-Sextant_Go",
	}
	return cfg, builder, nil
}

// Run executes a command and returns combined output. The context cancels the
// remote command by closing the session.
func (c *Client) Run(ctx context.Context, command string) ([]byte, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var buf bytesBuffer
	session.Stdout = &buf
	session.Stderr = &buf
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		<-done
		return nil, ctx.Err()
	case err := <-done:
		return buf.Bytes(), err
	}
}

// RunStreaming executes a command and streams stdout and stderr line by line.
// cancel stops it by interrupting the remote process.
func (c *Client) RunStreaming(ctx context.Context, command string, onLine func(stream, line string)) error {
	session, err := c.conn.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	session.Stdout = outW
	session.Stderr = errW
	if err := session.Start(command); err != nil {
		outW.Close()
		errW.Close()
		return err
	}
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go scanLines(outR, func(line string) { onLine("stdout", line) }, doneOut)
	go scanLines(errR, func(line string) { onLine("stderr", line) }, doneErr)
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case <-ctx.Done():
		// Ctrl+C is the closest thing to a polite stop over an exec channel;
		// closing the session afterwards guarantees the process ends.
		_ = session.Signal(ssh.SIGINT)
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		<-wait
		outW.Close()
		errW.Close()
		<-doneOut
		<-doneErr
		return ctx.Err()
	case err := <-wait:
		outW.Close()
		errW.Close()
		<-doneOut
		<-doneErr
		return err
	}
}

func scanLines(r io.Reader, emit func(string), done chan struct{}) {
	defer close(done)
	buf := make([]byte, 32*1024)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				idx := indexByte(pending, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimSuffix(string(pending[:idx]), "\r")
				emit(line)
				pending = pending[idx+1:]
			}
		}
		if err != nil {
			if len(pending) > 0 {
				emit(strings.TrimSuffix(string(pending), "\r"))
			}
			return
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// bytesBuffer is a minimal bytes.Buffer that avoids importing bytes in two
// files' worth of aliases.
type bytesBuffer struct {
	data []byte
}

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *bytesBuffer) Bytes() []byte { return b.data }

func (b *bytesBuffer) String() string { return string(b.data) }
