package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/putty"
	"github.com/coddan/sextant/internal/sshconfig"
)

// buildHostsTab is the dashboard: filter, actions and the host list.
func (g *App) buildHostsTab() fyne.CanvasObject {
	filter := widget.NewEntry()
	filter.SetPlaceHolder(g.tr.T(i18n.DashboardSearchPlaceholder))
	filter.OnChanged = func(s string) {
		g.filterText = s
		g.refreshHosts()
	}

	newBtn := widget.NewButtonWithIcon(g.tr.T(i18n.DashboardAddHost), theme.ContentAddIcon(), func() {
		g.editHost(model.NewHost("", ""))
	})
	refreshBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonRefresh), theme.ViewRefreshIcon(), func() {
		g.refreshHosts()
		g.probeAll()
	})
	sshBtn := widget.NewButton(g.tr.T(i18n.DashboardImportSSHConfig), func() { g.importSSHConfig() })
	puttyBtn := widget.NewButton(g.tr.T(i18n.DashboardImportPuTTY), func() { g.importPuTTY() })

	g.onlyOnline = widget.NewCheck(g.tr.T(i18n.DashboardShowOnlyOnline), func(bool) { g.refreshHosts() })
	bar := container.NewBorder(nil, nil, container.NewHBox(newBtn, refreshBtn, sshBtn, puttyBtn, g.onlyOnline), nil, filter)
	g.hostsBox = container.NewVBox()
	scroll := container.NewVScroll(g.hostsBox)

	g.refreshHosts()
	return container.NewBorder(container.NewVBox(bar, widget.NewSeparator()), nil, nil, nil, scroll)
}

// probeAll checks reachability in the background.
func (g *App) probeAll() {
	for _, h := range g.rt.Store.Hosts {
		host := h
		go func() {
			res := g.rt.Probe(g.ctx, host)
			fyne.Do(func() {
				if res.Online {
					g.setStatus(fmt.Sprintf("%s: %s (%s)", host.Name, g.tr.T(i18n.DashboardOnline), res.Latency.Round(time.Millisecond)))
				}
			})
		}()
	}
}

// refreshHosts rebuilds the visible list, honouring the filter.
func (g *App) refreshHosts() {
	if g.hostsBox == nil {
		return
	}
	needle := strings.ToLower(strings.TrimSpace(g.filterText))
	g.hosts = g.hosts[:0]
	for _, h := range g.rt.Store.Hosts {
		if h.Hidden {
			continue
		}
		if g.onlyOnline != nil && g.onlyOnline.Checked {
			met, ok := g.metrics[h.ID]
			if !ok || met.Error != "" {
				continue
			}
		}
		if needle != "" &&
			!strings.Contains(strings.ToLower(h.Name), needle) &&
			!strings.Contains(strings.ToLower(h.Hostname), needle) &&
			!strings.Contains(strings.ToLower(h.User), needle) &&
			!strings.Contains(strings.ToLower(h.Group), needle) {
			continue
		}
		g.hosts = append(g.hosts, h)
	}
	g.hostsBox.Objects = nil
	if len(g.hosts) == 0 {
		empty := widget.NewLabel(g.tr.T(i18n.DashboardEmpty))
		empty.Alignment = fyne.TextAlignCenter
		g.hostsBox.Add(container.NewPadded(empty))
	}
	for _, h := range g.hosts {
		g.hostsBox.Add(g.hostRow(h))
		g.hostsBox.Add(widget.NewSeparator())
	}
	g.hostsBox.Refresh()
}

func (g *App) hostRow(h model.Host) fyne.CanvasObject {
	title := widget.NewLabelWithStyle(h.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	addr := fmt.Sprintf("%s@%s:%d", orDefault(h.User, "-"), h.Hostname, h.Port)
	if h.Group != "" {
		addr += "   ·   " + h.Group
	}
	detail := addr
	if met, ok := g.metrics[h.ID]; ok && met.CollectedAt.After(time.Time{}) {
		if met.Error != "" {
			detail += "   ·   " + met.Error
		} else {
			detail += fmt.Sprintf("   ·   CPU %s  RAM %s  DISK %s  %s",
				pct(met.CPUPercent), pct(met.RAMPercent), pct(met.DiskPercent), met.Uptime)
		}
	}
	sub := widget.NewLabel(detail)
	sub.Wrapping = fyne.TextWrapWord

	connectBtn := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() { g.connect(h) })
	connectBtn.Importance = widget.HighImportance
	sftpBtn := widget.NewButtonWithIcon("", theme.FolderIcon(), func() { g.openSFTP(h) })
	sampleBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { g.sample(h) })
	editBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { g.editHost(h) })
	delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { g.confirmDelete(h) })
	actions := container.NewHBox(connectBtn, sftpBtn, sampleBtn, editBtn, delBtn)

	left := container.NewVBox(title, sub)
	return container.NewBorder(nil, nil, nil, actions, left)
}

// sample connects in the background and records metrics.
func (g *App) sample(h model.Host) {
	g.notify(model.NotifyInfo, fmt.Sprintf("%s %s…", g.tr.T(i18n.MetricsCollecting), h.Name))
	go func() {
		ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
		defer cancel()
		met := g.rt.Metrics(ctx, h)
		fyne.Do(func() {
			g.metrics[h.ID] = met
			g.refreshHosts()
		})
	}()
}

// editHost opens the add/edit dialog. An empty ID means a new host.
func (g *App) editHost(existing model.Host) {
	isNew := existing.ID == ""
	title := g.tr.T(i18n.HostEdit)
	if isNew {
		title = g.tr.T(i18n.HostNew)
	}

	name := widget.NewEntry()
	name.SetText(existing.Name)
	hostname := widget.NewEntry()
	hostname.SetText(existing.Hostname)
	user := widget.NewEntry()
	user.SetText(existing.User)
	port := widget.NewEntry()
	p := existing.Port
	if p == 0 {
		p = 22
	}
	port.SetText(strconv.Itoa(p))
	group := widget.NewEntry()
	group.SetText(existing.Group)
	password := widget.NewEntry()
	password.Password = true
	password.SetPlaceHolder(g.tr.T(i18n.HostSavePassword))
	identity := widget.NewEntry()
	identity.SetText(strings.Join(existing.IdentityFiles, ", "))
	certificate := widget.NewEntry()
	certificate.SetText(existing.CertificateFile)
	proxyJump := widget.NewEntry()
	proxyJump.SetText(existing.ProxyJump)
	localFwd := widget.NewEntry()
	localFwd.SetText(strings.Join(existing.LocalForwards, ", "))
	dynamic := widget.NewEntry()
	dynamic.SetText(existing.DynamicForward)
	notes := widget.NewEntry()
	notes.SetText(existing.Notes)
	notes.MultiLine = true
	notes.SetMinRowsVisible(3)

	monitor := widget.NewSelect([]string{"ssh", "tcp"}, nil)
	monitor.SetSelected(string(orDefault(string(existing.Monitoring), string(model.MonitorSSH))))

	groupSel := widget.NewSelect(g.groupNames(), func(v string) { group.SetText(v) })
	groupSel.PlaceHolder = g.tr.T(i18n.HostGroup)

	identitiesOnly := widget.NewCheck(g.tr.T(i18n.HostIdentitiesOnly), nil)
	identitiesOnly.SetChecked(existing.IdentitiesOnly)
	forwardAgent := widget.NewCheck(g.tr.T(i18n.HostForwardAgent), nil)
	forwardAgent.SetChecked(existing.ForwardAgent)
	autostart := widget.NewCheck(g.tr.T(i18n.HostTunnelAutostart), nil)
	autostart.SetChecked(existing.TunnelAutostart)
	hidden := widget.NewCheck(g.tr.T(i18n.HostHidden), nil)
	hidden.SetChecked(existing.Hidden)

	form := widget.NewForm(
		widget.NewFormItem(g.tr.T(i18n.HostName), name),
		widget.NewFormItem(g.tr.T(i18n.HostHostname), hostname),
		widget.NewFormItem(g.tr.T(i18n.HostUser), user),
		widget.NewFormItem(g.tr.T(i18n.HostPort), port),
		widget.NewFormItem(g.tr.T(i18n.HostGroup), container.NewBorder(nil, nil, nil, groupSel, group)),
		widget.NewFormItem(g.tr.T(i18n.HostPassword), password),
		widget.NewFormItem(g.tr.T(i18n.HostIdentityFiles), identity),
		widget.NewFormItem(g.tr.T(i18n.HostCertificateFile), certificate),
		widget.NewFormItem(g.tr.T(i18n.HostProxyJump), proxyJump),
		widget.NewFormItem(g.tr.T(i18n.HostLocalForwards), localFwd),
		widget.NewFormItem(g.tr.T(i18n.HostDynamicForward), dynamic),
		widget.NewFormItem(g.tr.T(i18n.HostMonitoring), monitor),
		widget.NewFormItem("", identitiesOnly),
		widget.NewFormItem("", forwardAgent),
		widget.NewFormItem("", autostart),
		widget.NewFormItem("", hidden),
		widget.NewFormItem(g.tr.T(i18n.HostNotes), notes),
	)
	scroll := container.NewVScroll(form)
	scroll.SetMinSize(fyne.NewSize(520, 480))

	d := dialog.NewCustomConfirm(title, g.tr.T(i18n.CommonSave), g.tr.T(i18n.CommonCancel), scroll, func(ok bool) {
		if !ok {
			return
		}
		h := existing
		if isNew {
			h = model.NewHost("", "")
		}
		h.Name = strings.TrimSpace(name.Text)
		h.Hostname = strings.TrimSpace(hostname.Text)
		h.User = strings.TrimSpace(user.Text)
		if h.Name == "" {
			h.Name = h.Hostname
		}
		if n, err := strconv.Atoi(strings.TrimSpace(port.Text)); err == nil && n > 0 && n <= 65535 {
			h.Port = n
		} else {
			h.Port = 22
		}
		h.Group = strings.TrimSpace(group.Text)
		h.IdentityFiles = splitList(identity.Text)
		h.CertificateFile = strings.TrimSpace(certificate.Text)
		h.ProxyJump = strings.TrimSpace(proxyJump.Text)
		h.LocalForwards = splitList(localFwd.Text)
		h.DynamicForward = strings.TrimSpace(dynamic.Text)
		h.Notes = notes.Text
		switch monitor.Selected {
		case "tcp":
			h.Monitoring = model.MonitorTCP
		default:
			h.Monitoring = model.MonitorSSH
		}
		h.IdentitiesOnly = identitiesOnly.Checked
		h.ForwardAgent = forwardAgent.Checked
		h.TunnelAutostart = autostart.Checked
		h.Hidden = hidden.Checked

		if h.Hostname == "" {
			dialog.ShowError(fmt.Errorf("%s: %s", g.tr.T(i18n.HostHostname), g.tr.T(i18n.CommonRequired)), g.win)
			return
		}
		if password.Text != "" {
			if err := g.rt.Store.HostPassword(&h, password.Text); err != nil {
				dialog.ShowError(err, g.win)
				return
			}
		}
		if h.Group != "" {
			g.rt.Store.AddGroup(h.Group)
		}
		g.rt.Store.UpsertHost(h)
		if err := g.rt.Save(); err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		g.refreshHosts()
		g.keys.refreshHosts()
		g.notify(model.NotifySuccess, g.tr.T(i18n.NotifySaved))
	}, g.win)
	d.Resize(fyne.NewSize(640, 640))
	d.Show()
}

func (g *App) confirmDelete(h model.Host) {
	dialog.ShowConfirm(g.tr.T(i18n.CommonDelete),
		fmt.Sprintf("%s\n\n%s", h.Name, g.tr.T(i18n.HostDeleteConfirm)),
		func(ok bool) {
			if !ok {
				return
			}
			g.rt.Store.DeleteHost(h.ID)
			_ = g.rt.Save()
			g.refreshHosts()
			g.keys.refreshHosts()
			g.notify(model.NotifySuccess, fmt.Sprintf("%s %s", g.tr.T(i18n.NotifyDeleted), h.Name))
		}, g.win)
}

func (g *App) groupNames() []string {
	var names []string
	for _, grp := range g.rt.Store.AllGroups() {
		names = append(names, grp.Name)
	}
	return names
}

// importSSHConfig reads ~/.ssh/config and adds the concrete hosts.
func (g *App) importSSHConfig() {
	cfg, err := sshconfig.LoadDefault()
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	count := 0
	for _, block := range cfg.Blocks {
		if block.Global || block.Match || len(block.Patterns) == 0 {
			continue
		}
		alias := block.Patterns[0]
		if strings.ContainsAny(alias, "*?!") {
			continue
		}
		opts := cfg.Apply(alias)
		if opts.HostName == "" || opts.HostName == alias {
			continue
		}
		if _, exists := g.rt.Store.HostByName(alias); exists {
			continue
		}
		h := model.NewHost(alias, opts.HostName)
		h.User = opts.User
		if opts.Port > 0 {
			h.Port = opts.Port
		}
		h.IdentityFiles = opts.IdentityFiles
		h.CertificateFile = opts.CertificateFile
		h.IdentitiesOnly = opts.IdentitiesOnly
		h.ProxyJump = opts.ProxyJump
		h.LocalForwards = opts.LocalForwards
		h.DynamicForward = opts.DynamicForward
		h.ForwardAgent = opts.ForwardAgent
		h.Source = model.SourceSSHConfig
		h.OriginalSSHHost = alias
		g.rt.Store.UpsertHost(h)
		count++
	}
	_ = g.rt.Save()
	g.refreshHosts()
	g.keys.refreshHosts()
	g.notify(model.NotifySuccess, fmt.Sprintf("%s: %d", g.tr.T(i18n.NotifyImportComplete), count))
}

// importPuTTY imports Windows PuTTY sessions.
func (g *App) importPuTTY() {
	sessions, err := putty.ReadRegistry()
	if err != nil {
		dialog.ShowError(fmt.Errorf("%s: %w", g.tr.T(i18n.ErrorImportFailed), err), g.win)
		return
	}
	opts := putty.ImportOptions{
		ConvertKeys: true,
		Passphrase: func(keyPath string, info putty.KeyInfo) ([]byte, error) {
			s, err := g.askEntry(g.tr.T(i18n.HostPassphrase), keyPath, true)
			if err != nil {
				return nil, err
			}
			return []byte(s), nil
		},
	}
	res := putty.ImportSessions(sessions, opts)
	for _, h := range res.Hosts {
		g.rt.Store.UpsertHost(h)
	}
	_ = g.rt.Save()
	g.refreshHosts()
	g.keys.refreshHosts()
	msg := fmt.Sprintf("%s: %d", g.tr.T(i18n.NotifyImportedCount), len(res.Hosts))
	if len(res.Warnings) > 0 {
		msg += "\n\n" + strings.Join(res.Warnings, "\n")
	}
	g.notify(model.NotifySuccess, msg)
}

func splitList(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func pct(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *v)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
