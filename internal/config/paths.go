package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ErrNotFound reports a configuration file that does not exist yet.
var ErrNotFound = errors.New("config: file not found")

// Paths holds every file Sextant uses. The store keeps a versioned schema and
// a separate encrypted secret store, so it writes those files alongside the
// plain JSON documents rather than folding secrets into them.
type Paths struct {
	Dir        string
	Settings   string
	Hosts      string
	Snippets   string
	Secrets    string
	SecretKey  string
	State      string
	KnownHosts string
	SSHConfig  string
	Portable   bool
}

// ConfigDir returns the Sextant configuration directory, honouring portable
// mode. Portable mode lets users keep settings on a USB stick next to the
// binary: set SEXTANT_PORTABLE=1, or place a file named
// "sextant.portable" beside the executable.
func ConfigDir() (string, bool, error) {
	if dir, ok := portableDir(); ok {
		return dir, true, nil
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", false, fmt.Errorf("config: APPDATA is not set")
		}
		return filepath.Join(base, "sextant"), false, nil
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		return filepath.Join(home, "Library", "Application Support", "sextant"), false, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sextant"), false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(home, ".config", "sextant"), false, nil
}

func portableDir() (string, bool) {
	if v := os.Getenv("SEXTANT_PORTABLE"); v == "1" || v == "true" || v == "yes" {
		if exe, err := os.Executable(); err == nil {
			return filepath.Join(filepath.Dir(exe), "sextant-data"), true
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	exe, _ = filepath.EvalSymlinks(exe)
	dir := filepath.Dir(exe)
	for _, marker := range []string{"sextant.portable", "portable"} {
		if st, err := os.Stat(filepath.Join(dir, marker)); err == nil && !st.IsDir() {
			return filepath.Join(dir, "sextant-data"), true
		}
	}
	return "", false
}

// DefaultPaths resolves all paths and creates the config directory.
func DefaultPaths() (Paths, error) {
	dir, portable, err := ConfigDir()
	if err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Paths{}, fmt.Errorf("config: create %s: %w", dir, err)
	}
	home, _ := os.UserHomeDir()
	p := Paths{
		Dir:       dir,
		Settings:  filepath.Join(dir, "settings.json"),
		Hosts:     filepath.Join(dir, "hosts.json"),
		Snippets:  filepath.Join(dir, "snippets.json"),
		Secrets:   filepath.Join(dir, "secrets.json"),
		SecretKey: filepath.Join(dir, "secrets.key"),
		State:     filepath.Join(dir, "state.json"),
		Portable:  portable,
	}
	if home != "" {
		p.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
		p.SSHConfig = filepath.Join(home, ".ssh", "config")
	}
	return p, nil
}
