package gui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
)

// hostListRow is the template used by the virtualised dashboard list. Keeping
// the widgets alive and updating them in place makes scrolling hundreds of
// hosts cheap: only visible rows are allocated by widget.List.
type hostListRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	dot     *canvas.Circle
	name    *widget.Label
	detail  *widget.Label
	health  *widget.Label
	connect *widget.Button
	sftp    *widget.Button
	edit    *widget.Button
	del     *widget.Button
}

func (g *App) newHostListRow() *hostListRow {
	r := &hostListRow{}
	r.dot = canvas.NewCircle(theme.Color(theme.ColorNameDisabled))
	dotBox := container.NewGridWrap(fyne.NewSize(9, 9), r.dot)

	r.name = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	r.name.Truncation = fyne.TextTruncateEllipsis
	r.detail = widget.NewLabel("")
	r.detail.Importance = widget.LowImportance
	r.detail.Truncation = fyne.TextTruncateEllipsis
	r.health = widget.NewLabel("")

	r.connect = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), nil)
	r.connect.Importance = widget.HighImportance
	r.sftp = widget.NewButtonWithIcon("", theme.FolderIcon(), nil)
	r.edit = widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), nil)
	r.del = widget.NewButtonWithIcon("", theme.DeleteIcon(), nil)

	actions := container.NewHBox(r.health, r.connect, r.sftp, r.edit, r.del)
	text := container.NewVBox(r.name, r.detail)
	r.content = container.NewPadded(container.NewBorder(nil, nil, dotBox, actions, text))
	r.ExtendBaseWidget(r)
	return r
}

func (r *hostListRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (g *App) updateHostListRow(r *hostListRow, h model.Host) {
	r.name.SetText(orDefault(h.Name, h.Hostname))
	r.detail.SetText(g.hostListDetail(h))

	health := g.health(h)
	r.dot.FillColor = health.color()
	r.dot.Refresh()
	r.health.SetText(g.healthText(health))
	r.health.Importance = health.importance()

	r.connect.OnTapped = func() { g.connect(h) }
	r.sftp.OnTapped = func() { g.openSFTP(h) }
	r.edit.OnTapped = func() { g.editHost(h) }
	r.del.OnTapped = func() { g.confirmDelete(h) }
	r.Refresh()
}

// hostListDetail keeps the row to two lines: address and group, then a short
// monitoring or load summary so large inventories remain scannable.
func (g *App) hostListDetail(h model.Host) string {
	parts := []string{fmt.Sprintf("%s@%s:%d", orDefault(h.User, "-"), h.Hostname, h.Port)}
	if h.Group != "" {
		parts = append(parts, h.Group)
	}
	switch {
	case h.Monitoring == model.MonitorOff:
		parts = append(parts, g.tr.T(i18n.DashboardMonitoringOff))
	case h.Monitoring == model.MonitorTCP:
		parts = append(parts, g.tr.T(i18n.DashboardTCPOnly))
	case g.metricsDisabled():
		parts = append(parts, g.tr.T(i18n.DashboardMetricsOff))
	default:
		if met, ok := g.metrics[h.ID]; ok && met.CollectedAt.After(time.Time{}) && met.Error == "" {
			if met.CPUPercent != nil {
				parts = append(parts, fmt.Sprintf("CPU %.0f%%", *met.CPUPercent))
			}
			if met.RAMPercent != nil {
				parts = append(parts, fmt.Sprintf("RAM %.0f%%", *met.RAMPercent))
			}
			if met.DiskPercent != nil {
				parts = append(parts, fmt.Sprintf("Disk %.0f%%", *met.DiskPercent))
			}
		}
	}
	return strings.Join(parts, "   ·   ")
}
