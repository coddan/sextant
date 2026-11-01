package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

func testRuntime(settings config.Settings, hosts ...model.Host) *app.Runtime {
	return &app.Runtime{Store: &config.Store{Settings: settings, Hosts: hosts}}
}

func TestMetricsDisabledFallsBackToReachability(t *testing.T) {
	settings := config.DefaultSettings()
	settings.General.MetricsDisabled = true
	now := time.Now()
	cpu := 50.0
	g := &App{
		tr: i18n.New("en"),
		rt: testRuntime(settings),
		metrics: map[string]model.Metrics{
			"h1": {CollectedAt: now, CPUPercent: &cpu},
		},
		probes: map[string]sshclient.ProbeResult{"h1": {Online: true}},
	}
	host := model.Host{ID: "h1", Name: "web", Hostname: "10.0.0.1", Port: 22, Monitoring: model.MonitorSSH}

	if got := g.health(host); got != healthOnline {
		t.Fatalf("health with metrics disabled = %v, want probe-derived online", got)
	}
	if !g.hostReachable(host) {
		t.Fatal("probe should still make a host reachable when metrics are disabled")
	}
	if got := g.hostMetricsHint(host); got != g.tr.T(i18n.DashboardMetricsOff) {
		t.Fatalf("metrics hint = %q, want the disabled notice", got)
	}
}

func TestRefreshAllMetricsHonoursOptOut(t *testing.T) {
	settings := config.DefaultSettings()
	settings.General.MetricsDisabled = true
	g := &App{
		tr:      i18n.New("en"),
		rt:      testRuntime(settings, model.Host{ID: "h1", Monitoring: model.MonitorSSH}),
		metrics: map[string]model.Metrics{},
	}
	if g.refreshAllMetrics(true) {
		t.Fatal("refreshAllMetrics started while metrics were disabled")
	}
	if g.metricsRun {
		t.Fatal("metrics pass should not be marked running when disabled")
	}
}

func TestMonitorOffSkipsProbesAndMetrics(t *testing.T) {
	settings := config.DefaultSettings()
	now := time.Now()
	g := &App{
		tr: i18n.New("en"),
		rt: testRuntime(settings),
		metrics: map[string]model.Metrics{
			"h1": {CollectedAt: now},
		},
		probes: map[string]sshclient.ProbeResult{"h1": {Online: true}},
	}
	host := model.Host{ID: "h1", Name: "appliance", Hostname: "10.0.0.2", Port: 22, Monitoring: model.MonitorOff}

	if got := g.health(host); got != healthDisabled {
		t.Fatalf("health = %v, want disabled", got)
	}
	if g.hostReachable(host) {
		t.Fatal("monitor-off host should not be reachable")
	}
	if got := g.hostMetricsHint(host); got != g.tr.T(i18n.DashboardMonitoringOff) {
		t.Fatalf("metrics hint = %q, want monitoring off", got)
	}
}

func TestHostListDetailShowsState(t *testing.T) {
	settings := config.DefaultSettings()
	cpu, ram, disk := 41.0, 68.0, 92.0
	g := &App{
		tr: i18n.New("en"),
		rt: testRuntime(settings),
		metrics: map[string]model.Metrics{
			"h1": {CollectedAt: time.Now(), CPUPercent: &cpu, RAMPercent: &ram, DiskPercent: &disk},
		},
	}
	host := model.Host{ID: "h1", Name: "web", User: "root", Hostname: "10.0.0.3", Port: 22, Group: "prod", Monitoring: model.MonitorSSH}
	detail := g.hostListDetail(host)
	for _, want := range []string{"root@10.0.0.3:22", "prod", "CPU 41%", "RAM 68%", "Disk 92%"} {
		if !contains(detail, want) {
			t.Fatalf("host list detail %q does not contain %q", detail, want)
		}
	}
}

func TestListLayoutUsesVirtualisedRows(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	settings := config.DefaultSettings()
	settings.UI.CardLayout = "list"
	hosts := []model.Host{
		{ID: "h1", Name: "web-01", Hostname: "10.0.0.4", Port: 22},
		{ID: "h2", Name: "db-01", Hostname: "10.0.0.5", Port: 22},
	}
	g := &App{
		tr:      i18n.New("en"),
		rt:      testRuntime(settings, hosts...),
		metrics: map[string]model.Metrics{},
		probes:  map[string]sshclient.ProbeResult{},
	}
	g.buildHostsTab()

	if len(g.hostsBody.Objects) != 1 || g.hostsBody.Objects[0] != g.hostsList {
		t.Fatal("list layout should show the virtualised list")
	}
	if len(g.hostsBox.Objects) != 0 {
		t.Fatalf("list layout built %d card objects, want none", len(g.hostsBox.Objects))
	}
	if len(g.hosts) != len(hosts) {
		t.Fatalf("visible hosts = %d, want %d", len(g.hosts), len(hosts))
	}
}

func TestGridLayoutStillBuildsCards(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	settings := config.DefaultSettings()
	settings.UI.CardLayout = "grid"
	hosts := []model.Host{{ID: "h1", Name: "web-01", Hostname: "10.0.0.6", Port: 22}}
	g := &App{
		tr:      i18n.New("en"),
		rt:      testRuntime(settings, hosts...),
		metrics: map[string]model.Metrics{},
		probes:  map[string]sshclient.ProbeResult{},
	}
	g.buildHostsTab()

	if len(g.hostsBox.Objects) != 1 {
		t.Fatalf("grid built %d cards, want 1", len(g.hostsBox.Objects))
	}
	if len(g.hostsBody.Objects) != 1 || g.hostsBody.Objects[0] != g.hostsScroll {
		t.Fatal("grid layout should show the scrollable card grid")
	}
}
