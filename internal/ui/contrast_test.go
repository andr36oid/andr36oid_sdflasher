package ui

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// luminance is the relative luminance of c (WCAG 2).
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		x := float64(v) / 0xffff
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// Grey text reaches 4.5:1 against the window and the cards in both variants.
func TestGreyTextContrast(t *testing.T) {
	th := uiTheme()
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		for _, text := range []fyne.ThemeColorName{theme.ColorNameDisabled, theme.ColorNamePlaceHolder} {
			for _, bg := range []fyne.ThemeColorName{theme.ColorNameBackground, theme.ColorNameInputBackground} {
				if c := contrast(th.Color(text, v), th.Color(bg, v)); c < 4.5 {
					t.Errorf("variant %d: %s on %s is %.2f:1", v, text, bg, c)
				}
			}
		}
		// and still dimmer than the normal text
		if contrast(th.Color(theme.ColorNameDisabled, v), th.Color(theme.ColorNameForeground, v)) < 1.3 {
			t.Errorf("variant %d: grey text looks like normal text", v)
		}
	}
}
