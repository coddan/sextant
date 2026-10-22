package vterm

import (
	"strings"
	"testing"
)

func TestPlainTextAndNewlines(t *testing.T) {
	vt := New(20, 5, 100)
	vt.Write([]byte("hello\r\nworld"))
	if got := vt.TextLine(0); got != "hello" {
		t.Fatalf("line0 = %q", got)
	}
	if got := vt.TextLine(1); got != "world" {
		t.Fatalf("line1 = %q", got)
	}
	x, y, visible := vt.Cursor()
	if x != 5 || y != 1 || !visible {
		t.Fatalf("cursor = %d,%d,%v", x, y, visible)
	}
}

func TestWrapping(t *testing.T) {
	vt := New(5, 3, 10)
	vt.Write([]byte("abcdef"))
	if got := vt.TextLine(0); got != "abcde" {
		t.Fatalf("line0 = %q", got)
	}
	if got := vt.TextLine(1); got != "f" {
		t.Fatalf("line1 = %q", got)
	}
}

func TestCursorMovementAndErase(t *testing.T) {
	vt := New(10, 4, 10)
	vt.Write([]byte("abcdefghij"))
	vt.Write([]byte("\x1b[2;3H")) // row 2, col 3
	vt.Write([]byte("X"))
	if got := vt.CellAt(2, 1); got.Rune != 'X' {
		t.Fatalf("cursor move failed: %q", got.Rune)
	}
	vt.Write([]byte("\x1b[K")) // erase from the cursor to the end of line
	if got := vt.TextLine(1); got != "  X" {
		t.Fatalf("erase line failed: %q", got)
	}
	vt.Write([]byte("\x1b[2J"))
	for y := 0; y < 4; y++ {
		if vt.TextLine(y) != "" {
			t.Fatalf("screen not cleared at %d: %q", y, vt.TextLine(y))
		}
	}
}

func TestSGR(t *testing.T) {
	vt := New(10, 2, 10)
	vt.Write([]byte("\x1b[1;31mred\x1b[0m normal"))
	c := vt.CellAt(0, 0)
	if c.Attr&Bold == 0 {
		t.Fatal("bold not set")
	}
	if c.Fg.Mode != ColorANSI || c.Fg.Index != 1 {
		t.Fatalf("fg = %+v", c.Fg)
	}
	n := vt.CellAt(4, 0)
	if n.Fg.Mode != ColorDefault || n.Attr != 0 {
		t.Fatalf("reset failed: %+v %+v", n.Fg, n.Attr)
	}
	vt.Write([]byte("\r\n\x1b[38;2;1;2;3mX"))
	if got := vt.CellAt(0, 1); got.Fg != RGB(1, 2, 3) {
		t.Fatalf("rgb fg = %+v", got.Fg)
	}
}

func TestScrollbackAndSearch(t *testing.T) {
	vt := New(10, 3, 50)
	for i := 0; i < 10; i++ {
		vt.Write([]byte("line" + string(rune('0'+i)) + "\r\n"))
	}
	if vt.ScrollbackLen() < 8 {
		t.Fatalf("scrollback = %d", vt.ScrollbackLen())
	}
	if got := vt.TextLine(0); got != "line0" {
		t.Fatalf("oldest = %q", got)
	}
	matches, err := vt.Search("LINE3", false, false)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches = %v err=%v", matches, err)
	}
	matches, err = vt.Search("ame9$", false, true)
	if err != nil || len(matches) != 0 {
		t.Fatalf("regex anchored matches = %v err=%v", matches, err)
	}
	matches, _ = vt.Search("line[0-9]$", false, true)
	if len(matches) < 5 {
		t.Fatalf("regex matches = %v", matches)
	}
}

func TestAltScreenRestoresPrimary(t *testing.T) {
	vt := New(20, 3, 10)
	vt.Write([]byte("primary"))
	vt.Write([]byte("\x1b[?1049h"))
	vt.Write([]byte("alternate"))
	if got := vt.TextLine(0); got != "alternate" {
		t.Fatalf("alt line = %q", got)
	}
	vt.Write([]byte("\x1b[?1049l"))
	if got := vt.TextLine(0); got != "primary" {
		t.Fatalf("primary not restored: %q", got)
	}
}

func TestSelectionExtraction(t *testing.T) {
	vt := New(20, 4, 10)
	vt.Write([]byte("alpha\r\nbravo\r\ncharlie"))
	got := vt.TextInRange(0, 1, 2, 3)
	if got != "lpha\nbravo\ncha" {
		t.Fatalf("selection = %q", got)
	}
}

func TestWideCharacters(t *testing.T) {
	vt := New(6, 2, 10)
	vt.Write([]byte("你好"))
	if got := vt.TextLine(0); got != "你好" {
		t.Fatalf("wide = %q", got)
	}
	if vt.CellAt(1, 0).Rune != 0 {
		t.Fatalf("wide continuation cell = %q", vt.CellAt(1, 0).Rune)
	}
}

func TestResizeKeepsContent(t *testing.T) {
	vt := New(10, 3, 10)
	vt.Write([]byte("abc\r\ndef"))
	vt.Resize(5, 4)
	if got := vt.TextLine(0); got != "abc" {
		t.Fatalf("after resize line0 = %q", got)
	}
	if got := vt.TextLine(1); got != "def" {
		t.Fatalf("after resize line1 = %q", got)
	}
}

func TestOSCTitle(t *testing.T) {
	vt := New(10, 2, 10)
	vt.Write([]byte("\x1b]2;my server\x07"))
	if vt.Title() != "my server" {
		t.Fatalf("title = %q", vt.Title())
	}
	if !strings.Contains(vt.PlainText(), "") {
		t.Fatal("unreachable")
	}
}
