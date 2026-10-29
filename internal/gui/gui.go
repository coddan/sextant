package gui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	sextant "github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
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

	status *widget.Label

	sidebar     fyne.CanvasObject
	navButtons  map[screenID]*widget.Button
	sessionRows *fyne.Container
	content     *fyne.Container
	views       map[screenID]fyne.CanvasObject
	screen      screenID
	sessions    []*terminalSession
	active      *terminalSession
	termStack   *fyne.Container

	hostsBox     *fyne.Container
	hostsWrap    *fyne.Container
	filterText   string
	onlyOnline   *widget.Check
	metrics      map[string]model.Metrics
	probes       map[string]sshclient.ProbeResult
	metricsRetry map[string]time.Time
	metricsRun   bool
	metricsOff   context.CancelFunc

	connMu   sync.Mutex
	connects map[string]*sync.Mutex
	sftp     *sftpView
	snip     *snippetView
	sett     *settingsView
	forw     *forwardsView
	keys     *keySetupView
	hosts    []model.Host
	ctx      context.Context
	cancel   context.CancelFunc
}

// Run opens the desktop window and blocks until it closes.
func Run(rt *sextant.Runtime) error {
	fa := app.New()
	w := fa.NewWindow("Sextant")
	ctx, cancel := context.WithCancel(context.Background())
	g := &App{
		rt:           rt,
		fa:           fa,
		win:          w,
		tr:           rt.Tr,
		metrics:      map[string]model.Metrics{},
		probes:       map[string]sshclient.ProbeResult{},
		metricsRetry: map[string]time.Time{},
		ctx:          ctx,
		cancel:       cancel,
	}
	g.installPrompts()
	g.applyTheme()
	w.SetContent(g.buildUI())
	w.SetOnDropped(g.onDropped)
	w.Resize(fyne.NewSize(1120, 740))
	w.CenterOnScreen()
	g.startMetricsLoop()
	w.SetOnClosed(func() {
		g.closeAllSessions()
		g.disconnectAll()
		cancel()
	})
	w.ShowAndRun()
	return nil
}

// buildUI creates the persistent left navigation and the active screen. It is
// called again after a language change so every label picks up the new
// translation. Open terminal sessions keep their live views.
func (g *App) buildUI() fyne.CanvasObject {
	screen := g.screen
	if screen == "" {
		screen = screenDashboard
	}
	g.status = widget.NewLabel("")
	g.status.Truncation = fyne.TextTruncateEllipsis

	// Session headers contain translated labels, so refresh them while their
	// terminal views stay untouched.
	for _, s := range g.sessions {
		s.content = s.buildContent()
	}

	g.views = map[screenID]fyne.CanvasObject{
		screenDashboard: g.buildHostsTab(),
		screenTerminal:  g.buildTerminalWorkspace(),
		screenSFTP:      g.buildSFTPTab(),
		screenSnippets:  g.buildSnippetsTab(),
		screenForwards:  g.buildForwardsTab(),
		screenKeys:      g.buildKeySetupTab(),
		screenSettings:  g.buildSettingsTab(),
	}
	g.content = container.NewStack()
	g.screen = screen
	g.sidebar = g.buildSidebar()
	g.showScreen(screen)

	statusRow := container.NewBorder(nil, nil, widget.NewLabel(g.tr.T(i18n.AppName)), nil, g.status)
	bottom := container.NewVBox(widget.NewSeparator(), container.NewPadded(statusRow))
	return container.NewBorder(nil, bottom, g.sidebar, nil, g.content)
}

// rebuildUI swaps in a freshly translated interface, preserving the active
// screen and every open terminal.
func (g *App) rebuildUI() {
	g.win.SetContent(g.buildUI())
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
			if g.status != nil && g.status.Text == text {
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
