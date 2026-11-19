package ui

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"golang.org/x/image/font/sfnt"
)

func TestLanguageChangePreservesSetup(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(uiTheme())
	w := a.NewWindow("test")
	defer w.Close()
	w.Resize(fyne.NewSize(820, 760))
	drive := platform.Drive{Path: "test-card", Size: 32 << 30}
	s := &screen{win: w, locale: "en", im: &firmware.Image{Version: "test", Profiles: []firmware.Profile{{ID: "r36s", Name: "R36S - Panel 4", Console: "R36S"}}}, profile: "r36s", drive: &drive, drives: []platform.Drive{drive}, card: &flash.Card{Installed: true, Treble: true, Profile: "r36s", NoROMs: true}, task: taskUpdate, updating: true, custom: "example.dtb", customAccepted: true}
	for _, l := range i18n.Languages {
		s.locale = l.Code
		s.localizeDialogs()
		s.build()
		if !s.updating || s.profile != "r36s" || s.drive == nil || !s.noROMs.Checked || s.custom != "example.dtb" || !s.customAccepted {
			t.Fatalf("%s: lost setup state", l.Code)
		}
		if s.task != taskUpdate || s.action.Disabled() {
			t.Fatalf("%s: task changed or write disabled", l.Code)
		}
		if lang.L("Cancel") != s.t("Cancel") {
			t.Fatalf("%s: file dialog not translated", l.Code)
		}
		if dir := os.Getenv("SDFLASHER_SCREENSHOTS"); dir != "" {
			os.MkdirAll(dir, 0755)
			f, e := os.Create(filepath.Join(dir, l.Code+".png"))
			if e != nil {
				t.Fatal(e)
			}
			if e = png.Encode(f, w.Canvas().Capture()); e != nil {
				t.Fatal(e)
			}
			f.Close()
		}
	}
}
func TestBundledFontsCoverTranslations(t *testing.T) {
	for _, name := range []string{"Regular", "Bold"} {
		data, _ := fontFiles.ReadFile("fonts/SDFlasherUI-" + name + ".ttf")
		f, e := sfnt.Parse(data)
		if e != nil {
			t.Fatal(e)
		}
		var buf sfnt.Buffer
		for _, l := range i18n.Languages {
			raw, _ := i18n.Files.ReadFile("locales/" + l.Code + ".json")
			m := map[string]string{}
			json.Unmarshal(raw, &m)
			m["language-name"] = l.Name
			for key, value := range m {
				for _, r := range value {
					if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
						continue
					}
					index, e := f.GlyphIndex(&buf, r)
					if e != nil || index == 0 {
						t.Errorf("%s/%s: missing %U in %q", name, l.Code, r, key)
					}
				}
			}
		}
	}
}
