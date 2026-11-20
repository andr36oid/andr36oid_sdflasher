package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The building blocks of the pages: a choice card (a row the user picks, with a
// title, a line of detail and a tag on the right), a note card (plain, warning or
// error) and the page frame with its fixed row of buttons.

// choiceCard is one choice in a list: a release, a card, a console, an action.
type choiceCard struct {
	widget.BaseWidget
	title, detail, tag                 *widget.Label
	icon                               *widget.Icon
	bg                                 *canvas.Rectangle
	actions                            *fyne.Container // a button on the row, at the right
	selected, disabled, hovered, radio bool
	onTap                              func()
}

// newChoiceCard is a card with a title, a line of detail ("" for none) and onTap.
func newChoiceCard(title, detail string, onTap func()) *choiceCard {
	c := &choiceCard{title: wrapped(title), detail: wrapped(detail), tag: widget.NewLabel(""), onTap: onTap, actions: container.NewVBox()}
	c.title.TextStyle = fyne.TextStyle{Bold: true}
	c.tag.Hide()
	c.icon = widget.NewIcon(nil)
	c.icon.Hide()
	c.bg = canvas.NewRectangle(color.Transparent)
	c.bg.CornerRadius = theme.InputRadiusSize() * 2
	c.ExtendBaseWidget(c)
	c.update()
	return c
}

// newRadioItem is a compact choice with a round marker, for long lists.
func newRadioItem(title string, onTap func()) *choiceCard {
	c := newChoiceCard(title, "", onTap)
	c.radio = true
	c.title.TextStyle = fyne.TextStyle{}
	c.icon.Show()
	c.update()
	return c
}

func (c *choiceCard) CreateRenderer() fyne.WidgetRenderer {
	text := container.New(layout.NewCustomPaddedVBoxLayout(-theme.Padding()), c.title, c.detail)
	content := container.NewBorder(nil, nil, container.NewCenter(c.icon), container.NewCenter(container.NewHBox(c.tag, c.actions)), text)
	return widget.NewSimpleRenderer(container.NewStack(c.bg, container.NewPadded(content)))
}

func (c *choiceCard) setText(title, detail string) {
	c.title.SetText(title)
	c.detail.SetText(detail)
	c.update()
}

// setAction puts a button on the row (nil takes it away).
func (c *choiceCard) setAction(b *widget.Button) {
	c.actions.RemoveAll()
	if b != nil {
		c.actions.Add(b)
	}
}

func (c *choiceCard) setTag(tag string) {
	c.tag.SetText(tag)
	c.update()
}

func (c *choiceCard) setSelected(v bool) {
	c.selected = v
	c.update()
}

func (c *choiceCard) setDisabled(v bool) {
	c.disabled = v
	c.update()
}

// update draws the card for its state: the accent border when chosen, dim when it
// can't be chosen.
func (c *choiceCard) update() {
	if c.detail.Text == "" {
		c.detail.Hide()
	} else {
		c.detail.Show()
	}
	if c.tag.Text == "" {
		c.tag.Hide()
	} else {
		c.tag.Show()
	}
	importance := widget.MediumImportance
	if c.disabled {
		importance = widget.LowImportance
	}
	c.title.Importance, c.detail.Importance, c.tag.Importance = importance, importance, widget.LowImportance
	if c.radio {
		c.icon.SetResource(theme.RadioButtonIcon())
		if c.selected {
			c.icon.SetResource(theme.RadioButtonCheckedIcon())
		}
		if c.disabled {
			c.icon.SetResource(theme.NewDisabledResource(theme.RadioButtonIcon()))
		}
	}
	c.bg.StrokeWidth = 1
	c.bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	c.bg.FillColor = theme.Color(theme.ColorNameInputBackground)
	if c.radio {
		c.bg.StrokeColor = color.Transparent
		c.bg.FillColor = color.Transparent
	}
	if c.hovered && !c.disabled {
		c.bg.FillColor = theme.Color(theme.ColorNameHover)
	}
	if c.selected && !c.radio {
		c.bg.StrokeWidth = 2
		c.bg.StrokeColor = theme.Color(theme.ColorNamePrimary)
	}
	c.title.Refresh()
	c.detail.Refresh()
	c.tag.Refresh()
	c.bg.Refresh()
}

func (c *choiceCard) Tapped(*fyne.PointEvent) {
	if !c.disabled && c.onTap != nil {
		c.onTap()
	}
}

func (c *choiceCard) MouseIn(*desktop.MouseEvent) {
	c.hovered = true
	c.update()
}

func (c *choiceCard) MouseMoved(*desktop.MouseEvent) {}

func (c *choiceCard) MouseOut() {
	c.hovered = false
	c.update()
}

func (c *choiceCard) Cursor() desktop.Cursor {
	if c.disabled {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

// note kinds
const (
	notePlain = iota
	noteWarning
	noteError
)

// noteCard is a framed block of content: plain, amber for a warning, red for an error.
func noteCard(kind int, objects ...fyne.CanvasObject) *fyne.Container {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = theme.InputRadiusSize() * 2
	bg.StrokeWidth = 1
	bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	switch kind {
	case noteWarning:
		bg.StrokeWidth = 2
		bg.StrokeColor = theme.Color(theme.ColorNameWarning)
		bg.FillColor = tint(theme.Color(theme.ColorNameWarning))
	case noteError:
		bg.StrokeWidth = 2
		bg.StrokeColor = theme.Color(theme.ColorNameError)
		bg.FillColor = tint(theme.Color(theme.ColorNameError))
	}
	return container.NewStack(bg, container.NewPadded(container.NewVBox(objects...)))
}

// tint is a colour faded far enough to put text on it.
func tint(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0x30}
}

// title is a page's large heading.
func title(text string) *widget.Label {
	l := wrapped(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	l.SizeName = theme.SizeNameSubHeadingText
	return l
}

// pageView is one page: the title and the lead sentence at the top, the content
// that scrolls, and the page's buttons in a fixed row at the bottom right. foot,
// when not nil, sits fixed just above the buttons (the erase tick box).
func pageView(head, lead *widget.Label, body, foot fyne.CanvasObject, buttons ...fyne.CanvasObject) fyne.CanvasObject {
	top := container.NewVBox(head)
	if lead != nil {
		top.Add(lead)
	}
	row := container.NewHBox(layout.NewSpacer())
	for _, b := range buttons {
		if b != nil {
			row.Add(b)
		}
	}
	bottom := container.NewVBox(widget.NewSeparator())
	if foot != nil {
		bottom.Add(foot)
	}
	bottom.Add(row)
	return container.NewBorder(top, bottom, nil, nil, container.NewVScroll(container.NewPadded(body)))
}

// tapLabel is the wrapped text of a tick box; tapping it ticks the box too.
type tapLabel struct {
	widget.Label
	check *widget.Check
}

func (l *tapLabel) Tapped(*fyne.PointEvent) {
	if !l.check.Disabled() {
		l.check.SetChecked(!l.check.Checked)
	}
}

// wrappedCheck is a tick box whose text wraps instead of running off the edge.
func wrappedCheck(text string, changed func(bool)) (*widget.Check, *tapLabel, fyne.CanvasObject) {
	c := widget.NewCheck("", changed)
	l := &tapLabel{check: c}
	l.Text = text
	l.Wrapping = fyne.TextWrapWord
	l.ExtendBaseWidget(l)
	return c, l, container.NewBorder(nil, nil, container.NewVBox(c), nil, l)
}

// quiet is a low-importance button for the second things on a page.
func quiet(text string, tapped func()) *widget.Button {
	b := widget.NewButton(text, tapped)
	b.Importance = widget.LowImportance
	return b
}

// fixedWidth gives its content one width, for the side bar.
type fixedWidth struct{ width float32 }

func (l fixedWidth) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objects {
		if o.Visible() {
			h = max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(l.width, h)
}

func (l fixedWidth) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

// atLeast keeps the window from getting smaller than the size every page was
// laid out for (980 x 680 opens, 900 x 620 is the least).
type atLeast struct{ size fyne.Size }

func (l atLeast) MinSize(objects []fyne.CanvasObject) fyne.Size {
	out := l.size
	for _, o := range objects {
		out = out.Max(o.MinSize())
	}
	return out
}

func (l atLeast) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

// shown is how a name that comes as data (a profile, a drive) is shown: with plain
// hyphens, never middle dots or long dashes. The value itself is never changed.
func shown(name string) string {
	return dataDashes.Replace(name)
}

// dataDashes turns em dashes, en dashes and middle dots into plain hyphens.
var dataDashes = func() *strings.Replacer {
	var pairs []string
	for _, r := range []rune{0x2014, 0x2013, 0x00b7} {
		pairs = append(pairs, " "+string(r)+" ", " - ", string(r), "-")
	}
	return strings.NewReplacer(pairs...)
}()

// sideBackground sets the side bar off from the page.
func sideBackground() *canvas.Rectangle {
	return canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
}

// fillLast stacks its objects like a VBox and gives the last one the height left.
type fillLast struct{}

func (fillLast) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	n := 0
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		size.Width = max(size.Width, m.Width)
		size.Height += m.Height
		n++
	}
	if n > 1 {
		size.Height += float32(n-1) * theme.Padding()
	}
	return size
}

func (f fillLast) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	extra := size.Height - f.MinSize(objects).Height
	last := -1
	for i, o := range objects {
		if o.Visible() {
			last = i
		}
	}
	y := float32(0)
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		if i == last && extra > 0 {
			h += extra
		}
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h + theme.Padding()
	}
}

// optionRow is one setting: its label and control on a line, its help directly
// below, and below that what only shows sometimes (notes hidden while empty).
func optionRow(label, control fyne.CanvasObject, help string, more ...fyne.CanvasObject) *fyne.Container {
	rows := []fyne.CanvasObject{container.NewBorder(nil, nil, nil, control, label)}
	if help != "" {
		rows = append(rows, wrapped(help))
	}
	rows = append(rows, more...)
	return container.New(layout.NewCustomPaddedVBoxLayout(-theme.Padding()), rows...)
}
