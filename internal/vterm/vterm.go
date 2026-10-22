// Package vterm is a small VT100/xterm screen emulator.
//
// It is deliberately frontend agnostic: the TUI and the Fyne desktop app both
// feed raw SSH bytes into Write and read the cell grid back. Keeping one
// emulator means search, selection, copy and scrollback behave identically in
// both, and fixes the "cannot copy terminal text" class of bugs at the
// source instead of in one renderer.
package vterm

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ColorMode says how a Color is interpreted.
type ColorMode uint8

const (
	// ColorDefault means the terminal's default foreground/background.
	ColorDefault ColorMode = iota
	// ColorANSI is a palette index (0..255).
	ColorANSI
	// ColorRGB is a 24-bit colour.
	ColorRGB
)

// Color is a terminal colour.
type Color struct {
	Mode    ColorMode
	Index   uint8
	R, G, B uint8
}

// DefaultColor is the terminal default.
var DefaultColor = Color{}

// ANSI builds a palette colour.
func ANSI(i uint8) Color { return Color{Mode: ColorANSI, Index: i} }

// RGB builds a true-colour value.
func RGB(r, g, b uint8) Color { return Color{Mode: ColorRGB, R: r, G: g, B: b} }

// Attribute bits.
const (
	Bold uint16 = 1 << iota
	Dim
	Italic
	Underline
	Blink
	Reverse
	Hidden
	Strike
)

// Cell is one character cell.
type Cell struct {
	Rune rune
	// Comb holds combining marks, which share the cell of their base rune.
	Comb string
	Fg   Color
	Bg   Color
	Attr uint16
}

// Blank returns the default cell.
func Blank() Cell { return Cell{Rune: ' '} }

// Line is one row of cells.
type Line []Cell

// Terminal is a VT screen with scrollback.
type Terminal struct {
	cols, rows int
	primary    screen
	alt        screen
	current    *screen
	scrollback []Line
	maxScroll  int

	cursorVisible bool
	altActive     bool
	title         string
	bell          bool

	// Parser state.
	state     parserState
	csi       []byte
	osc       []byte
	utf8buf   []byte
	lastRune  rune
	lastWidth int

	// Mode flags exposed to the frontend.
	AppCursor      bool
	AppKeypad      bool
	BracketedPaste bool
	MouseMode      int // 0 off, 9 X10, 1000 click, 1002 drag, 1003 any
	MouseSGR       bool
	OriginMode     bool
	AutoWrap       bool

	savedCursor [2]cursorState

	// OnBell and OnTitle let a frontend react without polling.
	OnBell  func()
	OnTitle func(string)
	// OnClipboard receives OSC 52 copies from the remote program.
	OnClipboard func(string)
}

type cursorState struct {
	x, y   int
	fg, bg Color
	attr   uint16
	valid  bool
}

type screen struct {
	cells    []Cell
	cols     int
	rows     int
	cx, cy   int
	fg, bg   Color
	attr     uint16
	top      int
	bottom   int
	wrap     bool // deferred wrap at the last column
	tabStops []bool
}

type parserState int

const (
	stateGround parserState = iota
	stateEscape
	stateCSI
	stateOSC
	stateOSCEsc
	stateString
	stateStringEsc
)

// New creates a terminal.
func New(cols, rows, scrollback int) *Terminal {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	if scrollback < 0 {
		scrollback = 0
	}
	t := &Terminal{
		cols:          cols,
		rows:          rows,
		maxScroll:     scrollback,
		cursorVisible: true,
		AutoWrap:      true,
	}
	t.primary = newScreen(cols, rows)
	t.alt = newScreen(cols, rows)
	t.current = &t.primary
	return t
}

func newScreen(cols, rows int) screen {
	s := screen{
		cells:    make([]Cell, cols*rows),
		cols:     cols,
		rows:     rows,
		bottom:   rows - 1,
		fg:       DefaultColor,
		bg:       DefaultColor,
		tabStops: make([]bool, cols),
	}
	for i := range s.cells {
		s.cells[i] = Blank()
	}
	for i := 0; i < cols; i += 8 {
		s.tabStops[i] = true
	}
	return s
}

// Size returns the grid size.
func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

// Title returns the last OSC 0/2 title.
func (t *Terminal) Title() string { return t.title }

// Cursor returns the cursor position and visibility.
func (t *Terminal) Cursor() (x, y int, visible bool) {
	return t.current.cx, t.current.cy, t.cursorVisible
}

// BellPending reports and clears a pending bell.
func (t *Terminal) BellPending() bool {
	b := t.bell
	t.bell = false
	return b
}

// AltActive reports whether the alternate screen is in use.
func (t *Terminal) AltActive() bool { return t.altActive }

// Resize changes the grid size, preserving as much content as possible.
func (t *Terminal) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == t.cols && rows == t.rows {
		return
	}
	t.primary = t.primary.resize(cols, rows)
	t.alt = t.alt.resize(cols, rows)
	t.cols, t.rows = cols, rows
	if t.altActive {
		t.current = &t.alt
	} else {
		t.current = &t.primary
	}
}

func (s screen) resize(cols, rows int) screen {
	out := newScreen(cols, rows)
	out.cx, out.cy = s.cx, s.cy
	out.fg, out.bg, out.attr = s.fg, s.bg, s.attr
	copyCols := min(cols, s.cols)
	copyRows := min(rows, s.rows)
	for y := 0; y < copyRows; y++ {
		for x := 0; x < copyCols; x++ {
			out.cells[y*cols+x] = s.cells[y*s.cols+x]
		}
	}
	if out.cx >= cols {
		out.cx = cols - 1
	}
	if out.cy >= rows {
		out.cy = rows - 1
	}
	out.top, out.bottom = 0, rows-1
	out.wrap = false
	return out
}

// Cells returns a copy of the visible grid.
func (t *Terminal) Cells() []Cell {
	out := make([]Cell, len(t.current.cells))
	copy(out, t.current.cells)
	return out
}

// CellAt returns a visible cell.
func (t *Terminal) CellAt(x, y int) Cell {
	if x < 0 || y < 0 || x >= t.cols || y >= t.rows {
		return Blank()
	}
	return t.current.cells[y*t.cols+x]
}

// ScrollbackLen returns the number of history lines above the screen.
func (t *Terminal) ScrollbackLen() int { return len(t.scrollback) }

// TotalLines returns scrollback plus screen rows.
func (t *Terminal) TotalLines() int { return len(t.scrollback) + t.rows }

// LineAt returns a line in virtual coordinates: 0 is the oldest scrollback
// line, TotalLines()-1 is the bottom screen row.
func (t *Terminal) LineAt(virtual int) Line {
	if virtual < 0 || virtual >= t.TotalLines() {
		return nil
	}
	if virtual < len(t.scrollback) {
		return t.scrollback[virtual]
	}
	y := virtual - len(t.scrollback)
	start := y * t.cols
	line := make(Line, t.cols)
	copy(line, t.current.cells[start:start+t.cols])
	return line
}

// TextLine renders a virtual line without trailing blanks.
func (t *Terminal) TextLine(virtual int) string {
	line := t.LineAt(virtual)
	if line == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range line {
		if c.Rune == 0 {
			// Second half of a wide character.
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Comb)
	}
	return strings.TrimRight(b.String(), " ")
}

// PlainText renders the whole buffer, used for copy-all and tests.
func (t *Terminal) PlainText() string {
	var b strings.Builder
	for i := 0; i < t.TotalLines(); i++ {
		b.WriteString(t.TextLine(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// Write parses raw terminal output.
func (t *Terminal) Write(p []byte) (int, error) {
	n := len(p)
	for _, b := range p {
		t.feed(b)
	}
	return n, nil
}

func (t *Terminal) feed(b byte) {
	switch t.state {
	case stateGround:
		t.feedGround(b)
	case stateEscape:
		t.feedEscape(b)
	case stateCSI:
		if b >= 0x40 && b <= 0x7e {
			t.handleCSI(string(t.csi), b)
			t.csi = t.csi[:0]
			t.state = stateGround
			return
		}
		if b == 0x1b {
			t.state = stateEscape
			return
		}
		if len(t.csi) < 128 {
			t.csi = append(t.csi, b)
		}
	case stateOSC:
		if b == 0x07 {
			t.handleOSC(string(t.osc))
			t.osc = t.osc[:0]
			t.state = stateGround
			return
		}
		if b == 0x1b {
			t.state = stateOSCEsc
			return
		}
		if len(t.osc) < 8192 {
			t.osc = append(t.osc, b)
		}
	case stateOSCEsc:
		if b == '\\' {
			t.handleOSC(string(t.osc))
			t.osc = t.osc[:0]
			t.state = stateGround
			return
		}
		t.state = stateOSC
	case stateString:
		if b == 0x1b {
			t.state = stateStringEsc
		}
	case stateStringEsc:
		t.state = stateString
		if b == '\\' {
			t.state = stateGround
		}
	}
}

func (t *Terminal) feedGround(b byte) {
	if b == 0x1b {
		t.utf8buf = t.utf8buf[:0]
		t.state = stateEscape
		return
	}
	if b < 0x20 || b == 0x7f {
		t.control(b)
		return
	}
	t.utf8buf = append(t.utf8buf, b)
	if !utf8.FullRune(t.utf8buf) {
		if len(t.utf8buf) > utf8.UTFMax {
			t.utf8buf = t.utf8buf[:0]
		}
		return
	}
	r, size := utf8.DecodeRune(t.utf8buf)
	if r == utf8.RuneError && size == 1 {
		t.utf8buf = t.utf8buf[1:]
		return
	}
	t.utf8buf = t.utf8buf[size:]
	t.lastRune = r
	t.put(r)
}

func (t *Terminal) control(b byte) {
	s := t.current
	switch b {
	case 0x07:
		t.bell = true
		if t.OnBell != nil {
			t.OnBell()
		}
	case 0x08: // backspace
		if s.cx > 0 {
			s.cx--
		}
		s.wrap = false
	case 0x09: // tab
		t.tab(1)
	case 0x0a, 0x0b, 0x0c: // LF, VT, FF
		t.lineFeed()
	case 0x0d: // CR
		s.cx = 0
		s.wrap = false
	case 0x0e, 0x0f: // SO/SI charset, ignored
	case 0x84: // IND
		t.lineFeed()
	case 0x85: // NEL
		t.lineFeed()
		s.cx = 0
	}
}

func (t *Terminal) put(r rune) {
	width := runeWidth(r)
	if width == 0 {
		// Combining mark: attach to the previous cell.
		s := t.current
		x := s.cx - 1
		if s.wrap {
			x = s.cx
		}
		if x < 0 {
			x = 0
		}
		if x < t.cols {
			s.cells[s.cy*t.cols+x].Comb += string(r)
		}
		return
	}
	s := t.current
	if s.wrap {
		s.cx = 0
		t.lineFeed()
		s.wrap = false
	}
	if width == 2 && s.cx == t.cols-1 {
		if t.AutoWrap {
			s.cx = 0
			t.lineFeed()
		} else {
			return
		}
	}
	cell := Cell{Rune: r, Fg: s.fg, Bg: s.bg, Attr: s.attr}
	idx := s.cy*t.cols + s.cx
	s.cells[idx] = cell
	if width == 2 && s.cx+1 < t.cols {
		s.cells[idx+1] = Cell{Rune: 0, Fg: s.fg, Bg: s.bg, Attr: s.attr}
	}
	s.cx += width
	if s.cx >= t.cols {
		s.cx = t.cols - 1
		s.wrap = t.AutoWrap
	}
}

func (t *Terminal) lineFeed() {
	s := t.current
	s.wrap = false
	if s.cy == s.bottom {
		t.scrollUp(1)
	} else if s.cy < t.rows-1 {
		s.cy++
	}
}

func (t *Terminal) reverseIndex() {
	s := t.current
	if s.cy == s.top {
		t.scrollDown(1)
	} else if s.cy > 0 {
		s.cy--
	}
}

func (t *Terminal) scrollUp(n int) {
	s := t.current
	if n < 1 {
		return
	}
	height := s.bottom - s.top + 1
	if n > height {
		n = height
	}
	if s.top == 0 && s.bottom == t.rows-1 && !t.altActive && t.maxScroll > 0 {
		for i := 0; i < n; i++ {
			line := make(Line, t.cols)
			copy(line, s.cells[:t.cols])
			t.scrollback = append(t.scrollback, line)
		}
		if len(t.scrollback) > t.maxScroll {
			t.scrollback = t.scrollback[len(t.scrollback)-t.maxScroll:]
		}
	}
	copy(s.cells[s.top*t.cols:(s.bottom+1)*t.cols], s.cells[(s.top+n)*t.cols:(s.bottom+1)*t.cols])
	for y := s.bottom - n + 1; y <= s.bottom; y++ {
		t.clearRow(y)
	}
}

func (t *Terminal) scrollDown(n int) {
	s := t.current
	height := s.bottom - s.top + 1
	if n > height {
		n = height
	}
	copy(s.cells[(s.top+n)*t.cols:(s.bottom+1)*t.cols], s.cells[s.top*t.cols:(s.bottom-n+1)*t.cols])
	for y := s.top; y < s.top+n; y++ {
		t.clearRow(y)
	}
}

func (t *Terminal) clearRow(y int) {
	if y < 0 || y >= t.rows {
		return
	}
	s := t.current
	start := y * t.cols
	for i := start; i < start+t.cols; i++ {
		s.cells[i] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
	}
}

func (t *Terminal) tab(n int) {
	s := t.current
	for i := 0; i < n; i++ {
		x := s.cx + 1
		for x < t.cols-1 && !s.tabStops[x] {
			x++
		}
		if x >= t.cols {
			x = t.cols - 1
		}
		s.cx = x
	}
	s.wrap = false
}

func (t *Terminal) backTab(n int) {
	s := t.current
	for i := 0; i < n; i++ {
		x := s.cx - 1
		for x > 0 && !s.tabStops[x] {
			x--
		}
		if x < 0 {
			x = 0
		}
		s.cx = x
	}
}

func (t *Terminal) feedEscape(b byte) {
	t.state = stateGround
	switch b {
	case '[':
		t.state = stateCSI
		t.csi = t.csi[:0]
	case ']':
		t.state = stateOSC
		t.osc = t.osc[:0]
	case 'P', '^', '_', 'X':
		t.state = stateString
	case '7':
		t.saveCursor(0)
	case '8':
		t.restoreCursor(0)
	case 'D':
		t.lineFeed()
	case 'E':
		t.lineFeed()
		t.current.cx = 0
	case 'M':
		t.reverseIndex()
	case 'H':
		t.current.tabStops[t.current.cx] = true
	case 'c':
		t.reset()
	case '=', '>':
		t.AppKeypad = b == '='
	case '(', ')', '*', '+':
		// Charset designation takes one more byte.
		t.state = stateEscape
	case '#':
		t.state = stateEscape
	}
}

func (t *Terminal) handleCSI(params string, final byte) {
	priv := byte(0)
	if len(params) > 0 && (params[0] == '?' || params[0] == '>' || params[0] == '<' || params[0] == '=') {
		priv = params[0]
		params = params[1:]
	}
	args := parseParams(params)
	switch final {
	case 'A':
		t.moveCursor(0, -arg(args, 0, 1))
	case 'B', 'e':
		t.moveCursor(0, arg(args, 0, 1))
	case 'C', 'a':
		t.moveCursor(arg(args, 0, 1), 0)
	case 'D':
		t.moveCursor(-arg(args, 0, 1), 0)
	case 'E':
		t.moveCursor(0, arg(args, 0, 1))
		t.current.cx = 0
	case 'F':
		t.moveCursor(0, -arg(args, 0, 1))
		t.current.cx = 0
	case 'G', '`':
		t.current.cx = clamp(arg(args, 0, 1)-1, 0, t.cols-1)
		t.current.wrap = false
	case 'd':
		t.current.cy = clamp(arg(args, 0, 1)-1, 0, t.rows-1)
		t.current.wrap = false
	case 'H', 'f':
		row := arg(args, 0, 1) - 1
		col := arg(args, 1, 1) - 1
		if t.OriginMode {
			row += t.current.top
		}
		t.current.cy = clamp(row, t.current.top, t.current.bottom)
		t.current.cx = clamp(col, 0, t.cols-1)
		t.current.wrap = false
	case 'J':
		t.eraseDisplay(arg(args, 0, 0))
	case 'K':
		t.eraseLine(arg(args, 0, 0))
	case 'L':
		t.insertLines(arg(args, 0, 1))
	case 'M':
		t.deleteLines(arg(args, 0, 1))
	case 'P':
		t.deleteChars(arg(args, 0, 1))
	case '@':
		t.insertChars(arg(args, 0, 1))
	case 'X':
		t.eraseChars(arg(args, 0, 1))
	case 'S':
		t.scrollUp(arg(args, 0, 1))
	case 'T':
		t.scrollDown(arg(args, 0, 1))
	case 'Z':
		t.backTab(arg(args, 0, 1))
	case 'b':
		if t.lastRune != 0 {
			n := arg(args, 0, 1)
			for i := 0; i < n; i++ {
				t.put(t.lastRune)
			}
		}
	case 'g':
		switch arg(args, 0, 0) {
		case 0:
			t.current.tabStops[t.current.cx] = false
		case 3:
			for i := range t.current.tabStops {
				t.current.tabStops[i] = false
			}
		}
	case 'h', 'l':
		t.setMode(priv, args, final == 'h')
	case 'm':
		t.sgr(args)
	case 'r':
		top := arg(args, 0, 1) - 1
		bottom := arg(args, 1, t.rows) - 1
		if top < 0 {
			top = 0
		}
		if bottom >= t.rows {
			bottom = t.rows - 1
		}
		if top < bottom {
			t.current.top, t.current.bottom = top, bottom
			t.current.cx, t.current.cy = 0, top
		}
	case 's':
		t.saveCursor(1)
	case 'u':
		t.restoreCursor(1)
	case 'n':
		// Device status reports are answered by the SSH layer, not the screen.
	case 'q':
		// DECSCA, ignored.
	case 'c':
		// Primary/secondary DA, ignored.
	}
}

func (t *Terminal) setMode(priv byte, args []int, on bool) {
	for _, a := range args {
		switch {
		case priv == '?':
			switch a {
			case 7:
				t.AutoWrap = on
				t.current.wrap = false
			case 1:
				t.AppCursor = on
			case 6:
				t.OriginMode = on
				if on {
					t.current.cy = t.current.top
				}
			case 25:
				t.cursorVisible = on
			case 47, 1047:
				t.setAlt(on)
			case 1048:
				if on {
					t.saveCursor(0)
				} else {
					t.restoreCursor(0)
				}
			case 1049:
				if on {
					t.saveCursor(0)
					t.setAlt(true)
					t.clearAll(t.current)
				} else {
					t.setAlt(false)
					t.restoreCursor(0)
				}
			case 1000, 1001, 1002, 1003:
				if on {
					t.MouseMode = a
				} else {
					t.MouseMode = 0
				}
			case 1006:
				t.MouseSGR = on
			case 2004:
				t.BracketedPaste = on
			}
		case priv == 0:
			switch a {
			case 4:
				// Insert mode is approximated by overwrite behaviour.
			}
		}
	}
}

func (t *Terminal) setAlt(on bool) {
	if on == t.altActive {
		return
	}
	if on {
		t.alt = newScreen(t.cols, t.rows)
		t.alt.fg, t.alt.bg, t.alt.attr = t.current.fg, t.current.bg, t.current.attr
		t.altActive = true
		t.current = &t.alt
	} else {
		t.altActive = false
		t.current = &t.primary
	}
}

func (t *Terminal) saveCursor(slot int) {
	s := t.current
	t.savedCursor[slot&1] = cursorState{x: s.cx, y: s.cy, fg: s.fg, bg: s.bg, attr: s.attr, valid: true}
}

func (t *Terminal) restoreCursor(slot int) {
	c := t.savedCursor[slot&1]
	if !c.valid {
		return
	}
	s := t.current
	s.cx = clamp(c.x, 0, t.cols-1)
	s.cy = clamp(c.y, 0, t.rows-1)
	s.fg, s.bg, s.attr = c.fg, c.bg, c.attr
	s.wrap = false
}

func (t *Terminal) eraseDisplay(mode int) {
	s := t.current
	switch mode {
	case 0:
		t.eraseLine(0)
		for y := s.cy + 1; y < t.rows; y++ {
			t.clearRow(y)
		}
	case 1:
		for y := 0; y < s.cy; y++ {
			t.clearRow(y)
		}
		t.eraseLine(1)
	case 2:
		t.clearAll(s)
	}
}

func (t *Terminal) eraseLine(mode int) {
	s := t.current
	start := s.cy * t.cols
	switch mode {
	case 0:
		for x := s.cx; x < t.cols; x++ {
			s.cells[start+x] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
		}
	case 1:
		for x := 0; x <= s.cx && x < t.cols; x++ {
			s.cells[start+x] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
		}
	case 2:
		t.clearRow(s.cy)
	}
}

func (t *Terminal) clearAll(s *screen) {
	for i := range s.cells {
		s.cells[i] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
	}
}

func (t *Terminal) insertLines(n int) {
	s := t.current
	if s.cy < s.top || s.cy > s.bottom {
		return
	}
	if n > s.bottom-s.cy+1 {
		n = s.bottom - s.cy + 1
	}
	copy(s.cells[(s.cy+n)*t.cols:(s.bottom+1)*t.cols], s.cells[s.cy*t.cols:(s.bottom+1-n)*t.cols])
	for y := s.cy; y < s.cy+n; y++ {
		t.clearRow(y)
	}
}

func (t *Terminal) deleteLines(n int) {
	s := t.current
	if s.cy < s.top || s.cy > s.bottom {
		return
	}
	if n > s.bottom-s.cy+1 {
		n = s.bottom - s.cy + 1
	}
	copy(s.cells[s.cy*t.cols:(s.bottom+1-n)*t.cols], s.cells[(s.cy+n)*t.cols:(s.bottom+1)*t.cols])
	for y := s.bottom - n + 1; y <= s.bottom; y++ {
		t.clearRow(y)
	}
}

func (t *Terminal) deleteChars(n int) {
	s := t.current
	if n < 1 {
		return
	}
	start := s.cy * t.cols
	end := (s.cy + 1) * t.cols
	copy(s.cells[start+s.cx:end-n], s.cells[start+s.cx+n:end])
	for x := end - n; x < end; x++ {
		s.cells[x] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
	}
}

func (t *Terminal) insertChars(n int) {
	s := t.current
	if n < 1 {
		return
	}
	start := s.cy * t.cols
	end := (s.cy + 1) * t.cols
	copy(s.cells[start+s.cx+n:end], s.cells[start+s.cx:end-n])
	for x := s.cx; x < s.cx+n && x < t.cols; x++ {
		s.cells[start+x] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
	}
}

func (t *Terminal) eraseChars(n int) {
	s := t.current
	for x := s.cx; x < s.cx+n && x < t.cols; x++ {
		s.cells[s.cy*t.cols+x] = Cell{Rune: ' ', Fg: s.fg, Bg: s.bg}
	}
}

func (t *Terminal) moveCursor(dx, dy int) {
	s := t.current
	s.cx = clamp(s.cx+dx, 0, t.cols-1)
	s.cy = clamp(s.cy+dy, s.top, s.bottom)
	s.wrap = false
}

func (t *Terminal) reset() {
	t.primary = newScreen(t.cols, t.rows)
	t.alt = newScreen(t.cols, t.rows)
	t.current = &t.primary
	t.altActive = false
	t.cursorVisible = true
	t.AutoWrap = true
	t.AppCursor = false
	t.AppKeypad = false
	t.BracketedPaste = false
	t.MouseMode = 0
	t.MouseSGR = false
	t.OriginMode = false
	t.title = ""
}

func (t *Terminal) sgr(args []int) {
	if len(args) == 0 {
		args = []int{0}
	}
	s := t.current
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == 0:
			s.fg, s.bg, s.attr = DefaultColor, DefaultColor, 0
		case a == 1:
			s.attr |= Bold
		case a == 2:
			s.attr |= Dim
		case a == 3:
			s.attr |= Italic
		case a == 4:
			s.attr |= Underline
		case a == 5:
			s.attr |= Blink
		case a == 7:
			s.attr |= Reverse
		case a == 8:
			s.attr |= Hidden
		case a == 9:
			s.attr |= Strike
		case a == 21:
			s.attr &^= Bold
		case a == 22:
			s.attr &^= Bold | Dim
		case a == 23:
			s.attr &^= Italic
		case a == 24:
			s.attr &^= Underline
		case a == 25:
			s.attr &^= Blink
		case a == 27:
			s.attr &^= Reverse
		case a == 28:
			s.attr &^= Hidden
		case a == 29:
			s.attr &^= Strike
		case a >= 30 && a <= 37:
			s.fg = ANSI(uint8(a - 30))
		case a == 39:
			s.fg = DefaultColor
		case a >= 40 && a <= 47:
			s.bg = ANSI(uint8(a - 40))
		case a == 49:
			s.bg = DefaultColor
		case a >= 90 && a <= 97:
			s.fg = ANSI(uint8(a - 90 + 8))
		case a >= 100 && a <= 107:
			s.bg = ANSI(uint8(a - 100 + 8))
		case a == 38 || a == 48 || a == 58:
			if i+1 >= len(args) {
				return
			}
			var c Color
			switch args[i+1] {
			case 5:
				if i+2 < len(args) {
					c = ANSI(uint8(clamp(args[i+2], 0, 255)))
					i += 2
				}
			case 2:
				if i+4 < len(args) {
					c = RGB(uint8(clamp(args[i+2], 0, 255)), uint8(clamp(args[i+3], 0, 255)), uint8(clamp(args[i+4], 0, 255)))
					i += 4
				}
			}
			if a == 38 {
				s.fg = c
			} else if a == 48 {
				s.bg = c
			}
		}
	}
}

func (t *Terminal) handleOSC(seq string) {
	parts := strings.SplitN(seq, ";", 2)
	if len(parts) != 2 {
		return
	}
	switch parts[0] {
	case "0", "2":
		t.title = parts[1]
		if t.OnTitle != nil {
			t.OnTitle(parts[1])
		}
	case "52":
		fields := strings.SplitN(parts[1], ";", 2)
		if len(fields) == 2 && t.OnClipboard != nil {
			// Only clipboard "c" and the default selection are honoured.
			if fields[0] == "c" || fields[0] == "" {
				if data, err := base64.StdEncoding.DecodeString(fields[1]); err == nil {
					t.OnClipboard(string(data))
				}
			}
		}
	}
}

func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	fields := strings.Split(s, ";")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			out = append(out, 0)
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}

func arg(args []int, i, def int) int {
	if i >= len(args) {
		return def
	}
	if args[i] == 0 {
		return def
	}
	return args[i]
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Search finds matches across scrollback and the screen.
type SearchMatch struct {
	Line  int // virtual line
	Start int // rune column
	End   int
}

// Search returns every match of query. When regexMode is set, query is a Go
// regular expression; otherwise it is a literal string.
func (t *Terminal) Search(query string, caseSensitive, regexMode bool) ([]SearchMatch, error) {
	if query == "" {
		return nil, nil
	}
	var re *regexp.Regexp
	if regexMode {
		flags := ""
		if !caseSensitive {
			flags = "(?i)"
		}
		compiled, err := regexp.Compile(flags + query)
		if err != nil {
			return nil, err
		}
		re = compiled
	}
	needle := query
	if !caseSensitive && !regexMode {
		needle = strings.ToLower(query)
	}
	var out []SearchMatch
	for line := 0; line < t.TotalLines(); line++ {
		text := t.TextLine(line)
		if re != nil {
			for _, loc := range re.FindAllStringIndex(text, -1) {
				out = append(out, SearchMatch{Line: line, Start: runeLen(text[:loc[0]]), End: runeLen(text[:loc[1]])})
			}
			continue
		}
		hay := text
		if !caseSensitive {
			hay = strings.ToLower(text)
		}
		offset := 0
		for {
			idx := strings.Index(hay[offset:], needle)
			if idx < 0 {
				break
			}
			start := offset + idx
			out = append(out, SearchMatch{
				Line:  line,
				Start: runeLen(text[:start]),
				End:   runeLen(text[:start+len(query)]),
			})
			offset = start + len(query)
		}
	}
	return out, nil
}

// TextInRange extracts text between two virtual positions, inclusive, and
// joins selected lines with newlines. Coordinates are rune columns.
func (t *Terminal) TextInRange(startLine, startCol, endLine, endCol int) string {
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}
	var b strings.Builder
	for line := startLine; line <= endLine; line++ {
		runes := []rune(t.TextLine(line))
		from, to := 0, len(runes)
		if line == startLine {
			from = clamp(startCol, 0, len(runes))
		}
		if line == endLine {
			to = clamp(endCol, 0, len(runes))
		}
		if from < to {
			b.WriteString(string(runes[from:to]))
		}
		if line != endLine {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// runeWidth reports the display width of r: 0 for combining marks, 2 for East
// Asian wide characters and emoji, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32:
		return 0
	case r >= 0x0300 && r <= 0x036F, r >= 0x1AB0 && r <= 0x1AFF, r >= 0x1DC0 && r <= 0x1DFF,
		r >= 0x20D0 && r <= 0x20FF, r >= 0xFE20 && r <= 0xFE2F:
		return 0
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}
