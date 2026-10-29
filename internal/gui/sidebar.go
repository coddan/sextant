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
func (g *App) activateSession(s *terminalSession) {
	if s == nil {
		return
	}
	g.active = s
	g.showScreen(screenTerminal)
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
		if g.screen == screenTerminal && g.active == session {
			button.Importance = widget.HighImportance
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

// buildTerminalWorkspace owns the terminal screen. The active session's content
// is the only child, but all sessions keep their live views in memory.
func (g *App) buildTerminalWorkspace() fyne.CanvasObject {
	g.termStack = container.NewStack()
	g.showActiveSession()
	return g.termStack
}

func (g *App) showActiveSession() {
	if g.termStack == nil {
		return
	}
	var body fyne.CanvasObject
	if g.active != nil && g.active.content != nil {
		body = g.active.content
	} else {
		body = g.terminalPlaceholder()
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
