package gui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	sextant "github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
	"github.com/coddan/sextant/internal/termutil"
)

// errCancelled is returned when the user dismisses a credential prompt.
var errCancelled = errors.New("cancelled")

// App is the Fyne frontend. It shares the engine (app.Runtime) with the TUI so
// both behave identically.
type App struct {
	rt  *sextant.Runtime
	fa  fyne.App
	win fyne.Window
	tr  *i18n.Translator

	tabs   *container.AppTabs
	status *widget.Label

	hostsBox   *fyne.Container
	filterText string
	onlyOnline *widget.Check
	metrics    map[string]model.Metrics

	termTab    *container.TabItem
	termBody   *fyne.Container
	termView   *TerminalView
	termHost   *widget.Label
	termCancel context.CancelFunc
	shell      *sshclient.Shell

	sftp   *sftpView
	snip   *snippetView
	sett   *settingsView
	forw   *forwardsView
	keys   *keySetupView
	hosts  []model.Host
	ctx    context.Context
	cancel context.CancelFunc
}

// Run opens the desktop window and blocks until it closes.
func Run(rt *sextant.Runtime) error {
	fa := app.New()
	w := fa.NewWindow("Sextant")
	ctx, cancel := context.WithCancel(context.Background())
	g := &App{
		rt:      rt,
		fa:      fa,
		win:     w,
		tr:      rt.Tr,
		metrics: map[string]model.Metrics{},
		ctx:     ctx,
		cancel:  cancel,
	}
	g.installPrompts()
	g.applyTheme()
	w.SetContent(g.buildUI())
	w.SetOnDropped(g.onDropped)
	w.Resize(fyne.NewSize(1120, 740))
	w.CenterOnScreen()
	w.SetOnClosed(func() {
		g.disconnectAll()
		cancel()
	})
	w.ShowAndRun()
	return nil
}

// buildTerminalTab creates the terminal pane. Its body is swapped when a
// session starts.
func (g *App) buildTerminalTab() *container.TabItem {
	g.termHost = widget.NewLabel(g.tr.T(i18n.TerminalStatusDisconnected))
	closeBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonClose), theme.CancelIcon(), g.closeTerminal)
	bar := container.NewBorder(nil, nil, nil, closeBtn, g.termHost)
	placeholder := widget.NewLabel(g.tr.T(i18n.TerminalStatusDisconnected))
	placeholder.Alignment = fyne.TextAlignCenter
	g.termBody = container.NewStack(placeholder)
	content := container.NewBorder(bar, nil, nil, nil, g.termBody)
	return container.NewTabItemWithIcon(g.tr.T(i18n.NavTerminal), theme.ComputerIcon(), content)
}

// buildUI creates the tab layout. It is called again after a language change so
// every label picks up the new translation.
func (g *App) buildUI() fyne.CanvasObject {
	g.status = widget.NewLabel("")
	g.status.Truncation = fyne.TextTruncateEllipsis
	g.termTab = g.buildTerminalTab()
	g.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon(g.tr.T(i18n.NavHosts), theme.HomeIcon(), g.buildHostsTab()),
		g.termTab,
		container.NewTabItemWithIcon(g.tr.T(i18n.NavSFTP), theme.FolderIcon(), g.buildSFTPTab()),
		container.NewTabItemWithIcon(g.tr.T(i18n.SnippetsTitle), theme.MediaPlayIcon(), g.buildSnippetsTab()),
		container.NewTabItemWithIcon(g.tr.T(i18n.ForwardsTitle), theme.MailSendIcon(), g.buildForwardsTab()),
		container.NewTabItemWithIcon(g.tr.T(i18n.KeySetupTitle), theme.LoginIcon(), g.buildKeySetupTab()),
		container.NewTabItemWithIcon(g.tr.T(i18n.NavSettings), theme.SettingsIcon(), g.buildSettingsTab()),
	)
	g.tabs.OnSelected = func(item *container.TabItem) {
		switch item.Text {
		case g.tr.T(i18n.ForwardsTitle):
			g.forw.refresh()
		case g.tr.T(i18n.NavSettings):
			g.sett.refresh()
		case g.tr.T(i18n.SnippetsTitle):
			g.snip.refresh()
		}
	}
	bottom := container.NewBorder(nil, nil, widget.NewLabel("Sextant"), nil, g.status)
	return container.NewBorder(nil, bottom, nil, nil, g.tabs)
}

// rebuildUI swaps in a freshly translated interface, preserving the selection.
func (g *App) rebuildUI() {
	idx := g.tabs.SelectedIndex()
	g.win.SetContent(g.buildUI())
	g.tabs.SelectIndex(idx)
}

func (g *App) setStatus(text string) {
	if g.status == nil {
		return
	}
	g.status.SetText(text)
}

func (g *App) setStatusAsync(text string) {
	fyne.Do(func() { g.setStatus(text) })
}

func (g *App) showError(err error) {
	if err == nil {
		return
	}
	dialog.ShowError(err, g.win)
}

func (g *App) disconnectAll() {
	for _, h := range g.rt.Store.Hosts {
		g.rt.Disconnect(h.ID)
	}
}

// installPrompts wires the engine's blocking prompts to Fyne dialogs. Each
// dialog runs on the UI goroutine while the dialing goroutine waits on a
// channel, which keeps authentication order and retries intact.
func (g *App) installPrompts() {
	g.rt.PasswordPrompt = func(hostName string, attempt int) (string, error) {
		msg := g.tr.T(i18n.HostPassword)
		if attempt > 1 {
			msg = fmt.Sprintf("%s (%s %d)", msg, g.tr.T(i18n.CommonRetry), attempt)
		}
		return g.askEntry(hostName, msg, true)
	}
	g.rt.PassphrasePrompt = func(keyPath string, attempt int) (string, error) {
		return g.askEntry(g.tr.T(i18n.HostPassphrase), keyPath, true)
	}
	g.rt.HostKeyPrompt = func(host, fingerprint, keyType string) (bool, error) {
		ch := make(chan bool, 1)
		fyne.Do(func() {
			body := g.tr.T(i18n.HostKeyUnknownBody, host)
			if keyType != "" {
				body = g.tr.T(i18n.HostKeyChangedBody, host)
			}
			body += "\n\n" + g.tr.T(i18n.HostKeyFingerprint) + ": " + fingerprint
			dialog.ShowConfirm(g.tr.T(i18n.HostKeyTitle), body, func(ok bool) {
				ch <- ok
			}, g.win)
		})
		ok := <-ch
		if !ok {
			return false, errCancelled
		}
		return true, nil
	}
	g.rt.KeyboardInteractive = func(hostName, name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i, q := range questions {
			echo := i < len(echos) && echos[i]
			answer, err := g.askEntry(hostName, q, !echo)
			if err != nil {
				return nil, err
			}
			answers[i] = answer
		}
		return answers, nil
	}
}

// askEntry shows a modal text field and blocks until the user answers.
func (g *App) askEntry(title, message string, secret bool) (string, error) {
	ch := make(chan string, 1)
	fyne.Do(func() {
		entry := widget.NewEntry()
		entry.Password = secret
		form := widget.NewForm(widget.NewFormItem(message, entry))
		d := dialog.NewCustomConfirm(title, g.tr.T(i18n.CommonOK), g.tr.T(i18n.CommonCancel), form, func(ok bool) {
			if ok {
				ch <- entry.Text
			} else {
				ch <- "\x00cancel"
			}
		}, g.win)
		d.Show()
		w := g.win.Canvas()
		fyne.Do(func() { w.Focus(entry) })
	})
	value := <-ch
	if value == "\x00cancel" {
		return "", errCancelled
	}
	return value, nil
}

// askText is a non-blocking (callback) variant used by panel actions.
func (g *App) askText(title, message, initial string, secret bool, done func(string)) {
	fyne.Do(func() {
		entry := widget.NewEntry()
		entry.SetText(initial)
		entry.Password = secret
		form := widget.NewForm(widget.NewFormItem(message, entry))
		dialog.ShowCustomConfirm(title, g.tr.T(i18n.CommonOK), g.tr.T(i18n.CommonCancel), form, func(ok bool) {
			if ok {
				done(entry.Text)
			}
		}, g.win)
		w := g.win.Canvas()
		fyne.Do(func() { w.Focus(entry) })
	})
}

// connect dials a host in the background and opens its terminal on success.
func (g *App) connect(h model.Host) {
	g.setStatus(fmt.Sprintf("%s %s…", g.tr.T(i18n.TerminalStatusConnecting), h.Name))
	go func() {
		client, err := g.rt.Connect(g.ctx, h)
		fyne.Do(func() {
			if err != nil {
				g.setStatus(err.Error())
				g.showError(friendlyError(g.tr, err))
				return
			}
			g.startTerminal(h, client)
		})
	}()
}

// startTerminal opens a PTY session for an established client.
func (g *App) startTerminal(h model.Host, client *sshclient.Client) {
	if g.termCancel != nil {
		g.termCancel()
		g.termCancel = nil
	}
	if g.shell != nil {
		_ = g.shell.Close()
		g.shell = nil
	}

	ctx, cancel := context.WithCancel(g.ctx)
	g.termCancel = cancel

	view := NewTerminalView(float32(g.rt.Store.Settings.Terminal.FontSize))
	shell, err := client.StartShell(ctx, sshclient.ShellOptions{
		Cols:         80,
		Rows:         24,
		AgentForward: h.ForwardAgent,
	})
	if err != nil {
		cancel()
		g.showError(err)
		return
	}
	g.shell = shell
	g.termView = view
	g.termHost.SetText(fmt.Sprintf("%s — %s@%s:%d", h.Name, orDefault(h.User, "root"), h.Hostname, h.Port))
	view.OnInput = func(p []byte) {
		go func() { _, _ = shell.Write(p) }()
	}
	view.OnResize = func(cols, rows int) { _ = shell.Resize(cols, rows) }
	g.termBody.Objects = []fyne.CanvasObject{view}
	g.termBody.Refresh()
	g.selectTab(g.tr.T(i18n.NavTerminal))
	g.win.Canvas().Focus(view)

	sudo := termutil.NewThrottle(5 * time.Second)
	go func() {
		buf := make([]byte, 16384)
		for {
			n, err := shell.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				if g.rt.Store.Settings.Terminal.SudoAutofill && termutil.SudoPrompt(data) && sudo.Allow() {
					if pw, ok := g.rt.Store.Password(h); ok && pw != "" {
						go func() { _, _ = shell.Write([]byte(pw + "\n")) }()
					}
				}
				fyne.Do(func() { view.Write(data) })
			}
			if err != nil {
				fyne.Do(func() {
					view.SetClosed()
					g.setStatus(fmt.Sprintf("%s: %s", h.Name, g.tr.T(i18n.TerminalStatusDisconnected)))
				})
				return
			}
		}
	}()
}

// closeTerminal ends the current session and returns to the placeholder.
func (g *App) closeTerminal() {
	if g.termCancel != nil {
		g.termCancel()
		g.termCancel = nil
	}
	if g.shell != nil {
		_ = g.shell.Close()
		g.shell = nil
	}
	placeholder := widget.NewLabel(g.tr.T(i18n.TerminalStatusDisconnected))
	placeholder.Alignment = fyne.TextAlignCenter
	g.termBody.Objects = []fyne.CanvasObject{placeholder}
	g.termBody.Refresh()
	g.termHost.SetText(g.tr.T(i18n.TerminalStatusDisconnected))
	g.termView = nil
}

func (g *App) selectTab(title string) {
	for i, item := range g.tabs.Items {
		if item.Text == title {
			g.tabs.SelectIndex(i)
			return
		}
	}
}

// notify shows a status message that expires according to settings.
func (g *App) notify(kind model.NotificationKind, text string) {
	g.setStatus(text)
	timeout := g.rt.Store.Settings.Notifications.TimeoutFor(kind)
	if timeout <= 0 {
		return
	}
	go func() {
		time.Sleep(timeout)
		fyne.Do(func() {
			if g.status.Text == text {
				g.setStatus("")
			}
		})
	}()
}

// onDropped receives files dragged from the OS file manager. Fyne only offers a
// window-wide drop callback, so the destination is inferred from app state: an
// open SFTP session receives the files in the directory it is showing.
func (g *App) onDropped(_ fyne.Position, items []fyne.URI) {
	if len(items) == 0 {
		return
	}
	if g.sftp == nil || g.sftp.sftp == nil {
		g.notify(model.NotifyWarning, g.tr.T(i18n.SFTPNoSession))
		return
	}
	g.sftp.uploadDropped(items)
}

func friendlyError(tr *i18n.Translator, err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case containsAny(text, "unable to authenticate", "no supported methods", "handshake failed"):
		return errors.New(tr.T(i18n.ErrorAuthFailed))
	case containsAny(text, "i/o timeout", "timed out", "timeout"):
		return errors.New(tr.T(i18n.ErrorTimeout, ""))
	case containsAny(text, "knownhosts: key mismatch", "host key changed"):
		return errors.New(tr.T(i18n.ErrorHostKeyChanged, ""))
	}
	return err
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
