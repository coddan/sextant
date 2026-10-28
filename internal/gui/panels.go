package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// ---------------------------------------------------------------------------
// Snippets
// ---------------------------------------------------------------------------

type snippetView struct {
	app     *App
	sel     int
	listBox *fyne.Container
	output  *widget.Label
	stream  *widget.Check
	runBtn  *widget.Button
	stopBtn *widget.Button
	running bool
	cancel  context.CancelFunc
	lines   []string
}

func (g *App) buildSnippetsTab() fyne.CanvasObject {
	g.snip = &snippetView{app: g, sel: -1}
	g.snip.listBox = container.NewVBox()
	g.snip.output = widget.NewLabel("")
	g.snip.output.Wrapping = fyne.TextWrapOff
	g.snip.output.TextStyle = fyne.TextStyle{Monospace: true}

	newBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SnippetsNew), theme.ContentAddIcon(), func() { g.snip.newSnippet() })
	g.snip.runBtn = widget.NewButtonWithIcon(g.tr.T(i18n.SnippetsRun), theme.MediaPlayIcon(), func() { g.snip.run() })
	g.snip.runBtn.Importance = widget.HighImportance
	g.snip.stopBtn = widget.NewButtonWithIcon(g.tr.T(i18n.SnippetsStop), theme.MediaStopIcon(), func() { g.snip.stop() })
	g.snip.stopBtn.Disable()
	g.snip.stream = widget.NewCheck(g.tr.T(i18n.SnippetsStreaming), nil)

	toolbar := container.NewHBox(newBtn, g.snip.runBtn, g.snip.stopBtn, g.snip.stream)
	left := container.NewVScroll(g.snip.listBox)
	left.SetMinSize(fyne.NewSize(320, 200))
	right := container.NewVScroll(container.NewPadded(g.snip.output))
	split := container.NewHSplit(left, right)
	split.SetOffset(0.36)
	top := container.NewVBox(toolbar, widget.NewSeparator())
	g.snip.refresh()
	return container.NewBorder(top, nil, nil, nil, split)
}

func (v *snippetView) refresh() {
	if v.listBox == nil {
		return
	}
	v.listBox.Objects = nil
	if len(v.app.rt.Store.Snippets) == 0 {
		v.listBox.Add(container.NewPadded(widget.NewLabel(v.app.tr.T(i18n.SnippetsEmpty))))
	}
	for i, s := range v.app.rt.Store.Snippets {
		idx, snippet := i, s
		btn := widget.NewButton(s.Name, func() {
			v.sel = idx
			v.appendLine("$ " + snippet.Command)
		})
		btn.Alignment = widget.ButtonAlignLeading
		v.listBox.Add(btn)
	}
	v.listBox.Refresh()
}

func (v *snippetView) newSnippet() {
	name := widget.NewEntry()
	command := widget.NewEntry()
	form := widget.NewForm(
		widget.NewFormItem(v.app.tr.T(i18n.CommonName), name),
		widget.NewFormItem(v.app.tr.T(i18n.SnippetsCommand), command),
	)
	dialog.ShowCustomConfirm(v.app.tr.T(i18n.SnippetsNew), v.app.tr.T(i18n.CommonSave), v.app.tr.T(i18n.CommonCancel), form, func(ok bool) {
		if !ok || strings.TrimSpace(name.Text) == "" || strings.TrimSpace(command.Text) == "" {
			return
		}
		s := model.NewSnippet()
		s.Name = strings.TrimSpace(name.Text)
		s.Command = strings.TrimSpace(command.Text)
		v.app.rt.Store.Snippets = append(v.app.rt.Store.Snippets, s)
		_ = v.app.rt.Save()
		v.refresh()
	}, v.app.win)
}

func (v *snippetView) run() {
	if v.running || v.sel < 0 || v.sel >= len(v.app.rt.Store.Snippets) {
		return
	}
	snippet := v.app.rt.Store.Snippets[v.sel]
	hosts := append([]model.Host(nil), v.app.hosts...)
	if len(hosts) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(v.app.ctx)
	v.cancel = cancel
	v.running = true
	v.runBtn.Disable()
	v.stopBtn.Enable()
	v.appendLine(fmt.Sprintf("== %s ==", snippet.Name))

	rt := v.app.rt
	stream := v.stream.Checked
	go func() {
		results := sshclient.RunSnippet(ctx, rt.Dialer, hosts, rt.Store.Hosts, rt.Resolver(), snippet.Command, sshclient.SnippetRunOptions{
			Stream:        stream,
			MaxConcurrent: maxInt(1, rt.Store.Settings.General.MaxConcurrent),
			OnEvent: func(e sshclient.SnippetEvent) {
				if e.Line != "" {
					line := e.HostName + ": " + e.Line
					fyne.Do(func() { v.appendLine(line) })
				}
			},
		})
		fyne.Do(func() {
			for _, r := range results {
				status := fmt.Sprintf("exit %d", r.ExitCode)
				if r.Err != "" {
					status = r.Err
				}
				v.appendLine(fmt.Sprintf("-- %s: %s (%s)", r.HostName, status, r.Duration.Round(1_000_000)))
			}
			v.running = false
			v.runBtn.Enable()
			v.stopBtn.Disable()
		})
	}()
}

func (v *snippetView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.appendLine(v.app.tr.T(i18n.SnippetsStop))
}

func (v *snippetView) appendLine(line string) {
	v.lines = append(v.lines, line)
	if len(v.lines) > 400 {
		v.lines = v.lines[len(v.lines)-400:]
	}
	v.output.SetText(strings.Join(v.lines, "\n"))
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

type settingsView struct {
	app          *App
	lang         *widget.Select
	themeSel     *widget.Select
	timeout      *widget.Entry
	errorTimeout *widget.Entry
	sticky       *widget.Check
	confirmClose *widget.Check
	sudo         *widget.Check
	showIP       *widget.Check
	refreshSec   *widget.Entry
	maxConc      *widget.Entry
	fontFamily   *widget.Entry
	fontSize     *widget.Entry

	langs []i18n.Lang
}

func (g *App) buildSettingsTab() fyne.CanvasObject {
	v := &settingsView{app: g}
	g.sett = v

	v.langs = i18n.Languages()
	langNames := make([]string, len(v.langs))
	for i, l := range v.langs {
		langNames[i] = l.NativeName()
	}
	v.lang = widget.NewSelect(langNames, nil)
	v.themeSel = widget.NewSelect([]string{
		g.tr.T(i18n.SettingsThemeDefault),
		g.tr.T(i18n.SettingsThemeDark),
		g.tr.T(i18n.SettingsThemeLight),
	}, nil)

	v.timeout = widget.NewEntry()
	v.errorTimeout = widget.NewEntry()
	v.refreshSec = widget.NewEntry()
	v.maxConc = widget.NewEntry()
	v.sticky = widget.NewCheck(g.tr.T(i18n.SettingsStickyErrors), nil)
	v.confirmClose = widget.NewCheck(g.tr.T(i18n.SettingsConfirmCloseTab), nil)
	v.sudo = widget.NewCheck(g.tr.T(i18n.TerminalSudoAutofill), nil)
	v.showIP = widget.NewCheck(g.tr.T(i18n.SettingsShowIP), nil)
	v.fontFamily = widget.NewEntry()
	v.fontFamily.SetPlaceHolder(g.tr.T(i18n.TerminalFontFamily))
	v.fontSize = widget.NewEntry()

	form := widget.NewForm(
		widget.NewFormItem(g.tr.T(i18n.SettingsLanguage), v.lang),
		widget.NewFormItem(g.tr.T(i18n.SettingsTheme), v.themeSel),
		widget.NewFormItem(g.tr.T(i18n.SettingsNotificationTimeout), v.timeout),
		widget.NewFormItem(g.tr.T(i18n.SettingsErrorTimeout), v.errorTimeout),
		widget.NewFormItem("", v.sticky),
		widget.NewFormItem("", v.confirmClose),
		widget.NewFormItem("", v.sudo),
		widget.NewFormItem("", v.showIP),
		widget.NewFormItem(g.tr.T(i18n.SettingsRefreshInterval), v.refreshSec),
		widget.NewFormItem(g.tr.T(i18n.SettingsMaxConcurrent), v.maxConc),
		widget.NewFormItem(g.tr.T(i18n.TerminalFontFamily), v.fontFamily),
		widget.NewFormItem(g.tr.T(i18n.TerminalFontSize), v.fontSize),
		widget.NewFormItem(g.tr.T(i18n.SettingsConfigDirectory), widget.NewLabel(g.rt.Store.Paths.Dir)),
	)

	apply := widget.NewButtonWithIcon(g.tr.T(i18n.CommonApply), theme.ConfirmIcon(), func() { v.apply() })
	apply.Importance = widget.HighImportance
	applyRow := container.NewHBox(apply)

	v.refresh()
	return container.NewBorder(nil, container.NewPadded(applyRow), nil, nil, container.NewVScroll(form))
}

func (v *settingsView) refresh() {
	s := v.app.rt.Store.Settings
	for i, l := range v.langs {
		if l == i18n.Parse(s.UI.Language) {
			v.lang.SetSelectedIndex(i)
		}
	}
	switch s.UI.Theme {
	case "dark":
		v.themeSel.SetSelectedIndex(1)
	case "light":
		v.themeSel.SetSelectedIndex(2)
	default:
		v.themeSel.SetSelectedIndex(0)
	}
	v.timeout.SetText(strconv.Itoa(s.Notifications.TimeoutSeconds))
	v.errorTimeout.SetText(strconv.Itoa(s.Notifications.ErrorTimeoutSeconds))
	v.sticky.SetChecked(s.Notifications.StickyErrors)
	v.confirmClose.SetChecked(s.General.ConfirmCloseTab)
	v.sudo.SetChecked(s.Terminal.SudoAutofill)
	v.showIP.SetChecked(s.UI.ShowIP)
	v.refreshSec.SetText(strconv.Itoa(s.General.RefreshIntervalSeconds))
	v.maxConc.SetText(strconv.Itoa(s.General.MaxConcurrent))
	v.fontFamily.SetText(s.Terminal.FontFamily)
	v.fontSize.SetText(strconv.FormatFloat(s.Terminal.FontSize, 'f', -1, 64))
}

func (v *settingsView) apply() {
	s := &v.app.rt.Store.Settings
	idx := v.lang.SelectedIndex()
	if idx >= 0 && idx < len(v.langs) {
		s.UI.Language = v.langs[idx].BCP47()
	}
	switch v.themeSel.SelectedIndex() {
	case 1:
		s.UI.Theme = "dark"
	case 2:
		s.UI.Theme = "light"
	default:
		s.UI.Theme = "default"
	}
	s.Notifications.TimeoutSeconds = atoiDefault(v.timeout.Text, s.Notifications.TimeoutSeconds)
	s.Notifications.ErrorTimeoutSeconds = atoiDefault(v.errorTimeout.Text, s.Notifications.ErrorTimeoutSeconds)
	s.Notifications.StickyErrors = v.sticky.Checked
	s.General.ConfirmCloseTab = v.confirmClose.Checked
	s.Terminal.SudoAutofill = v.sudo.Checked
	s.UI.ShowIP = v.showIP.Checked
	s.General.RefreshIntervalSeconds = atoiDefault(v.refreshSec.Text, s.General.RefreshIntervalSeconds)
	s.General.MaxConcurrent = atoiDefault(v.maxConc.Text, s.General.MaxConcurrent)
	s.Terminal.FontFamily = strings.TrimSpace(v.fontFamily.Text)
	if size, err := strconv.ParseFloat(strings.TrimSpace(v.fontSize.Text), 64); err == nil && size >= 6 && size <= 72 {
		s.Terminal.FontSize = size
	}

	if err := v.app.rt.Save(); err != nil {
		dialog.ShowError(err, v.app.win)
		return
	}
	v.app.rt.Tr.SetLang(s.UI.Language)
	v.app.applyTheme()
	v.app.setStatus(v.app.tr.T(i18n.SettingsSavedNotification))
	v.app.rebuildUI()
	v.app.startMetricsLoop()
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
		return n
	}
	return def
}

// ---------------------------------------------------------------------------
// Forwards
// ---------------------------------------------------------------------------

type forwardsView struct {
	app *App
	box *fyne.Container
}

func (g *App) buildForwardsTab() fyne.CanvasObject {
	g.forw = &forwardsView{app: g, box: container.NewVBox()}
	refreshBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonRefresh), theme.ViewRefreshIcon(), func() { g.forw.refresh() })
	startAll := widget.NewButtonWithIcon(g.tr.T(i18n.ForwardsStart), theme.MediaPlayIcon(), func() { g.startForwards() })
	stopAll := widget.NewButtonWithIcon(g.tr.T(i18n.ForwardsStop), theme.MediaStopIcon(), func() {
		g.rt.Forwards.StopAll()
		g.forw.refresh()
	})
	top := container.NewHBox(refreshBtn, startAll, stopAll)
	g.forw.refresh()
	return container.NewBorder(top, nil, nil, nil, container.NewVScroll(g.forw.box))
}

func (v *forwardsView) refresh() {
	if v.box == nil {
		return
	}
	v.box.Objects = nil
	statuses := v.app.rt.Forwards.Statuses()
	if len(statuses) == 0 {
		v.box.Add(container.NewPadded(widget.NewLabel(v.app.tr.T(i18n.CommonEmpty))))
	}
	for _, st := range statuses {
		name := st.HostID
		if h, ok := v.app.rt.Store.Host(st.HostID); ok {
			name = h.Name
		}
		state := widget.NewLabel(string(st.State))
		if st.State == sshclient.ForwardActive {
			state.Importance = widget.SuccessImportance
		} else {
			state.Importance = widget.WarningImportance
		}
		row := container.NewBorder(nil, nil,
			widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			state,
			widget.NewLabel(fmt.Sprintf("%s   %s", st.Kind, st.Spec)))
		v.box.Add(row)
		if st.Err != "" {
			v.box.Add(widget.NewLabel(st.Err))
		}
		v.box.Add(widget.NewSeparator())
	}
	v.box.Refresh()
}

// startForwards connects every host that has forwarding rules and starts its
// tunnels.
func (g *App) startForwards() {
	for _, h := range g.rt.Store.Hosts {
		if len(h.LocalForwards) == 0 && h.DynamicForward == "" || h.Hidden {
			continue
		}
		host := h
		go func() {
			client, err := g.rt.Connect(g.ctx, host)
			if err != nil {
				fyne.Do(func() { g.setStatus(err.Error()) })
				return
			}
			if err := g.rt.Forwards.StartHost(g.ctx, client, host); err != nil {
				fyne.Do(func() { g.showError(err) })
				return
			}
			fyne.Do(func() { g.forw.refresh() })
		}()
	}
}

// ---------------------------------------------------------------------------
// Key setup wizard
// ---------------------------------------------------------------------------

type keySetupView struct {
	app      *App
	hostSel  *widget.Select
	disable  *widget.Check
	startBtn *widget.Button
	log      *widget.Entry
	running  bool
}

func (g *App) buildKeySetupTab() fyne.CanvasObject {
	v := &keySetupView{app: g}
	g.keys = v
	v.hostSel = widget.NewSelect(nil, nil)
	v.disable = widget.NewCheck(g.tr.T(i18n.KeySetupDisablePassword), nil)
	v.startBtn = widget.NewButtonWithIcon(g.tr.T(i18n.KeySetupStart), theme.MediaPlayIcon(), func() { v.start() })
	v.startBtn.Importance = widget.HighImportance
	v.log = widget.NewMultiLineEntry()
	v.log.Wrapping = fyne.TextWrapWord
	v.log.SetMinRowsVisible(12)

	intro := widget.NewLabel(g.tr.T(i18n.KeySetupIntro))
	intro.Wrapping = fyne.TextWrapWord
	warning := widget.NewLabel(g.tr.T(i18n.KeySetupWarning))
	warning.Wrapping = fyne.TextWrapWord
	warning.Importance = widget.WarningImportance

	form := widget.NewForm(
		widget.NewFormItem(g.tr.T(i18n.CommonName), v.hostSel),
		widget.NewFormItem("", v.disable),
	)
	top := container.NewVBox(intro, form, warning, v.startBtn, widget.NewSeparator())
	v.refreshHosts()
	return container.NewBorder(top, nil, nil, nil, container.NewVScroll(v.log))
}

func (v *keySetupView) refreshHosts() {
	if v.hostSel == nil {
		return
	}
	var names []string
	for _, h := range v.app.rt.Store.Hosts {
		names = append(names, h.Name)
	}
	selected := v.hostSel.Selected
	v.hostSel.Options = names
	v.hostSel.Refresh()
	if selected != "" {
		v.hostSel.SetSelected(selected)
	} else if len(names) > 0 {
		v.hostSel.SetSelectedIndex(0)
	}
}

func (v *keySetupView) start() {
	if v.running || v.hostSel.Selected == "" {
		return
	}
	host, ok := v.app.rt.Store.HostByName(v.hostSel.Selected)
	if !ok {
		return
	}
	v.running = true
	v.startBtn.Disable()
	v.log.SetText("")

	rt := v.app.rt
	opts := sshclient.KeySetupOptions{
		DisablePassword: v.disable.Checked,
		Progress: func(step sshclient.KeySetupStep, state sshclient.KeySetupState, msg string) {
			line := fmt.Sprintf("[%s] %s %s", state, step.Description(), msg)
			fyne.Do(func() {
				v.log.SetText(strings.TrimRight(v.log.Text, "\n") + "\n" + line)
				v.log.CursorRow = len(strings.Split(v.log.Text, "\n"))
			})
		},
	}
	go func() {
		res, err := rt.Dialer.SetupKey(v.app.ctx, host, rt.Store.Hosts, rt.Resolver(), opts)
		fyne.Do(func() {
			v.running = false
			v.startBtn.Enable()
			if err != nil {
				v.log.SetText(v.log.Text + "\n" + err.Error())
				v.app.showError(err)
				return
			}
			summary := fmt.Sprintf("%s: %s", v.app.tr.T(i18n.KeySetupDone), res.PrivateKeyPath)
			v.log.SetText(v.log.Text + "\n" + summary)
			v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.NotifyKeySetupComplete))
			if res.HostKeyVerified {
				v.app.rt.Store.UpsertHost(host)
				_ = v.app.rt.Save()
			}
		})
	}()
}
