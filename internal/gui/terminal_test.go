package gui

import (
	"fyne.io/fyne/v2/driver/desktop"
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestIsLightBackground(t *testing.T) {
	if !isLightBackground(color.White) {
		t.Fatal("white should be detected as a light background")
	}
	if isLightBackground(color.Black) {
		t.Fatal("black should be detected as a dark background")
	}
}

func TestANSIWhiteIsReadableOnLightTheme(t *testing.T) {
	light := color.NRGBAModel.Convert(ansiPalette(7, true)).(color.NRGBA)
	if light.R > 0x80 || light.G > 0x80 || light.B > 0x80 {
		t.Fatalf("light-theme ANSI white = %#v, want a dark foreground", light)
	}
	if got := ansiPalette(7, false); got != standardPalette(7) {
		t.Fatal("dark theme ANSI white changed")
	}
}

func TestLightPaletteDarkensBrightCubeColours(t *testing.T) {
	bright := color.NRGBAModel.Convert(ansiPalette(231, true)).(color.NRGBA) // 6x6x6 white
	if bright.R > 0x96 || bright.G > 0x96 || bright.B > 0x96 {
		t.Fatalf("light-theme cube white = %#v, want a darker colour", bright)
	}
}

func TestTerminalRendererTracksTheme(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	a.Settings().SetTheme(theme.LightTheme())
	v := NewTerminalView(13)
	r := v.CreateRenderer().(*termRenderer)
	r.Layout(fyne.NewSize(240, 120))
	if got, want := r.bg.FillColor, theme.Color(theme.ColorNameBackground); got != want {
		t.Fatalf("light background = %v, want %v", got, want)
	}

	a.Settings().SetTheme(theme.DarkTheme())
	r.Refresh()
	if got, want := r.bg.FillColor, theme.Color(theme.ColorNameBackground); got != want {
		t.Fatalf("dark background = %v, want %v", got, want)
	}
}

func newShortcutView(t *testing.T) (*TerminalView, fyne.App, *[]byte) {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	v := NewTerminalView(13)
	var got []byte
	v.OnInput = func(p []byte) { got = append(got, p...) }
	return v, a, &got
}

func TestTerminalShortcutsReachShell(t *testing.T) {
	v, _, got := newShortcutView(t)
	v.TypedShortcut(&fyne.ShortcutCopy{})
	v.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyD, Modifier: fyne.KeyModifierControl})
	if string(*got) != "\x03\x04" {
		t.Fatalf("sent %q, want ^C^D", *got)
	}
}

func TestTerminalPasteShortcuts(t *testing.T) {
	v, a, got := newShortcutView(t)
	a.Clipboard().SetContent("hello")
	v.TypedShortcut(&fyne.ShortcutPaste{})
	v.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyV, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	if string(*got) != "hellohello" {
		t.Fatalf("sent %q", *got)
	}
}

func TestTerminalCopyAllAndSelection(t *testing.T) {
	v, a, _ := newShortcutView(t)
	v.vt.Write([]byte("hello world\r\nsecond"))
	copyKey := &desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}
	v.TypedShortcut(copyKey)
	if c := a.Clipboard().Content(); !strings.Contains(c, "hello world") || !strings.Contains(c, "second") {
		t.Fatalf("copy-all = %q", c)
	}
	_, rows := v.vt.Size()
	line := v.vt.TotalLines() - rows
	v.hasSelection = true
	v.selAnchorLine, v.selAnchorCol = line, 6
	v.selCursorLine, v.selCursorCol = line, 10
	v.TypedShortcut(copyKey)
	if c := a.Clipboard().Content(); c != "world" {
		t.Fatalf("selection copy = %q", c)
	}
}

func TestTerminalAcceptsTab(t *testing.T) {
	if !NewTerminalView(13).AcceptsTab() {
		t.Fatal("tab must reach the shell")
	}
}

func TestMouseWheelSequence(t *testing.T) {
	if got, want := string(mouseWheelSequence(true, 4, 2, true)), "\x1b[<64;5;3M\x1b[<64;5;3m"; got != want {
		t.Fatalf("SGR wheel up = %q, want %q", got, want)
	}
	want := []byte{0x1b, '[', 'M', 32 + 65, 32 + 1, 32 + 1, 0x1b, '[', 'M', 32 + 3, 32 + 1, 32 + 1}
	if got := mouseWheelSequence(false, 0, 0, false); string(got) != string(want) {
		t.Fatalf("X10 wheel down = %q, want %q", got, want)
	}
}

func TestWheelScrollsLocalScrollback(t *testing.T) {
	v, _, got := newShortcutView(t)
	v.vt.Resize(80, 24)
	for i := 0; i < 100; i++ {
		v.vt.Write([]byte("line\r\n"))
	}
	v.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, 3)})
	if v.offset == 0 {
		t.Fatal("wheel up did not scroll back")
	}
	if len(*got) != 0 {
		t.Fatalf("wheel sent %q to a shell without mouse reporting", *got)
	}
}

func TestGridCellClampsToViewport(t *testing.T) {
	if col, row := gridCell(fyne.NewPos(10, 20), 10, 20, 80, 24); col != 1 || row != 1 {
		t.Fatalf("cell = %d,%d, want 1,1", col, row)
	}
	if col, row := gridCell(fyne.NewPos(-5, -5), 10, 20, 80, 24); col != 0 || row != 0 {
		t.Fatalf("negative cell = %d,%d, want 0,0", col, row)
	}
	if col, row := gridCell(fyne.NewPos(5000, 5000), 10, 20, 80, 24); col != 79 || row != 23 {
		t.Fatalf("overflow cell = %d,%d, want 79,23", col, row)
	}
}
