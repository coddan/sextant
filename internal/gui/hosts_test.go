package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

func TestHostHealth(t *testing.T) {
	now := time.Now()
	host := model.Host{ID: "h1"}
	cases := []struct {
		name    string
		metrics map[string]model.Metrics
		probes  map[string]sshclient.ProbeResult
		want    hostHealth
	}{
		{
			name: "unknown",
			want: healthUnknown,
		},
		{
			name:    "metrics online",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now}},
			want:    healthOnline,
		},
		{
			name:    "background auth",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now, Error: "interactive authentication required"}},
			want:    healthDegraded,
		},
		{
			name:    "unknown host key",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now, Error: "ssh: unknown host key: example.com offers ssh-ed25519"}},
			want:    healthDegraded,
		},
		{
			name:    "changed host key",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now, Error: "ssh: knownhosts: key mismatch"}},
			want:    healthDegraded,
		},
		{
			name:    "generic auth failure",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now, Error: "ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain"}},
			want:    healthDegraded,
		},
		{
			name:    "network failure",
			metrics: map[string]model.Metrics{host.ID: {CollectedAt: now, Error: "dial tcp: connection refused"}},
			want:    healthOffline,
		},
		{
			name:   "probe online",
			probes: map[string]sshclient.ProbeResult{host.ID: {Online: true}},
			want:   healthOnline,
		},
		{
			name:   "probe offline",
			probes: map[string]sshclient.ProbeResult{host.ID: {Online: false}},
			want:   healthOffline,
		},
		{
			name: "probe online but metrics failed",
			metrics: map[string]model.Metrics{host.ID: {
				CollectedAt: now,
				Error:       "dial tcp: connection refused",
			}},
			probes: map[string]sshclient.ProbeResult{host.ID: {Online: true}},
			want:   healthDegraded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &App{metrics: tc.metrics, probes: tc.probes}
			if got := g.health(host); got != tc.want {
				t.Fatalf("health = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShortOS(t *testing.T) {
	cases := map[string]string{
		"Fedora Linux 43 (Server Edition)": "Fedora Linux 43",
		"Ubuntu 24.04.1 LTS":               "Ubuntu 24.04.1 LTS",
		"(unsupported)":                    "(unsupported)",
	}
	for in, want := range cases {
		if got := shortOS(in); got != want {
			t.Errorf("shortOS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostCardRenders(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	cpu, ram, disk := 41.0, 68.0, 92.0
	dockerUp, dockerAll := 2, 3
	g := &App{
		tr: i18n.New("en"),
		metrics: map[string]model.Metrics{
			"h1": {
				CPUPercent:  &cpu,
				RAMPercent:  &ram,
				DiskPercent: &disk,
				Uptime:      "3d 4h",
				OS:          "Debian",
				Load:        "0.42",
				DockerUp:    &dockerUp,
				DockerAll:   &dockerAll,
				CollectedAt: time.Now(),
			},
		},
		probes: map[string]sshclient.ProbeResult{"h1": {Online: true}},
	}
	host := model.Host{
		ID:       "h1",
		Name:     "web-01",
		User:     "root",
		Hostname: "10.0.0.5",
		Port:     22,
		Group:    "prod",
	}
	card := g.hostCard(host)
	if card == nil {
		t.Fatal("hostCard returned nil")
	}
	if size := card.MinSize(); size.Width <= 0 || size.Height <= 0 {
		t.Fatalf("card MinSize = %v, want positive dimensions", size)
	}

	// A card without a sample must still render with the same layout.
	empty := g.hostCard(model.Host{ID: "h2", Name: "db-01", User: "root", Hostname: "10.0.0.6", Port: 22})
	if size := empty.MinSize(); size.Width <= 0 || size.Height <= 0 {
		t.Fatalf("empty card MinSize = %v, want positive dimensions", size)
	}

	// TCP monitors explain why there are no system metrics.
	tcp := g.hostCard(model.Host{ID: "h3", Name: "appliance", Hostname: "10.0.0.7", Port: 22, Monitoring: model.MonitorTCP})
	if size := tcp.MinSize(); size.Width <= 0 || size.Height <= 0 {
		t.Fatalf("tcp card MinSize = %v, want positive dimensions", size)
	}
}
