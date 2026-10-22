package i18n

import (
	"strings"
	"testing"
)

// TestCatalogsComplete fails when a language table is missing a key or has one
// the others do not, so translations cannot silently fall back to a raw key.
func TestCatalogsComplete(t *testing.T) {
	keys := AllKeys()
	if len(keys) == 0 {
		t.Fatal("no keys defined")
	}
	for _, lang := range Languages() {
		table := catalogs[lang]
		if table == nil {
			t.Fatalf("language %s has no catalog", lang)
		}
		for _, key := range keys {
			if strings.TrimSpace(table[key]) == "" {
				t.Errorf("%s is missing translation for %q", lang, key)
			}
		}
		if extra := len(table) - len(keys); extra != 0 {
			// Extra entries are dead weight but not fatal; report them.
			for k := range table {
				found := false
				for _, key := range keys {
					if k == key {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s has an unknown key %q", lang, k)
				}
			}
		}
	}
}

func TestParse(t *testing.T) {
	cases := map[string]Lang{
		"":        English,
		"en":      English,
		"en-US":   English,
		"zh":      SimplifiedChinese,
		"zh-CN":   SimplifiedChinese,
		"zh_CN":   SimplifiedChinese,
		"zh-Hans": SimplifiedChinese,
		"zh-TW":   SimplifiedChinese, // no traditional table yet; falls back
		"fr":      English,
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranslatorSwitches(t *testing.T) {
	tr := New("en")
	if got := tr.T(CommonSave); got != "Save" {
		t.Fatalf("en save = %q", got)
	}
	tr.SetLang("zh-CN")
	if got := tr.T(CommonSave); got != "保存" {
		t.Fatalf("zh save = %q", got)
	}
	if tr.Lang() != SimplifiedChinese {
		t.Fatalf("lang = %q", tr.Lang())
	}
	// Unknown language falls back to English.
	tr.SetLang("klingon")
	if got := tr.T(CommonSave); got != "Save" {
		t.Fatalf("fallback save = %q", got)
	}
}

func TestFormatting(t *testing.T) {
	tr := New("en")
	if got := tr.T(NotifyImportedCount, 3); got != "Imported 3 host(s)" {
		t.Fatalf("en = %q", got)
	}
	tr.SetLang("zh-CN")
	if got := tr.T(NotifyImportedCount, 3); got != "已导入 3 台主机" {
		t.Fatalf("zh = %q", got)
	}
}

func TestMissing(t *testing.T) {
	for _, lang := range Languages() {
		if missing := Missing(lang); len(missing) != 0 {
			t.Errorf("%s missing %d keys: %v", lang, len(missing), missing)
		}
	}
}

func TestNativeNames(t *testing.T) {
	if got := SimplifiedChinese.NativeName(); got != "简体中文" {
		t.Fatalf("native name = %q", got)
	}
	if got := English.EnglishName(); got != "English" {
		t.Fatalf("english name = %q", got)
	}
}
