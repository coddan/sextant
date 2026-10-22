package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// General holds runtime settings.
type General struct {
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	DefaultShell           string `json:"default_shell"`
	MaxConcurrent          int    `json:"max_concurrent_connections"`
	ConfirmCloseTab        bool   `json:"confirm_close_tab"`
	ImportSSHConfig        bool   `json:"import_ssh_config"`
	StartMinimized         bool   `json:"start_minimized"`
}

// UI holds visual settings.
type UI struct {
	Theme        string `json:"theme"`
	Language     string `json:"language"`
	ShowIP       bool   `json:"show_ip"`
	ShowUptime   bool   `json:"show_uptime"`
	CardLayout   string `json:"card_layout"`
	HideOffline  bool   `json:"hide_offline"`
	SortBy       string `json:"sort_by"`
	StatusFilter string `json:"status_filter"`
}

// Terminal holds terminal emulation settings. Any installed font can be
// selected, including Nerd Fonts for Powerline glyphs.
type Terminal struct {
	FontFamily     string  `json:"font_family"`
	FontSize       float64 `json:"font_size"`
	LineHeight     float64 `json:"line_height"`
	Scrollback     int     `json:"scrollback_lines"`
	SudoAutofill   bool    `json:"sudo_autofill"`
	CopyOnSelect   bool    `json:"copy_on_select"`
	BracketedPaste bool    `json:"bracketed_paste"`
	ConfirmPaste   bool    `json:"confirm_multiline_paste"`
}

// Notifications configures toast auto-dismiss.
type Notifications struct {
	TimeoutSeconds      int  `json:"timeout_seconds"`
	ErrorTimeoutSeconds int  `json:"error_timeout_seconds"`
	StickyErrors        bool `json:"sticky_errors"`
}

// Security configures at-rest encryption of credentials.
type Security struct {
	// MasterPassword means the secret store is unlocked with a password the
	// user types at startup. Without it a random key file (0600) is used.
	MasterPassword  bool `json:"master_password"`
	AutoLockMinutes int  `json:"auto_lock_minutes"`
}

// Sync configures optional encrypted config sync (self-hosted bonus).
type Sync struct {
	Enabled        bool   `json:"enabled"`
	URL            string `json:"url,omitempty"`
	Username       string `json:"username,omitempty"`
	PasswordSecret string `json:"password_secret,omitempty"`
	AutoPush       bool   `json:"auto_push"`
}

// Keybindings are TUI key names. Empty disables the action.
type Keybindings struct {
	Quit       string `json:"quit"`
	Search     string `json:"search"`
	Dashboard  string `json:"dashboard"`
	SFTP       string `json:"sftp"`
	Snippets   string `json:"snippets"`
	Settings   string `json:"settings"`
	NextScreen string `json:"next_screen"`
	NextTab    string `json:"next_tab"`
	CloseTab   string `json:"close_tab"`
	CopyMode   string `json:"copy_mode"`
	Find       string `json:"find"`
	Paste      string `json:"paste"`
	NewHost    string `json:"new_host"`
	Refresh    string `json:"refresh"`
}

// Settings is the root of settings.json.
type Settings struct {
	Version       int           `json:"version"`
	General       General       `json:"general"`
	UI            UI            `json:"ui"`
	Terminal      Terminal      `json:"terminal"`
	Notifications Notifications `json:"notifications"`
	Security      Security      `json:"security"`
	Sync          Sync          `json:"sync"`
	Keybindings   Keybindings   `json:"keybindings"`
}

// DefaultSettings returns the shipped defaults.
func DefaultSettings() Settings {
	return Settings{
		Version: model.ConfigVersion,
		General: General{
			RefreshIntervalSeconds: 30,
			DefaultShell:           "",
			MaxConcurrent:          10,
			ConfirmCloseTab:        true,
			ImportSSHConfig:        true,
		},
		UI: UI{
			Theme:        "default",
			Language:     "en",
			ShowIP:       true,
			ShowUptime:   true,
			CardLayout:   "grid",
			StatusFilter: "all",
			SortBy:       "name",
		},
		Terminal: Terminal{
			FontFamily:     "JetBrains Mono",
			FontSize:       13,
			LineHeight:     1.2,
			Scrollback:     10000,
			SudoAutofill:   false,
			BracketedPaste: true,
			ConfirmPaste:   true,
		},
		Notifications: Notifications{
			TimeoutSeconds:      5,
			ErrorTimeoutSeconds: 12,
		},
		Security: Security{MasterPassword: false, AutoLockMinutes: 15},
		Keybindings: Keybindings{
			Quit:       "q",
			Search:     "/",
			Dashboard:  "g",
			SFTP:       "f",
			Snippets:   "s",
			Settings:   ",",
			NextScreen: "Tab",
			NextTab:    "Ctrl+N",
			// Ctrl+W is deliberately NOT bound to closing a tab: it is delete-
			// previous-word in every shell. F10 requires intent.
			CloseTab: "F10",
			CopyMode: "Ctrl+Space",
			Find:     "Ctrl+F",
			Paste:    "Ctrl+V",
			NewHost:  "a",
			Refresh:  "r",
		},
	}
}

// Normalize repairs out-of-range values after loading a hand-edited file.
func (s *Settings) Normalize() {
	d := DefaultSettings()
	if s.Version == 0 {
		s.Version = model.ConfigVersion
	}
	if s.General.RefreshIntervalSeconds < 5 {
		s.General.RefreshIntervalSeconds = d.General.RefreshIntervalSeconds
	}
	if s.General.MaxConcurrent < 1 {
		s.General.MaxConcurrent = d.General.MaxConcurrent
	}
	if s.UI.Theme == "" {
		s.UI.Theme = d.UI.Theme
	}
	if s.UI.Language == "" {
		s.UI.Language = d.UI.Language
	}
	if s.Terminal.FontSize <= 0 {
		s.Terminal.FontSize = d.Terminal.FontSize
	}
	if s.Terminal.LineHeight <= 0 {
		s.Terminal.LineHeight = d.Terminal.LineHeight
	}
	if s.Terminal.Scrollback < 100 {
		s.Terminal.Scrollback = d.Terminal.Scrollback
	}
	if s.Notifications.TimeoutSeconds < 0 {
		s.Notifications.TimeoutSeconds = d.Notifications.TimeoutSeconds
	}
	if s.Notifications.ErrorTimeoutSeconds < 0 {
		s.Notifications.ErrorTimeoutSeconds = d.Notifications.ErrorTimeoutSeconds
	}
	if s.Keybindings.CloseTab == "Ctrl+W" {
		s.Keybindings.CloseTab = d.Keybindings.CloseTab
	}
}

// TimeoutFor returns how long a notification of the given kind should stay on
// screen. Errors use the error timeout and can be made sticky, so a failure a
// user must read is never whisked away.
func (n Notifications) TimeoutFor(kind model.NotificationKind) time.Duration {
	if kind == model.NotifyError {
		if n.StickyErrors {
			return 0
		}
		return time.Duration(n.ErrorTimeoutSeconds) * time.Second
	}
	return time.Duration(n.TimeoutSeconds) * time.Second
}

// ApplyTimeout stamps the configured timeout onto a notification. A zero
// timeout means the toast stays until dismissed.
func (n Notifications) ApplyTimeout(notification model.Notification) model.Notification {
	notification.Timeout = n.TimeoutFor(notification.Kind)
	return notification
}

// HostsFile is hosts.json.
type HostsFile struct {
	Version int           `json:"version"`
	Hosts   []model.Host  `json:"hosts"`
	Groups  []model.Group `json:"groups,omitempty"`
}

// SnippetsFile is snippets.json.
type SnippetsFile struct {
	Version  int             `json:"version"`
	Snippets []model.Snippet `json:"snippets"`
}

// State is runtime UI state persisted between runs (window size, last tab).
type State struct {
	Version  int    `json:"version"`
	WindowW  int    `json:"window_width,omitempty"`
	WindowH  int    `json:"window_height,omitempty"`
	LastView string `json:"last_view,omitempty"`
	Theme    string `json:"theme,omitempty"`
	Language string `json:"language,omitempty"`
}

// Store owns all persisted state.
type Store struct {
	Paths    Paths
	Settings Settings
	Hosts    []model.Host
	Groups   []model.Group
	Snippets []model.Snippet
	Secrets  *SecretStore
	State    State
}

// Open loads (or initialises) the configuration directory.
func Open() (*Store, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	return OpenAt(paths)
}

// OpenAt loads from explicit paths. Used by tests and portable mode.
func OpenAt(paths Paths) (*Store, error) {
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{Paths: paths, Settings: DefaultSettings()}
	if err := readJSON(paths.Settings, &s.Settings); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	s.Settings.Normalize()

	var hf HostsFile
	if err := readJSON(paths.Hosts, &hf); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	hf.Version = model.ConfigVersion
	s.Hosts = hf.Hosts
	s.Groups = hf.Groups
	normalizeHosts(s.Hosts)

	var sf SnippetsFile
	if err := readJSON(paths.Snippets, &sf); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	sf.Version = model.ConfigVersion
	s.Snippets = sf.Snippets

	var st State
	_ = readJSON(paths.State, &st)
	s.State = st

	secrets, err := openSecretStore(paths)
	if err != nil {
		return nil, err
	}
	s.Secrets = secrets
	return s, nil
}

func normalizeHosts(hosts []model.Host) {
	now := time.Now().UTC()
	for i := range hosts {
		h := &hosts[i]
		if h.ID == "" {
			h.ID = model.NewID()
		}
		if h.Port == 0 {
			h.Port = 22
		}
		if h.Monitoring == "" {
			h.Monitoring = model.MonitorSSH
		}
		if h.Source == "" {
			h.Source = model.SourceManual
		}
		if h.CreatedAt.IsZero() {
			h.CreatedAt = now
		}
		if h.UpdatedAt.IsZero() {
			h.UpdatedAt = h.CreatedAt
		}
		h.Tags = h.SortedTags()
	}
}

// Save writes every file atomically.
func (s *Store) Save() error {
	if s.Settings.Version == 0 {
		s.Settings.Version = model.ConfigVersion
	}
	if err := writeJSON(s.Paths.Settings, s.Settings); err != nil {
		return err
	}
	hf := HostsFile{Version: model.ConfigVersion, Hosts: s.Hosts, Groups: s.Groups}
	if err := writeJSON(s.Paths.Hosts, hf); err != nil {
		return err
	}
	sf := SnippetsFile{Version: model.ConfigVersion, Snippets: s.Snippets}
	if err := writeJSON(s.Paths.Snippets, sf); err != nil {
		return err
	}
	if s.State.Version == 0 {
		s.State.Version = model.ConfigVersion
	}
	if err := writeJSON(s.Paths.State, s.State); err != nil {
		return err
	}
	return s.Secrets.Save()
}

// Host returns a host by ID.
func (s *Store) Host(id string) (model.Host, bool) {
	for _, h := range s.Hosts {
		if h.ID == id {
			return h, true
		}
	}
	return model.Host{}, false
}

// HostByName returns a host by display name (case-insensitive).
func (s *Store) HostByName(name string) (model.Host, bool) {
	for _, h := range s.Hosts {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return model.Host{}, false
}

// UpsertHost inserts or replaces a host by ID.
func (s *Store) UpsertHost(h model.Host) {
	h.UpdatedAt = time.Now().UTC()
	if h.CreatedAt.IsZero() {
		h.CreatedAt = h.UpdatedAt
	}
	h.Tags = h.SortedTags()
	for i := range s.Hosts {
		if s.Hosts[i].ID == h.ID {
			s.Hosts[i] = h
			return
		}
	}
	s.Hosts = append(s.Hosts, h)
}

// DeleteHost removes a host and all secrets that belong to it.
func (s *Store) DeleteHost(id string) {
	out := s.Hosts[:0]
	for _, h := range s.Hosts {
		if h.ID != id {
			out = append(out, h)
		}
	}
	s.Hosts = out
	s.Secrets.DeletePrefix("host:" + id + ":")
}

// HostPassword sets (or clears) a host's login password in the secret store.
// The password is never written to hosts.json.
func (s *Store) HostPassword(h *model.Host, password string) error {
	id := "host:" + h.ID + ":password"
	if password == "" {
		h.PasswordSecret = ""
		s.Secrets.Delete(id)
		return nil
	}
	if err := s.Secrets.Set(id, password); err != nil {
		return err
	}
	h.PasswordSecret = id
	return nil
}

// Password returns the stored login password, if any.
func (s *Store) Password(h model.Host) (string, bool) {
	if h.PasswordSecret == "" {
		return "", false
	}
	return s.Secrets.Get(h.PasswordSecret)
}

// HostPassphrase sets (or clears) a host's key passphrase.
func (s *Store) HostPassphrase(h *model.Host, passphrase string) error {
	id := "host:" + h.ID + ":passphrase"
	if passphrase == "" {
		h.PassphraseSecret = ""
		s.Secrets.Delete(id)
		return nil
	}
	if err := s.Secrets.Set(id, passphrase); err != nil {
		return err
	}
	h.PassphraseSecret = id
	return nil
}

// Passphrase returns the stored key passphrase, if any.
func (s *Store) Passphrase(h model.Host) (string, bool) {
	if h.PassphraseSecret == "" {
		return "", false
	}
	return s.Secrets.Get(h.PassphraseSecret)
}

// AddGroup creates a group if it does not exist.
func (s *Store) AddGroup(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for _, g := range s.Groups {
		if strings.EqualFold(g.Name, name) {
			return
		}
	}
	s.Groups = append(s.Groups, model.Group{Name: name})
}

// GroupByName returns a group.
func (s *Store) GroupByName(name string) (model.Group, bool) {
	for _, g := range s.Groups {
		if strings.EqualFold(g.Name, name) {
			return g, true
		}
	}
	return model.Group{}, false
}

// AllGroups returns configured groups plus any referenced by hosts.
func (s *Store) AllGroups() []model.Group {
	out := append([]model.Group(nil), s.Groups...)
	seen := map[string]bool{}
	for _, g := range out {
		seen[strings.ToLower(g.Name)] = true
	}
	for _, h := range s.Hosts {
		if h.Group != "" && !seen[strings.ToLower(h.Group)] {
			seen[strings.ToLower(h.Group)] = true
			out = append(out, model.Group{Name: h.Group})
		}
	}
	return out
}

// HasMasterPassword reports whether the secret store needs a password.
func (s *Store) HasMasterPassword() bool {
	return s.Secrets.RequiresPassword()
}

// Unlock unlocks the secret store with the master password.
func (s *Store) Unlock(password string) error {
	return s.Secrets.Unlock(password)
}

// Locked reports whether secrets are inaccessible until Unlock.
func (s *Store) Locked() bool {
	return s.Secrets.Locked()
}

// EnableMasterPassword switches the store to password encryption.
func (s *Store) EnableMasterPassword(password string) error {
	if err := s.Secrets.EnableMasterPassword(password); err != nil {
		return err
	}
	s.Settings.Security.MasterPassword = true
	return s.Save()
}

// DisableMasterPassword switches back to the local key file.
func (s *Store) DisableMasterPassword(password string) error {
	if err := s.Secrets.DisableMasterPassword(password); err != nil {
		return err
	}
	s.Settings.Security.MasterPassword = false
	return s.Save()
}

// ChangeMasterPassword re-encrypts with a new password.
func (s *Store) ChangeMasterPassword(old, next string) error {
	return s.Secrets.ChangePassword(old, next)
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ErrNotFound
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// The rename keeps the temp file's mode, but be explicit on platforms that
	// inherit differently.
	_ = os.Chmod(path, mode)
	return nil
}
