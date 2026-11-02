package gui

import (
	"fmt"
	"image/color"
	"runtime"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/vterm"
)

// termCols/termRows are the fallback grid before the window has been laid out.
const (
	maxRunPerRow = 16
	maxPoolRows  = 80
)

// TerminalView is a canvas widget that renders the VT emulator and forwards
// keystrokes to the remote shell. Rendering happens on the UI goroutine; remote
// output arrives through Write, which the caller must invoke via fyne.Do.
type TerminalView struct {
	widget.BaseWidget

	vt       *vterm.Terminal
	OnInput  func([]byte)
	OnResize func(cols, rows int)
	OnFocus  func()

	mu       sync.Mutex
	size     fyne.Size
	fontSize float32
	cellW    float32
	cellH    float32
	offset   int
	closed   bool

	// Selection is stored in virtual-line and cell coordinates so it keeps its
	// meaning while the view scrolls or new output arrives. End positions are
	// inclusive for rendering and converted to exclusive rune offsets on copy.
	selecting                   bool
	hasSelection                bool
	selAnchorLine, selAnchorCol int
	selCursorLine, selCursorCol int
}

var (
	_ fyne.Shortcutable = (*TerminalView)(nil)
	_ fyne.Tabbable     = (*TerminalView)(nil)
	_ fyne.Draggable    = (*TerminalView)(nil)
	_ desktop.Mouseable = (*TerminalView)(nil)
)

// NewTerminalView creates a terminal with a 80x24 default grid.
func NewTerminalView(fontSize float32) *TerminalView {
	if fontSize <= 0 {
		fontSize = 14
	}
	t := &TerminalView{
		vt:       vterm.New(80, 24, 10000),
		fontSize: fontSize,
	}
	t.ExtendBaseWidget(t)
	t.vt.OnClipboard = func(s string) {
		if app := fyne.CurrentApp(); app != nil && app.Clipboard() != nil {
			app.Clipboard().SetContent(s)
		}
	}
	return t
}

// Emulator exposes the underlying terminal (used for tests and search).
func (t *TerminalView) Emulator() *vterm.Terminal { return t.vt }

// SetClosed marks the session as finished.
func (t *TerminalView) SetClosed() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.Refresh()
}

// Write feeds remote output into the emulator. Call from the UI goroutine.
func (t *TerminalView) Write(p []byte) {
	t.mu.Lock()
	t.vt.Write(p)
	t.offset = 0
	t.mu.Unlock()
	t.Refresh()
}

// Scrollback returns the current scroll offset.
func (t *TerminalView) Scrolled(ev *fyne.ScrollEvent) {
	t.mu.Lock()
	t.offset = clampInt(t.offset+int(ev.Scrolled.DY)*3, 0, t.vt.ScrollbackLen())
	t.mu.Unlock()
	t.Refresh()
}

// Tapped focuses the terminal so it receives key events.
func (t *TerminalView) Tapped(*fyne.PointEvent) {
	t.focus()
}

func (t *TerminalView) focus() {
	if t.OnFocus != nil {
		t.OnFocus()
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
}

// AcceptsTab lets Tab and Shift+Tab reach the shell instead of moving focus.
func (t *TerminalView) AcceptsTab() bool { return true }

func (t *TerminalView) FocusGained() {}
func (t *TerminalView) FocusLost()   {}

// MouseDown starts a text selection and focuses the pane, matching normal
// terminal behaviour without swallowing keyboard focus after a drag.
func (t *TerminalView) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	t.focus()
	line, col := t.cellAt(ev.Position)
	t.mu.Lock()
	t.selecting = true
	t.hasSelection = false
	t.selAnchorLine, t.selAnchorCol = line, col
	t.selCursorLine, t.selCursorCol = line, col
	t.mu.Unlock()
	t.Refresh()
}

func (t *TerminalView) MouseUp(*desktop.MouseEvent) {
	t.mu.Lock()
	t.selecting = false
	t.mu.Unlock()
}

// Dragged extends the selection to the cell under the pointer.
func (t *TerminalView) Dragged(ev *fyne.DragEvent) {
	line, col := t.cellAt(ev.Position)
	t.mu.Lock()
	if !t.selecting {
		t.mu.Unlock()
		return
	}
	t.selCursorLine, t.selCursorCol = line, col
	t.hasSelection = line != t.selAnchorLine || col != t.selAnchorCol
	t.mu.Unlock()
	t.Refresh()
}

func (t *TerminalView) DragEnd() {
	t.mu.Lock()
	t.selecting = false
	t.mu.Unlock()
}

func (t *TerminalView) cellAt(pos fyne.Position) (int, int) {
	cw, ch := t.cellSize()
	if cw <= 0 || ch <= 0 {
		return 0, 0
	}
	col := int(pos.X / cw)
	row := int(pos.Y / ch)

	t.mu.Lock()
	total := t.vt.TotalLines()
	cols, _ := t.vt.Size()
	virtual := t.vt.ScrollbackLen() + row - t.offset
	t.mu.Unlock()

	if total < 1 {
		total = 1
	}
	if virtual < 0 {
		virtual = 0
	}
	if virtual >= total {
		virtual = total - 1
	}
	if col < 0 {
		col = 0
	}
	if col >= cols {
		col = cols - 1
	}
	return virtual, col
}

func (t *TerminalView) cellSize() (float32, float32) {
	t.mu.Lock()
	cw, ch := t.cellW, t.cellH
	t.mu.Unlock()
	if cw > 0 && ch > 0 {
		return cw, ch
	}
	cw = fyne.MeasureText("M", t.fontSize, fyne.TextStyle{Monospace: true}).Width
	ch = fyne.MeasureText("M", t.fontSize, fyne.TextStyle{Monospace: true}).Height
	if cw <= 0 {
		cw = t.fontSize * 0.6
	}
	if ch <= 0 {
		ch = t.fontSize * 1.25
	}
	t.mu.Lock()
	t.cellW, t.cellH = cw, ch
	t.mu.Unlock()
	return cw, ch
}

func (t *TerminalView) TypedRune(r rune) {
	if r < 0x20 || r == 0x7f {
		return
	}
	t.send([]byte(string(r)))
}

func (t *TerminalView) TypedKey(ev *fyne.KeyEvent) {
	mods := fyne.KeyModifier(0)
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		mods = d.CurrentKeyModifiers()
	}
	if mods&fyne.KeyModifierControl != 0 {
		name := string(ev.Name)
		if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
			if mods&fyne.KeyModifierShift != 0 {
				switch name {
				case "C":
					t.copySelection()
					return
				case "V":
					t.paste()
					return
				}
			}
			t.send([]byte{name[0] - 'A' + 1})
			return
		}
	}
	switch ev.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		t.send([]byte("\r"))
	case fyne.KeyTab:
		if mods&fyne.KeyModifierShift != 0 {
			t.send([]byte("\x1b[Z"))
		} else {
			t.send([]byte("\t"))
		}
	case fyne.KeyBackspace:
		t.send([]byte("\x7f"))
	case fyne.KeyEscape:
		t.send([]byte("\x1b"))
	case fyne.KeyDelete:
		t.send([]byte("\x1b[3~"))
	case fyne.KeyInsert:
		t.send([]byte("\x1b[2~"))
	case fyne.KeyUp:
		t.send([]byte("\x1b[A"))
	case fyne.KeyDown:
		t.send([]byte("\x1b[B"))
	case fyne.KeyRight:
		t.send([]byte("\x1b[C"))
	case fyne.KeyLeft:
		t.send([]byte("\x1b[D"))
	case fyne.KeyHome:
		t.send([]byte("\x1b[H"))
	case fyne.KeyEnd:
		t.send([]byte("\x1b[F"))
	case fyne.KeyPageUp:
		t.mu.Lock()
		t.offset = clampInt(t.offset+10, 0, t.vt.ScrollbackLen())
		t.mu.Unlock()
		t.Refresh()
	case fyne.KeyPageDown:
		t.mu.Lock()
		t.offset = clampInt(t.offset-10, 0, t.vt.ScrollbackLen())
		t.mu.Unlock()
		t.Refresh()
	case fyne.KeyF1:
		t.send([]byte("\x1bOP"))
	case fyne.KeyF2:
		t.send([]byte("\x1bOQ"))
	case fyne.KeyF3:
		t.send([]byte("\x1bOR"))
	case fyne.KeyF4:
		t.send([]byte("\x1bOS"))
	case fyne.KeyF5:
		t.send([]byte("\x1b[15~"))
	case fyne.KeyF6:
		t.send([]byte("\x1b[17~"))
	case fyne.KeyF7:
		t.send([]byte("\x1b[18~"))
	case fyne.KeyF8:
		t.send([]byte("\x1b[19~"))
	case fyne.KeyF9:
		t.send([]byte("\x1b[20~"))
	case fyne.KeyF10:
		t.send([]byte("\x1b[21~"))
	case fyne.KeyF11:
		t.send([]byte("\x1b[23~"))
	case fyne.KeyF12:
		t.send([]byte("\x1b[24~"))
	}
}

// TypedShortcut is where Fyne delivers every key press that has a modifier.
// Without it, Fyne consumes Ctrl+C, Ctrl+V and custom Ctrl+Shift combinations
// before TypedKey can forward them to the remote shell.
func (t *TerminalView) TypedShortcut(s fyne.Shortcut) {
	switch sc := s.(type) {
	case *fyne.ShortcutCopy:
		// Ctrl+C on Linux/Windows must interrupt the shell; Cmd+C on macOS and
		// Ctrl+Insert (Secondary) are copies.
		if sc.Secondary || runtime.GOOS == "darwin" {
			t.copySelection()
			return
		}
		t.sendControl('C')
	case *fyne.ShortcutPaste:
		// Ctrl+V / Cmd+V and Shift+Insert.
		t.paste()
	case *fyne.ShortcutCut:
		if runtime.GOOS != "darwin" {
			t.sendControl('X')
		}
	case *fyne.ShortcutUndo:
		t.sendControl('Z')
	case *fyne.ShortcutRedo:
		t.sendControl('Y')
	case *fyne.ShortcutSelectAll:
		if runtime.GOOS == "darwin" {
			t.selectAll()
		} else {
			t.sendControl('A')
		}
	case *desktop.CustomShortcut:
		t.customShortcut(sc)
	}
}

func (t *TerminalView) customShortcut(sc *desktop.CustomShortcut) {
	key := sc.KeyName
	mods := sc.Modifier
	command := mods&fyne.KeyModifierControl != 0
	if runtime.GOOS == "darwin" {
		command = mods&fyne.KeyModifierSuper != 0
	}

	switch {
	case command && key == fyne.KeyC && mods&fyne.KeyModifierShift != 0:
		t.copySelection()
		return
	case command && key == fyne.KeyV && mods&fyne.KeyModifierShift != 0:
		t.paste()
		return
	case runtime.GOOS == "darwin" && key == fyne.KeyV && mods == fyne.KeyModifierControl:
		// Cmd+V pastes; Ctrl+V keeps its literal-next meaning on macOS.
		t.sendControl('V')
		return
	case mods&fyne.KeyModifierAlt != 0 && !command:
		// Alt+key is the usual meta prefix in readline and Emacs.
		if name := string(key); len(name) == 1 {
			t.send([]byte("\x1b"))
			t.send([]byte(strings.ToLower(name)))
		}
		return
	}

	if command {
		if b, ok := controlByte(key); ok {
			t.send([]byte{b})
		}
	}
}

func (t *TerminalView) sendControl(letter byte) {
	if letter >= 'a' && letter <= 'z' {
		letter -= 'a' - 'A'
	}
	if letter < 'A' || letter > 'Z' {
		return
	}
	t.send([]byte{letter - 'A' + 1})
}

func controlByte(key fyne.KeyName) (byte, bool) {
	name := string(key)
	if len(name) != 1 {
		return 0, false
	}
	c := name[0]
	switch {
	case c >= 'a' && c <= 'z':
		return c - 'a' + 1, true
	case c >= 'A' && c <= 'Z':
		return c - 'A' + 1, true
	case c == ' ' || c == '@':
		return 0, true
	case c == '[':
		return 0x1b, true
	case c == '\\':
		return 0x1c, true
	case c == ']':
		return 0x1d, true
	case c == '^':
		return 0x1e, true
	case c == '_':
		return 0x1f, true
	case c == '?':
		return 0x7f, true
	}
	return 0, false
}

func (t *TerminalView) send(p []byte) {
	if t.OnInput != nil {
		t.OnInput(p)
	}
}

func (t *TerminalView) copySelection() {
	text := t.selectedText()
	if text == "" {
		// No selection: the historical behaviour is copy-all, but strip the
		// newline padding from the empty bottom of the screen.
		t.mu.Lock()
		text = strings.TrimRight(t.vt.PlainText(), "\n")
		t.mu.Unlock()
	}
	if text == "" {
		return
	}
	app := fyne.CurrentApp()
	if app == nil || app.Clipboard() == nil {
		return
	}
	app.Clipboard().SetContent(text)
}

func (t *TerminalView) selectedText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasSelection {
		return ""
	}
	startLine, startCol, endLine, endCol := t.selectionBounds()
	from := t.runeColumn(startLine, startCol)
	to := t.runeColumn(endLine, endCol) + 1
	return t.vt.TextInRange(startLine, from, endLine, to)
}

// selectionBounds normalises anchor and cursor into inclusive coordinates.
// The caller must hold t.mu.
func (t *TerminalView) selectionBounds() (int, int, int, int) {
	startLine, startCol := t.selAnchorLine, t.selAnchorCol
	endLine, endCol := t.selCursorLine, t.selCursorCol
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	return startLine, startCol, endLine, endCol
}

// runeColumn converts a cell column to the rune index used by TextInRange.
// Wide characters occupy two cells but only one rune.
func (t *TerminalView) runeColumn(line, cell int) int {
	if cell <= 0 {
		return 0
	}
	cells := t.vt.LineAt(line)
	n := 0
	for x := 0; x < cell && x < len(cells); x++ {
		if cells[x].Rune != 0 {
			n++
		}
	}
	return n
}

func (t *TerminalView) selectAll() {
	t.mu.Lock()
	total := t.vt.TotalLines()
	cols, _ := t.vt.Size()
	t.selAnchorLine, t.selAnchorCol = 0, 0
	t.selCursorLine, t.selCursorCol = total-1, cols-1
	t.hasSelection = total > 0
	t.mu.Unlock()
	t.Refresh()
}

func (t *TerminalView) paste() {
	app := fyne.CurrentApp()
	if app == nil || app.Clipboard() == nil {
		return
	}
	text := app.Clipboard().Content()
	if text == "" {
		return
	}
	t.send([]byte(text))
}

// CreateRenderer implements fyne.Widget.
func (t *TerminalView) CreateRenderer() fyne.WidgetRenderer {
	r := &termRenderer{w: t, bg: canvas.NewRectangle(color.Transparent), cursor: canvas.NewRectangle(color.Transparent)}
	r.bg.FillColor = theme.Color(theme.ColorNameBackground)
	for i := 0; i < maxPoolRows*maxRunPerRow; i++ {
		txt := canvas.NewText("", theme.Color(theme.ColorNameForeground))
		txt.TextSize = t.fontSize
		txt.TextStyle = fyne.TextStyle{Monospace: true}
		txt.Hide()
		r.runs = append(r.runs, txt)
	}
	for i := 0; i < maxPoolRows; i++ {
		sel := canvas.NewRectangle(theme.Color(theme.ColorNameSelection))
		sel.Hide()
		r.selection = append(r.selection, sel)
	}
	r.cursor.FillColor = terminalCursorColor()
	r.cursor.Hide()
	return r
}

func terminalCursorColor() color.Color {
	cursorCol := theme.Color(theme.ColorNamePrimary)
	if c, ok := cursorCol.(color.NRGBA); ok {
		c.A = 160
		return c
	}
	return cursorCol
}

type termRun struct {
	text   string
	x      int
	fg, bg color.Color
	bold   bool
	italic bool
	under  bool
}

type termRenderer struct {
	w         *TerminalView
	bg        *canvas.Rectangle
	cursor    *canvas.Rectangle
	selection []*canvas.Rectangle
	runs      []*canvas.Text
	cellW     float32
	cellH     float32
	cols      int
	rows      int
}

func (r *termRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	cw, ch := r.cellWidth(), r.cellHeight()
	r.w.mu.Lock()
	r.w.size = size
	r.w.cellW = cw
	r.w.cellH = ch
	r.w.mu.Unlock()
	r.rebuild(size)
}

func (r *termRenderer) MinSize() fyne.Size {
	r.w.mu.Lock()
	cols, rows := r.w.vt.Size()
	r.w.mu.Unlock()
	return fyne.NewSize(float32(cols)*r.cellWidth(), float32(rows)*r.cellHeight())
}

func (r *termRenderer) Refresh() {
	r.w.mu.Lock()
	size := r.w.size
	r.w.mu.Unlock()
	if size.Width == 0 || size.Height == 0 {
		return
	}
	r.rebuild(size)
}

func (r *termRenderer) Objects() []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, 0, len(r.runs)+len(r.selection)+2)
	out = append(out, r.bg)
	for _, sel := range r.selection {
		out = append(out, sel)
	}
	out = append(out, r.cursor)
	for _, run := range r.runs {
		out = append(out, run)
	}
	return out
}

func (r *termRenderer) Destroy() {}

func (r *termRenderer) cellWidth() float32 {
	if r.cellW == 0 {
		r.cellW = fyne.MeasureText("M", r.w.fontSize, fyne.TextStyle{Monospace: true}).Width
		if r.cellW <= 0 {
			r.cellW = r.w.fontSize * 0.6
		}
	}
	return r.cellW
}

func (r *termRenderer) cellHeight() float32 {
	if r.cellH == 0 {
		r.cellH = fyne.MeasureText("M", r.w.fontSize, fyne.TextStyle{Monospace: true}).Height
		if r.cellH <= 0 {
			r.cellH = r.w.fontSize * 1.25
		}
	}
	return r.cellH
}

// rebuild repaints the visible screen into the pooled text objects.
func (r *termRenderer) rebuild(size fyne.Size) {
	cw, ch := r.cellWidth(), r.cellHeight()
	cols := maxInt(2, int(size.Width/cw))
	rows := maxInt(2, int(size.Height/ch))

	r.w.mu.Lock()
	if curCols, curRows := r.w.vt.Size(); curCols != cols || curRows != rows {
		r.w.vt.Resize(cols, rows)
		if r.w.OnResize != nil {
			r.w.OnResize(cols, rows)
		}
	}
	t := r.w.vt
	offset := r.w.offset
	sbLen := t.ScrollbackLen()
	cx, cy, cursorVisible := t.Cursor()
	hasSelection := r.w.hasSelection
	selStartLine, selStartCol, selEndLine, selEndCol := 0, 0, 0, 0
	if hasSelection {
		selStartLine, selStartCol, selEndLine, selEndCol = r.w.selectionBounds()
	}
	r.w.mu.Unlock()

	r.cols, r.rows = cols, rows
	fgDefault := theme.Color(theme.ColorNameForeground)
	bgDefault := theme.Color(theme.ColorNameBackground)
	r.bg.FillColor = bgDefault
	r.cursor.FillColor = terminalCursorColor()

	idx := 0
	for y := 0; y < rows; y++ {
		virtual := sbLen + y - offset
		line := t.LineAt(virtual)
		cursorX := -1
		if cursorVisible && offset == 0 && y == cy {
			cursorX = cx
		}
		runs := buildRuns(line, cols, cursorX, fgDefault, bgDefault)
		if len(runs) > maxRunPerRow {
			runs = runs[:maxRunPerRow]
		}
		for _, run := range runs {
			if idx >= len(r.runs) {
				break
			}
			obj := r.runs[idx]
			obj.Text = run.text
			obj.Color = run.fg
			obj.TextSize = r.w.fontSize
			obj.TextStyle = fyne.TextStyle{Monospace: true, Bold: run.bold, Italic: run.italic}
			obj.Move(fyne.NewPos(float32(run.x)*cw, float32(y)*ch))
			obj.Show()
			obj.Refresh()
			idx++
		}
	}
	for ; idx < len(r.runs); idx++ {
		r.runs[idx].Hide()
	}

	// Selection highlights are drawn under the text so the glyph colours keep
	// their meaning. One rectangle is enough because a selection is contiguous.
	selIdx := 0
	if hasSelection {
		for y := 0; y < rows && selIdx < len(r.selection); y++ {
			virtual := sbLen + y - offset
			if virtual < selStartLine || virtual > selEndLine {
				continue
			}
			from, to := 0, cols-1
			if virtual == selStartLine {
				from = selStartCol
			}
			if virtual == selEndLine {
				to = selEndCol
			}
			from = clampInt(from, 0, cols-1)
			to = clampInt(to, 0, cols-1)
			if from > to {
				continue
			}
			rect := r.selection[selIdx]
			rect.FillColor = theme.Color(theme.ColorNameSelection)
			rect.Move(fyne.NewPos(float32(from)*cw, float32(y)*ch))
			rect.Resize(fyne.NewSize(float32(to-from+1)*cw, ch))
			rect.Show()
			rect.Refresh()
			selIdx++
		}
	}
	for ; selIdx < len(r.selection); selIdx++ {
		r.selection[selIdx].Hide()
	}

	// Cursor block.
	if cursorVisible && offset == 0 && cy >= 0 && cy < rows {
		r.cursor.Move(fyne.NewPos(float32(cx)*cw, float32(cy)*ch))
		r.cursor.Resize(fyne.NewSize(cw, ch))
		r.cursor.Show()
	} else {
		r.cursor.Hide()
	}
	r.bg.Resize(size)
}

// buildRuns turns a line into colour runs, trimmed to the last painted cell.
func buildRuns(line vterm.Line, cols, cursorX int, fgDefault, bgDefault color.Color) []termRun {
	if line == nil {
		return nil
	}
	// Trim trailing blanks.
	last := -1
	for i := 0; i < len(line) && i < cols; i++ {
		c := line[i]
		if c.Rune != 0 && (c.Rune != ' ' || c.Bg.Mode != vterm.ColorDefault) {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	var runs []termRun
	var cur *termRun
	flush := func() {
		if cur != nil && cur.text != "" {
			runs = append(runs, *cur)
		}
		cur = nil
	}
	for x := 0; x <= last; x++ {
		c := line[x]
		if c.Rune == 0 {
			c.Rune = ' '
		}
		fg, bg := cellColors(c, fgDefault, bgDefault)
		bold := c.Attr&vterm.Bold != 0
		italic := c.Attr&vterm.Italic != 0
		under := c.Attr&vterm.Underline != 0
		if cursorX == x {
			fg, bg = bg, fg
			under = false
		}
		if cur == nil || cur.fg != fg || cur.bg != bg || cur.bold != bold || cur.italic != italic || cur.under != under {
			flush()
			cur = &termRun{x: x, fg: fg, bg: bg, bold: bold, italic: italic, under: under}
		}
		cur.text += string(c.Rune) + c.Comb
	}
	flush()
	return runs
}

// cellColors resolves a cell's palette colour against the current theme. The
// background is folded into the text colour when the widget cannot paint a
// per-run background, so reverse video is still readable.
func cellColors(c vterm.Cell, fgDefault, bgDefault color.Color) (color.Color, color.Color) {
	light := isLightBackground(bgDefault)
	fg, fgSet := resolveColor(c.Fg, light)
	bg, bgSet := resolveColor(c.Bg, light)
	if !fgSet {
		fg = fgDefault
	}
	if !bgSet {
		bg = bgDefault
	}
	if c.Attr&vterm.Reverse != 0 {
		fg, bg = bg, fg
	}
	if c.Attr&vterm.Dim != 0 {
		fg = dim(fg)
	}
	return fg, bg
}

func resolveColor(c vterm.Color, light bool) (color.Color, bool) {
	switch c.Mode {
	case vterm.ColorANSI:
		return ansiPalette(c.Index, light), true
	case vterm.ColorRGB:
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}, true
	default:
		return nil, false
	}
}

func isLightBackground(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return (299*uint64(r)+587*uint64(g)+114*uint64(b))/1000 > 0x8000
}

// ansiPalette returns the xterm 256-colour palette adapted to the terminal
// background. The dark palette is the usual xterm set. On light themes the
// first sixteen colours are darkened and washed-out cube colours are clamped,
// otherwise ANSI white and pastel output disappears into the white canvas.
func ansiPalette(i uint8, light bool) color.Color {
	if light {
		switch i {
		case 0:
			return color.NRGBA{0x20, 0x21, 0x24, 0xff}
		case 1:
			return color.NRGBA{0xb3, 0x26, 0x1e, 0xff}
		case 2:
			return color.NRGBA{0x2e, 0x7d, 0x32, 0xff}
		case 3:
			return color.NRGBA{0x8a, 0x61, 0x00, 0xff}
		case 4:
			return color.NRGBA{0x15, 0x65, 0xc0, 0xff}
		case 5:
			return color.NRGBA{0x7b, 0x1f, 0xa2, 0xff}
		case 6:
			return color.NRGBA{0x00, 0x79, 0x6b, 0xff}
		case 7:
			return color.NRGBA{0x3c, 0x40, 0x43, 0xff}
		case 8:
			return color.NRGBA{0x5f, 0x63, 0x68, 0xff}
		case 9:
			return color.NRGBA{0xc5, 0x22, 0x1f, 0xff}
		case 10:
			return color.NRGBA{0x2e, 0x7d, 0x32, 0xff}
		case 11:
			return color.NRGBA{0x7a, 0x5a, 0x00, 0xff}
		case 12:
			return color.NRGBA{0x19, 0x67, 0xd2, 0xff}
		case 13:
			return color.NRGBA{0x8e, 0x24, 0xaa, 0xff}
		case 14:
			return color.NRGBA{0x00, 0x79, 0x6b, 0xff}
		case 15:
			return color.NRGBA{0x20, 0x21, 0x24, 0xff}
		}
		return darkenForLight(standardPalette(i))
	}
	return standardPalette(i)
}

// standardPalette is the usual xterm 256-colour palette for dark terminals.
func standardPalette(i uint8) color.Color {
	switch i {
	case 0:
		return color.NRGBA{0x1d, 0x1f, 0x21, 0xff}
	case 1:
		return color.NRGBA{0xcc, 0x66, 0x66, 0xff}
	case 2:
		return color.NRGBA{0xb5, 0xbd, 0x68, 0xff}
	case 3:
		return color.NRGBA{0xf0, 0xc6, 0x74, 0xff}
	case 4:
		return color.NRGBA{0x81, 0xa2, 0xbe, 0xff}
	case 5:
		return color.NRGBA{0xb2, 0x94, 0xbb, 0xff}
	case 6:
		return color.NRGBA{0x8a, 0xbe, 0xb7, 0xff}
	case 7:
		return color.NRGBA{0xc5, 0xc8, 0xc6, 0xff}
	case 8:
		return color.NRGBA{0x66, 0x66, 0x66, 0xff}
	case 9:
		return color.NRGBA{0xd5, 0x4e, 0x53, 0xff}
	case 10:
		return color.NRGBA{0x9e, 0xc1, 0x6a, 0xff}
	case 11:
		return color.NRGBA{0xe6, 0xc0, 0x7c, 0xff}
	case 12:
		return color.NRGBA{0x6c, 0x99, 0xbb, 0xff}
	case 13:
		return color.NRGBA{0xa6, 0x7c, 0xb8, 0xff}
	case 14:
		return color.NRGBA{0x7f, 0xc1, 0xbf, 0xff}
	case 15:
		return color.NRGBA{0xf0, 0xf0, 0xf0, 0xff}
	}
	if i >= 232 { // grayscale ramp
		v := 8 + (int(i)-232)*10
		return color.NRGBA{uint8(v), uint8(v), uint8(v), 0xff}
	}
	// 6x6x6 colour cube.
	n := int(i) - 16
	levels := []uint8{0, 95, 135, 175, 215, 255}
	return color.NRGBA{levels[n/36], levels[(n/6)%6], levels[n%6], 0xff}
}

// darkenForLight keeps palette colours legible on a white background without
// changing their hue.
func darkenForLight(c color.Color) color.Color {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	lum := 0.299*float64(n.R) + 0.587*float64(n.G) + 0.114*float64(n.B)
	const target = 128.0
	if lum <= target {
		return n
	}
	scale := target / lum
	return color.NRGBA{
		R: uint8(float64(n.R) * scale),
		G: uint8(float64(n.G) * scale),
		B: uint8(float64(n.B) * scale),
		A: n.A,
	}
}

func dim(c color.Color) color.Color {
	r, g, b, a := c.RGBA()
	return color.NRGBA{R: uint8(r / 3), G: uint8(g / 3), B: uint8(b / 3), A: uint8(a / 256)}
}

// composeVisible is used by the status line to show a short preview.
func composeVisible(t *vterm.Terminal, max int) string {
	text := strings.TrimRight(t.PlainText(), "\n")
	if len(text) > max {
		text = text[len(text)-max:]
	}
	return fmt.Sprintf("%q", text)
}
