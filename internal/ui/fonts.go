package ui

import (
	"embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

//go:embed fonts/*.ttf
var fontFiles embed.FS

func uiTheme() fyne.Theme {
	normal, _ := fontFiles.ReadFile("fonts/SDFlasherUI-Regular.ttf")
	bold, _ := fontFiles.ReadFile("fonts/SDFlasherUI-Bold.ttf")
	return localizedTheme{Theme: theme.DefaultTheme(), normal: fyne.NewStaticResource("SDFlasherUI-Regular.ttf", normal), bold: fyne.NewStaticResource("SDFlasherUI-Bold.ttf", bold)}
}

type localizedTheme struct {
	fyne.Theme
	normal, bold fyne.Resource
}

func (t localizedTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace {
		return t.Theme.Font(style)
	}
	if style.Bold {
		return t.bold
	}
	return t.normal
}

// Grey text (a version, a tag such as "newest" or "not a card", a choice that can't
// be taken, technical details) is drawn in the theme's disabled and placeholder
// colours. Fyne's own are too faint to read (1.5:1 on the dark background); these
// keep at least 4.5:1 against the window and the cards in both variants, and are
// still clearly dimmer than the normal text.
var (
	subtleDark  = color.NRGBA{R: 0x8c, G: 0x8c, B: 0x8c, A: 0xff}
	subtleLight = color.NRGBA{R: 0x6e, G: 0x6e, B: 0x6e, A: 0xff}
)

func (t localizedTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameDisabled:
		if variant == theme.VariantLight {
			return subtleLight
		}
		return subtleDark
	case theme.ColorNamePlaceHolder:
		if variant == theme.VariantLight {
			return subtleLight
		}
	}
	return t.Theme.Color(name, variant)
}
