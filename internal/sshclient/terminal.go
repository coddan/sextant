package sshclient

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

// ShellOptions configures an interactive or one-shot remote shell.
type ShellOptions struct {
	// Command runs one command instead of an interactive shell.
	Command string
	Term    string
	Cols    int
	Rows    int
	Env     map[string]string
	// AgentForward requests ssh-agent forwarding for this session.
	AgentForward bool
}

// Shell is one PTY session.
type Shell struct {
	Host    model.Host
	session *ssh.Session
	reader  *io.PipeReader
	writer  *io.PipeWriter
	stdin   io.WriteCloser
	once    sync.Once
	waitErr error
	done    chan struct{}
}

// StartShell opens a PTY session. The caller reads remote output from the
// returned Shell and writes keystrokes to it.
func (c *Client) StartShell(ctx context.Context, opts ShellOptions) (*Shell, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	term := opts.Term
	if term == "" {
		term = "xterm-256color"
	}
	cols, rows := opts.Cols, opts.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty(term, rows, cols, modes); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("ssh: request PTY: %w", err)
	}
	for k, v := range opts.Env {
		_ = session.Setenv(k, v)
	}
	if opts.AgentForward || c.Host.ForwardAgent {
		// Not all servers accept the request; a server without agent
		// forwarding should still get a working shell.
		_, _ = session.SendRequest("auth-agent-req@openssh.com", true, nil)
	}

	pr, pw := io.Pipe()
	session.Stdout = pw
	session.Stderr = pw
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if opts.Command != "" {
		err = session.Start(opts.Command)
	} else {
		err = session.Shell()
	}
	if err != nil {
		_ = session.Close()
		_ = pw.Close()
		return nil, err
	}
	sh := &Shell{
		Host:    c.Host,
		session: session,
		reader:  pr,
		writer:  pw,
		stdin:   stdin,
		done:    make(chan struct{}),
	}
	go func() {
		sh.waitErr = session.Wait()
		_ = pw.Close()
		close(sh.done)
	}()
	go func() {
		<-ctx.Done()
		_ = sh.Close()
	}()
	return sh, nil
}

// Read reads remote output.
func (s *Shell) Read(p []byte) (int, error) { return s.reader.Read(p) }

// Write sends input to the remote PTY.
func (s *Shell) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Resize informs the server of a new window size.
func (s *Shell) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.session.WindowChange(rows, cols)
}

// Signal sends a signal to the remote process.
func (s *Shell) Signal(sig ssh.Signal) error { return s.session.Signal(sig) }

// Wait blocks until the session ends and returns its exit status.
func (s *Shell) Wait() error {
	<-s.done
	if s.waitErr == io.EOF {
		return nil
	}
	return s.waitErr
}

// Done is closed when the session ends.
func (s *Shell) Done() <-chan struct{} { return s.done }

// Close ends the session. It is safe to call more than once.
func (s *Shell) Close() error {
	var err error
	s.once.Do(func() {
		_ = s.stdin.Close()
		err = s.session.Close()
		_ = s.writer.CloseWithError(io.EOF)
		_ = s.reader.CloseWithError(io.EOF)
	})
	return err
}

// setupAgentForwarding proxies server-opened agent channels to the local
// agent (: the same path works with the Windows OpenSSH agent pipe).
func setupAgentForwarding(conn *ssh.Client) {
	chans := conn.HandleChannelOpen("auth-agent@openssh.com")
	go func() {
		for newChan := range chans {
			go func(newChan ssh.NewChannel) {
				ch, reqs, err := newChan.Accept()
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				local, err := dialAgent()
				if err != nil {
					_ = ch.Close()
					return
				}
				go func() {
					_, _ = io.Copy(ch, local)
					_ = ch.Close()
					_ = local.Close()
				}()
				_, _ = io.Copy(local, ch)
				_ = local.Close()
				_ = ch.Close()
			}(newChan)
		}
	}()
}

// Exec runs a command with a timeout and returns stdout, stderr and exit code.
// It is used for metrics, service discovery and snippet runs.
func (c *Client) Exec(ctx context.Context, command string, timeout time.Duration) (stdout, stderr []byte, exit int, err error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, -1, err
	}
	defer session.Close()
	outPipe, outW := io.Pipe()
	errPipe, errW := io.Pipe()
	session.Stdout = outW
	session.Stderr = errW
	if err := session.Start(command); err != nil {
		_ = outW.Close()
		_ = errW.Close()
		return nil, nil, -1, err
	}
	outCh := make(chan []byte, 1)
	errCh := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(outPipe); outCh <- b }()
	go func() { b, _ := io.ReadAll(errPipe); errCh <- b }()
	waitCh := make(chan error, 1)
	go func() { waitCh <- session.Wait() }()
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		waitErr = <-waitCh
		_ = outW.Close()
		_ = errW.Close()
		<-outCh
		<-errCh
		return nil, nil, -1, ctx.Err()
	case <-timer:
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		waitErr = <-waitCh
		_ = outW.Close()
		_ = errW.Close()
		<-outCh
		<-errCh
		return nil, nil, -1, fmt.Errorf("ssh: command timed out after %s", timeout)
	}
	_ = outW.Close()
	_ = errW.Close()
	out := <-outCh
	errb := <-errCh
	code := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*ssh.ExitError); ok {
			code = ee.ExitStatus()
		} else {
			return out, errb, -1, waitErr
		}
	}
	return out, errb, code, nil
}

// LocalEnv returns the environment Sextant passes to remote shells.
func LocalEnv() map[string]string {
	env := map[string]string{"TERM": "xterm-256color"}
	if v := os.Getenv("LANG"); v != "" {
		env["LANG"] = v
	}
	if v := os.Getenv("LC_ALL"); v != "" {
		env["LC_ALL"] = v
	}
	return env
}
