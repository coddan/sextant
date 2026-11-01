package gui

import (
	"testing"

	"fyne.io/fyne/v2"
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

func TestSidebarSettingsButtonSwitchesScreen(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	g := &App{
		tr:      i18n.New("en"),
		content: container.NewStack(),
		views: map[screenID]fyne.CanvasObject{
			screenDashboard: widget.NewLabel("dashboard"),
			screenSettings:  widget.NewLabel("settings"),
		},
	}
	_ = g.buildSidebar()
	test.Tap(g.navButtons[screenSettings])
	if g.screen != screenSettings {
		t.Fatalf("screen = %q, want %q", g.screen, screenSettings)
	}
}

func TestTerminalSplitOrientation(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	first := &terminalSession{title: "web-01", content: widget.NewLabel("one")}
	second := &terminalSession{title: "db-01", content: widget.NewLabel("two")}
	g := &App{
		tr:          i18n.New("en"),
		sessions:    []*terminalSession{first, second},
		active:      first,
		termStack:   container.NewStack(),
		sessionRows: container.NewVBox(),
		navButtons:  map[screenID]*widget.Button{},
	}

	g.setSplitMode(splitVertical)
	split, ok := g.termStack.Objects[0].(*container.Split)
	if !ok {
		t.Fatalf("vertical split rendered %T, want *container.Split", g.termStack.Objects[0])
	}
	if !split.Horizontal || split.Leading != first.content || split.Trailing != second.content {
		t.Fatal("vertical split should place the two terminals side by side")
	}

	g.setSplitMode(splitHorizontal)
	split, ok = g.termStack.Objects[0].(*container.Split)
	if !ok {
		t.Fatalf("horizontal split rendered %T, want *container.Split", g.termStack.Objects[0])
	}
	if split.Horizontal {
		t.Fatal("horizontal split should stack the two terminals")
	}

	g.setSplitMode(splitNone)
	if g.termStack.Objects[0] != first.content {
		t.Fatal("single layout should show only the focused terminal")
	}
}

func TestActivatingSessionSwapsSplitPanes(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	first := &terminalSession{title: "web-01"}
	second := &terminalSession{title: "db-01"}
	g := &App{
		tr:                 i18n.New("en"),
		sessions:           []*terminalSession{first, second},
		active:             first,
		splitMode:          splitVertical,
		splitSecondary:     second,
		splitActiveLeading: true,
		sessionRows:        container.NewVBox(),
		navButtons:         map[screenID]*widget.Button{},
	}

	g.activateSession(second)
	if g.active != second || g.splitSecondary != first {
		t.Fatal("clicking the other pane should swap focus without changing the two visible sessions")
	}
	if g.splitActiveLeading {
		t.Fatal("the clicked pane should remain in its original trailing position")
	}
}

func TestFocusSplitSessionKeepsPaneOrder(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	first := &terminalSession{title: "web-01", content: widget.NewLabel("one")}
	second := &terminalSession{title: "db-01", content: widget.NewLabel("two")}
	g := &App{
		tr:                 i18n.New("en"),
		sessions:           []*terminalSession{first, second},
		active:             first,
		splitMode:          splitVertical,
		splitSecondary:     second,
		splitActiveLeading: true,
		termStack:          container.NewStack(),
		sessionRows:        container.NewVBox(),
		navButtons:         map[screenID]*widget.Button{},
	}

	g.focusSplitSession(second)
	g.showActiveSession()
	split, ok := g.termStack.Objects[0].(*container.Split)
	if !ok {
		t.Fatalf("split rendered %T, want *container.Split", g.termStack.Objects[0])
	}
	if split.Leading != first.content || split.Trailing != second.content {
		t.Fatal("focusing the trailing terminal should not move it to the leading side")
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
