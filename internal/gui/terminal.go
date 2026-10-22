package gui

import (
	"fmt"
	"image/color"
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

	mu       sync.Mutex
	size     fyne.Size
	fontSize float32
	offset   int
	closed   bool
}

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
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
}

func (t *TerminalView) FocusGained() {}
func (t *TerminalView) FocusLost()   {}

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
					t.copyAll()
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
		t.send([]byte("\t"))
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

func (t *TerminalView) send(p []byte) {
	if t.OnInput != nil {
		t.OnInput(p)
	}
}

func (t *TerminalView) copyAll() {
	app := fyne.CurrentApp()
	if app == nil || app.Clipboard() == nil {
		return
	}
	t.mu.Lock()
	text := t.vt.PlainText()
	t.mu.Unlock()
	app.Clipboard().SetContent(text)
}

func (t *TerminalView) paste() {
	app := fyne.CurrentApp()
	if app == nil || app.Clipboard() == nil {
		return
	}
	text := app.Clipboard().Content()
	if text != "" {
		t.send([]byte(text))
	}
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
	cursorCol := theme.Color(theme.ColorNamePrimary)
	if c, ok := cursorCol.(color.NRGBA); ok {
		c.A = 160
		cursorCol = c
	}
	r.cursor.FillColor = cursorCol
	r.cursor.Hide()
	return r
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
	w      *TerminalView
	bg     *canvas.Rectangle
	cursor *canvas.Rectangle
	runs   []*canvas.Text
	cellW  float32
	cellH  float32
	cols   int
	rows   int
}

func (r *termRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.w.mu.Lock()
	r.w.size = size
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
	out := make([]fyne.CanvasObject, 0, len(r.runs)+2)
	out = append(out, r.bg, r.cursor)
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
	r.w.mu.Unlock()

	r.cols, r.rows = cols, rows
	fgDefault := theme.Color(theme.ColorNameForeground)
	bgDefault := theme.Color(theme.ColorNameBackground)

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
	fg, fgSet := resolveColor(c.Fg)
	bg, bgSet := resolveColor(c.Bg)
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

func resolveColor(c vterm.Color) (color.Color, bool) {
	switch c.Mode {
	case vterm.ColorANSI:
		return ansiPalette(c.Index), true
	case vterm.ColorRGB:
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}, true
	default:
		return nil, false
	}
}

// ansiPalette is the standard xterm 256-colour palette.
func ansiPalette(i uint8) color.Color {
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
