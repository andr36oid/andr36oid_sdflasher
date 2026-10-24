package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"image"
	"image/color"
	"image/draw"
	"math"
)

const mapCols = 72
const mapRows = 28

// contentFrac is how much of the map the written image and preserved partitions
// keep when a big unassigned tail is squashed into the "assigned on first boot" band.
const contentFrac = 0.7

var mapColors = []color.RGBA{
	{240, 240, 240, 255}, // 0 empty card
	{170, 170, 170, 255}, // 1 pending
	{0, 210, 210, 255},   // 2 preserved
	{0, 0, 180, 255},     // 3 verified
	{250, 20, 20, 255},   // 4 writing head
	{0, 110, 240, 255},   // 5 written
	{96, 170, 110, 255},  // 6 assigned on first boot
}

type diskMap struct {
	img        *canvas.Image
	state      []byte
	size       int64
	contentEnd int64 // start of the unassigned tail; 0 when there is nothing to squash
	squash     bool
}

func newDiskMap() *diskMap {
	m := &diskMap{state: make([]byte, mapCols*mapRows)}
	m.img = canvas.NewImageFromImage(m.render())
	// A small minimum so the view shrinks onto a phone; the VBox stretches it to
	// the window width, so the map scales with the window.
	m.img.SetMinSize(fyne.NewSize(200, 160))
	m.img.FillMode = canvas.ImageFillStretch
	return m
}
func (m *diskMap) render() image.Image {
	im := image.NewRGBA(image.Rect(0, 0, mapCols*8, mapRows*9))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.RGBA{20, 20, 20, 255}), image.Point{}, draw.Src)
	for i, s := range m.state {
		x := (i % mapCols) * 8
		y := (i / mapCols) * 9
		c := mapColors[s]
		draw.Draw(im, image.Rect(x+1, y+1, x+7, y+8), image.NewUniform(c), image.Point{}, draw.Src)
		if s == 3 {
			for yy := y + 2; yy < y+8; yy += 2 {
				for xx := x + 2; xx < x+7; xx += 2 {
					im.Set(xx, yy, color.RGBA{190, 190, 255, 255})
				}
			}
		}
	}
	return im
}

// span maps a byte offset to a fractional cell position. When a large unassigned
// tail is present it is compressed into the last 1-contentFrac of the cells, so the
// real partitions near the start stay visible instead of a tiny sliver.
func (m *diskMap) span(off int64) float64 {
	n := float64(len(m.state))
	if m.size <= 0 {
		return 0
	}
	if off < 0 {
		off = 0
	}
	if off > m.size {
		off = m.size
	}
	if !m.squash {
		return float64(off) / float64(m.size) * n
	}
	content := n * contentFrac
	if off <= m.contentEnd {
		return float64(off) / float64(m.contentEnd) * content
	}
	return content + float64(off-m.contentEnd)/float64(m.size-m.contentEnd)*(n-content)
}
func (m *diskMap) mark(off, length int64, state byte) {
	if m.size <= 0 || length <= 0 {
		return
	}
	start := int(m.span(off))
	end := int(math.Ceil(m.span(off + length)))
	if start < 0 {
		start = 0
	}
	if end > len(m.state) {
		end = len(m.state)
	}
	for i := start; i < end; i++ {
		if m.state[i] != 2 {
			m.state[i] = state
		}
	}
}
func (m *diskMap) plan(p *flash.Plan) {
	m.size = p.Size
	// The content extent is the furthest real content, ignoring the tiny backup
	// partition table pinned to the very end of the card.
	m.contentEnd = 0
	for _, w := range p.Writes {
		if w.Name == "Backup partition table" {
			continue
		}
		if w.End() > m.contentEnd {
			m.contentEnd = w.End()
		}
	}
	for _, r := range p.Protected {
		if r.End() > m.contentEnd {
			m.contentEnd = r.End()
		}
	}
	// Only squash when the unassigned tail is a meaningful slice of the card; on an
	// update the preserved userdata already fills the end, so nothing is squashed.
	m.squash = m.contentEnd > 0 && m.size-m.contentEnd > m.size/8
	for i := range m.state {
		m.state[i] = 0
	}
	if m.squash {
		m.mark(m.contentEnd, m.size-m.contentEnd, 6)
	}
	for _, w := range p.Writes {
		m.mark(w.Offset, w.Length, 1)
	}
	for _, r := range p.Protected {
		m.mark(r.Offset, r.Length, 2)
	}
	m.refresh()
}
func (m *diskMap) update(p flash.Progress) {
	if p.Plan != nil {
		m.plan(p.Plan)
	}
	switch p.Phase {
	case "writing":
		m.mark(p.Offset, p.Length, 5)
		block := m.size / int64(len(m.state))
		if block < 1 {
			block = 1
		}
		m.mark(p.Offset+p.Length-block, block, 4)
	case "verifying":
		m.mark(p.Offset, p.Length, 3)
	}
	m.refresh()
}
func (m *diskMap) refresh() { m.img.Image = m.render(); m.img.Refresh() }
func legend(locale string) *fyne.Container {
	items := []fyne.CanvasObject{}
	for _, x := range []struct {
		state int
		name  string
	}{{1, "Pending"}, {4, "Writing"}, {5, "Written"}, {3, "Verified"}, {2, "Preserved"}, {6, "Assigned on first boot"}} {
		// An explicit size and a border so the swatch is drawn on every platform,
		// including Android where a bare filled rectangle can come out invisible.
		sw := canvas.NewRectangle(mapColors[x.state])
		sw.SetMinSize(fyne.NewSize(14, 14))
		sw.StrokeColor = color.RGBA{90, 90, 90, 255}
		sw.StrokeWidth = 1
		items = append(items, container.NewHBox(container.NewGridWrap(fyne.NewSize(14, 14), sw), widget.NewLabel(i18n.Text(locale, x.name))))
	}
	// GridWrap reflows the entries to the window width: several columns on a desktop,
	// one per row on a phone, so nothing is clipped off the side.
	return container.NewGridWrap(fyne.NewSize(220, 34), items...)
}
