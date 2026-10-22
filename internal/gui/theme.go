package gui

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// terminalTheme overrides only the monospace font, which is what the terminal
// widget asks for. Everything else keeps the selected base theme. This is how
// any installed font (including Nerd Fonts for Powerline glyphs) can be used
// for the terminal without changing the rest of the UI.
type terminalTheme struct {
	fyne.Theme
	mono fyne.Resource
}

func (t *terminalTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace && t.mono != nil {
		return t.mono
	}
	return t.Theme.Font(style)
}

var fontCache = struct {
	mu sync.Mutex
	m  map[string]fyne.Resource
}{m: map[string]fyne.Resource{}}

// loadFontResource resolves a font family name or a path to a font file.
func loadFontResource(family string) fyne.Resource {
	family = strings.TrimSpace(family)
	if family == "" {
		return nil
	}
	fontCache.mu.Lock()
	if res, ok := fontCache.m[family]; ok {
		fontCache.mu.Unlock()
		return res
	}
	fontCache.mu.Unlock()

	path := resolveFontPath(family)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	res := fyne.NewStaticResource(filepath.Base(path), data)
	fontCache.mu.Lock()
	fontCache.m[family] = res
	fontCache.mu.Unlock()
	return res
}

func resolveFontPath(family string) string {
	if st, err := os.Stat(family); err == nil && !st.IsDir() {
		return family
	}
	// fontconfig knows where every installed family lives.
	if out, err := exec.Command("fc-match", "-f", "%{file}", family).Output(); err == nil {
		p := strings.TrimSpace(string(out))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Fall back to a filename scan for systems without fontconfig.
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/usr/share/fonts",
		"/usr/local/share/fonts",
		filepath.Join(home, ".local/share/fonts"),
		filepath.Join(home, ".fonts"),
	}
	needle := strings.ToLower(strings.ReplaceAll(family, " ", ""))
	for _, dir := range dirs {
		found := ""
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".ttf" && ext != ".otf" {
				return nil
			}
			base := strings.ToLower(strings.ReplaceAll(filepath.Base(p), " ", ""))
			if strings.Contains(base, needle) {
				found = p
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// applyTheme installs the theme chosen in settings, including the optional
// terminal font.
func (g *App) applyTheme() {
	var base fyne.Theme = theme.DefaultTheme()
	switch g.rt.Store.Settings.UI.Theme {
	case "dark":
		base = theme.DarkTheme()
	case "light":
		base = theme.LightTheme()
	}
	if res := loadFontResource(g.rt.Store.Settings.Terminal.FontFamily); res != nil {
		base = &terminalTheme{Theme: base, mono: res}
	}
	g.fa.Settings().SetTheme(base)
}
