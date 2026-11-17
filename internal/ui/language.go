package ui

import (
	"embed"
	"encoding/json"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

//go:embed flags/*.svg
var flags embed.FS

func preferredLocale(a fyne.App) string {
	saved := a.Preferences().String("language")
	if saved == "" {
		saved = string(lang.SystemLocale())
	}
	return i18n.Resolve(saved)
}
func (s *screen) languageName() string {
	for _, l := range i18n.Languages {
		if l.Code == s.locale {
			return l.Name
		}
	}
	return "English"
}
func (s *screen) chooseLanguage() {
	if s.busy {
		return
	}
	change := func(code string) {
		fyne.CurrentApp().Preferences().SetString("language", code)
		s.locale = preferredLocale(fyne.CurrentApp())
		s.localizeDialogs()
		if s.started {
			s.build()
		} else {
			s.showCardAdvice()
		}
	}
	automatic := fyne.NewMenuItem(s.t("System language"), func() { change("") })
	automatic.Checked = fyne.CurrentApp().Preferences().String("language") == ""
	items := []*fyne.MenuItem{automatic, fyne.NewMenuItemSeparator()}
	for _, l := range i18n.Languages {
		code := l.Code
		item := fyne.NewMenuItem(l.Name, func() { change(code) })
		data, _ := flags.ReadFile("flags/" + code + ".svg")
		item.Icon = fyne.NewStaticResource(code+".svg", data)
		item.Checked = fyne.CurrentApp().Preferences().String("language") == code
		items = append(items, item)
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu(s.t("Language"), items...), s.win.Canvas())
	menu.ShowAtPosition(fyne.CurrentApp().Driver().AbsolutePositionForObject(s.language).Add(fyne.NewPos(0, s.language.Size().Height)))
}
func (s *screen) localizeDialogs() {
	// Fyne file dialogs use the system locale. Replace their common messages
	// through its public translation API when the app language changes.
	messages := map[string]string{}
	for _, key := range []string{"Advanced", "Cancel", "Confirm", "Copy", "Create Folder", "Cut", "Enter filename", "Error", "Favourites", "File", "Folder", "New Folder", "No", "OK", "Open", "Paste", "Quit", "Redo", "Save", "Select all", "Show Hidden Files", "Undo", "Yes", "file.name", "file.parent"} {
		messages[key] = s.t(key)
	}
	data, _ := json.Marshal(messages)
	_ = lang.AddTranslationsForLocale(data, lang.SystemLocale())
}
func (s *screen) info(title, message string, parent fyne.Window) {
	d := dialog.NewCustom(title, s.t("OK"), wrapped(message), parent)
	fitDialog(d, message, parent)
	d.Show()
}
func (s *screen) confirmDialog(title, message string, callback func(bool), parent fyne.Window) {
	d := dialog.NewCustomConfirm(title, s.t("Continue"), s.t("Cancel"), wrapped(message), callback, parent)
	fitDialog(d, message, parent)
	d.Show()
}

// ask: a question with its own button for yes
func (s *screen) ask(title, message, yes string, callback func(bool)) {
	d := dialog.NewCustomConfirm(title, yes, s.t("Cancel"), wrapped(message), callback, s.win)
	fitDialog(d, message, s.win)
	d.Show()
}

// fitDialog gives a dialog with a word-wrapped message a readable width; on its
// own a wrapped label asks for almost none, and the text ends up a few words per
// line. The height fits the message wrapped at that width.
func fitDialog(d dialog.Dialog, message string, parent fyne.Window) {
	w := float32(520)
	if pw := parent.Canvas().Size().Width - 4*theme.Padding(); pw < w {
		w = pw
	}
	text := w - 6*theme.Padding()
	line := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{}).Height + theme.LineSpacing()
	lines := float32(0)
	for _, p := range strings.Split(message, "\n") {
		n := float32(1)
		cur := float32(0)
		for _, word := range strings.Fields(p) {
			ww := fyne.MeasureText(word+" ", theme.TextSize(), fyne.TextStyle{}).Width
			if cur > 0 && cur+ww > text {
				n++
				cur = 0
			}
			cur += ww
		}
		lines += n
	}
	h := lines*line + 12*theme.Padding() + theme.InputBorderSize()
	if ph := parent.Canvas().Size().Height - 4*theme.Padding(); h > ph {
		h = ph
	}
	d.Resize(fyne.NewSize(w, h))
}
func (s *screen) showError(err error) {
	s.info(s.t("Operation could not finish"), s.explain(err)+"\n\n"+s.t("Technical details")+":\n"+err.Error(), s.win)
}

// explain is the user's message in err, translated (i18n.Find), or the general
// advice for an error that has none.
func (s *screen) explain(err error) string {
	if key, args, ok := i18n.Find(err); ok {
		return s.t(key, args...)
	}
	return s.t("Check the selected image and card, then try again.")
}
