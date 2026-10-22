package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
	"github.com/coddan/sextant/internal/termutil"
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.term != nil {
			m.term.resize(msg.Width-2, msg.Height-6)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case hostsChangedMsg:
		m.reloadHosts()
		m.applyFilter()
		return m, nil

	case metricsMsg:
		m.metrics[msg.hostID] = msg.m
		return m, nil

	case statusMsg:
		m.status = string(msg)
		m.errText = ""
		return m, nil

	case errMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = ""
		}
		return m, nil

	case promptMsg:
		m.prompt = newPromptState(msg.req)
		return m, nil

	case connectedMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.setStatus("")
			return m, nil
		}
		m.setStatus("%s %s", m.tr(i18n.NotifyConnected), msg.host.Name)
		m.term = newTerminalTab(m, msg.host)
		m.screen = screenTerminal
		return m, tea.Batch(m.term.startCmd(m.rt, m.ctx), m.waitPromptCmd())

	case termStartedMsg:
		if m.term != nil {
			m.term.shell = msg.shell
			return m, m.term.waitCmd()
		}
		return m, nil

	case termMsg:
		if m.term == nil {
			return m, nil
		}
		if len(msg.data) > 0 {
			m.term.write(msg.data)
			m.maybeSudo(msg.data)
		}
		if msg.err != nil {
			m.term.err = msg.err.Error()
		}
		if msg.closed {
			m.term.closed = true
			return m, nil
		}
		return m, m.term.waitCmd()

	case snippetLineMsg:
		m.snip.lines = append(m.snip.lines, msg.host+": "+msg.line)
		return m, m.snip.waitCmd()

	case snippetDoneMsg:
		if len(msg.results) > 0 {
			m.snip.results = msg.results
		}
		m.snip.running = false
		return m, m.snip.waitCmd()

	case keySetupMsg:
		m.keySetup.onEvent(msg)
		return m, m.waitKeyCmd()

	case keySetupDoneMsg:
		m.keySetup.running = false
		m.keySetup.done = true
		m.keySetup.result = msg.result
		if msg.err != nil {
			m.keySetup.err = msg.err.Error()
		}
		return m, nil

	case sftpOpenedMsg:
		m.sftp.busy = false
		if msg.err != nil {
			m.sftp.err = msg.err.Error()
			return m, nil
		}
		m.sftp.sftp = msg.sftp
		m.sftp.client = msg.client
		m.sftp.host = msg.host
		return m, m.sftp.listCmd()

	case sftpListMsg:
		m.sftp.busy = false
		if msg.err != nil {
			m.sftp.err = msg.err.Error()
			return m, nil
		}
		m.sftp.err = ""
		m.sftp.path = msg.path
		m.sftp.files = msg.files
		m.sftp.cursor = 0
		return m, nil

	case sftpActionResultMsg:
		m.sftp.busy = false
		if msg.err != nil {
			m.sftp.err = msg.err.Error()
			return m, nil
		}
		m.sftp.err = ""
		if msg.status != "" {
			m.setStatus("%s", msg.status)
		}
		return m, m.sftp.listCmd()
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if m.prompt != nil {
			return m.updatePrompt(key)
		}
		return m.updateKey(key)
	}
	return m, nil
}

func (m *Model) updatePrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	done, reply := m.prompt.handleKey(key)
	if !done {
		return m, nil
	}
	m.prompt.req.reply <- reply
	m.prompt = nil
	return m, m.waitPromptCmd()
}

func (m *Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.updateFilter(key)
	}
	switch m.screen {
	case screenDashboard:
		return m.updateDashboard(key)
	case screenHostForm:
		return m.updateHostForm(key)
	case screenTerminal:
		return m.updateTerminal(key)
	case screenSnippets:
		return m.updateSnippets(key)
	case screenSettings:
		return m.updateSettings(key)
	case screenForwards:
		return m.updateForwards(key)
	case screenKeySetup:
		return m.updateKeySetup(key)
	case screenSFTP:
		return m.updateSFTP(key)
	case screenHelp, screenAbout:
		switch key.String() {
		case "esc", "q", "enter", "?":
			m.screen = m.prevScreen
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) updateFilter(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.filter = ""
		m.applyFilter()
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.applyFilter()
	case tea.KeyRunes, tea.KeySpace:
		m.filter += key.String()
		m.applyFilter()
	}
	return m, nil
}

func (m *Model) updateDashboard(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		m.quit = true
		m.cancel()
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = maxInt(0, len(m.visible)-1)
	case "/":
		m.filtering = true
	case "enter":
		if h, ok := m.selectedHost(); ok {
			m.setStatus("%s %s…", m.tr(i18n.TerminalStatusConnecting), h.Name)
			return m, tea.Batch(m.connectCmd(h), m.waitPromptCmd())
		}
	case "a":
		m.form = newHostForm(m.rt.Tr, m.tr(i18n.HostNew), model.NewHost("", ""))
		m.form.setGroups(m.groupNames())
		m.screen = screenHostForm
	case "e":
		if h, ok := m.selectedHost(); ok {
			m.form = newHostForm(m.rt.Tr, m.tr(i18n.HostEdit), h)
			m.form.setGroups(m.groupNames())
			m.screen = screenHostForm
		}
	case "d":
		if h, ok := m.selectedHost(); ok {
			m.rt.Store.DeleteHost(h.ID)
			_ = m.rt.Save()
			m.reloadHosts()
			m.applyFilter()
			m.setStatus("%s %s", m.tr(i18n.NotifyDeleted), h.Name)
		}
	case "o":
		m.onlyOnline = !m.onlyOnline
		m.applyFilter()
		if m.onlyOnline {
			// Sampling populates the online set used by the filter.
			m.loading = true
			return m, m.collectAllCmd()
		}
	case "m":
		if h, ok := m.selectedHost(); ok {
			m.loading = true
			return m, m.metricsCmd(h)
		}
	case "r":
		m.loading = true
		return m, m.collectAllCmd()
	case "s":
		m.screen = screenSnippets
		m.snip.ensure(m.rt)
	case "f":
		if h, ok := m.selectedHost(); ok {
			m.sftp = newSFTPView(h)
			m.sftp.busy = true
			m.screen = screenSFTP
			return m, tea.Batch(m.sftp.openCmd(m), m.waitPromptCmd())
		}
	case "F":
		m.forwards.refresh(m.rt)
		m.screen = screenForwards
	case "K":
		if h, ok := m.selectedHost(); ok {
			m.keySetup.reset(h)
			m.screen = screenKeySetup
		}
	case ",":
		m.settings.ensure(m.rt)
		m.screen = screenSettings
	case "?":
		m.prevScreen = m.screen
		m.screen = screenHelp
	}
	return m, nil
}

func (m *Model) groupNames() []string {
	var names []string
	for _, g := range m.rt.Store.AllGroups() {
		names = append(names, g.Name)
	}
	return names
}

func (m *Model) updateHostForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = screenDashboard
		return m, nil
	case "tab", "down":
		m.form.moveFocus(1)
		return m, nil
	case "shift+tab", "up":
		m.form.moveFocus(-1)
		return m, nil
	case "ctrl+s":
		return m.saveHostForm()
	case "left":
		m.form.cycle(-1)
		return m, nil
	case "right":
		m.form.cycle(1)
		return m, nil
	case " ":
		if fl := m.form.focused(); fl != nil && (fl.kind == formToggle || fl.kind == formChoice) {
			m.form.cycle(1)
			return m, nil
		}
	case "ctrl+d":
		if fl := m.form.focused(); fl != nil && (fl.kind == formToggle || fl.kind == formChoice) {
			m.form.cycle(1)
			return m, nil
		}
	}
	return m, m.form.updateInput(key)
}

func (m *Model) saveHostForm() (tea.Model, tea.Cmd) {
	if err := m.form.save(m.rt); err != nil {
		m.form.err = err.Error()
		return m, nil
	}
	m.form.err = ""
	m.reloadHosts()
	m.applyFilter()
	m.screen = screenDashboard
	m.setStatus("%s", m.tr(i18n.NotifySaved))
	return m, nil
}

func (m *Model) updateTerminal(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.term.findActive {
		switch key.String() {
		case "f10":
			// fall through to close
		case "ctrl+q":
			m.quit = true
			m.cancel()
			return m, tea.Quit
		default:
			m.term.findKey(key)
			return m, nil
		}
	}
	switch key.String() {
	case "f10":
		m.term.close()
		m.term = nil
		m.screen = screenDashboard
		return m, nil
	case "ctrl+f":
		m.term.beginFind()
		return m, nil
	case "ctrl+q":
		m.quit = true
		m.cancel()
		return m, tea.Quit
	}
	return m, m.term.sendKey(key)
}

func (m *Model) updateSnippets(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.snip.editing {
		switch key.String() {
		case "esc":
			m.snip.editing = false
			return m, nil
		case "tab":
			m.snip.form.focus = (m.snip.form.focus + 1) % 2
			if m.snip.form.focus == 0 {
				m.snip.form.name.Focus()
				m.snip.form.command.Blur()
			} else {
				m.snip.form.command.Focus()
				m.snip.form.name.Blur()
			}
			return m, nil
		case "enter":
			m.snip.saveSnippet(m.rt, nil)
			m.snip.editing = false
			return m, nil
		}
		var cmd tea.Cmd
		if m.snip.form.focus == 0 {
			m.snip.form.name, cmd = m.snip.form.name.Update(key)
		} else {
			m.snip.form.command, cmd = m.snip.form.command.Update(key)
		}
		return m, cmd
	}

	list := m.snip.snippets(m.rt)
	switch key.String() {
	case "esc", "q":
		m.screen = screenDashboard
	case "up", "k":
		if m.snip.cursor > 0 {
			m.snip.cursor--
		}
	case "down", "j":
		if m.snip.cursor < len(list)-1 {
			m.snip.cursor++
		}
	case "a":
		m.snip.editing = true
		m.snip.form.name.SetValue("")
		m.snip.form.command.SetValue("")
		m.snip.form.focus = 0
		m.snip.form.name.Focus()
	case "d":
		if m.snip.cursor < len(list) {
			id := list[m.snip.cursor].ID
			var kept []model.Snippet
			for _, s := range m.rt.Store.Snippets {
				if s.ID != id {
					kept = append(kept, s)
				}
			}
			m.rt.Store.Snippets = kept
			_ = m.rt.Save()
		}
	case "t":
		m.snip.stream = !m.snip.stream
	case "enter", "r":
		if m.snip.cursor < len(list) && !m.snip.running {
			return m, tea.Batch(m.snip.runCmd(m, list[m.snip.cursor]), m.snip.waitCmd())
		}
	}
	return m, nil
}

func (m *Model) updateSettings(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		m.screen = screenDashboard
		return m, nil
	case "up", "k":
		if m.settings.cursor > 0 {
			m.settings.cursor--
		}
		return m, nil
	case "down", "j":
		if m.settings.cursor < len(m.settings.rows(m))-1 {
			m.settings.cursor++
		}
		return m, nil
	}
	changed, err := m.settings.handle(key, m)
	if err != nil {
		return m, m.setError(err)
	}
	if changed {
		_ = m.rt.Save()
		m.setStatus("%s", m.tr(i18n.SettingsSavedNotification))
	}
	return m, nil
}

func (m *Model) setError(err error) tea.Cmd {
	if err != nil {
		m.errText = err.Error()
	}
	return nil
}

func (m *Model) updateForwards(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		m.screen = screenDashboard
	case "r":
		m.forwards.refresh(m.rt)
	}
	return m, nil
}

func (m *Model) updateKeySetup(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = screenDashboard
	case " ":
		m.keySetup.disablePassword = !m.keySetup.disablePassword
	case "enter":
		if !m.keySetup.running && !m.keySetup.done {
			return m, tea.Batch(m.keySetup.startCmd(m), m.waitKeyCmd())
		}
	}
	return m, nil
}

func (m *Model) updateSFTP(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sftp.inputMode != "" {
		switch key.String() {
		case "esc":
			m.sftp.inputMode = ""
			m.sftp.input.Blur()
			return m, nil
		case "enter":
			mode := m.sftp.inputMode
			value := strings.TrimSpace(m.sftp.input.Value())
			m.sftp.inputMode = ""
			m.sftp.input.SetValue("")
			if value == "" {
				return m, nil
			}
			m.sftp.busy = true
			return m, m.sftp.opCmd(m, mode, value)
		}
		var cmd tea.Cmd
		m.sftp.input, cmd = m.sftp.input.Update(key)
		return m, cmd
	}

	switch key.String() {
	case "esc", "q":
		m.sftp.close()
		m.screen = screenDashboard
	case "up", "k":
		if m.sftp.cursor > 0 {
			m.sftp.cursor--
		}
	case "down", "j":
		if m.sftp.cursor < len(m.sftp.files)-1 {
			m.sftp.cursor++
		}
	case "backspace":
		parent := path.Dir(m.sftp.path)
		if parent == "" {
			parent = "."
		}
		m.sftp.busy = true
		m.sftp.path = parent
		return m, m.sftp.listCmd()
	case "enter":
		if f, ok := m.sftp.selected(); ok {
			if f.IsDir {
				m.sftp.busy = true
				m.sftp.path = f.Path
				return m, m.sftp.listCmd()
			}
			m.sftp.busy = true
			return m, m.sftp.opCmd(m, "download", f.Path)
		}
	case "n":
		m.sftp.inputMode = "mkdir"
		m.sftp.input.Placeholder = m.tr(i18n.SFTPNewFolder)
		m.sftp.input.Focus()
	case "u":
		m.sftp.inputMode = "upload"
		m.sftp.input.Placeholder = m.tr(i18n.SFTPUploadFile)
		m.sftp.input.Focus()
	case "e":
		if f, ok := m.sftp.selected(); ok && !f.IsDir {
			m.sftp.busy = true
			return m, m.sftp.prepareEditCmd(f)
		}
	case "d":
		if f, ok := m.sftp.selected(); ok {
			m.sftp.busy = true
			return m, m.sftp.opCmd(m, "delete", f.Path)
		}
	case "r":
		m.sftp.busy = true
		return m, m.sftp.listCmd()
	}
	return m, nil
}

func (p *sftpPanel) selected() (sshclient.SFTPFile, bool) {
	if p.cursor < 0 || p.cursor >= len(p.files) {
		return sshclient.SFTPFile{}, false
	}
	return p.files[p.cursor], true
}

// opCmd performs a mkdir, upload, download or delete.
func (p *sftpPanel) opCmd(m *Model, mode, value string) tea.Cmd {
	sftp := p.sftp
	dir := p.path
	localDir := p.localDir
	report := func() string { return m.tr(i18n.SFTPTransferDone, value) }
	return func() tea.Msg {
		if sftp == nil {
			return sftpActionResultMsg{err: errNotConnected}
		}
		switch mode {
		case "mkdir":
			return sftpActionResultMsg{err: sftp.Mkdir(path.Join(dir, value))}
		case "upload":
			return sftpActionResultMsg{status: report(), err: sftp.Upload(m.ctx, value, path.Join(dir, path.Base(value)), nil)}
		case "download":
			local := localFilePath(localDir, value)
			return sftpActionResultMsg{status: report(), err: sftp.Download(m.ctx, value, local, nil)}
		case "delete":
			return sftpActionResultMsg{status: report(), err: sftp.Remove(value)}
		}
		return sftpActionResultMsg{}
	}
}

func localFilePath(dir, remote string) string {
	base := path.Base(remote)
	if base == "." || base == "/" {
		base = "download"
	}
	if dir == "" {
		return base
	}
	return dir + "/" + base
}

// Commands.

func (m *Model) connectCmd(h model.Host) tea.Cmd {
	rt := m.rt
	ctx := m.ctx
	host := h
	return func() tea.Msg {
		client, err := rt.Connect(ctx, host)
		if err != nil {
			return connectedMsg{host: host, err: friendlyError(rt, err)}
		}
		return connectedMsg{host: host, client: client}
	}
}

func (m *Model) metricsCmd(h model.Host) tea.Cmd {
	rt := m.rt
	host := h
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		return metricsMsg{hostID: host.ID, m: rt.Metrics(ctx, host)}
	}
}

// collectAllCmd samples every host in parallel.
func (m *Model) collectAllCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, h := range m.hosts {
		cmds = append(cmds, m.metricsCmd(h))
	}
	if len(cmds) == 0 {
		m.loading = false
		return nil
	}
	return tea.Batch(cmds...)
}

// errorString is a plain error built from user-facing text.
type errorString string

func (e errorString) Error() string { return string(e) }

func friendlyError(rt *app.Runtime, err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "unable to authenticate"),
		strings.Contains(text, "no supported methods"),
		strings.Contains(text, "handshake failed"):
		return errorString(rt.Tr.T(i18n.ErrorAuthFailed))
	case strings.Contains(text, "i/o timeout"), strings.Contains(text, "timed out"):
		return errorString(rt.Tr.T(i18n.ErrorTimeout, ""))
	case strings.Contains(text, "host key"):
		return errorString(rt.Tr.T(i18n.ErrorHostKeyChanged, ""))
	default:
		return err
	}
}

// sftpEditReadyMsg carries a downloaded file ready for the external editor.
type sftpEditReadyMsg struct {
	file     sshclient.SFTPFile
	local    string
	original []byte
	err      error
}

// prepareEditCmd downloads a remote file so it can be edited locally.
func (p *sftpPanel) prepareEditCmd(f sshclient.SFTPFile) tea.Cmd {
	sftp := p.sftp
	return func() tea.Msg {
		if sftp == nil {
			return sftpEditReadyMsg{err: errNotConnected}
		}
		data, err := sftp.ReadFile(f.Path, 8<<20)
		if err != nil {
			return sftpEditReadyMsg{err: err}
		}
		dir, err := os.MkdirTemp("", "sextant-edit-")
		if err != nil {
			return sftpEditReadyMsg{err: err}
		}
		local := filepath.Join(dir, path.Base(f.Path))
		if err := os.WriteFile(local, data, 0o600); err != nil {
			return sftpEditReadyMsg{err: err}
		}
		return sftpEditReadyMsg{file: f, local: local, original: data}
	}
}

// startEditorCmd suspends the TUI, runs $EDITOR and uploads any changes.
func (m *Model) startEditorCmd(msg sftpEditReadyMsg) tea.Cmd {
	fields := strings.Fields(os.Getenv("EDITOR"))
	if len(fields) == 0 {
		fields = []string{"vi"}
	}
	args := append(append([]string(nil), fields[1:]...), msg.local)
	cmd := exec.Command(fields[0], args...)
	sftp := m.sftp.sftp
	file := msg.file
	local := msg.local
	original := msg.original
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.RemoveAll(filepath.Dir(local))
		if err != nil {
			return sftpActionResultMsg{err: err}
		}
		after, err := os.ReadFile(local)
		if err != nil {
			return sftpActionResultMsg{err: err}
		}
		if bytes.Equal(after, original) {
			return sftpActionResultMsg{status: m.tr(i18n.CommonNo)}
		}
		if sftp == nil {
			return sftpActionResultMsg{err: errNotConnected}
		}
		if err := sftp.WriteFile(file.Path, after, file.Mode); err != nil {
			return sftpActionResultMsg{err: err}
		}
		return sftpActionResultMsg{status: m.tr(i18n.NotifySaved)}
	})
}

// maybeSudo fills sudo's password prompt from the host's saved password when
// the user opted in. It is throttled so a redraw cannot resend it.
func (m *Model) maybeSudo(data []byte) {
	if m.term == nil || m.term.shell == nil || m.term.sudo == nil {
		return
	}
	if !m.rt.Store.Settings.Terminal.SudoAutofill {
		return
	}
	if !termutil.SudoPrompt(data) || !m.term.sudo.Allow() {
		return
	}
	pw, ok := m.rt.Store.Password(m.term.host)
	if !ok || pw == "" {
		return
	}
	shell := m.term.shell
	go func() { _, _ = shell.Write([]byte(pw + "\n")) }()
}
