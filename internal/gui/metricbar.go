package gui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/model"
)

// metricBar is a compact label/value readout with a severity-coloured bar. It
// is used on the dashboard cards instead of the stock progress bar, whose
// colour cannot reflect CPU/RAM/disk thresholds.
type metricBar struct {
	widget.BaseWidget

	Label   string
	Percent *float64

	size fyne.Size
}

func newMetricBar(label string, percent *float64) *metricBar {
	b := &metricBar{Label: label, Percent: percent}
	b.ExtendBaseWidget(b)
	return b
}

func (b *metricBar) CreateRenderer() fyne.WidgetRenderer {
	r := &metricBarRenderer{
		w:     b,
		track: canvas.NewRectangle(color.Transparent),
		fill:  canvas.NewRectangle(color.Transparent),
		label: canvas.NewText("", color.White),
		value: canvas.NewText("", color.White),
	}
	r.label.TextStyle = fyne.TextStyle{Bold: true}
	r.value.Alignment = fyne.TextAlignTrailing
	r.track.CornerRadius = theme.Size(theme.SizeNameInputRadius)
	r.fill.CornerRadius = theme.Size(theme.SizeNameInputRadius)
	r.Refresh()
	return r
}

type metricBarRenderer struct {
	w     *metricBar
	track *canvas.Rectangle
	fill  *canvas.Rectangle
	label *canvas.Text
	value *canvas.Text
	ratio float32
}

func (r *metricBarRenderer) Layout(size fyne.Size) {
	r.w.size = size
	textH := r.label.MinSize().Height
	r.label.Resize(fyne.NewSize(size.Width, textH))
	r.label.Move(fyne.NewPos(0, 0))
	r.value.Resize(fyne.NewSize(size.Width, textH))
	r.value.Move(fyne.NewPos(0, 0))

	barH := float32(6)
	barY := textH + 2
	r.track.Resize(fyne.NewSize(size.Width, barH))
	r.track.Move(fyne.NewPos(0, barY))
	r.fill.Resize(fyne.NewSize(size.Width*r.ratio, barH))
	r.fill.Move(fyne.NewPos(0, barY))
}

func (r *metricBarRenderer) MinSize() fyne.Size {
	text := fyne.MeasureText(r.w.Label, theme.TextSize(), fyne.TextStyle{Bold: true})
	value := fyne.MeasureText("100%", theme.TextSize(), fyne.TextStyle{})
	return fyne.NewSize(maxF(text.Width, value.Width)+theme.Padding()*2, text.Height+2+6)
}

func (r *metricBarRenderer) Refresh() {
	r.label.Text = r.w.Label
	r.label.Color = theme.Color(theme.ColorNameForeground)
	r.label.TextSize = theme.TextSize()

	value := "—"
	fill := theme.Color(theme.ColorNameDisabled)
	r.ratio = 0
	if r.w.Percent != nil {
		pct := clampFloat(*r.w.Percent, 0, 100)
		r.ratio = float32(pct / 100)
		value = fmt.Sprintf("%.0f%%", pct)
		fill = thresholdColor(pct)
	}
	r.value.Text = value
	r.value.Color = fill
	r.value.TextSize = theme.TextSize()
	r.track.FillColor = theme.Color(theme.ColorNameSeparator)
	r.fill.FillColor = fill

	r.Layout(r.w.size)
	for _, o := range r.Objects() {
		o.Refresh()
	}
}

func (r *metricBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill, r.label, r.value}
}

func (r *metricBarRenderer) Destroy() {}

// thresholdColor maps a percentage to a green, amber or red bar.
func thresholdColor(pct float64) color.Color {
	switch model.Threshold(pct) {
	case "crit":
		return theme.Color(theme.ColorNameError)
	case "warn":
		return theme.Color(theme.ColorNameWarning)
	default:
		return theme.Color(theme.ColorNameSuccess)
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
