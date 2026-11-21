package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestColabOneNormalizeLocale(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"zh-CN", colabOneLocaleZhCN, true},
		{"zh_SG.UTF-8", colabOneLocaleZhCN, true},
		{"zh-Hans", colabOneLocaleZhCN, true},
		{"zh", colabOneLocaleZhCN, true},
		{"zh-TW", colabOneLocaleZhTW, true},
		{"zh-Hant", colabOneLocaleZhTW, true},
		{"zh-HK", colabOneLocaleZhHK, true},
		{"zh-MO", colabOneLocaleZhHK, true},
		{"zh-Hant-HK", colabOneLocaleZhHK, true},
		{"yue-HK", colabOneLocaleZhHK, true},
		{"en", colabOneLocaleEnUS, true},
		{"en-GB", colabOneLocaleEnUS, true},
		{"ja", colabOneLocaleJaJP, true},
		{"ja_JP.UTF-8", colabOneLocaleJaJP, true},
		{"ko", colabOneLocaleKoKR, true},
		{"ko-KR", colabOneLocaleKoKR, true},
		{"fr-FR", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := colabOneNormalizeLocale(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("colabOneNormalizeLocale(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestColabOneNativeCatalogCompleteness(t *testing.T) {
	base := colabOneNativeCatalog[colabOneLocaleZhCN]
	locales := []string{
		colabOneLocaleZhCN,
		colabOneLocaleEnUS,
		colabOneLocaleZhTW,
		colabOneLocaleZhHK,
		colabOneLocaleJaJP,
		colabOneLocaleKoKR,
	}
	for _, loc := range locales {
		table, ok := colabOneNativeCatalog[loc]
		if !ok {
			t.Fatalf("missing catalog for locale %s", loc)
		}
		for key := range base {
			if val, exists := table[key]; !exists || val == "" {
				t.Fatalf("locale %s missing translation for key %q", loc, key)
			}
		}
	}
}

func TestColabOneDesktopSetLocale(t *testing.T) {
	prev := colabOneGetLocale()
	t.Cleanup(func() { colabOneSetLocaleState(prev) })

	d := &ColabOneDesktop{}
	if err := d.SetLocale("ja-JP"); err != nil {
		t.Fatalf("SetLocale(ja-JP) unexpected err: %v", err)
	}
	if got := colabOneGetLocale(); got != colabOneLocaleJaJP {
		t.Fatalf("active locale = %q, want %q", got, colabOneLocaleJaJP)
	}
	if got := colabOneT("window.logs"); got != "実行ログ · ColabOne" {
		t.Fatalf("localized title = %q", got)
	}
	if err := d.SetLocale("invalid-locale"); err == nil {
		t.Fatal("expected error for invalid locale")
	}
}

func TestColabOneResolveInitialLocalePriority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COLABONE_CONFIG_DIR", dir)
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "fr_FR.UTF-8")

	// Unsupported OS locale falls back to en-US (matching frontend detectColabOneSystemLocale).
	if got := colabOneResolveInitialLocale(); got != colabOneLocaleEnUS {
		t.Fatalf("unsupported OS locale = %q, want %q", got, colabOneLocaleEnUS)
	}

	// Supported OS locale is honored when settings.json has no manual override.
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := colabOneResolveInitialLocale(); got != colabOneLocaleJaJP {
		t.Fatalf("OS ja_JP locale = %q, want %q", got, colabOneLocaleJaJP)
	}

	// Explicit saved locale in settings.json overrides OS locale.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"locale":"zh-HK"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := colabOneResolveInitialLocale(); got != colabOneLocaleZhHK {
		t.Fatalf("saved settings.json locale = %q, want %q", got, colabOneLocaleZhHK)
	}
}

func TestColabOneConfigDirMatchesPythonController(t *testing.T) {
	t.Setenv("COLABONE_CONFIG_DIR", "")
	t.Setenv("LOCALAPPDATA", "")
	got := colabOneConfigDir()
	if runtime.GOOS == "linux" && !strings.HasSuffix(got, filepath.Join(".local", "share", "ColabOne")) {
		t.Fatalf("linux config dir = %q, want suffix .local/share/ColabOne", got)
	}
}
