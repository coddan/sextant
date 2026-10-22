package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/atotto/clipboard"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
	"github.com/coddan/sextant/internal/termutil"
	"github.com/coddan/sextant/internal/vterm"
)

// errNotConnected is returned when a terminal is opened without a connection.
var errNotConnected = errors.New("tui: not connected")

// terminalTab is one SSH terminal session rendered through the VT emulator.
type terminalTab struct {
	host   model.Host
	shell  *sshclient.Shell
	vt     *vterm.Terminal
	ch     chan tea.Msg
	cols   int
	rows   int
	offset int
	closed bool
	err    string

	findActive  bool
	findInput   textinput.Model
	findMatches []vterm.SearchMatch
	findIndex   int
	findErr     string
	sudo        *termutil.Throttle
}

// termStartedMsg carries the live shell back to the update loop.
type termStartedMsg struct{ shell *sshclient.Shell }

func newTerminalTab(m *Model, host model.Host) *terminalTab {
	cols := maxInt(20, m.width-2)
	rows := maxInt(5, m.height-6)
	fi := textinput.New()
	fi.Placeholder = m.tr(i18n.TerminalFindPlaceholder)
	fi.CharLimit = 128
	tab := &terminalTab{
		host: host,
		vt:   vterm.New(cols, rows, 5000),
		ch:   make(chan tea.Msg, 256),
		cols: cols,
		rows: rows,
		sudo: termutil.NewThrottle(5 * time.Second),
	}
	// Remote programs copy through OSC 52; honour it with the local
	// clipboard instead of asking the user to drag-select.
	tab.vt.OnClipboard = func(text string) {
		if err := clipboard.WriteAll(text); err == nil {
			m.setStatus("%s", m.tr(i18n.NotifyCopied))
		}
	}
	return tab
}

// startCmd opens the remote shell and begins streaming its output.
func (t *terminalTab) startCmd(rt *app.Runtime, ctx context.Context) tea.Cmd {
	host := t.host
	cols, rows := t.cols, t.rows
	ch := t.ch
	return func() tea.Msg {
		client, ok := rt.Client(host.ID)
		if !ok {
			return termMsg{err: errNotConnected, closed: true}
		}
		shell, err := client.StartShell(ctx, sshclient.ShellOptions{
			Cols:         cols,
			Rows:         rows,
			AgentForward: host.ForwardAgent,
		})
		if err != nil {
			return termMsg{err: err, closed: true}
		}
		go func() {
			buf := make([]byte, 16384)
			for {
				n, err := shell.Read(buf)
				if n > 0 {
					ch <- termMsg{data: append([]byte(nil), buf[:n]...)}
				}
				if err != nil {
					ch <- termMsg{err: err, closed: true}
					return
				}
			}
		}()
		return termStartedMsg{shell: shell}
	}
}

func (t *terminalTab) waitCmd() tea.Cmd {
	ch := t.ch
	return func() tea.Msg { return <-ch }
}

// write feeds remote output into the emulator.
func (t *terminalTab) write(data []byte) {
	_, _ = t.vt.Write(data)
	// Reset the scroll offset when the user was already at the bottom.
	t.offset = 0
}

func (t *terminalTab) resize(width, height int) {
	t.cols = maxInt(20, width)
	t.rows = maxInt(5, height)
	t.vt.Resize(t.cols, t.rows)
	if t.shell != nil {
		_ = t.shell.Resize(t.cols, t.rows)
	}
}

func (t *terminalTab) close() {
	if t.shell != nil {
		_ = t.shell.Close()
	}
}

// sendKey forwards a keystroke to the remote shell.
func (t *terminalTab) sendKey(key tea.KeyMsg) tea.Cmd {
	if t.shell == nil || t.closed {
		return nil
	}
	// PageUp/PageDown scroll the local scrollback instead of reaching the
	// remote shell, which is what users expect from a terminal.
	switch key.String() {
	case "pgup":
		t.scrollUp(t.rows / 2)
		return nil
	case "pgdown":
		t.scrollDown(t.rows / 2)
		return nil
	}
	data := keyBytes(key)
	if len(data) == 0 {
		return nil
	}
	shell := t.shell
	return func() tea.Msg {
		if _, err := shell.Write(data); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}

func (t *terminalTab) scrollUp(n int) {
	total := t.vt.ScrollbackLen()
	t.offset = minInt(total, t.offset+n)
}

func (t *terminalTab) scrollDown(n int) {
	t.offset = maxInt(0, t.offset-n)
}

// beginFind opens the find bar.
func (t *terminalTab) beginFind() {
	t.findActive = true
	t.findInput.Focus()
	t.findInput.SetValue("")
	t.findMatches = nil
	t.findErr = ""
}

func (t *terminalTab) findKey(key tea.KeyMsg) {
	switch key.Type {
	case tea.KeyEsc:
		t.findActive = false
		return
	case tea.KeyEnter:
		t.runFind()
		t.nextMatch(1)
		return
	}
	var cmd tea.Cmd
	t.findInput, cmd = t.findInput.Update(key)
	_ = cmd
	t.runFind()
}

func (t *terminalTab) runFind() {
	query := t.findInput.Value()
	t.findErr = ""
	if query == "" {
		t.findMatches = nil
		t.findIndex = 0
		return
	}
	matches, err := t.vt.Search(query, false, false)
	if err != nil {
		t.findErr = err.Error()
		t.findMatches = nil
		return
	}
	t.findMatches = matches
	t.findIndex = 0
}

// nextMatch moves to the next (+1) or previous (-1) match and scrolls there.
func (t *terminalTab) nextMatch(dir int) {
	if len(t.findMatches) == 0 {
		return
	}
	t.findIndex = (t.findIndex + dir + len(t.findMatches)) % len(t.findMatches)
	target := t.findMatches[t.findIndex].Line
	// Scroll so the match is visible: offset counts lines above the screen.
	total := t.vt.TotalLines()
	t.offset = minInt(t.vt.ScrollbackLen(), total-1-target)
	if t.offset < 0 {
		t.offset = 0
	}
}

// keyBytes converts a bubbletea key into terminal input.
func keyBytes(key tea.KeyMsg) []byte {
	switch key.Type {
	case tea.KeyRunes:
		return []byte(string(key.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyBackspace:
		return []byte("\x7f")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyEsc:
		return []byte("\x1b")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyCtrlE,
		tea.KeyCtrlG, tea.KeyCtrlH, tea.KeyCtrlJ, tea.KeyCtrlK, tea.KeyCtrlL,
		tea.KeyCtrlN, tea.KeyCtrlO, tea.KeyCtrlP, tea.KeyCtrlR, tea.KeyCtrlS,
		tea.KeyCtrlT, tea.KeyCtrlU, tea.KeyCtrlV, tea.KeyCtrlW, tea.KeyCtrlX,
		tea.KeyCtrlY, tea.KeyCtrlZ:
		// Control letters are the letter minus 'a' plus 1.
		name := key.String() // "ctrl+x"
		if len(name) >= 6 {
			return []byte{name[len(name)-1] - 'a' + 1}
		}
	}
	return nil
}

// render draws the visible part of the emulated screen.
func (t *terminalTab) render(width, height int) string {
	if t.closed {
		msg := t.err
		if msg == "" {
			msg = "session closed"
		}
		return mutedStyle.Render(msg)
	}
	var b strings.Builder
	sbLen := t.vt.ScrollbackLen()
	for y := 0; y < t.rows && y < height; y++ {
		virtual := sbLen + y - t.offset
		line := t.vt.LineAt(virtual)
		b.WriteString(t.renderLine(line, virtual, width))
		if y < t.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderLine renders one line with colour, trimming trailing blanks.
func (t *terminalTab) renderLine(line vterm.Line, virtual, width int) string {
	if line == nil {
		return ""
	}
	// Find the last non-blank cell so we do not paint the whole width.
	last := -1
	for i, c := range line {
		if c.Rune != 0 && (c.Rune != ' ' || c.Bg.Mode != vterm.ColorDefault) {
			last = i
		}
	}
	if last < 0 {
		last = 0
	}
	if last > width-1 {
		last = width - 1
	}
	if last > len(line)-1 {
		last = len(line) - 1
	}
	cx, cy, visible := t.vt.Cursor()
	cursorVirtual := t.vt.ScrollbackLen() + cy
	var b strings.Builder
	for x := 0; x <= last; x++ {
		c := line[x]
		if c.Rune == 0 {
			continue
		}
		style := cellStyle(c)
		if visible && t.offset == 0 && virtual == cursorVirtual && x == cx {
			style = style.Reverse(true)
		}
		b.WriteString(style.Render(string(c.Rune) + c.Comb))
	}
	return b.String()
}
