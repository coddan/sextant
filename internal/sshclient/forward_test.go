package sshclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coddan/sextant/internal/model"
)

func TestParseLocalForward(t *testing.T) {
	cases := []struct {
		spec string
		want LocalForward
	}{
		{"8080:localhost:80", LocalForward{BindPort: 8080, RemoteHost: "localhost", RemotePort: 80}},
		{"127.0.0.1:8080:db.internal:5432", LocalForward{BindAddress: "127.0.0.1", BindPort: 8080, RemoteHost: "db.internal", RemotePort: 5432}},
		{"[::1]:9000:[2001:db8::1]:443", LocalForward{BindAddress: "::1", BindPort: 9000, RemoteHost: "2001:db8::1", RemotePort: 443}},
	}
	for _, tc := range cases {
		got, err := ParseLocalForward(tc.spec)
		if err != nil {
			t.Fatalf("ParseLocalForward(%q): %v", tc.spec, err)
		}
		if got != tc.want {
			t.Errorf("ParseLocalForward(%q) = %+v, want %+v", tc.spec, got, tc.want)
		}
	}
	for _, bad := range []string{"", "80", "a:b:c:d:e", "8080:host:notaport", "8080::80"} {
		if _, err := ParseLocalForward(bad); err == nil {
			t.Errorf("ParseLocalForward(%q) should fail", bad)
		}
	}
}

func TestParseDynamicForward(t *testing.T) {
	if got, err := ParseDynamicForward("1080"); err != nil || got.BindPort != 1080 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got, err := ParseDynamicForward("127.0.0.1:1080"); err != nil || got.BindPort != 1080 || got.BindAddress != "127.0.0.1" {
		t.Fatalf("got %+v err %v", got, err)
	}
	for _, bad := range []string{"", "0", "notaport", "a:b:c"} {
		if _, err := ParseDynamicForward(bad); err == nil {
			t.Errorf("ParseDynamicForward(%q) should fail", bad)
		}
	}
}

// startEchoServer listens on a loopback port and echoes everything it reads.
func startEchoServer(t *testing.T) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

// freePort reserves and releases a loopback port for a tunnel to bind.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// dialTestClient signs in to a fresh in-process SSH server for tunnel tests.
func dialTestClient(t *testing.T) *Client {
	t.Helper()
	addr, hostKey := startTestServer(t, "pw")
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(lineFor(host, hostKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Dialer{
		Prompts:        Prompts{Password: func(string, int) (string, error) { return "pw", nil }},
		KnownHostsPath: khPath,
		Timeout:        5 * time.Second,
		Cache:          NewAuthCache(),
	}
	h := model.NewHost("tunnel", host)
	h.Port = port
	h.User = "tester"
	client, err := d.Dial(context.Background(), h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestLocalForwardEndToEnd(t *testing.T) {
	target := startEchoServer(t)
	targetHost, targetPortStr, _ := net.SplitHostPort(target)
	targetPort := 0
	fmt.Sscanf(targetPortStr, "%d", &targetPort)

	client := dialTestClient(t)
	fwdPort := freePort(t)
	host := model.Host{ID: "h1", Name: "tunnel"}
	host.LocalForwards = []string{fmt.Sprintf("127.0.0.1:%d:%s:%d", fwdPort, targetHost, targetPort)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewForwardManager()
	if err := m.StartHost(ctx, client, host); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()

	statuses := m.Statuses()
	if len(statuses) != 1 || statuses[0].State != ForwardActive || statuses[0].Kind != "local" {
		t.Fatalf("statuses = %+v", statuses)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
}

func TestDynamicForwardSOCKS5EndToEnd(t *testing.T) {
	target := startEchoServer(t)
	targetHost, targetPortStr, _ := net.SplitHostPort(target)
	targetPort := 0
	fmt.Sscanf(targetPortStr, "%d", &targetPort)

	client := dialTestClient(t)
	socksPort := freePort(t)
	host := model.Host{ID: "h2", Name: "tunnel"}
	host.DynamicForward = fmt.Sprintf("127.0.0.1:%d", socksPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewForwardManager()
	if err := m.StartHost(ctx, client, host); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Greeting: version 5, one method, no-auth.
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 5 || reply[1] != 0 {
		t.Fatalf("greeting reply = %v", reply)
	}

	// CONNECT request with a domain-name address.
	req := []byte{5, 1, 0, 3, byte(len(targetHost))}
	req = append(req, targetHost...)
	req = append(req, byte(targetPort>>8), byte(targetPort))
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0 {
		t.Fatalf("connect reply code = %d", head[1])
	}
	// Consume the bound address (IPv4 form).
	if _, err := io.ReadFull(conn, make([]byte, 6)); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("socks echo = %q", buf)
	}
}

func TestForwardManagerStopReportsStopped(t *testing.T) {
	var last TunnelStatus
	m := NewForwardManager()
	m.OnStatus = func(s TunnelStatus) { last = s }

	client := dialTestClient(t)
	fwdPort := freePort(t)
	host := model.Host{ID: "h3", Name: "tunnel"}
	host.LocalForwards = []string{fmt.Sprintf("127.0.0.1:%d:example.com:80", fwdPort)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.StartHost(ctx, client, host); err != nil {
		t.Fatal(err)
	}
	m.StopHost("h3")
	if last.State != ForwardStopped {
		t.Fatalf("last status = %+v", last)
	}
	if len(m.Statuses()) != 0 {
		t.Fatalf("statuses should be empty after stop: %+v", m.Statuses())
	}
	// The port must be free again.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort))
	if err != nil {
		t.Fatalf("port still bound: %v", err)
	}
	_ = ln.Close()
}
