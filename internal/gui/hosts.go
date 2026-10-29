package gui

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/putty"
	"github.com/coddan/sextant/internal/sshclient"
	"github.com/coddan/sextant/internal/sshconfig"
)

// probeUpdate and metricsUpdate carry background results back to the UI thread.
type probeUpdate struct {
	id  string
	res sshclient.ProbeResult
}

type metricsUpdate struct {
	id  string
	met model.Metrics
}

// buildHostsTab is the dashboard: filter, actions and a responsive card grid.
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
	newBtn.Importance = widget.HighImportance
	refreshBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonRefresh), theme.ViewRefreshIcon(), func() {
		g.refreshAll()
	})
	sshBtn := widget.NewButton(g.tr.T(i18n.DashboardImportSSHConfig), func() { g.importSSHConfig() })
	puttyBtn := widget.NewButton(g.tr.T(i18n.DashboardImportPuTTY), func() { g.importPuTTY() })

	g.onlyOnline = widget.NewCheck(g.tr.T(i18n.DashboardShowOnlyOnline), func(checked bool) {
		g.refreshHosts()
		if checked {
			// Respect the background auth backoff: this is a filter, not an
			// explicit request to retry hosts that need a login.
			g.probeAll()
			g.refreshAllMetrics(false)
		}
	})

	title := canvas.NewText(g.tr.T(i18n.NavDashboard), theme.Color(theme.ColorNameForeground))
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}
	search := container.NewGridWrap(fyne.NewSize(240, 38), filter)
	header := container.NewBorder(nil, nil, title, container.NewHBox(search, refreshBtn, newBtn))
	imports := container.NewHBox(sshBtn, puttyBtn, g.onlyOnline)
	top := container.NewVBox(
		container.NewPadded(header),
		container.NewPadded(imports),
		widget.NewSeparator(),
	)

	g.hostsBox = container.NewAdaptiveGrid(2)
	// Wrap the grid in a VBox so a short list keeps its natural card height
	// instead of stretching the rows to fill the scroll viewport.
	g.hostsWrap = container.NewVBox(g.hostsBox)
	scroll := container.NewVScroll(g.hostsWrap)

	g.refreshHosts()
	return container.NewBorder(top, nil, nil, nil, scroll)
}

// refreshAll probes reachability and refreshes metrics for the whole list. This
// is the toolbar action; it never prompts for credentials, so a single click
// cannot trigger a stack of login dialogs.
func (g *App) refreshAll() {
	g.probeAll()
	if g.refreshAllMetrics(true) {
		g.notify(model.NotifyInfo, g.tr.T(i18n.MetricsCollecting))
	}
}

// startMetricsLoop samples the dashboard in the background on the configured
// interval. It is restarted when settings change.
func (g *App) startMetricsLoop() {
	if g.metricsOff != nil {
		g.metricsOff()
		g.metricsOff = nil
	}
	seconds := g.rt.Store.Settings.General.RefreshIntervalSeconds
	if seconds <= 0 {
		return
	}
	interval := time.Duration(seconds) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(g.ctx)
	g.metricsOff = cancel
	go func() {
		// Let the window settle before the first round of background dials.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			fyne.Do(func() {
				g.probeAll()
				g.refreshAllMetrics(false)
			})
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fyne.Do(func() {
					g.probeAll()
					g.refreshAllMetrics(false)
				})
			case <-ctx.Done():
				return
			}
		}
	}()
}

// probeAll checks reachability in the background. It is cheap enough to run for
// every host; SSH metrics are collected separately.
func (g *App) probeAll() {
	hosts := append([]model.Host(nil), g.rt.Store.Hosts...)
	if len(hosts) == 0 {
		return
	}
	results := make(chan probeUpdate, len(hosts))
	var wg sync.WaitGroup
	for _, h := range hosts {
		if h.Hidden {
			continue
		}
		host := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- probeUpdate{id: host.ID, res: g.rt.Probe(g.ctx, host)}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		fyne.Do(func() {
			for update := range results {
				g.probes[update.id] = update.res
			}
			g.refreshHosts()
		})
	}()
}

// metricsAuthBackoff keeps background collection from retrying hosts that need
// interactive authentication every interval. Manual refresh ignores it.
const metricsAuthBackoff = 10 * time.Minute

// refreshAllMetrics samples every SSH-monitored host using stored credentials
// only. It returns false when a pass is already running. When force is false,
// hosts that recently needed interactive authentication are skipped.
func (g *App) refreshAllMetrics(force bool) bool {
	if g.metricsRun {
		return false
	}
	now := time.Now()
	var hosts []model.Host
	for _, h := range g.rt.Store.Hosts {
		if h.Hidden || h.Monitoring == model.MonitorTCP {
			continue
		}
		if !force {
			if until, ok := g.metricsRetry[h.ID]; ok && now.Before(until) {
				continue
			}
		}
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return false
	}
	g.metricsRun = true

	limit := g.rt.Store.Settings.General.MaxConcurrent
	if limit <= 0 {
		limit = 10
	}
	if limit > len(hosts) {
		limit = len(hosts)
	}
	sem := make(chan struct{}, limit)
	results := make(chan metricsUpdate, len(hosts))
	var wg sync.WaitGroup
	for _, h := range hosts {
		host := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-g.ctx.Done():
				return
			}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
			defer cancel()
			results <- metricsUpdate{id: host.ID, met: g.rt.MetricsQuiet(ctx, host)}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		fyne.Do(func() {
			for update := range results {
				g.metrics[update.id] = update.met
				if update.met.Error == "" {
					delete(g.metricsRetry, update.id)
				} else if metricNeedsManualAction(update.met.Error) {
					g.metricsRetry[update.id] = time.Now().Add(metricsAuthBackoff)
				}
			}
			g.metricsRun = false
			g.refreshHosts()
		})
	}()
	return true
}

// refreshHosts rebuilds the visible card grid, honouring the filter.
func (g *App) refreshHosts() {
	if g.hostsBox == nil || g.hostsWrap == nil {
		return
	}
	needle := strings.ToLower(strings.TrimSpace(g.filterText))
	g.hosts = g.hosts[:0]
	for _, h := range g.rt.Store.Hosts {
		if h.Hidden {
			continue
		}
		if g.onlyOnline != nil && g.onlyOnline.Checked && !g.hostReachable(h) {
			continue
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
	if len(g.hosts) == 0 {
		empty := widget.NewLabel(g.tr.T(i18n.DashboardEmpty))
		empty.Alignment = fyne.TextAlignCenter
		g.hostsBox.Objects = nil
		// Put the empty message in the wrapper so it spans the full width
		// instead of occupying the first cell of the two-column grid.
		g.hostsWrap.Objects = []fyne.CanvasObject{container.NewPadded(empty)}
	} else {
		cards := make([]fyne.CanvasObject, 0, len(g.hosts))
		for _, h := range g.hosts {
			cards = append(cards, g.hostCard(h))
		}
		g.hostsBox.Objects = cards
		g.hostsWrap.Objects = []fyne.CanvasObject{g.hostsBox}
	}
	g.hostsBox.Refresh()
	g.hostsWrap.Refresh()
}

// hostReachable reports the best reachability signal available for a host.
func (g *App) hostReachable(h model.Host) bool {
	if met, ok := g.metrics[h.ID]; ok && met.CollectedAt.After(time.Time{}) && met.Error == "" {
		return true
	}
	if probe, ok := g.probes[h.ID]; ok {
		return probe.Online
	}
	return false
}

// hostHealth summarises what the dashboard knows about a host.
type hostHealth int

const (
	healthUnknown hostHealth = iota
	healthOnline
	healthOffline
	healthDegraded
)

func (g *App) health(h model.Host) hostHealth {
	probe, hasProbe := g.probes[h.ID]
	met, hasMetrics := g.metrics[h.ID]
	hasMetrics = hasMetrics && met.CollectedAt.After(time.Time{})

	switch {
	case hasMetrics && met.Error == "":
		return healthOnline
	case hasMetrics && met.Error != "":
		if metricNeedsManualAction(met.Error) || (hasProbe && probe.Online) {
			return healthDegraded
		}
		return healthOffline
	case hasProbe && probe.Online:
		return healthOnline
	case hasProbe:
		return healthOffline
	default:
		return healthUnknown
	}
}

// metricNeedsManualAction reports errors that mean the host is reachable but
// background collection could not authenticate or verify its host key.
func metricNeedsManualAction(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "interactive authentication required") ||
		strings.Contains(lower, "unknown host key") ||
		strings.Contains(lower, "knownhosts: key mismatch") ||
		strings.Contains(lower, "host key changed") ||
		strings.Contains(lower, "unable to authenticate") ||
		strings.Contains(lower, "no supported methods remain") ||
		strings.Contains(lower, "password authentication required") ||
		strings.Contains(lower, "needs a passphrase")
}

func (s hostHealth) color() color.Color {
	switch s {
	case healthOnline:
		return theme.Color(theme.ColorNameSuccess)
	case healthOffline:
		return theme.Color(theme.ColorNameError)
	case healthDegraded:
		return theme.Color(theme.ColorNameWarning)
	default:
		return theme.Color(theme.ColorNameDisabled)
	}
}

func (s hostHealth) importance() widget.Importance {
	switch s {
	case healthOnline:
		return widget.SuccessImportance
	case healthOffline:
		return widget.DangerImportance
	case healthDegraded:
		return widget.WarningImportance
	default:
		return widget.LowImportance
	}
}

func (g *App) healthText(s hostHealth) string {
	switch s {
	case healthOnline:
		return g.tr.T(i18n.DashboardOnline)
	case healthOffline:
		return g.tr.T(i18n.DashboardOffline)
	case healthDegraded:
		return g.tr.T(i18n.DashboardDegraded)
	default:
		return g.tr.T(i18n.CommonUnknown)
	}
}

// hostCard renders one host with live metrics and its actions.
func (g *App) hostCard(h model.Host) fyne.CanvasObject {
	met := g.metrics[h.ID]
	health := g.health(h)

	dot := canvas.NewCircle(health.color())
	dot.Resize(fyne.NewSize(9, 9))
	dotBox := container.NewGridWrap(fyne.NewSize(9, 9), dot)

	title := widget.NewLabelWithStyle(h.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis
	status := widget.NewLabel(g.healthText(health))
	status.Importance = health.importance()
	header := container.NewBorder(nil, nil, dotBox, status, title)

	addrText := fmt.Sprintf("%s@%s:%d", orDefault(h.User, "-"), h.Hostname, h.Port)
	if h.Group != "" {
		addrText += "   ·   " + h.Group
	}
	addr := widget.NewLabel(addrText)
	addr.Importance = widget.LowImportance
	addr.Truncation = fyne.TextTruncateEllipsis

	bars := container.NewGridWithColumns(3,
		newMetricBar(g.tr.T(i18n.MetricsCPU), met.CPUPercent),
		newMetricBar(g.tr.T(i18n.MetricsRAM), met.RAMPercent),
		newMetricBar(g.tr.T(i18n.MetricsDisk), met.DiskPercent),
	)

	detail := g.metricDetail(met)
	if h.Monitoring == model.MonitorTCP {
		hint := widget.NewLabel(g.tr.T(i18n.MetricsTCPOnly))
		hint.Importance = widget.LowImportance
		hint.Truncation = fyne.TextTruncateEllipsis
		detail = hint
	}
	errorText := g.metricError(met)

	connectBtn := widget.NewButtonWithIcon(g.tr.T(i18n.NavTerminal), theme.MediaPlayIcon(), func() { g.connect(h) })
	connectBtn.Importance = widget.HighImportance
	sftpBtn := widget.NewButtonWithIcon("", theme.FolderIcon(), func() { g.openSFTP(h) })
	sampleBtn := widget.NewButtonWithIcon(g.tr.T(i18n.MetricsRefresh), theme.ViewRefreshIcon(), func() { g.sample(h) })
	editBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { g.editHost(h) })
	delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { g.confirmDelete(h) })
	leftActions := container.NewHBox(connectBtn, sftpBtn, sampleBtn)
	if h.Monitoring == model.MonitorTCP {
		leftActions = container.NewHBox(connectBtn, sftpBtn)
	}
	actions := container.NewBorder(nil, nil, leftActions, container.NewHBox(editBtn, delBtn))

	content := container.NewVBox(header, addr, bars)
	if detail != nil {
		content.Add(detail)
	}
	if errorText != nil {
		content.Add(errorText)
	}
	content.Add(widget.NewSeparator())
	content.Add(actions)
	return container.NewPadded(widget.NewCard("", "", content))
}

// metricDetail shows uptime and OS when a sample is available.
func (g *App) metricDetail(met model.Metrics) fyne.CanvasObject {
	if !met.CollectedAt.After(time.Time{}) || met.Error != "" {
		return nil
	}
	var parts []string
	if met.OS != "" {
		parts = append(parts, shortOS(met.OS))
	}
	if met.Uptime != "" {
		parts = append(parts, met.Uptime)
	}
	if load := strings.Fields(met.Load); len(load) > 0 {
		parts = append(parts, "load "+load[0])
	}
	if met.DockerAll != nil && *met.DockerAll > 0 {
		parts = append(parts, fmt.Sprintf("docker %d/%d", orDefaultInt(met.DockerUp, 0), *met.DockerAll))
	}
	if len(parts) == 0 {
		parts = append(parts, met.CollectedAt.Format("15:04:05"))
	}
	label := widget.NewLabel(strings.Join(parts, "   ·   "))
	label.Importance = widget.LowImportance
	label.Truncation = fyne.TextTruncateEllipsis
	return label
}

// shortOS drops an edition suffix such as " (Server Edition)" so the compact
// card has room for uptime, load and container counts.
func shortOS(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}

// metricError turns background auth failures into a short, actionable line.
func (g *App) metricError(met model.Metrics) fyne.CanvasObject {
	if met.Error == "" {
		return nil
	}
	text := met.Error
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "unknown host key"):
		text = g.tr.T(i18n.MetricsHostKeyRequired)
	case strings.Contains(lower, "knownhosts: key mismatch"), strings.Contains(lower, "host key changed"):
		text = g.tr.T(i18n.MetricsHostKeyChanged)
	case metricNeedsManualAction(text):
		text = g.tr.T(i18n.MetricsLoginRequired)
	}
	label := widget.NewLabel(text)
	label.Importance = widget.WarningImportance
	label.Wrapping = fyne.TextWrapWord
	return label
}

// sample connects in the background and records metrics, allowing prompts for
// hosts that background collection cannot authenticate.
func (g *App) sample(h model.Host) {
	g.notify(model.NotifyInfo, fmt.Sprintf("%s %s…", g.tr.T(i18n.MetricsCollecting), h.Name))
	go func() {
		ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
		defer cancel()
		met := g.rt.Metrics(ctx, h)
		fyne.Do(func() {
			g.metrics[h.ID] = met
			if met.Error == "" {
				delete(g.metricsRetry, h.ID)
			} else if metricNeedsManualAction(met.Error) {
				g.metricsRetry[h.ID] = time.Now().Add(metricsAuthBackoff)
			}
			g.refreshHosts()
		})
	}()
}

func orDefaultInt(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
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
		// New credentials or host settings deserve a fresh background try.
		delete(g.metricsRetry, h.ID)
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
			delete(g.metricsRetry, h.ID)
			delete(g.metrics, h.ID)
			delete(g.probes, h.ID)
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
