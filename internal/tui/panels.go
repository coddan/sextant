package tui

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// ---------------------------------------------------------------------------
// Snippets
// ---------------------------------------------------------------------------

// snippetDoneMsg carries the results of a finished snippet run.
type snippetDoneMsg struct{ results []sshclient.SnippetResult }

// snippetLineMsg is one streamed line of snippet output.
type snippetLineMsg struct {
	host   string
	stream string
	line   string
}

type snippetPanel struct {
	cursor  int
	editing bool
	form    snippetForm
	running bool
	stream  bool
	lines   []string
	results []sshclient.SnippetResult
	ch      chan tea.Msg
	scroll  int
}

type snippetForm struct {
	name    textinput.Model
	command textinput.Model
	focus   int
}

func (p *snippetPanel) ensure(rt *app.Runtime) {
	if p.ch == nil {
		p.ch = make(chan tea.Msg, 256)
	}
	if p.form.name.Width == 0 {
		p.form.name = textinput.New()
		p.form.name.Placeholder = "name"
		p.form.name.CharLimit = 128
		p.form.command = textinput.New()
		p.form.command.Placeholder = "command"
		p.form.command.CharLimit = 1024
	}
}

func (p *snippetPanel) snippets(rt *app.Runtime) []model.Snippet { return rt.Store.Snippets }

func (p *snippetPanel) waitCmd() tea.Cmd {
	ch := p.ch
	return func() tea.Msg { return <-ch }
}

// runCmd executes the selected snippet on every host.
func (p *snippetPanel) runCmd(m *Model, snippet model.Snippet) tea.Cmd {
	rt := m.rt
	ctx := m.ctx
	hosts := append([]model.Host(nil), m.hosts...)
	stream := p.stream
	ch := p.ch
	p.running = true
	p.lines = nil
	p.results = nil
	p.scroll = 0
	return func() tea.Msg {
		results := sshclient.RunSnippet(ctx, rt.Dialer, hosts, rt.Store.Hosts, rt.Resolver(), snippet.Command, sshclient.SnippetRunOptions{
			Stream:        stream,
			MaxConcurrent: maxInt(1, rt.Store.Settings.General.MaxConcurrent),
			OnEvent: func(e sshclient.SnippetEvent) {
				if e.Line != "" {
					select {
					case ch <- snippetLineMsg{host: e.HostName, stream: e.Stream, line: e.Line}:
					default:
					}
				}
				if e.Result != nil {
					select {
					case ch <- snippetDoneMsg{results: []sshclient.SnippetResult{*e.Result}}:
					default:
					}
				}
			},
		})
		return snippetDoneMsg{results: results}
	}
}

func (p *snippetPanel) saveSnippet(rt *app.Runtime, existing *model.Snippet) {
	name := strings.TrimSpace(p.form.name.Value())
	command := strings.TrimSpace(p.form.command.Value())
	if name == "" || command == "" {
		return
	}
	s := model.NewSnippet()
	if existing != nil {
		s = *existing
	}
	s.Name = name
	s.Command = command
	if existing == nil {
		rt.Store.Snippets = append(rt.Store.Snippets, s)
	} else {
		for i := range rt.Store.Snippets {
			if rt.Store.Snippets[i].ID == s.ID {
				rt.Store.Snippets[i] = s
			}
		}
	}
	_ = rt.Save()
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

type settingRow struct {
	key   string
	label string
	value string
}

type settingsPanel struct {
	cursor int
}

func (p *settingsPanel) ensure(*app.Runtime) {}

func (p *settingsPanel) rows(m *Model) []settingRow {
	s := m.rt.Store.Settings
	return []settingRow{
		{"ui.language", m.tr(i18n.SettingsLanguage), i18n.Lang(s.UI.Language).NativeName()},
		{"ui.theme", m.tr(i18n.SettingsTheme), s.UI.Theme},
		{"notify.timeout", m.tr(i18n.SettingsNotificationTimeout), strconv.Itoa(s.Notifications.TimeoutSeconds) + "s"},
		{"notify.errortimeout", m.tr(i18n.SettingsErrorTimeout), strconv.Itoa(s.Notifications.ErrorTimeoutSeconds) + "s"},
		{"notify.sticky", m.tr(i18n.SettingsStickyErrors), boolText(s.Notifications.StickyErrors)},
		{"terminal.sudo", m.tr(i18n.TerminalSudoAutofill), boolText(s.Terminal.SudoAutofill)},
		{"general.confirmclose", m.tr(i18n.SettingsConfirmCloseTab), boolText(s.General.ConfirmCloseTab)},
		{"ui.showip", m.tr(i18n.SettingsShowIP), boolText(s.UI.ShowIP)},
		{"general.refresh", m.tr(i18n.SettingsRefreshInterval), strconv.Itoa(s.General.RefreshIntervalSeconds)},
		{"general.maxconcurrent", m.tr(i18n.SettingsMaxConcurrent), strconv.Itoa(s.General.MaxConcurrent)},
	}
}

// handle changes the focused setting and reports whether it was modified.
func (p *settingsPanel) handle(key tea.KeyMsg, m *Model) (changed bool, err error) {
	rows := p.rows(m)
	if p.cursor < 0 || p.cursor >= len(rows) {
		return false, nil
	}
	s := &m.rt.Store.Settings
	delta := 0
	switch key.String() {
	case "left":
		delta = -1
	case "right":
		delta = 1
	}
	switch rows[p.cursor].key {
	case "ui.language":
		if delta != 0 {
			langs := i18n.Languages()
			idx := indexOfLang(langs, i18n.Parse(s.UI.Language))
			s.UI.Language = string(langs[(idx+delta+len(langs))%len(langs)])
			m.rt.Tr.SetLang(s.UI.Language)
			return true, nil
		}
	case "ui.theme":
		if delta != 0 {
			themes := []string{"default", "dark", "light"}
			idx := indexOf(themes, s.UI.Theme)
			s.UI.Theme = themes[(idx+delta+len(themes))%len(themes)]
			return true, nil
		}
	case "notify.timeout":
		s.Notifications.TimeoutSeconds = clampInt(s.Notifications.TimeoutSeconds+delta*1, 0, 300)
		if delta != 0 {
			return true, nil
		}
	case "notify.errortimeout":
		s.Notifications.ErrorTimeoutSeconds = clampInt(s.Notifications.ErrorTimeoutSeconds+delta, 0, 600)
		if delta != 0 {
			return true, nil
		}
	case "notify.sticky":
		if isToggleKey(key) {
			s.Notifications.StickyErrors = !s.Notifications.StickyErrors
			return true, nil
		}
	case "terminal.sudo":
		if isToggleKey(key) {
			s.Terminal.SudoAutofill = !s.Terminal.SudoAutofill
			return true, nil
		}
	case "general.confirmclose":
		if isToggleKey(key) {
			s.General.ConfirmCloseTab = !s.General.ConfirmCloseTab
			return true, nil
		}
	case "ui.showip":
		if isToggleKey(key) {
			s.UI.ShowIP = !s.UI.ShowIP
			return true, nil
		}
	case "general.refresh":
		s.General.RefreshIntervalSeconds = clampInt(s.General.RefreshIntervalSeconds+delta*5, 5, 600)
		if delta != 0 {
			return true, nil
		}
	case "general.maxconcurrent":
		s.General.MaxConcurrent = clampInt(s.General.MaxConcurrent+delta, 1, 64)
		if delta != 0 {
			return true, nil
		}
	}
	_ = s
	return false, nil
}

// ---------------------------------------------------------------------------
// Forwards
// ---------------------------------------------------------------------------

type forwardsPanel struct {
	statuses []sshclient.TunnelStatus
	cursor   int
}

func (p *forwardsPanel) refresh(rt *app.Runtime) {
	p.statuses = rt.Forwards.Statuses()
}

// ---------------------------------------------------------------------------
// Key setup
// ---------------------------------------------------------------------------

// keySetupMsg reports a progress step.
type keySetupMsg struct {
	step    sshclient.KeySetupStep
	state   sshclient.KeySetupState
	message string
}

// keySetupDoneMsg reports the final outcome.
type keySetupDoneMsg struct {
	result sshclient.KeySetupResult
	err    error
}

type keySetupPanel struct {
	host            model.Host
	running         bool
	done            bool
	disablePassword bool
	states          map[sshclient.KeySetupStep]sshclient.KeySetupState
	messages        map[sshclient.KeySetupStep]string
	log             []string
	result          sshclient.KeySetupResult
	err             string
}

func (p *keySetupPanel) reset(host model.Host) {
	p.host = host
	p.running = false
	p.done = false
	p.states = map[sshclient.KeySetupStep]sshclient.KeySetupState{}
	p.messages = map[sshclient.KeySetupStep]string{}
	p.log = nil
	p.result = sshclient.KeySetupResult{}
	p.err = ""
}

func (p *keySetupPanel) startCmd(m *Model) tea.Cmd {
	rt := m.rt
	ctx := m.ctx
	host := p.host
	opts := sshclient.KeySetupOptions{
		DisablePassword: p.disablePassword,
		Progress: func(step sshclient.KeySetupStep, state sshclient.KeySetupState, message string) {
			select {
			case m.keyCh <- keySetupMsg{step: step, state: state, message: message}:
			case <-ctx.Done():
			}
		},
	}
	p.running = true
	p.done = false
	return func() tea.Msg {
		res, err := rt.Dialer.SetupKey(ctx, host, rt.Store.Hosts, rt.Resolver(), opts)
		return keySetupDoneMsg{result: res, err: err}
	}
}

func (p *keySetupPanel) onEvent(msg keySetupMsg) {
	p.states[msg.step] = msg.state
	if msg.message != "" {
		p.messages[msg.step] = msg.message
		p.log = append(p.log, msg.message)
	}
}

// ---------------------------------------------------------------------------
// SFTP
// ---------------------------------------------------------------------------

type sftpOpenedMsg struct {
	sftp   *sshclient.SFTP
	client *sshclient.Client
	host   model.Host
	err    error
}

type sftpListMsg struct {
	path  string
	files []sshclient.SFTPFile
	err   error
}

type sftpActionResultMsg struct {
	status string
	err    error
}

type sftpPanel struct {
	host      model.Host
	sftp      *sshclient.SFTP
	client    *sshclient.Client
	path      string
	files     []sshclient.SFTPFile
	cursor    int
	busy      bool
	err       string
	localDir  string
	inputMode string // "", "mkdir", "upload"
	input     textinput.Model
}

func newSFTPView(host model.Host) *sftpPanel {
	in := textinput.New()
	in.CharLimit = 512
	dir, _ := os.Getwd()
	return &sftpPanel{host: host, path: ".", input: in, localDir: dir}
}

func (p *sftpPanel) openCmd(m *Model) tea.Cmd {
	rt := m.rt
	ctx := m.ctx
	host := p.host
	return func() tea.Msg {
		client, ok := rt.Client(host.ID)
		if !ok {
			var err error
			client, err = rt.Connect(ctx, host)
			if err != nil {
				return sftpOpenedMsg{host: host, err: err}
			}
		}
		sftp, err := client.OpenSFTP()
		return sftpOpenedMsg{sftp: sftp, client: client, host: host, err: err}
	}
}

func (p *sftpPanel) listCmd() tea.Cmd {
	sftp := p.sftp
	dir := p.path
	return func() tea.Msg {
		files, err := sftp.List(dir)
		return sftpListMsg{path: dir, files: files, err: err}
	}
}

func (p *sftpPanel) close() {
	if p.sftp != nil {
		_ = p.sftp.Close() // disconnect the SFTP session, keeping SSH up
		p.sftp = nil
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func boolText(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func isToggleKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "enter", " ", "space":
		return true
	}
	return false
}

func indexOfLang(list []i18n.Lang, v i18n.Lang) int {
	for i, l := range list {
		if l == v {
			return i
		}
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var _ = context.Background
