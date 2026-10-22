package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// View implements tea.Model.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return m.tr(i18n.CommonLoading)
	}
	var body string
	switch m.screen {
	case screenDashboard:
		body = m.dashboardView()
	case screenHostForm:
		body = m.hostFormView()
	case screenTerminal:
		body = m.terminalView()
	case screenSFTP:
		body = m.sftpView()
	case screenSnippets:
		body = m.snippetsView()
	case screenSettings:
		body = m.settingsView()
	case screenForwards:
		body = m.forwardsView()
	case screenKeySetup:
		body = m.keySetupView()
	case screenHelp:
		body = m.helpView()
	case screenAbout:
		body = m.aboutView()
	}
	body = lipgloss.NewStyle().MaxHeight(m.bodyHeight()).Render(body)
	out := lipgloss.JoinVertical(lipgloss.Left, m.header(), body, m.statusBar())
	if m.prompt != nil {
		out = m.overlay(out, m.prompt.view(m, m.width))
	}
	return out
}

func (m *Model) bodyHeight() int { return maxInt(4, m.height-3) }

func (m *Model) screenTitle() string {
	switch m.screen {
	case screenDashboard:
		return m.tr(i18n.NavDashboard)
	case screenHostForm:
		return m.tr(i18n.HostTitle)
	case screenTerminal:
		return m.tr(i18n.NavTerminal)
	case screenSFTP:
		return m.tr(i18n.SFTPTitle)
	case screenSnippets:
		return m.tr(i18n.SnippetsTitle)
	case screenSettings:
		return m.tr(i18n.SettingsTitle)
	case screenForwards:
		return m.tr(i18n.ForwardsTitle)
	case screenKeySetup:
		return m.tr(i18n.KeySetupTitle)
	case screenHelp:
		return m.tr(i18n.NavAbout)
	case screenAbout:
		return m.tr(i18n.NavAbout)
	}
	return ""
}

func (m *Model) header() string {
	left := titleStyle.Render(" " + m.tr(i18n.AppName) + " ")
	sep := mutedStyle.Render("  ›  ")
	crumb := valueStyle.Render(m.screenTitle())
	right := ""
	if m.loading {
		right = m.spinner.View()
	}
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(sep) - lipgloss.Width(crumb) - lipgloss.Width(right) - 2
	if pad < 0 {
		pad = 0
	}
	return left + sep + crumb + strings.Repeat(" ", pad) + right + " "
}

func (m *Model) statusBar() string {
	var left string
	switch {
	case m.errText != "":
		left = errorStyle.Render(m.errText)
	case m.status != "":
		left = successStyle.Render(sanitizeLine(m.status))
	default:
		left = helpStyle.Render(m.hints())
	}
	right := mutedStyle.Render(fmt.Sprintf("%d×%d", m.width, m.height))
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 0 {
		pad = 0
	}
	return statusBarStyle.Render(left + strings.Repeat(" ", pad) + right)
}

func (m *Model) hints() string {
	switch m.screen {
	case screenDashboard:
		return "↑↓ move  ⏎ connect  a add  e edit  d delete  / filter  o online-only  s snippets  f sftp  F tunnels  K keys  , settings  ? help  q quit"
	case screenHostForm:
		return "tab/↑↓ field  ←→ change  space toggle  ctrl+s save  esc cancel"
	case screenTerminal:
		return "ctrl+f find  pgup/pgdn scroll  f10 close  ctrl+q quit"
	case screenSFTP:
		return "↑↓ move  ⏎ open  ⌫ parent  u upload  n mkdir  d delete  r refresh  esc close"
	case screenSnippets:
		return "↑↓ move  ⏎ run  a add  d delete  t stream  esc back"
	case screenSettings:
		return "↑↓ move  ←→ change  space toggle  esc back"
	case screenForwards:
		return "r refresh  esc back"
	case screenKeySetup:
		return "space toggle disable-password  ⏎ start  esc back"
	}
	return "esc back"
}

// overlay centres a modal over the rendered background.
func (m *Model) overlay(bg, modal string) string {
	bgLines := strings.Split(bg, "\n")
	modalLines := strings.Split(modal, "\n")
	top := maxInt(0, (len(bgLines)-len(modalLines))/2)
	leftPad := maxInt(0, (m.width-lipgloss.Width(modal))/2)
	for i, line := range modalLines {
		idx := top + i
		if idx >= len(bgLines) {
			break
		}
		// Keep the background after the modal so the layout does not collapse.
		orig := stripANSI(bgLines[idx])
		tail := ""
		if len(orig) > leftPad+lipgloss.Width(line) {
			tail = orig[leftPad+lipgloss.Width(line):]
		}
		bgLines[idx] = strings.Repeat(" ", leftPad) + line + tail
	}
	return strings.Join(bgLines, "\n")
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

func (m *Model) dashboardView() string {
	var b strings.Builder

	if m.filtering || m.filter != "" {
		cursor := ""
		if m.filtering {
			cursor = "▌"
		}
		b.WriteString(labelStyle.Render(m.tr(i18n.CommonFilter)+": ") + m.filter + cursor + "\n")
	}

	header := joinCols(
		col(m.tr(i18n.CommonName), 18),
		col(m.tr(i18n.HostHostname), 24),
		col(m.tr(i18n.HostUser), 10),
		col(m.tr(i18n.HostGroup), 12),
		col(m.tr(i18n.CommonStatus), 9),
		col(m.tr(i18n.MetricsCPU), 6),
		col(m.tr(i18n.MetricsRAM), 6),
		col(m.tr(i18n.MetricsDisk), 6),
	)
	b.WriteString(labelStyle.Render(header) + "\n")

	if len(m.visible) == 0 {
		msg := m.tr(i18n.DashboardEmpty)
		if m.filter != "" {
			msg = m.tr(i18n.CommonEmpty)
		}
		b.WriteString("\n  " + mutedStyle.Render(msg) + "\n")
		return b.String()
	}

	maxRows := m.bodyHeight() - 2
	start := 0
	if m.cursor >= maxRows {
		start = m.cursor - maxRows + 1
	}
	for i := start; i < len(m.visible) && i < start+maxRows; i++ {
		h := m.visible[i]
		met, hasMetrics := m.metrics[h.ID]
		status := mutedStyle.Render(m.tr(i18n.CommonUnknown))
		if hasMetrics && met.Error == "" {
			status = successStyle.Render(m.tr(i18n.DashboardOnline))
		} else if hasMetrics && met.Error != "" {
			status = errorStyle.Render(m.tr(i18n.DashboardOffline))
		}
		cpu, ram, disk := "-", "-", "-"
		if hasMetrics {
			cpu = pct(met.CPUPercent)
			ram = pct(met.RAMPercent)
			disk = pct(met.DiskPercent)
		}
		line := joinCols(
			col(h.Name, 18),
			col(h.Hostname, 24),
			col(orDefault(h.User, "-"), 10),
			col(orDefault(h.Group, "-"), 12),
			colPlain(lipgloss.NewStyle().Render(status), 9),
			col(cpu, 6),
			col(ram, 6),
			col(disk, 6),
		)
		if i == m.cursor {
			b.WriteString(selectedStyle.Render(padRight(" "+line, m.width-1)) + "\n")
		} else {
			b.WriteString(" " + line + "\n")
		}
	}
	return b.String()
}

func pct(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *v)
}

// ---------------------------------------------------------------------------
// Host form
// ---------------------------------------------------------------------------

func (m *Model) hostFormView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.form.title+" ") + "\n\n")
	for i := range m.form.fields {
		f := &m.form.fields[i]
		marker := "  "
		if i == m.form.focus {
			marker = "▸ "
		}
		label := padRight(f.label, 18)
		var value string
		switch f.kind {
		case formToggle:
			value = "[ ] " + m.tr(i18n.CommonOff)
			if f.toggled {
				value = "[x] " + m.tr(i18n.CommonOn)
			}
		case formChoice:
			cur := ""
			if len(f.choices) > 0 {
				cur = f.choices[f.choice]
				if cur == "" {
					cur = m.tr(i18n.CommonNone)
				}
			}
			value = "‹ " + cur + " ›"
		default:
			value = f.input.View()
		}
		if i == m.form.focus {
			b.WriteString(marker + selectedStyle.Render(label) + value + "\n")
		} else {
			b.WriteString(marker + labelStyle.Render(label) + value + "\n")
		}
	}
	if m.form.err != "" {
		b.WriteString("\n" + errorStyle.Render(m.form.err) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("ctrl+s "+m.tr(i18n.CommonSave)+"   esc "+m.tr(i18n.CommonCancel)))
	return b.String()
}

// ---------------------------------------------------------------------------
// Terminal
// ---------------------------------------------------------------------------

func (m *Model) terminalView() string {
	t := m.term
	if t == nil {
		return mutedStyle.Render(m.tr(i18n.TerminalStatusDisconnected))
	}
	var b strings.Builder
	title := t.host.Name
	if t.vt.Title() != "" {
		title = t.vt.Title()
	}
	state := successStyle.Render(m.tr(i18n.TerminalStatusConnected))
	if t.closed {
		state = errorStyle.Render(m.tr(i18n.TerminalStatusDisconnected))
	}
	b.WriteString(truncate(title, maxInt(10, m.width-30)) + "  " + state)
	if t.offset > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑%d", t.offset)))
	}
	b.WriteString("\n")
	b.WriteString(t.render(m.width-2, m.bodyHeight()-2))
	if t.findActive {
		info := ""
		if len(t.findMatches) > 0 {
			info = fmt.Sprintf(" (%d/%d)", t.findIndex+1, len(t.findMatches))
		} else if t.findErr != "" {
			info = " " + t.findErr
		}
		b.WriteString("\n" + labelStyle.Render(m.tr(i18n.TerminalFind)+": ") + t.findInput.View() + info)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// SFTP
// ---------------------------------------------------------------------------

func (m *Model) sftpView() string {
	p := m.sftp
	var b strings.Builder
	b.WriteString(labelStyle.Render(m.tr(i18n.SFTPRemotePath)+": ") + p.path)
	if p.busy {
		b.WriteString(" " + m.spinner.View())
	}
	b.WriteString("\n")
	b.WriteString(labelStyle.Render(joinCols(
		col("", 2),
		col(m.tr(i18n.SFTPName), 40),
		col(m.tr(i18n.CommonSize), 12),
		col(m.tr(i18n.SFTPPermissions), 12),
		col(m.tr(i18n.SFTPModified), 20),
	)) + "\n")
	if len(p.files) == 0 {
		b.WriteString("  " + mutedStyle.Render(m.tr(i18n.SFTPEmptyDirectory)) + "\n")
	}
	maxRows := m.bodyHeight() - 4
	start := 0
	if p.cursor >= maxRows && maxRows > 0 {
		start = p.cursor - maxRows + 1
	}
	for i := start; i < len(p.files) && i < start+maxRows; i++ {
		f := p.files[i]
		icon := "📄"
		if f.IsDir {
			icon = "📁"
		} else if f.IsLink {
			icon = "🔗"
		}
		line := joinCols(
			col(icon, 2),
			col(f.Name, 40),
			col(sizeText(f.Size, f.IsDir), 12),
			col(f.Mode.String(), 12),
			col(f.ModTime.Format("2006-01-02 15:04"), 20),
		)
		if i == p.cursor {
			b.WriteString(selectedStyle.Render(padRight(line, m.width-1)) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	if p.inputMode != "" {
		b.WriteString("\n" + labelStyle.Render(p.inputMode+": ") + p.input.View())
	}
	if p.err != "" {
		b.WriteString("\n" + errorStyle.Render(p.err))
	}
	return b.String()
}

func sizeText(n int64, isDir bool) string {
	if isDir {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------------------
// Snippets
// ---------------------------------------------------------------------------

func (m *Model) snippetsView() string {
	p := m.snip
	list := p.snippets(m.rt)
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.SnippetsTitle)+" ") + "\n")
	b.WriteString(labelStyle.Render(fmt.Sprintf("%s: %s", m.tr(i18n.SnippetsStreaming), boolText(p.stream))) + "\n\n")
	if len(list) == 0 {
		b.WriteString(mutedStyle.Render(m.tr(i18n.SnippetsEmpty)) + "\n")
	}
	for i, s := range list {
		line := truncate(s.Name, 30) + "  " + mutedStyle.Render(truncate(s.Command, maxInt(10, m.width-40)))
		if i == p.cursor {
			b.WriteString(selectedStyle.Render(padRight(line, m.width-1)) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}

	if p.running {
		b.WriteString("\n" + m.spinner.View() + " " + m.tr(i18n.KeySetupRunning) + "\n")
	}
	if len(p.lines) > 0 {
		b.WriteString("\n" + labelStyle.Render(m.tr(i18n.SnippetsOutput)+":") + "\n")
		max := m.bodyHeight() - len(list) - 8
		lines := p.lines
		if len(lines) > max && max > 0 {
			lines = lines[len(lines)-max:]
		}
		for _, line := range lines {
			b.WriteString("  " + sanitizeLine(line) + "\n")
		}
	}
	if len(p.results) > 0 {
		b.WriteString("\n")
		for _, r := range p.results {
			style := successStyle
			status := fmt.Sprintf("exit %d", r.ExitCode)
			if r.Err != "" || r.ExitCode != 0 {
				style = errorStyle
			}
			b.WriteString(style.Render(fmt.Sprintf("%-20s %-10s %s", r.HostName, status, r.Duration.Round(time.Millisecond))) + "\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

func (m *Model) settingsView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.SettingsTitle)+" ") + "\n\n")
	for i, row := range m.settings.rows(m) {
		label := padRight(row.label, 32)
		line := labelStyle.Render(label) + valueStyle.Render(row.value)
		if i == m.settings.cursor {
			b.WriteString(selectedStyle.Render(padRight(" "+line, m.width-1)) + "\n")
		} else {
			b.WriteString(" " + line + "\n")
		}
	}
	b.WriteString("\n" + mutedStyle.Render(m.tr(i18n.SettingsConfigDirectory)+": "+m.rt.Store.Paths.Dir))
	return b.String()
}

// ---------------------------------------------------------------------------
// Forwards
// ---------------------------------------------------------------------------

func (m *Model) forwardsView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.ForwardsTitle)+" ") + "\n\n")
	if len(m.forwards.statuses) == 0 {
		b.WriteString(mutedStyle.Render(m.tr(i18n.CommonEmpty)) + "\n")
		return b.String()
	}
	b.WriteString(labelStyle.Render(joinCols(
		col(m.tr(i18n.CommonName), 18),
		col(m.tr(i18n.ForwardsKindLocal), 10),
		col(m.tr(i18n.ForwardsBindAddress), 28),
		col(m.tr(i18n.CommonStatus), 12),
	)) + "\n")
	for _, st := range m.forwards.statuses {
		name := st.HostID
		if h, ok := m.rt.Store.Host(st.HostID); ok {
			name = h.Name
		}
		state := successStyle.Render(string(st.State))
		if st.State != sshclient.ForwardActive {
			state = warnStyle.Render(string(st.State))
		}
		b.WriteString(joinCols(
			col(name, 18),
			col(st.Kind, 10),
			col(st.Spec, 28),
			colPlain(state, 12),
		) + "\n")
		if st.Err != "" {
			b.WriteString("  " + errorStyle.Render(st.Err) + "\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Key setup
// ---------------------------------------------------------------------------

func (m *Model) keySetupView() string {
	p := m.keySetup
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.KeySetupTitle)+" ") + " " + p.host.Name + "\n")
	b.WriteString(mutedStyle.Render(m.tr(i18n.KeySetupIntro)) + "\n\n")
	b.WriteString(labelStyle.Render(m.tr(i18n.KeySetupDisablePassword)+": ") + boolText(p.disablePassword) + "\n")
	if p.disablePassword {
		b.WriteString(warnStyle.Render("  "+m.tr(i18n.KeySetupWarning)) + "\n")
	}
	b.WriteString("\n")
	for _, step := range sshclient.AllKeySetupSteps() {
		icon := mutedStyle.Render("○")
		switch p.states[step] {
		case sshclient.StateSuccess:
			icon = successStyle.Render("✓")
		case sshclient.StateFailedSafe, sshclient.StateNeedsRollback, sshclient.StatePartialSuccess:
			icon = errorStyle.Render("✗")
		case sshclient.StateInProgress:
			icon = m.spinner.View()
		}
		line := icon + " " + step.Description()
		if msg := p.messages[step]; msg != "" {
			line += mutedStyle.Render("  " + truncate(msg, maxInt(10, m.width-len(line)-6)))
		}
		b.WriteString(line + "\n")
	}
	if p.done {
		b.WriteString("\n")
		if p.err != "" {
			b.WriteString(errorStyle.Render(m.tr(i18n.KeySetupFailed)+": "+p.err) + "\n")
		} else {
			b.WriteString(successStyle.Render(m.tr(i18n.KeySetupDone)) + "\n")
			if p.result.PrivateKeyPath != "" {
				b.WriteString(mutedStyle.Render("key: "+p.result.PrivateKeyPath) + "\n")
			}
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Help and about
// ---------------------------------------------------------------------------

func (m *Model) helpView() string {
	sections := []struct{ title, body string }{
		{m.tr(i18n.NavDashboard), "↑/k ↓/j move · g/G top/bottom · ⏎ connect · a add · e edit · d delete\n/ filter · m sample · r refresh all · s snippets · f SFTP · F tunnels · K keys · , settings · o online-only"},
		{m.tr(i18n.NavTerminal), "type to send · ctrl+f find · pgup/pgdn scrollback · f10 close · ctrl+q quit"},
		{m.tr(i18n.NavSFTP), "↑↓ move · ⏎ open/download · ⌫ parent · u upload · n mkdir · e edit · d delete · r refresh · esc close"},
		{m.tr(i18n.SnippetsTitle), "↑↓ move · ⏎/r run on all hosts · a add · d delete · t toggle streaming"},
		{m.tr(i18n.NavSettings), "↑↓ move · ←→ change · space toggle · esc back"},
		{"Global", "? help · q quit"},
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.NavAbout)+" ") + "\n\n")
	for _, s := range sections {
		b.WriteString(labelStyle.Render(s.title) + "\n")
		for _, line := range strings.Split(s.body, "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) aboutView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.tr(i18n.AppName)+" ") + "\n\n")
	b.WriteString(m.tr(i18n.AppTagline) + "\n\n")
	b.WriteString(labelStyle.Render(m.tr(i18n.AboutWebsite)+": ") + "https://github.com/coddan/sextant\n")
	b.WriteString(labelStyle.Render(m.tr(i18n.AboutLicense)+": ") + "MIT\n")
	b.WriteString(labelStyle.Render(m.tr(i18n.SettingsLanguage)+": ") + m.rt.Tr.Lang().NativeName() + "\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Small text helpers
// ---------------------------------------------------------------------------

func col(s string, width int) string { return padRight(truncate(s, width), width) }

// colPlain pads text that already contains ANSI escapes.
func colPlain(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func joinCols(cols ...string) string {
	var b strings.Builder
	for i, c := range cols {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(c)
	}
	return b.String()
}

func padRight(s string, width int) string {
	w := []rune(s)
	if len(w) >= width {
		return string(w[:width])
	}
	return s + strings.Repeat(" ", width-len(w))
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return string(r[:1])
	}
	return string(r[:width-1]) + "…"
}

// sanitizeLine removes control characters so one hostile line cannot break the
// layout.
func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// stripANSI removes SGR sequences for overlay alignment.
func stripANSI(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case skip:
			// skip other escape bytes
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var _ = path.Base
var _ = model.MonitorSSH
