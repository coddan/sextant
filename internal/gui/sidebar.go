package gui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
)

// screenID identifies the screen shown to the right of the navigation rail.
type screenID string

const (
	screenDashboard screenID = "dashboard"
	screenTerminal  screenID = "terminal"
	screenSFTP      screenID = "sftp"
	screenSnippets  screenID = "snippets"
	screenForwards  screenID = "forwards"
	screenKeys      screenID = "keys"
	screenSettings  screenID = "settings"
)

// Terminal split modes. "vertical" puts the panes side by side (a vertical
// divider); "horizontal" stacks them above and below each other.
const (
	splitNone       = ""
	splitVertical   = "vertical"
	splitHorizontal = "horizontal"
)

// buildSidebar creates the SecureCRT/OmnySSH-style left rail: fixed navigation
// on top, the live session list in the middle, and settings at the bottom.
func (g *App) buildSidebar() fyne.CanvasObject {
	g.navButtons = map[screenID]*widget.Button{}

	title := widget.NewLabelWithStyle(g.tr.T(i18n.AppName), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(container.NewHBox(widget.NewIcon(theme.ComputerIcon()), title))

	nav := container.NewVBox(
		g.navButton(screenDashboard, g.tr.T(i18n.NavDashboard), theme.HomeIcon(), func() {
			g.selectScreen(screenDashboard)
		}),
		g.navButton(screenSnippets, g.tr.T(i18n.SnippetsTitle), theme.MediaPlayIcon(), func() {
			g.selectScreen(screenSnippets)
		}),
		g.navButton(screenSFTP, g.tr.T(i18n.NavSFTP), theme.FolderIcon(), func() {
			g.openSFTPView()
		}),
		g.navButton(screenTerminal, g.tr.T(i18n.NavTerminal), theme.ComputerIcon(), func() {
			g.openTerminalView()
		}),
		widget.NewSeparator(),
		g.navButton(screenForwards, g.tr.T(i18n.ForwardsTitle), theme.MailSendIcon(), func() {
			g.selectScreen(screenForwards)
		}),
		g.navButton(screenKeys, g.tr.T(i18n.KeySetupTitle), theme.LoginIcon(), func() {
			g.selectScreen(screenKeys)
		}),
	)

	sessionsTitle := widget.NewLabelWithStyle(
		g.tr.T(i18n.SidebarSessions), fyne.TextAlignLeading, fyne.TextStyle{Bold: true},
	)
	addSession := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		g.pickHost(g.tr.T(i18n.TerminalPickHost), g.connect)
	})
	addSession.Importance = widget.LowImportance
	sessionsHeader := container.NewBorder(nil, nil, sessionsTitle, addSession)
	g.sessionRows = container.NewVBox()
	sessionsScroll := container.NewVScroll(g.sessionRows)

	top := container.NewVBox(
		header,
		widget.NewSeparator(),
		container.NewPadded(nav),
		widget.NewSeparator(),
		container.NewPadded(sessionsHeader),
	)
	settings := g.navButton(screenSettings, g.tr.T(i18n.NavSettings), theme.SettingsIcon(), func() {
		g.selectScreen(screenSettings)
	})
	bottom := container.NewVBox(widget.NewSeparator(), container.NewPadded(settings))

	body := container.NewBorder(top, bottom, nil, nil, sessionsScroll)
	background := canvas.NewRectangle(theme.Color(theme.ColorNameMenuBackground))
	background.SetMinSize(fyne.NewSize(258, 1))
	side := container.NewStack(background, body)

	line := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	return container.NewBorder(nil, nil, nil, line, side)
}

func (g *App) navButton(id screenID, text string, icon fyne.Resource, tapped func()) *widget.Button {
	b := widget.NewButtonWithIcon(text, icon, tapped)
	b.Alignment = widget.ButtonAlignLeading
	b.Importance = widget.LowImportance
	g.navButtons[id] = b
	return b
}

// selectScreen performs the screen-specific refresh before switching.
func (g *App) selectScreen(id screenID) {
	switch id {
	case screenDashboard:
		g.refreshHosts()
	case screenSnippets:
		if g.snip != nil {
			g.snip.refresh()
		}
	case screenForwards:
		if g.forw != nil {
			g.forw.refresh()
		}
	case screenKeys:
		if g.keys != nil {
			g.keys.refreshHosts()
		}
	case screenSettings:
		if g.sett != nil {
			g.sett.refresh()
		}
	}
	g.showScreen(id)
}

// showScreen swaps the content pane and updates the active navigation or
// session row. Only one screen is visible at a time, exactly like OmnySSH.
func (g *App) showScreen(id screenID) {
	view := g.views[id]
	if g.content == nil || view == nil {
		return
	}
	g.screen = id
	if id == screenTerminal {
		g.showActiveSession()
	}
	g.content.Objects = []fyne.CanvasObject{view}
	g.content.Refresh()
	g.refreshSidebar()
	if id == screenTerminal && g.active != nil && g.win != nil && g.active.view != nil {
		g.win.Canvas().Focus(g.active.view)
	}
}

func (g *App) openTerminalView() {
	if g.active != nil {
		g.activateSession(g.active)
		return
	}
	if n := len(g.sessions); n > 0 {
		g.activateSession(g.sessions[n-1])
		return
	}
	g.showScreen(screenTerminal)
}

func (g *App) openSFTPView() {
	if g.sftp != nil && g.sftp.sftp != nil {
		g.showScreen(screenSFTP)
		return
	}
	g.pickHost(g.tr.T(i18n.SFTPPickHost), g.openSFTP)
}

// activateSession makes a terminal the visible screen and highlights its row.
// In a split workspace the clicked session becomes the focused pane and the old
// focused pane moves to the other side, so clicking is enough to swap panes.
func (g *App) activateSession(s *terminalSession) {
	if s == nil {
		return
	}
	if g.splitMode != splitNone && s != g.active {
		secondary := g.splitSecondarySession()
		switch {
		case g.active == nil:
			g.splitSecondary = g.firstOtherSession(s)
			g.splitActiveLeading = true
		case s == secondary:
			// The pane the user clicked stays on its side; focus moves to it.
			previous := g.active
			g.active = s
			g.splitSecondary = previous
			g.splitActiveLeading = !g.splitActiveLeading
		default:
			// A new third session replaces the focused pane in place.
			if g.splitSecondary == nil || g.splitSecondary == s || g.sessionIndex(g.splitSecondary) < 0 {
				g.splitSecondary = g.firstOtherSession(s)
				g.splitActiveLeading = true
			}
		}
	}
	g.active = s
	g.showScreen(screenTerminal)
	state := g.tr.T(i18n.TerminalStatusConnected)
	if s.closed {
		state = g.tr.T(i18n.TerminalStatusDisconnected)
	}
	g.setStatus(fmt.Sprintf("%s: %s", s.host.Name, state))
}

// focusSplitSession makes a clicked pane the keyboard focus without moving the
// panes around. The sidebar and status bar follow the focused session.
func (g *App) focusSplitSession(s *terminalSession) {
	if s == nil || s == g.active || g.splitMode == splitNone {
		return
	}
	if s != g.splitSecondarySession() {
		return
	}
	previous := g.active
	g.active = s
	g.splitSecondary = previous
	g.splitActiveLeading = !g.splitActiveLeading
	g.refreshSidebar()
	state := g.tr.T(i18n.TerminalStatusConnected)
	if s.closed {
		state = g.tr.T(i18n.TerminalStatusDisconnected)
	}
	g.setStatus(fmt.Sprintf("%s: %s", s.host.Name, state))
}

func (g *App) refreshSidebar() {
	g.refreshNav()
	g.refreshSessionRows()
}

func (g *App) refreshNav() {
	for id, b := range g.navButtons {
		active := id == g.screen
		// While a session is visible, the session row owns the highlight.
		if id == screenTerminal && g.active != nil {
			active = false
		}
		if active {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
}

func (g *App) refreshSessionRows() {
	if g.sessionRows == nil {
		return
	}
	rows := make([]fyne.CanvasObject, 0, len(g.sessions))
	for _, s := range g.sessions {
		session := s
		button := widget.NewButtonWithIcon(
			truncateName(session.title, 18), theme.ComputerIcon(), func() {
				g.activateSession(session)
			},
		)
		button.Alignment = widget.ButtonAlignLeading
		button.Importance = widget.LowImportance
		if g.screen == screenTerminal {
			switch {
			case g.active == session:
				button.Importance = widget.HighImportance
			case g.splitMode != splitNone && g.splitSecondarySession() == session:
				button.Importance = widget.MediumImportance
			}
		}

		closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			g.closeSession(session)
		})
		closeBtn.Importance = widget.LowImportance

		dot := canvas.NewCircle(sessionDotColor(session))
		dotBox := container.NewGridWrap(fyne.NewSize(10, 10), dot)
		row := container.NewBorder(nil, nil, container.NewPadded(dotBox), closeBtn, button)
		rows = append(rows, row)
	}
	g.sessionRows.Objects = rows
	g.sessionRows.Refresh()
}

func sessionDotColor(s *terminalSession) color.Color {
	if s.closed {
		return theme.Color(theme.ColorNameWarning)
	}
	return theme.Color(theme.ColorNameSuccess)
}

// buildTerminalWorkspace owns the terminal screen. One or two session views are
// visible at a time; every session keeps its live view in memory.
func (g *App) buildTerminalWorkspace() fyne.CanvasObject {
	g.termStack = container.NewStack()
	layoutLabel := widget.NewLabelWithStyle(
		g.tr.T(i18n.TerminalLayout), fyne.TextAlignLeading, fyne.TextStyle{Bold: true},
	)
	g.termLayout = widget.NewSelect([]string{
		g.tr.T(i18n.TerminalLayoutSingle),
		g.tr.T(i18n.TerminalLayoutVertical),
		g.tr.T(i18n.TerminalLayoutHorizontal),
	}, nil)
	g.termLayout.SetSelectedIndex(g.splitModeIndex())
	g.termLayout.OnChanged = func(string) {
		switch g.termLayout.SelectedIndex() {
		case 1:
			g.setSplitMode(splitVertical)
		case 2:
			g.setSplitMode(splitHorizontal)
		default:
			g.setSplitMode(splitNone)
		}
	}
	layoutBox := container.NewGridWrap(fyne.NewSize(180, 44), g.termLayout)
	toolbar := container.NewVBox(
		container.NewPadded(container.NewHBox(layoutLabel, layoutBox)),
		widget.NewSeparator(),
	)
	g.showActiveSession()
	return container.NewBorder(toolbar, nil, nil, nil, g.termStack)
}

func (g *App) splitModeIndex() int {
	switch g.splitMode {
	case splitVertical:
		return 1
	case splitHorizontal:
		return 2
	default:
		return 0
	}
}

func (g *App) setSplitMode(mode string) {
	if mode != splitVertical && mode != splitHorizontal {
		mode = splitNone
	}
	wasSingle := g.splitMode == splitNone
	g.splitMode = mode
	if mode == splitNone {
		g.splitSecondary = nil
		g.splitActiveLeading = true
	} else {
		if g.splitSecondary == nil || g.splitSecondary == g.active || g.sessionIndex(g.splitSecondary) < 0 {
			g.splitSecondary = g.firstOtherSession(g.active)
		}
		if wasSingle {
			g.splitActiveLeading = true
		}
	}
	if g.termLayout != nil && g.termLayout.SelectedIndex() != g.splitModeIndex() {
		g.termLayout.SetSelectedIndex(g.splitModeIndex())
	}
	g.showActiveSession()
	g.refreshSidebar()
	if g.win != nil && g.active != nil && g.active.view != nil {
		g.win.Canvas().Focus(g.active.view)
	}
}

// splitSecondarySession returns the other visible pane, preferring the session
// the user last placed there. If it has been closed, the next open session fills
// the pane so a split never shows the same terminal twice.
func (g *App) splitSecondarySession() *terminalSession {
	if g.splitMode == splitNone {
		return nil
	}
	if g.splitSecondary != nil && g.splitSecondary != g.active && g.sessionIndex(g.splitSecondary) >= 0 {
		return g.splitSecondary
	}
	return g.firstOtherSession(g.active)
}

func (g *App) firstOtherSession(s *terminalSession) *terminalSession {
	for _, open := range g.sessions {
		if open != s {
			return open
		}
	}
	return nil
}

func (g *App) showActiveSession() {
	if g.termStack == nil {
		return
	}
	var body fyne.CanvasObject
	if g.active == nil || g.active.content == nil {
		body = g.terminalPlaceholder()
	} else if g.splitMode == splitNone {
		body = g.active.content
	} else {
		secondary := g.splitSecondarySession()
		if secondary != nil {
			g.splitSecondary = secondary
		}
		leading := g.active
		trailing := secondary
		if !g.splitActiveLeading && trailing != nil {
			leading, trailing = trailing, leading
		}
		trailingObj := g.splitPlaceholder()
		if trailing != nil && trailing.content != nil {
			trailingObj = trailing.content
		}
		if g.splitMode == splitHorizontal {
			body = container.NewVSplit(leading.content, trailingObj)
		} else {
			body = container.NewHSplit(leading.content, trailingObj)
		}
	}
	g.termStack.Objects = []fyne.CanvasObject{body}
	g.termStack.Refresh()
}

func (g *App) terminalPlaceholder() fyne.CanvasObject {
	title := widget.NewLabelWithStyle(g.tr.T(i18n.NavTerminal), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel(g.tr.T(i18n.TerminalNoSessions))
	hint.Alignment = fyne.TextAlignCenter
	newBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SidebarNewTerminal), theme.ComputerIcon(), func() {
		g.pickHost(g.tr.T(i18n.TerminalPickHost), g.connect)
	})
	newBtn.Importance = widget.HighImportance
	content := container.NewVBox(title, hint, newBtn)
	return container.NewCenter(container.NewPadded(content))
}

func (g *App) splitPlaceholder() fyne.CanvasObject {
	hint := widget.NewLabel(g.tr.T(i18n.TerminalSplitEmpty))
	hint.Alignment = fyne.TextAlignCenter
	hint.Wrapping = fyne.TextWrapWord
	newBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SidebarNewTerminal), theme.ComputerIcon(), func() {
		g.pickHost(g.tr.T(i18n.TerminalPickHost), g.connect)
	})
	newBtn.Importance = widget.HighImportance
	return container.NewCenter(container.NewPadded(container.NewVBox(hint, newBtn)))
}

// pickHost lets the sidebar spawn a session without first visiting the
// dashboard. Hidden hosts stay hidden, just as they do on the card grid.
func (g *App) pickHost(title string, done func(model.Host)) {
	if g.win == nil || g.rt == nil {
		return
	}
	var hosts []model.Host
	names := make([]string, 0, len(g.rt.Store.Hosts))
	for _, h := range g.rt.Store.Hosts {
		if h.Hidden {
			continue
		}
		hosts = append(hosts, h)
		names = append(names, h.Name)
	}
	if len(hosts) == 0 {
		g.notify(model.NotifyInfo, g.tr.T(i18n.DashboardEmpty))
		g.selectScreen(screenDashboard)
		return
	}

	selector := widget.NewSelect(names, nil)
	selector.SetSelectedIndex(0)
	form := widget.NewForm(widget.NewFormItem(g.tr.T(i18n.CommonName), selector))
	d := dialog.NewCustomConfirm(
		title,
		g.tr.T(i18n.CommonOK),
		g.tr.T(i18n.CommonCancel),
		form,
		func(ok bool) {
			if !ok {
				return
			}
			idx := selector.SelectedIndex()
			if idx >= 0 && idx < len(hosts) {
				done(hosts[idx])
			}
		},
		g.win,
	)
	d.Show()
}
