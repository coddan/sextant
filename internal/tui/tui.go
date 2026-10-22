// Package tui is the terminal frontend. It drives the shared app.Runtime and
// renders the dashboard, host editor, SSH terminal, SFTP browser, snippets,
// settings and the key-setup wizard.
//
// The package owns no SSH logic: everything it shows comes from internal/app
// and internal/sshclient, which the desktop frontend uses too.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
)

type screen int

const (
	screenDashboard screen = iota
	screenHostForm
	screenTerminal
	screenSFTP
	screenSnippets
	screenSettings
	screenForwards
	screenKeySetup
	screenHelp
	screenAbout
)

// Model is the bubbletea model for the whole interface.
type Model struct {
	rt     *app.Runtime
	ctx    context.Context
	cancel context.CancelFunc
	width  int
	height int

	screen     screen
	prevScreen screen

	status  string
	errText string

	// Dashboard state.
	hosts      []model.Host
	visible    []model.Host
	cursor     int
	filter     string
	filtering  bool
	onlyOnline bool
	metrics    map[string]model.Metrics
	loading    bool
	spinner    spinner.Model

	// Sub-views. They are always non-nil so Update never has to check.
	form     *hostForm
	term     *terminalTab
	snip     *snippetPanel
	settings *settingsPanel
	sftp     *sftpPanel
	forwards *forwardsPanel
	keySetup *keySetupPanel
	prompt   *promptState
	promptCh chan promptRequest
	keyCh    chan tea.Msg

	quit bool
}

// New builds the model around a runtime.
func New(rt *app.Runtime) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{
		rt:       rt,
		ctx:      ctx,
		cancel:   cancel,
		screen:   screenDashboard,
		metrics:  map[string]model.Metrics{},
		spinner:  sp,
		settings: &settingsPanel{},
		snip:     &snippetPanel{},
		sftp:     &sftpPanel{},
		forwards: &forwardsPanel{},
		keySetup: &keySetupPanel{},
		promptCh: make(chan promptRequest),
		keyCh:    make(chan tea.Msg, 64),
	}
	rt.PasswordPrompt = m.askPassword
	rt.PassphrasePrompt = m.askPassphrase
	rt.HostKeyPrompt = m.askHostKey
	rt.KeyboardInteractive = m.askKeyboardInteractive
	m.reloadHosts()
	m.applyFilter()
	return m
}

// Run starts the terminal interface.
func Run(rt *app.Runtime) error {
	m := New(rt)
	defer m.cancel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitPromptCmd())
}

// tr is a short alias for the translator.
func (m *Model) tr(key i18n.Key, args ...any) string {
	return m.rt.Tr.T(key, args...)
}

func (m *Model) reloadHosts() {
	m.hosts = append([]model.Host(nil), m.rt.Store.Hosts...)
}

func (m *Model) applyFilter() {
	m.visible = m.visible[:0]
	needle := strings.ToLower(strings.TrimSpace(m.filter))
	for _, h := range m.hosts {
		if h.Hidden {
			continue
		}
		if m.onlyOnline {
			met, ok := m.metrics[h.ID]
			if !ok || met.Error != "" {
				continue
			}
		}
		if needle == "" ||
			strings.Contains(strings.ToLower(h.Name), needle) ||
			strings.Contains(strings.ToLower(h.Hostname), needle) ||
			strings.Contains(strings.ToLower(h.User), needle) ||
			strings.Contains(strings.ToLower(h.Group), needle) {
			m.visible = append(m.visible, h)
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = maxInt(0, len(m.visible)-1)
	}
}

func (m *Model) selectedHost() (model.Host, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return model.Host{}, false
	}
	return m.visible[m.cursor], true
}

func (m *Model) setStatus(format string, args ...any) {
	m.status = fmt.Sprintf(format, args...)
	m.errText = ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
