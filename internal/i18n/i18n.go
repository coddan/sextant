// Package i18n provides the translation framework used by both frontends.
//
// It is deliberately tiny: a typed key, a per-language string table and a
// small thread-safe Translator. There are no external dependencies, matching
// the zero-dependency approach used throughout. Every key must exist in
// every language; a unit test enforces that, so a missing translation fails the
// build instead of showing a raw key.
package i18n

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Lang identifies a supported language.
type Lang string

const (
	// English is the source language and the fallback for every key.
	English Lang = "en"
	// SimplifiedChinese localises the interface for zh-CN users.
	SimplifiedChinese Lang = "zh-CN"
)

// Languages lists the supported languages in display order.
func Languages() []Lang {
	return []Lang{English, SimplifiedChinese}
}

// Parse normalises a language tag from settings or the environment. It accepts
// the common forms users and systems produce: "zh", "zh-Hans", "zh_CN",
// "zh-CN" all resolve to SimplifiedChinese; anything unknown falls back to
// English.
func Parse(tag string) Lang {
	t := strings.ToLower(strings.TrimSpace(tag))
	t = strings.ReplaceAll(t, "_", "-")
	switch {
	case t == "" || t == "en" || strings.HasPrefix(t, "en-"):
		return English
	case t == "zh" || strings.HasPrefix(t, "zh-"):
		// Only Simplified Chinese ships today; any other zh variant falls
		// back to it rather than to English.
		return SimplifiedChinese
	default:
		return English
	}
}

// NativeName is the language's name in its own script, for the language picker.
func (l Lang) NativeName() string {
	switch l {
	case SimplifiedChinese:
		return "简体中文"
	default:
		return "English"
	}
}

// EnglishName is the language's name in English.
func (l Lang) EnglishName() string {
	switch l {
	case SimplifiedChinese:
		return "Simplified Chinese"
	default:
		return "English"
	}
}

// BCP47 returns the canonical tag, suitable for storing in settings.json.
func (l Lang) BCP47() string { return string(l) }

// Translator holds the active language and resolves keys to strings. It is safe
// for concurrent use because the frontends render from multiple goroutines.
type Translator struct {
	mu   sync.RWMutex
	lang Lang
}

// New returns a Translator for the given language, parsed leniently.
func New(lang string) *Translator {
	return &Translator{lang: Parse(lang)}
}

// Lang returns the active language.
func (t *Translator) Lang() Lang {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lang
}

// SetLang switches language. Unknown tags fall back to English.
func (t *Translator) SetLang(lang string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lang = Parse(lang)
}

// T resolves a key. When args are given they are formatted with fmt.Sprintf, so
// translations may use %s, %d and the usual verbs.
func (t *Translator) T(key Key, args ...any) string {
	t.mu.RLock()
	lang := t.lang
	t.mu.RUnlock()
	return translate(lang, key, args...)
}

// translate resolves a key for a fixed language; it is the pure core of T.
func translate(lang Lang, key Key, args ...any) string {
	text, ok := catalogs[lang][key]
	if !ok || text == "" {
		// Fall back to English before giving up, so a partial translation
		// degrades to the source language rather than to a raw key.
		text = catalogs[English][key]
	}
	if text == "" {
		text = string(key)
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

// T is a package-level convenience that uses the default language. Frontends
// that support switching should hold a *Translator instead.
func T(key Key, args ...any) string {
	return translate(English, key, args...)
}

// Missing reports keys that are absent from the given language's catalog. It
// exists so tests and tooling can list gaps.
func Missing(lang Lang) []Key {
	var missing []Key
	for _, key := range AllKeys() {
		if catalogs[lang][key] == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

// AllKeys returns every known key, sorted. It is used by tests that assert
// catalogs stay in sync.
func AllKeys() []Key {
	seen := map[Key]struct{}{}
	for _, table := range catalogs {
		for k := range table {
			seen[k] = struct{}{}
		}
	}
	out := make([]Key, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
