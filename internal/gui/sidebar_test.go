package gui

import (
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
)

func TestRefreshSidebarShowsEverySession(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	first := &terminalSession{title: "web-01"}
	second := &terminalSession{title: "db-01"}
	g := &App{
		tr:          i18n.New("en"),
		sessions:    []*terminalSession{first, second},
		active:      second,
		screen:      screenTerminal,
		sessionRows: container.NewVBox(),
		navButtons:  map[screenID]*widget.Button{},
	}
	g.refreshSidebar()
	if got := len(g.sessionRows.Objects); got != 2 {
		t.Fatalf("session rows = %d, want 2", got)
	}
}

func TestNavHighlightFollowsScreenAndSession(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	session := &terminalSession{title: "web-01"}
	g := &App{
		tr:         i18n.New("en"),
		sessions:   []*terminalSession{session},
		active:     session,
		screen:     screenTerminal,
		navButtons: map[screenID]*widget.Button{},
	}
	g.navButtons[screenDashboard] = widget.NewButton("Dashboard", nil)
	g.navButtons[screenTerminal] = widget.NewButton("Terminal", nil)
	g.refreshNav()
	if g.navButtons[screenDashboard].Importance != widget.LowImportance {
		t.Fatal("dashboard should not be highlighted while a session is active")
	}
	if g.navButtons[screenTerminal].Importance != widget.LowImportance {
		t.Fatal("terminal selector should not compete with the active session row")
	}

	g.active = nil
	g.refreshNav()
	if g.navButtons[screenTerminal].Importance != widget.HighImportance {
		t.Fatal("terminal selector should be highlighted on the empty workspace")
	}
}

func TestEndSessionKeepsOtherSessionsAlive(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	first := &terminalSession{title: "web-01", writeEnd: make(chan struct{})}
	second := &terminalSession{title: "db-01", writeEnd: make(chan struct{})}
	g := &App{
		tr:          i18n.New("en"),
		sessions:    []*terminalSession{first, second},
		active:      second,
		screen:      screenDashboard,
		sessionRows: container.NewVBox(),
		navButtons:  map[screenID]*widget.Button{},
	}
	g.endSession(first)
	if len(g.sessions) != 1 || g.sessions[0] != second {
		t.Fatalf("remaining sessions = %+v, want only db-01", g.sessions)
	}
	if g.active != second {
		t.Fatal("closing a background terminal changed the active session")
	}
}
