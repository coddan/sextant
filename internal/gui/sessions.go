package gui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
	"github.com/coddan/sextant/internal/termutil"
)

// terminalSession is one SSH PTY session. Each session owns its view, shell and
// tab, so closing or losing one connection leaves the others running.
type terminalSession struct {
	app     *App
	host    model.Host
	title   string
	shell   *sshclient.Shell
	cancel  context.CancelFunc
	view    *TerminalView
	content fyne.CanvasObject
	status  *widget.Label
	sudo    *termutil.Throttle

	// writeQ serialises keystrokes onto one goroutine. Spawning a goroutine per
	// key event can reorder fast input; a single writer preserves typeahead.
	writeQ    chan []byte
	writeEnd  chan struct{}
	writeOnce sync.Once

	closed  bool
	closing bool
}

// connect dials a host in the background and opens a new terminal session on
// success. Existing sessions are never replaced.
func (g *App) connect(h model.Host) {
	g.setStatus(fmt.Sprintf("%s %s…", g.tr.T(i18n.TerminalStatusConnecting), h.Name))
	go func() {
		client, err := g.connectedClient(g.ctx, h)
		if err != nil {
			fyne.Do(func() {
				g.setStatus(err.Error())
				g.showError(friendlyError(g.tr, err))
			})
			return
		}
		// Opening a channel can block on the network, so keep it off the UI
		// goroutine. The tab only appears once the shell is ready.
		ctx, cancel := context.WithCancel(g.ctx)
		shell, err := client.StartShell(ctx, sshclient.ShellOptions{
			Cols:         80,
			Rows:         24,
			AgentForward: h.ForwardAgent,
		})
		if err != nil {
			cancel()
			fyne.Do(func() {
				g.setStatus(err.Error())
				g.showError(err)
			})
			return
		}
		fyne.Do(func() { g.openSession(h, shell, cancel) })
	}()
}

// connectedClient reuses a live connection or dials one. The per-host lock
// keeps two quick clicks on the same host from racing and leaking the client
// that loses the map write; different hosts still dial in parallel.
func (g *App) connectedClient(ctx context.Context, h model.Host) (*sshclient.Client, error) {
	g.connMu.Lock()
	if g.connects == nil {
		g.connects = map[string]*sync.Mutex{}
	}
	lock := g.connects[h.ID]
	if lock == nil {
		lock = &sync.Mutex{}
		g.connects[h.ID] = lock
	}
	g.connMu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	if client, ok := g.rt.Client(h.ID); ok {
		return client, nil
	}
	return g.rt.Connect(ctx, h)
}

// openSession adds a ready shell to the terminal workspace.
func (g *App) openSession(h model.Host, shell *sshclient.Shell, cancel context.CancelFunc) {
	if g.content == nil {
		cancel()
		_ = shell.Close()
		return
	}

	view := NewTerminalView(float32(g.rt.Store.Settings.Terminal.FontSize))
	s := &terminalSession{
		app:      g,
		host:     h,
		title:    g.uniqueSessionTitle(h),
		shell:    shell,
		cancel:   cancel,
		view:     view,
		sudo:     termutil.NewThrottle(5 * time.Second),
		writeQ:   make(chan []byte, 256),
		writeEnd: make(chan struct{}),
	}
	view.OnInput = func(p []byte) {
		if s.closed {
			return
		}
		data := append([]byte(nil), p...)
		select {
		case s.writeQ <- data:
		case <-s.writeEnd:
		}
	}
	view.OnResize = func(cols, rows int) { _ = shell.Resize(cols, rows) }
	go s.writeLoop(shell)
	s.content = s.buildContent()

	// A successful connection also means background metrics can use the live
	// client, even if the host previously needed an interactive login.
	delete(g.metricsRetry, h.ID)
	g.sessions = append(g.sessions, s)
	g.active = s
	g.showScreen(screenTerminal)
	if g.win != nil {
		g.win.Canvas().Focus(view)
	}
	g.notify(model.NotifySuccess, fmt.Sprintf(g.tr.T(i18n.NotifyConnected), h.Name))

	go g.readSession(s)
}

// writeLoop drains typed input in order and stops when the session ends.
func (s *terminalSession) writeLoop(shell *sshclient.Shell) {
	for {
		select {
		case <-s.writeEnd:
			return
		case p := <-s.writeQ:
			if _, err := shell.Write(p); err != nil {
				fyne.Do(func() { s.app.markSessionClosed(s) })
				return
			}
		}
	}
}

func (s *terminalSession) stopWriter() {
	if s.writeEnd == nil {
		return
	}
	s.writeOnce.Do(func() { close(s.writeEnd) })
}

// buildContent creates the per-session header and body. It is rebuilt when the
// language changes, reusing the live terminal view.
func (s *terminalSession) buildContent() fyne.CanvasObject {
	state := s.app.tr.T(i18n.TerminalStatusConnected)
	importance := widget.MediumImportance
	if s.closed {
		state = s.app.tr.T(i18n.TerminalStatusDisconnected)
		importance = widget.WarningImportance
	}
	s.status = widget.NewLabel(state)
	s.status.Importance = importance

	host := widget.NewLabelWithStyle(
		fmt.Sprintf("%s — %s@%s:%d", s.title, orDefault(s.host.User, "root"), s.host.Hostname, s.host.Port),
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true},
	)
	host.Truncation = fyne.TextTruncateEllipsis
	closeBtn := widget.NewButtonWithIcon(s.app.tr.T(i18n.CommonClose), theme.CancelIcon(), func() {
		s.app.closeSession(s)
	})
	closeBtn.Importance = widget.LowImportance

	bar := container.NewBorder(nil, nil, nil, container.NewHBox(s.status, closeBtn), host)
	return container.NewBorder(bar, nil, nil, nil, s.view)
}

// readSession streams remote output until the shell ends.
func (g *App) readSession(s *terminalSession) {
	shell := s.shell
	buf := make([]byte, 16384)
	for {
		n, err := shell.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if g.rt.Store.Settings.Terminal.SudoAutofill && termutil.SudoPrompt(data) && s.sudo.Allow() {
				if pw, ok := g.rt.Store.Password(s.host); ok && pw != "" {
					go func() { _, _ = shell.Write([]byte(pw + "\n")) }()
				}
			}
			fyne.Do(func() { s.view.Write(data) })
		}
		if err != nil {
			fyne.Do(func() { g.markSessionClosed(s) })
			return
		}
	}
}

// markSessionClosed keeps the tab and its output visible after the remote side
// ends the session. The tab can then be closed like any other.
func (g *App) markSessionClosed(s *terminalSession) {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.shell = nil
	s.stopWriter()
	s.view.SetClosed()
	if s.status != nil {
		s.status.SetText(g.tr.T(i18n.TerminalStatusDisconnected))
		s.status.Importance = widget.WarningImportance
	}
	g.refreshSidebar()
	if g.active == s {
		g.setStatus(fmt.Sprintf("%s: %s", s.host.Name, g.tr.T(i18n.TerminalStatusDisconnected)))
	}
	g.notify(model.NotifyWarning, fmt.Sprintf(g.tr.T(i18n.NotifyDisconnected), s.host.Name))
}

// closeSession closes one terminal. With the confirm setting on, a live
// session asks first; already finished sessions close immediately.
func (g *App) closeSession(s *terminalSession) {
	if s == nil || s.closing {
		return
	}
	if !s.closed && g.rt.Store.Settings.General.ConfirmCloseTab {
		s.closing = true
		dialog.ShowConfirm(
			g.tr.T(i18n.CommonClose),
			fmt.Sprintf(g.tr.T(i18n.TerminalCloseConfirm), s.host.Name),
			func(ok bool) {
				s.closing = false
				if ok {
					g.endSession(s)
				}
			},
			g.win,
		)
		return
	}
	g.endSession(s)
}

// endSession tears down a session and removes it from the sidebar and
// workspace. Closing the active terminal falls through to the next one instead
// of leaving an empty pane.
func (g *App) endSession(s *terminalSession) {
	if s == nil {
		return
	}
	wasClosed := s.closed
	idx := g.sessionIndex(s)
	wasActive := g.active == s
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.shell != nil {
		_ = s.shell.Close()
		s.shell = nil
	}
	s.stopWriter()
	g.removeSession(s)
	if !wasClosed {
		g.setStatus(fmt.Sprintf("%s: %s", s.host.Name, g.tr.T(i18n.TerminalStatusDisconnected)))
	}
	if wasActive && g.screen == screenTerminal {
		if len(g.sessions) > 0 {
			g.activateSession(g.sessions[clampInt(idx, 0, len(g.sessions)-1)])
		} else {
			g.active = nil
			g.showScreen(screenDashboard)
		}
	}
}

func (g *App) removeSession(s *terminalSession) {
	for i, open := range g.sessions {
		if open == s {
			g.sessions = append(g.sessions[:i], g.sessions[i+1:]...)
			break
		}
	}
	if g.active == s {
		g.active = nil
	}
	g.refreshSidebar()
	if g.screen == screenTerminal {
		g.showActiveSession()
	}
}

func (g *App) sessionIndex(s *terminalSession) int {
	for i, open := range g.sessions {
		if open == s {
			return i
		}
	}
	return -1
}

// closeAllSessions shuts down every shell without prompting, for application
// exit.
func (g *App) closeAllSessions() {
	for _, s := range g.sessions {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		if s.shell != nil {
			_ = s.shell.Close()
			s.shell = nil
		}
		s.stopWriter()
	}
	g.sessions = nil
	g.active = nil
}

// uniqueSessionTitle avoids identical tab names when the same host is opened
// more than once.
func (g *App) uniqueSessionTitle(h model.Host) string {
	base := h.Name
	if base == "" {
		base = h.Hostname
	}
	title := base
	n := 2
	for g.sessionTitleTaken(title) {
		title = fmt.Sprintf("%s (%d)", base, n)
		n++
	}
	return title
}

func (g *App) sessionTitleTaken(title string) bool {
	for _, s := range g.sessions {
		if s.title == title {
			return true
		}
	}
	return false
}
