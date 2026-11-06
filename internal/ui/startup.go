package ui

import (
	"context"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
)

func (s *screen) start() {
	s.started = true
	s.build()
	s.listDrives()
	ctx, cancel := context.WithCancel(context.Background())
	s.updateCancel = cancel
	go func() {
		update, err := catalog.CheckAppUpdate(ctx, Version)
		if err == nil && update != nil && ctx.Err() == nil {
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				s.update = update
				s.renderAppUpdate()
			})
		}
	}()
}

func (s *screen) showCardAdvice() {
	s.language = widget.NewButton(s.languageName(), s.chooseLanguage)
	body := container.NewVBox(widget.NewLabelWithStyle(s.t("Your SD card matters"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	for _, key := range []string{
		"Android is much heavier on storage than ArkOS and similar Linux firmware. Use a high-quality, fast microSD card for a smooth experience.",
		"Low-quality cards can fail early, behave unpredictably, cause crashes, or corrupt data.",
		"We don’t recommend specific brands: a brand label doesn’t prove a card is genuine. Buy quality storage from reputable sellers.",
		"16 GB is the minimum. For Android, we recommend at least 32 GB. 128 GB is the sweet spot for Android and EASYROMS to fit comfortably.",
	} {
		body.Add(wrapped(s.t(key)))
		body.Add(widget.NewSeparator())
	}
	agree := widget.NewButton(s.t("I understand"), func() {
		fyne.CurrentApp().Preferences().SetBool("sd-card-advice-v1", true)
		s.start()
	})
	agree.Importance = widget.HighImportance
	s.win.SetContent(container.NewPadded(container.NewBorder(
		container.NewBorder(nil, nil, nil, s.language, widget.NewLabelWithStyle("andr36oid SD Flasher", 0, fyne.TextStyle{Bold: true})),
		container.NewVBox(agree, widget.NewButton(s.t("Quit"), s.win.Close)), nil, nil, container.NewVScroll(body))))
}

func (s *screen) renderAppUpdate() {
	if s.update == nil {
		s.updateBanner.Hide()
		return
	}
	download := widget.NewButton(s.t("Download update"), func() {
		link, err := url.Parse(s.update.URL)
		if err == nil {
			_ = fyne.CurrentApp().OpenURL(link)
		}
	})
	dismiss := widget.NewButton(s.t("Close"), func() {
		s.update = nil
		s.renderAppUpdate()
	})
	s.updateBanner.Objects = []fyne.CanvasObject{container.NewBorder(nil, nil, nil, container.NewHBox(download, dismiss), wrapped(s.t("Flasher %s is available", s.update.Version)))}
	s.updateBanner.Show()
	s.updateBanner.Refresh()
}
