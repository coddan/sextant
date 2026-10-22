package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

// startTestServer runs an in-process SSH server with password auth and a shell
// that echoes its input. It exercises the real client path, including host-key
// verification and PTY requests.
func startTestServer(t *testing.T, password string) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)
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
			go serveTestConn(conn, cfg)
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

func serveTestConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() == "direct-tcpip" {
			extra := newChan.ExtraData()
			ch, requests, err := newChan.Accept()
			if err != nil {
				continue
			}
			go serveDirectTCPIP(ch, requests, extra)
			continue
		}
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range requests {
				switch req.Type {
				case "pty-req", "env", "auth-agent-req@openssh.com":
					_ = req.Reply(true, nil)
				case "shell":
					_ = req.Reply(true, nil)
					_, _ = ch.Write([]byte("welcome\r\n"))
					go func() {
						buf := make([]byte, 1024)
						for {
							n, err := ch.Read(buf)
							if n > 0 {
								_, _ = ch.Write(buf[:n])
							}
							if err != nil {
								_ = ch.Close()
								return
							}
						}
					}()
				case "exec":
					cmd := string(req.Payload[4:])
					_ = req.Reply(true, nil)
					_, _ = ch.Write([]byte("ran:" + cmd + "\n"))
					_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					_ = ch.Close()
				case "subsystem":
					name := string(req.Payload[4:])
					if name != "sftp" {
						_ = req.Reply(false, nil)
						continue
					}
					_ = req.Reply(true, nil)
					go func() {
						server, err := sftp.NewServer(ch)
						if err != nil {
							_ = ch.Close()
							return
						}
						_ = server.Serve()
						_ = server.Close()
						_ = ch.Close()
					}()
				case "window-change":
					_ = req.Reply(true, nil)
				default:
					_ = req.Reply(false, nil)
				}
			}
		}()
	}
}

// serveDirectTCPIP accepts a direct-tcpip channel, dials the requested target
// and pipes bytes, so local and dynamic forwarding can be tested end to end.
func serveDirectTCPIP(ch ssh.Channel, requests <-chan *ssh.Request, extra []byte) {
	go ssh.DiscardRequests(requests)
	var payload struct {
		DestAddr string
		DestPort uint32
		OrigAddr string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(extra, &payload); err != nil {
		_ = ch.Close()
		return
	}
	target, err := net.DialTimeout("tcp", net.JoinHostPort(payload.DestAddr, fmt.Sprint(payload.DestPort)), 3*time.Second)
	if err != nil {
		_ = ch.Close()
		return
	}
	defer target.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, ch); closeWrite(target); done <- struct{}{} }()
	go func() { _, _ = io.Copy(ch, target); ch.CloseWrite(); done <- struct{}{} }()
	<-done
	_ = ch.Close()
}

func readTimeout(t *testing.T, r *Shell, d time.Duration) string {
	t.Helper()
	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 128)
		n, err := r.Read(buf)
		ch <- result{string(buf[:n]), err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatal(res.err)
		}
		return res.s
	case <-time.After(d):
		t.Fatal("read timed out")
		return ""
	}
}

func TestDialKnownHostsAndShell(t *testing.T) {
	addr, hostKey := startTestServer(t, "pw")
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(lineFor(host, hostKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	prompts := Prompts{
		Password: func(string, int) (string, error) { return "pw", nil },
	}
	d := &Dialer{Prompts: prompts, KnownHostsPath: khPath, Timeout: 5 * time.Second, Cache: NewAuthCache()}
	h := model.NewHost("test", host)
	h.Port = port
	h.User = "tester"

	client, err := d.Dial(context.Background(), h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shell, err := client.StartShell(ctx, ShellOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Close()
	if got := readTimeout(t, shell, 2*time.Second); got != "welcome\r\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := shell.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := readTimeout(t, shell, 2*time.Second); got != "abc" {
		t.Fatalf("echo = %q", got)
	}
	out, _, code, err := client.Exec(context.Background(), "hello", 2*time.Second)
	if err != nil || code != 0 || string(out) != "ran:hello\n" {
		t.Fatalf("exec = %q code=%d err=%v", out, code, err)
	}
}

func TestDialRejectsChangedHostKey(t *testing.T) {
	addr, _ := startTestServer(t, "pw")
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	wrong := testKey(t)
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(lineFor(host, wrong)), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Dialer{
		Prompts:        Prompts{Password: func(string, int) (string, error) { return "pw", nil }},
		KnownHostsPath: khPath,
		Timeout:        5 * time.Second,
		Cache:          NewAuthCache(),
	}
	h := model.NewHost("test", host)
	h.Port = port
	if _, err := d.Dial(context.Background(), h, nil, nil); err == nil {
		t.Fatal("expected host key rejection")
	}
}
