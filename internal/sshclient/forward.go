package sshclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// ForwardState is the lifecycle of a tunnel.
type ForwardState string

const (
	ForwardStopped ForwardState = "stopped"
	ForwardActive  ForwardState = "active"
	ForwardError   ForwardState = "error"
)

// TunnelStatus is reported to the UI.
type TunnelStatus struct {
	HostID string       `json:"host_id"`
	Spec   string       `json:"spec"`
	Kind   string       `json:"kind"` // local or dynamic
	State  ForwardState `json:"state"`
	Err    string       `json:"error,omitempty"`
}

// LocalForward is one `ssh -L` rule: [bind_address:]port:host:hostport.
type LocalForward struct {
	BindAddress string
	BindPort    int
	RemoteHost  string
	RemotePort  int
}

func (f LocalForward) String() string {
	bind := f.BindAddress
	if bind != "" {
		bind = bracket(bind) + ":"
	}
	return fmt.Sprintf("%s%d:%s:%d", bind, f.BindPort, bracket(f.RemoteHost), f.RemotePort)
}

// ListenAddress is where the tunnel listens locally.
func (f LocalForward) ListenAddress() string {
	return net.JoinHostPort(bindHost(f.BindAddress), strconv.Itoa(f.BindPort))
}

// ParseLocalForward parses the OpenSSH notation, including bracketed IPv6.
func ParseLocalForward(spec string) (LocalForward, error) {
	fields, err := splitColonFields(strings.TrimSpace(spec))
	if err != nil {
		return LocalForward{}, err
	}
	usage := func() error {
		return fmt.Errorf("sftp: expected [bind_address:]port:host:hostport, got %q", spec)
	}
	var f LocalForward
	switch len(fields) {
	case 3:
		f.BindPort, err = parsePort(fields[0])
		f.RemoteHost = fields[1]
		if err == nil {
			f.RemotePort, err = parsePort(fields[2])
		}
	case 4:
		f.BindAddress = fields[0]
		f.BindPort, err = parsePort(fields[1])
		f.RemoteHost = fields[2]
		if err == nil {
			f.RemotePort, err = parsePort(fields[3])
		}
	default:
		return LocalForward{}, usage()
	}
	if err != nil {
		return LocalForward{}, usage()
	}
	if f.RemoteHost == "" {
		return LocalForward{}, usage()
	}
	return f, nil
}

// DynamicForward is one `ssh -D` SOCKS5 listener: [bind_address:]port.
type DynamicForward struct {
	BindAddress string
	BindPort    int
}

func (d DynamicForward) String() string {
	if d.BindAddress != "" {
		return bracket(d.BindAddress) + ":" + strconv.Itoa(d.BindPort)
	}
	return strconv.Itoa(d.BindPort)
}

// ListenAddress is where the SOCKS5 proxy listens.
func (d DynamicForward) ListenAddress() string {
	return net.JoinHostPort(bindHost(d.BindAddress), strconv.Itoa(d.BindPort))
}

// ParseDynamicForward parses "port" or "bind:port".
func ParseDynamicForward(spec string) (DynamicForward, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return DynamicForward{}, errors.New("sftp: empty DynamicForward")
	}
	if !strings.Contains(spec, ":") {
		port, err := parsePort(spec)
		if err != nil {
			return DynamicForward{}, fmt.Errorf("ssh: invalid DynamicForward %q", spec)
		}
		return DynamicForward{BindPort: port}, nil
	}
	fields, err := splitColonFields(spec)
	if err != nil || len(fields) != 2 {
		return DynamicForward{}, fmt.Errorf("ssh: invalid DynamicForward %q", spec)
	}
	port, err := parsePort(fields[1])
	if err != nil {
		return DynamicForward{}, fmt.Errorf("ssh: invalid DynamicForward %q", spec)
	}
	return DynamicForward{BindAddress: fields[0], BindPort: port}, nil
}

func bindHost(addr string) string {
	switch addr {
	case "", "localhost":
		return "127.0.0.1"
	case "*":
		return "0.0.0.0"
	default:
		return addr
	}
}

func bracket(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// splitColonFields splits on colons outside brackets, unwrapping IPv6.
func splitColonFields(spec string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	depth := 0
	for _, r := range spec {
		switch r {
		case '[':
			depth++
			cur.WriteRune(r)
		case ']':
			depth--
			cur.WriteRune(r)
			if depth < 0 {
				return nil, errors.New("unbalanced ]")
			}
		case ':':
			if depth == 0 {
				fields = append(fields, strings.Trim(cur.String(), "[]"))
				cur.Reset()
			} else {
				cur.WriteRune(r)
			}
		default:
			cur.WriteRune(r)
		}
	}
	if depth != 0 {
		return nil, errors.New("unbalanced [")
	}
	fields = append(fields, strings.Trim(cur.String(), "[]"))
	return fields, nil
}

// Tunnel is one listening forward.
type Tunnel struct {
	hostID  string
	spec    string
	kind    string
	client  *Client
	ln      net.Listener
	dynamic *DynamicForward
	local   *LocalForward
	ctx     context.Context
	cancel  context.CancelFunc
}

// ForwardManager owns all tunnels and reports status changes.
type ForwardManager struct {
	mu       sync.Mutex
	tunnels  map[string][]*Tunnel
	statuses map[string]TunnelStatus
	OnStatus func(TunnelStatus)
	Dialer   *Dialer
}

// NewForwardManager creates an empty manager.
func NewForwardManager() *ForwardManager {
	return &ForwardManager{
		tunnels:  map[string][]*Tunnel{},
		statuses: map[string]TunnelStatus{},
	}
}

func (m *ForwardManager) report(status TunnelStatus) {
	m.mu.Lock()
	if status.State == ForwardStopped {
		delete(m.statuses, status.HostID+status.Spec)
	} else {
		m.statuses[status.HostID+status.Spec] = status
	}
	cb := m.OnStatus
	m.mu.Unlock()
	if cb != nil {
		cb(status)
	}
}

// Statuses returns a snapshot of every tunnel.
func (m *ForwardManager) Statuses() []TunnelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TunnelStatus, 0, len(m.statuses))
	for _, st := range m.statuses {
		out = append(out, st)
	}
	return out
}

// StartHost opens every forward defined by the host. Errors for one rule do
// not stop the others.
func (m *ForwardManager) StartHost(ctx context.Context, client *Client, host model.Host) error {
	var firstErr error
	for _, spec := range host.LocalForwards {
		f, err := ParseLocalForward(spec)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		t := &Tunnel{hostID: host.ID, spec: f.String(), kind: "local", client: client, local: &f}
		if err := m.start(ctx, host, t); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if host.DynamicForward != "" {
		d, err := ParseDynamicForward(host.DynamicForward)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			t := &Tunnel{hostID: host.ID, spec: d.String(), kind: "dynamic", client: client, dynamic: &d}
			if err := m.start(ctx, host, t); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m *ForwardManager) start(parent context.Context, host model.Host, t *Tunnel) error {
	listenAddr := ""
	if t.local != nil {
		listenAddr = t.local.ListenAddress()
	} else if t.dynamic != nil {
		listenAddr = t.dynamic.ListenAddress()
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		m.report(TunnelStatus{HostID: t.hostID, Spec: t.spec, Kind: t.kind, State: ForwardError, Err: err.Error()})
		return fmt.Errorf("ssh: listen on %s: %w", listenAddr, err)
	}
	t.ln = ln
	t.ctx, t.cancel = context.WithCancel(parent)
	m.mu.Lock()
	m.tunnels[t.hostID] = append(m.tunnels[t.hostID], t)
	m.mu.Unlock()
	m.report(TunnelStatus{HostID: t.hostID, Spec: t.spec, Kind: t.kind, State: ForwardActive})
	go m.accept(t)
	return nil
}

func (m *ForwardManager) accept(t *Tunnel) {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			m.report(TunnelStatus{HostID: t.hostID, Spec: t.spec, Kind: t.kind, State: ForwardError, Err: err.Error()})
			return
		}
		go func() {
			defer conn.Close()
			if t.dynamic != nil {
				m.handleSOCKS(t, conn)
				return
			}
			remote := net.JoinHostPort(t.local.RemoteHost, strconv.Itoa(t.local.RemotePort))
			m.proxy(t, conn, remote)
		}()
	}
}

// proxy opens a direct-tcpip channel and copies bytes both ways.
func (m *ForwardManager) proxy(t *Tunnel, local net.Conn, remote string) {
	if t.client == nil || t.client.SSH() == nil {
		return
	}
	upstream, err := t.client.SSH().Dial("tcp", remote)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, local); closeWrite(upstream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, upstream); closeWrite(local); done <- struct{}{} }()
	select {
	case <-done:
	case <-t.ctx.Done():
	}
}

// handleSOCKS serves one SOCKS5 client (: DynamicForward).
func (m *ForwardManager) handleSOCKS(t *Tunnel, conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	br := make([]byte, 2)
	if _, err := io.ReadFull(conn, br); err != nil || br[0] != 5 {
		return
	}
	methods := make([]byte, br[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil { // no authentication
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if header[0] != 5 {
		return
	}
	if header[1] != 1 { // CONNECT only
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch header[3] {
	case 1:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case 3:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return
		}
		buf := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = string(buf)
	case 4:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	default:
		_, _ = conn.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(portBuf))
	target := net.JoinHostPort(host, strconv.Itoa(port))
	if t.client == nil || t.client.SSH() == nil {
		_, _ = conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	upstream, err := t.client.SSH().Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	_ = conn.SetDeadline(time.Time{})
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(upstream, conn); closeWrite(upstream) }()
	_, _ = io.Copy(conn, upstream)
}

// closeWrite half-closes a connection when the transport supports it. SSH
// channels and TCP connections both do, but the concrete types differ, so the
// assertion goes through an interface rather than *net.TCPConn.
func closeWrite(c net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// StopHost closes every tunnel for a host.
func (m *ForwardManager) StopHost(hostID string) {
	m.mu.Lock()
	tunnels := m.tunnels[hostID]
	delete(m.tunnels, hostID)
	m.mu.Unlock()
	for _, t := range tunnels {
		if t.cancel != nil {
			t.cancel()
		}
		if t.ln != nil {
			_ = t.ln.Close()
		}
		m.report(TunnelStatus{HostID: t.hostID, Spec: t.spec, Kind: t.kind, State: ForwardStopped})
	}
}

// StopAll closes every tunnel.
func (m *ForwardManager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.tunnels))
	for id := range m.tunnels {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.StopHost(id)
	}
}
