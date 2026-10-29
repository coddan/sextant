package gui

import (
	"image/color"
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
