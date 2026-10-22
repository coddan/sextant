package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/lipgloss"

	"github.com/coddan/sextant/internal/vterm"
)

// Styles for the whole interface. Kept in one place so the look is consistent
// and easy to retheme.
var (
	colorAccent = lipgloss.Color("39")
	colorMuted  = lipgloss.Color("245")
	colorOK     = lipgloss.Color("42")
	colorWarn   = lipgloss.Color("214")
	colorCrit   = lipgloss.Color("203")
	colorBorder = lipgloss.Color("238")
	colorFg     = lipgloss.Color("252")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(colorAccent).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().Foreground(colorMuted)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(colorBorder).
			Padding(0, 1)

	errorStyle   = lipgloss.NewStyle().Foreground(colorCrit).Bold(true)
	successStyle = lipgloss.NewStyle().Foreground(colorOK)
	warnStyle    = lipgloss.NewStyle().Foreground(colorWarn)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(colorAccent)

	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(colorMuted)
	valueStyle = lipgloss.NewStyle().Foreground(colorFg)
)

// severityStyle maps model.Threshold output to a style.
func severityStyle(level string) lipgloss.Style {
	switch level {
	case "crit":
		return lipgloss.NewStyle().Foreground(colorCrit)
	case "warn":
		return lipgloss.NewStyle().Foreground(colorWarn)
	default:
		return lipgloss.NewStyle().Foreground(colorOK)
	}
}

// cellStyle maps a terminal cell onto a lipgloss style, so remote programs look
// the way they do in any other terminal.
func cellStyle(c vterm.Cell) lipgloss.Style {
	style := lipgloss.NewStyle()
	if col, ok := termColor(c.Fg); ok {
		style = style.Foreground(col)
	}
	if col, ok := termColor(c.Bg); ok {
		style = style.Background(col)
	}
	if c.Attr&vterm.Bold != 0 {
		style = style.Bold(true)
	}
	if c.Attr&vterm.Dim != 0 {
		style = style.Faint(true)
	}
	if c.Attr&vterm.Italic != 0 {
		style = style.Italic(true)
	}
	if c.Attr&vterm.Underline != 0 {
		style = style.Underline(true)
	}
	if c.Attr&vterm.Strike != 0 {
		style = style.Strikethrough(true)
	}
	if c.Attr&vterm.Reverse != 0 {
		style = style.Reverse(true)
	}
	return style
}

// termColor converts an emulator colour into a lipgloss colour, reporting false
// for the terminal default.
func termColor(c vterm.Color) (lipgloss.TerminalColor, bool) {
	switch c.Mode {
	case vterm.ColorANSI:
		return lipgloss.Color(strconv.Itoa(int(c.Index))), true
	case vterm.ColorRGB:
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)), true
	default:
		return nil, false
	}
}
