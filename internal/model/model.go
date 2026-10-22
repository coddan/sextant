// Package model defines the data model shared by the TUI, the desktop
// application and the sync engine. It contains no I/O.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ConfigVersion is the schema version of every file Sextant writes.
const ConfigVersion = 1

// HostSource records where a host entry came from.
type HostSource string

const (
	SourceManual    HostSource = "manual"
	SourceSSHConfig HostSource = "ssh_config"
	SourcePutty     HostSource = "putty"
)

// MonitorMode is how a host is watched on the dashboard.
type MonitorMode string

const (
	// MonitorSSH logs in with SSH and collects the full metric set.
	MonitorSSH MonitorMode = "ssh"
	// MonitorTCP only checks whether the SSH port accepts a connection.
	MonitorTCP MonitorMode = "tcp"
)

// Host is one server the user manages.
//
// Passwords and key passphrases are never part of this structure's JSON: only
// opaque secret IDs are stored, and the actual values live in the encrypted
// secret store (see internal/config). This is what keeps credentials out of
// hosts.json.
type Host struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Hostname string   `json:"hostname"`
	User     string   `json:"user"`
	Port     int      `json:"port"`
	Notes    string   `json:"notes,omitempty"`
	Group    string   `json:"group,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Hidden   bool     `json:"hidden,omitempty"`

	// IdentityFiles are private key paths, in the order they should be tried.
	IdentityFiles []string `json:"identity_files,omitempty"`
	// CertificateFile is an OpenSSH user certificate. When empty, a
	// "<key>-cert.pub" next to an identity file is used automatically.
	CertificateFile string `json:"certificate_file,omitempty"`
	// IdentitiesOnly limits authentication to IdentityFiles, like ssh -o
	// IdentitiesOnly=yes. Useful when an agent holds many keys.
	IdentitiesOnly bool `json:"identities_only,omitempty"`

	// PasswordSecret / PassphraseSecret are secret-store IDs, never values.
	PasswordSecret   string `json:"password_secret,omitempty"`
	PassphraseSecret string `json:"passphrase_secret,omitempty"`

	// ProxyJump is a comma separated [user@]host[:port] chain, as in ssh -J.
	ProxyJump string `json:"proxy_jump,omitempty"`
	// LocalForwards holds "-L" rules: [bind:]port:host:hostport.
	LocalForwards []string `json:"local_forwards,omitempty"`
	// DynamicForward is a SOCKS5 listen address, e.g. "127.0.0.1:1080".
	DynamicForward string `json:"dynamic_forward,omitempty"`
	// TunnelAutostart starts all forwards when the app starts.
	TunnelAutostart bool `json:"tunnel_autostart,omitempty"`
	// ForwardAgent lends the local agent, like ssh -A. Opt in: root on the
	// server can use it while a session is open.
	ForwardAgent bool `json:"forward_agent,omitempty"`

	Monitoring  MonitorMode `json:"monitoring,omitempty"`
	MonitorPort int         `json:"monitor_port,omitempty"`

	Source          HostSource `json:"source,omitempty"`
	OriginalSSHHost string     `json:"original_ssh_host,omitempty"`

	KeySetupAt           *time.Time `json:"key_setup_at,omitempty"`
	PasswordAuthDisabled bool       `json:"password_auth_disabled,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Address returns host:port for dialling.
func (h Host) Address() string {
	port := h.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(h.Hostname, strconv.Itoa(port))
}

// EffectiveUser returns the user, defaulting to the current login name and
// then root.
func (h Host) EffectiveUser() string {
	if h.User != "" {
		return h.User
	}
	if u := currentUser(); u != "" {
		return u
	}
	return "root"
}

func currentUser() string {
	for _, key := range []string{"USER", "LOGNAME", "USERNAME"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// ProbeAddress is the address used by the reachability monitor.
func (h Host) ProbeAddress() string {
	port := h.MonitorPort
	if port <= 0 {
		port = h.Port
	}
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(h.Hostname, strconv.Itoa(port))
}

// NewHost builds a host with a fresh ID and sane defaults.
func NewHost(name, hostname string) Host {
	now := time.Now().UTC()
	return Host{
		ID:         NewID(),
		Name:       name,
		Hostname:   hostname,
		User:       currentUser(),
		Port:       22,
		Monitoring: MonitorSSH,
		Source:     SourceManual,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// NewID returns a random, URL-safe identifier.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Clone returns a deep copy.
func (h Host) Clone() Host {
	c := h
	c.Tags = append([]string(nil), h.Tags...)
	c.IdentityFiles = append([]string(nil), h.IdentityFiles...)
	c.LocalForwards = append([]string(nil), h.LocalForwards...)
	if h.KeySetupAt != nil {
		t := *h.KeySetupAt
		c.KeySetupAt = &t
	}
	return c
}

// SortedTags returns a deduplicated, sorted tag list.
func (h Host) SortedTags() []string {
	seen := make(map[string]struct{}, len(h.Tags))
	out := make([]string, 0, len(h.Tags))
	for _, t := range h.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// Group describes a collapsible dashboard section.
type Group struct {
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
}

// SnippetScope limits which hosts a snippet can run on.
type SnippetScope string

const (
	ScopeGlobal SnippetScope = "global"
	ScopeHost   SnippetScope = "host"
)

// Snippet is a saved command, optionally parameterised.
type Snippet struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Command string       `json:"command"`
	Scope   SnippetScope `json:"scope"`
	Host    string       `json:"host,omitempty"`
	Tags    []string     `json:"tags,omitempty"`
	Params  []string     `json:"params,omitempty"`
	// Stream keeps the process running and streams output (journalctl -f,
	// tail -f). It can be stopped from the UI.
	Stream bool `json:"stream,omitempty"`
}

// NewSnippet builds a snippet with a fresh ID.
func NewSnippet() Snippet {
	return Snippet{ID: NewID(), Scope: ScopeGlobal}
}

// PlaceholderNames returns parameters referenced as {{name}} in the command.
func (s Snippet) PlaceholderNames() []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(s.Command); i++ {
		start := strings.Index(s.Command[i:], "{{")
		if start < 0 {
			break
		}
		start += i + 2
		end := strings.Index(s.Command[start:], "}}")
		if end < 0 {
			break
		}
		name := strings.TrimSpace(s.Command[start : start+end])
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i = start + end + 1
	}
	return out
}

// Render substitutes {{name}} placeholders with values.
func (s Snippet) Render(values map[string]string) string {
	out := s.Command
	for k, v := range values {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// ProcessInfo is one process row on a dashboard card.
type ProcessInfo struct {
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float64 `json:"mem_percent"`
}

// GPUInfo is one GPU's utilisation.
type GPUInfo struct {
	Name       string  `json:"name"`
	UtilPct    float64 `json:"util_percent"`
	MemoryPct  float64 `json:"memory_percent"`
	MemoryUsed int64   `json:"memory_used_bytes,omitempty"`
	MemoryTot  int64   `json:"memory_total_bytes,omitempty"`
}

// Metrics is a dashboard sample. Nil pointers mean "not available" and are
// rendered as "N/A", never as zero.
type Metrics struct {
	CPUPercent  *float64      `json:"cpu_percent,omitempty"`
	RAMPercent  *float64      `json:"ram_percent,omitempty"`
	DiskPercent *float64      `json:"disk_percent,omitempty"`
	SwapPercent *float64      `json:"swap_percent,omitempty"`
	Load        string        `json:"load,omitempty"`
	Uptime      string        `json:"uptime,omitempty"`
	OS          string        `json:"os,omitempty"`
	Kernel      string        `json:"kernel,omitempty"`
	Hostname    string        `json:"hostname,omitempty"`
	Processes   []ProcessInfo `json:"processes,omitempty"`
	GPUs        []GPUInfo     `json:"gpus,omitempty"`
	DockerUp    *int          `json:"docker_up,omitempty"`
	DockerAll   *int          `json:"docker_all,omitempty"`
	CollectedAt time.Time     `json:"collected_at"`
	Error       string        `json:"error,omitempty"`
}

// Threshold maps a percentage to the UI severity: ok, warn or crit.
func Threshold(pct float64) string {
	switch {
	case pct >= 85:
		return "crit"
	case pct >= 60:
		return "warn"
	default:
		return "ok"
	}
}

// ConnectionStatus is runtime-only and never persisted.
type ConnectionStatus string

const (
	StatusUnknown    ConnectionStatus = "unknown"
	StatusConnecting ConnectionStatus = "connecting"
	StatusOnline     ConnectionStatus = "online"
	StatusOffline    ConnectionStatus = "offline"
)

// NotificationKind selects icon and timeout defaults.
type NotificationKind string

const (
	NotifyInfo    NotificationKind = "info"
	NotifySuccess NotificationKind = "success"
	NotifyWarning NotificationKind = "warning"
	NotifyError   NotificationKind = "error"
)

// Notification is a toast shown by either frontend. Timeouts are applied by
// the frontend: errors stay longer, and 0 means sticky.
type Notification struct {
	ID      string           `json:"id"`
	Kind    NotificationKind `json:"kind"`
	Message string           `json:"message"`
	Detail  string           `json:"detail,omitempty"`
	Created time.Time        `json:"created"`
	Timeout time.Duration    `json:"timeout"`
}

// NewNotification builds a toast with a fresh ID and timestamp. A zero timeout
// means the notification stays until it is dismissed or a timeout is applied
// from the settings.
func NewNotification(kind NotificationKind, message string) Notification {
	return Notification{
		ID:      NewID(),
		Kind:    kind,
		Message: message,
		Created: time.Now().UTC(),
	}
}

// Expired reports whether the notification should be dismissed at now. A
// non-positive timeout never expires.
func (n Notification) Expired(now time.Time) bool {
	if n.Timeout <= 0 {
		return false
	}
	return !now.Before(n.Created.Add(n.Timeout))
}

// Remaining is the time left before auto-dismiss, or 0 when the notification
// is sticky or already expired.
func (n Notification) Remaining(now time.Time) time.Duration {
	if n.Timeout <= 0 {
		return 0
	}
	left := n.Created.Add(n.Timeout).Sub(now)
	if left < 0 {
		return 0
	}
	return left
}

// TransferDirection is upload or download.
type TransferDirection string

const (
	Upload   TransferDirection = "upload"
	Download TransferDirection = "download"
)

// TransferProgress describes an SFTP or snippet operation in flight.
type TransferProgress struct {
	ID        string            `json:"id"`
	Label     string            `json:"label"`
	Direction TransferDirection `json:"direction,omitempty"`
	Done      int64             `json:"done"`
	Total     int64             `json:"total,omitempty"`
	Files     int               `json:"files,omitempty"`
	DoneFiles int               `json:"done_files,omitempty"`
	Error     string            `json:"error,omitempty"`
	Finished  bool              `json:"finished"`
	Cancelled bool              `json:"cancelled,omitempty"`
}

// Fraction returns 0..1, or -1 when the size is unknown.
func (p TransferProgress) Fraction() float64 {
	if p.Total <= 0 {
		return -1
	}
	f := float64(p.Done) / float64(p.Total)
	if f > 1 {
		f = 1
	}
	return f
}

// SnippetChunk is a live line of snippet output.
type SnippetChunk struct {
	RunID  string `json:"run_id"`
	HostID string `json:"host_id"`
	Host   string `json:"host"`
	Stream string `json:"stream"` // stdout, stderr or status
	Line   string `json:"line"`
	Done   bool   `json:"done,omitempty"`
	Err    string `json:"err,omitempty"`
}
