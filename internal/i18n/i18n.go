// Package i18n contains the translations shared by the desktop and Android apps.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var Files embed.FS

type Language struct{ Code, Name, Flag string }

var Languages = []Language{
	{"en", "English", "🇬🇧"}, {"de", "Deutsch", "🇩🇪"}, {"ru", "Русский", "🇷🇺"},
	{"uk", "Українська", "🇺🇦"}, {"es", "Español", "🇪🇸"}, {"pt", "Português", "🇵🇹"},
	{"pt-BR", "Português brasileiro", "🇧🇷"}, {"hi", "हिन्दी", "🇮🇳"},
	{"ja", "日本語", "🇯🇵"}, {"ko", "한국어", "🇰🇷"}, {"zh-Hans", "简体中文", "🇨🇳"},
}
var catalogs = map[string]map[string]string{}

func init() {
	for _, l := range Languages {
		b, e := Files.ReadFile("locales/" + l.Code + ".json")
		if e != nil {
			panic(e)
		}
		var m map[string]string
		if e = json.Unmarshal(b, &m); e != nil {
			panic(e)
		}
		catalogs[l.Code] = m
	}
}
func Resolve(locale string) string {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
	s = strings.SplitN(s, ".", 2)[0]
	s = strings.SplitN(s, "@", 2)[0]
	if strings.HasPrefix(s, "pt-") && (strings.Contains("-"+s+"-", "-br-")) {
		return "pt-BR"
	}
	base := strings.SplitN(s, "-", 2)[0]
	if base == "zh" {
		for _, part := range strings.Split(s, "-")[1:] {
			if part == "tw" || part == "hant" {
				return "en"
			}
		}
		return "zh-Hans"
	}
	for _, l := range Languages {
		if l.Code == base {
			return base
		}
	}
	return "en"
}
func Text(locale, key string, args ...any) string {
	code := Resolve(locale)
	out := catalogs[code][key]
	if out == "" {
		out = catalogs["en"][key]
	}
	if out == "" {
		out = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(out, args...)
	}
	return out
}
