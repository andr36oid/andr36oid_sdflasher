package i18n

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestLocaleSelection(t *testing.T) {
	cases := map[string]string{"": "en", "C": "en", "fr_FR": "en", "en_US.UTF-8": "en", "de_DE": "de", "ru-RU": "ru", "uk_UA": "uk", "es-MX": "es", "pt_PT": "pt", "pt-BR": "pt-BR", "pt_BR.UTF-8": "pt-BR", "pt-Latn-BR": "pt-BR", "pt-AO": "pt", "hi-IN": "hi", "ja_JP.UTF-8": "ja", "ja": "ja", "ko_KR": "ko", "zh-CN": "zh-Hans", "zh-Hans-SG": "zh-Hans", "zh-TW": "en", "zh_TW.UTF-8": "en", "zh-Hant-TW": "en", "zh-Hant": "en"}
	for input, want := range cases {
		if got := Resolve(input); got != want {
			t.Errorf("%q: got %s, want %s", input, got, want)
		}
	}
}
func TestCatalogsComplete(t *testing.T) {
	format := regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)
	for _, l := range Languages {
		m := catalogs[l.Code]
		if len(m) != len(catalogs["en"]) {
			t.Errorf("%s: different number of keys", l.Code)
		}
		for key, english := range catalogs["en"] {
			value, ok := m[key]
			if !ok || strings.TrimSpace(value) == "" {
				t.Errorf("%s missing %q", l.Code, key)
			}
			if fmt.Sprint(format.FindAllString(english, -1)) != fmt.Sprint(format.FindAllString(value, -1)) {
				t.Errorf("%s format changed for %q", l.Code, key)
			}
		}
	}
}
func TestFallbackAndFormatting(t *testing.T) {
	if Text("unknown", "Continue") != "Continue" {
		t.Fatal("unsupported language must use English")
	}
	if Text("ko", "not-a-key") != "not-a-key" {
		t.Fatal("technical detail should be preserved")
	}
	if Text("zh-CN", "%d device profiles", 38) != "38 个设备配置" {
		t.Fatal("format lost")
	}
	if Text("pt-BR", "Download") == Text("pt-PT", "Download") {
		t.Fatal("Brazilian Portuguese must be a distinct catalog")
	}
}
