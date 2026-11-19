package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// The block view of the working page: a strip of the whole card to scale with its
// partitions named and a marker where the writer is, and the blocks of only what
// is written, grouped by partition. Everything comes from the write job's own
// progress events: the plan (writes, kept ranges, partition table) with the first
// one, then the offset and length of every piece written and checked.

const (
	blockWaiting = iota
	blockWriting
	blockWritten
	blockChecked
	blockKept
	blockFailed
)

var blockNames = []string{"Waiting", "Writing", "Written", "Checked", "Kept", "Failed"}

var blockColors = []color.NRGBA{
	{150, 150, 150, 255}, // waiting: outline only
	{40, 140, 255, 255},  // writing: half filled
	{30, 90, 210, 255},   // written
	{40, 170, 80, 255},   // checked
	{0, 160, 160, 255},   // kept
	{220, 40, 40, 255},   // failed: with a cross
}

const (
	mapCols   = 44 // the columns the plan's fine blocks are counted for
	mapCell   = 12 // a block and its gap
	mapGap    = 6  // between two partitions
	mapLabelW = 110
	mapMinCol = 20
)

type mapBlock struct {
	off, length int64
	state       byte
}

type blockGroup struct {
	name   string
	kept   bool
	blocks []mapBlock
	label  *canvas.Text
	note   *canvas.Text // kept: your data, not touched
	raster *canvas.Raster
}

// shown is the group's blocks as drawn: every k fine blocks are one.
func (g *blockGroup) shown(k int) []mapBlock {
	if k <= 1 {
		return g.blocks
	}
	var out []mapBlock
	for i := 0; i < len(g.blocks); i += k {
		part := g.blocks[i:min(i+k, len(g.blocks))]
		b := mapBlock{off: part[0].off, state: merged(part)}
		for _, x := range part {
			b.length += x.length
		}
		out = append(out, b)
	}
	return out
}

// merged is the state of blocks drawn as one: a failure shows, then the least done.
func merged(part []mapBlock) byte {
	n := map[byte]int{}
	for _, b := range part {
		n[b.state]++
	}
	switch {
	case n[blockFailed] > 0:
		return blockFailed
	case n[blockChecked] == len(part):
		return blockChecked
	case n[blockChecked]+n[blockWritten] == len(part):
		return blockWritten
	case n[blockWaiting] == len(part):
		return blockWaiting
	}
	return blockWriting
}

// rows is the number of block rows the group takes with cols columns and k fine
// blocks to one.
func (g *blockGroup) rows(cols, k int) int {
	if g.kept {
		return 1
	}
	n := (len(g.blocks) + k - 1) / k
	return max(1, (n+cols-1)/cols)
}

// groupHeight is the height of a group in the map.
func groupHeight(g *blockGroup, cols, k int) float32 {
	h := float32(g.rows(cols, k)*mapCell) + mapGap
	if g.kept {
		h += 6
	}
	return h
}

// drawBlock draws one block of size px at x, y in its state's colour and shape.
func drawBlock(im *image.NRGBA, x, y, px int, state byte) {
	c := blockColors[state]
	fill := func(x0, y0, x1, y1 int, c color.Color) {
		draw.Draw(im, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	line := max(1, px/8)
	switch state {
	case blockWaiting:
		fill(x, y, x+px, y+line, c)
		fill(x, y+px-line, x+px, y+px, c)
		fill(x, y, x+line, y+px, c)
		fill(x+px-line, y, x+px, y+px, c)
	case blockWriting:
		fill(x, y, x+px, y+line, c)
		fill(x, y, x+line, y+px, c)
		fill(x+px-line, y, x+px, y+px, c)
		fill(x, y+px/2, x+px, y+px, c)
	default:
		fill(x, y, x+px, y+px, c)
	}
	if state == blockFailed {
		for i := 0; i < px; i++ {
			for j := 0; j < line; j++ {
				im.Set(x+i, y+min(px-1, i+j), color.White)
				im.Set(x+px-1-i, y+min(px-1, i+j), color.White)
			}
		}
	}
}

// blockMap is the block grid, one row group per partition.
type blockMap struct {
	widget.BaseWidget
	groups  []*blockGroup
	objects []fyne.CanvasObject
	hover   func(string)
	// describe says what a block is, for the hover text
	describe  func(g *blockGroup, b mapBlock) string
	refreshed time.Time
	// cols and k: the columns and the fine blocks to one drawn block that fit
	cols, k int
}

func newBlockMap() *blockMap {
	m := &blockMap{cols: mapCols, k: 1}
	m.ExtendBaseWidget(m)
	return m
}

func (m *blockMap) setGroups(groups []*blockGroup, keptNote string) {
	m.groups = groups
	m.objects = nil
	for _, g := range groups {
		g := g
		g.label = canvas.NewText(g.name, theme.Color(theme.ColorNameForeground))
		g.label.TextSize = theme.TextSize() * 0.9
		g.raster = canvas.NewRaster(func(w, h int) image.Image { return m.paint(g, w, h) })
		m.objects = append(m.objects, g.label, g.raster)
		if g.kept {
			g.note = canvas.NewText(keptNote, color.White)
			g.note.TextSize = theme.TextSize() * 0.85
			m.objects = append(m.objects, g.note)
		}
	}
	m.Refresh()
}

func (m *blockMap) paint(g *blockGroup, w, h int) image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	if g.kept {
		draw.Draw(im, im.Bounds(), image.NewUniform(blockColors[blockKept]), image.Point{}, draw.Src)
		return im
	}
	cols := m.cols
	scale := float64(w) / float64(cols*mapCell)
	px := int(float64(mapCell-2) * scale)
	for i, b := range g.shown(m.k) {
		x := int(float64(i%cols*mapCell) * scale)
		y := int(float64(i/cols*mapCell) * scale)
		drawBlock(im, x, y, max(px, 2), b.state)
	}
	return im
}

// mark sets the state of the blocks in [off, off+length); a block only partly in it
// is being written.
func (m *blockMap) mark(off, length int64, state byte) {
	end := off + length
	for _, g := range m.groups {
		if g.kept {
			continue
		}
		for i := range g.blocks {
			b := &g.blocks[i]
			if b.off >= end || b.off+b.length <= off || b.state == blockFailed {
				continue
			}
			if b.off >= off && b.off+b.length <= end {
				b.state = state
			} else if state == blockWritten && b.state != blockChecked {
				b.state = blockWriting
			}
		}
	}
}

// fail marks the block at off; it returns the group's name.
func (m *blockMap) fail(off int64) string {
	for _, g := range m.groups {
		if g.kept {
			continue
		}
		for i := range g.blocks {
			b := &g.blocks[i]
			if off >= b.off && off < b.off+b.length {
				b.state = blockFailed
				return g.name
			}
		}
	}
	return ""
}

// finish shows every written block as checked.
func (m *blockMap) finish() {
	for _, g := range m.groups {
		for i := range g.blocks {
			g.blocks[i].state = blockChecked
		}
	}
}

// update redraws, at most ten times a second unless forced.
func (m *blockMap) update(force bool) {
	if !force && time.Since(m.refreshed) < 100*time.Millisecond {
		return
	}
	m.refreshed = time.Now()
	for _, g := range m.groups {
		g.raster.Refresh()
	}
}

func (m *blockMap) MouseIn(*desktop.MouseEvent) {}
func (m *blockMap) MouseOut() {
	if m.hover != nil {
		m.hover("")
	}
}

func (m *blockMap) MouseMoved(e *desktop.MouseEvent) {
	if m.hover == nil || m.describe == nil {
		return
	}
	y := float32(0)
	for _, g := range m.groups {
		h := groupHeight(g, m.cols, m.k)
		if e.Position.Y >= y && e.Position.Y < y+h && !g.kept {
			col := int((e.Position.X - mapLabelW) / mapCell)
			row := int((e.Position.Y - y) / mapCell)
			i := row*m.cols + col
			if shown := g.shown(m.k); col >= 0 && col < m.cols && i < len(shown) {
				m.hover(m.describe(g, shown[i]))
				return
			}
		}
		y += h
	}
	m.hover("")
}

func (m *blockMap) CreateRenderer() fyne.WidgetRenderer { return &blockMapRenderer{m} }

type blockMapRenderer struct{ m *blockMap }

// fit picks the columns for the width and the fewest fine blocks to one drawn
// block that let every group fit the height.
func (m *blockMap) fit(size fyne.Size) {
	m.cols = max(mapMinCol, int((size.Width-mapLabelW)/mapCell))
	most := 1
	for _, g := range m.groups {
		most = max(most, len(g.blocks))
	}
	for m.k = 1; m.k < most; m.k++ {
		if m.height(m.cols, m.k) <= size.Height {
			return
		}
	}
}

func (m *blockMap) height(cols, k int) float32 {
	h := float32(0)
	for _, g := range m.groups {
		h += groupHeight(g, cols, k)
	}
	return h
}

func (r *blockMapRenderer) Layout(size fyne.Size) {
	m := r.m
	k, cols := m.k, m.cols
	m.fit(size)
	y := float32(0)
	for _, g := range m.groups {
		ts := g.label.MinSize()
		g.label.Move(fyne.NewPos(0, y+(mapCell-ts.Height)/2))
		g.raster.Move(fyne.NewPos(mapLabelW, y))
		g.raster.Resize(fyne.NewSize(float32(m.cols*mapCell), float32(g.rows(m.cols, m.k)*mapCell)))
		if g.note != nil {
			g.raster.Resize(fyne.NewSize(float32(m.cols*mapCell), mapCell+6))
			g.note.Move(fyne.NewPos(mapLabelW+6, y+(mapCell+6-g.note.MinSize().Height)/2))
		}
		y += groupHeight(g, m.cols, m.k)
	}
	if k != m.k || cols != m.cols {
		for _, g := range m.groups {
			g.raster.Refresh()
		}
	}
}

// MinSize is the map at its coarsest: one row a group.
func (r *blockMapRenderer) MinSize() fyne.Size {
	h := float32(0)
	for _, g := range r.m.groups {
		h += float32(mapCell) + mapGap
		if g.kept {
			h += 6
		}
	}
	return fyne.NewSize(mapLabelW+mapMinCol*mapCell, h)
}

func (r *blockMapRenderer) Refresh() {
	for _, g := range r.m.groups {
		g.raster.Refresh()
		g.label.Refresh()
	}
	r.Layout(r.m.Size())
}

func (r *blockMapRenderer) Objects() []fyne.CanvasObject { return r.m.objects }
func (r *blockMapRenderer) Destroy()                     {}

// legendRow is one aligned row: each state's square, then its word.
func legendRow(t func(string, ...any) string) fyne.CanvasObject {
	row := container.NewHBox()
	for i, name := range blockNames {
		state := byte(i)
		r := canvas.NewRaster(func(w, h int) image.Image {
			im := image.NewNRGBA(image.Rect(0, 0, w, h))
			drawBlock(im, 0, 0, min(w, h), state)
			return im
		})
		r.SetMinSize(fyne.NewSize(12, 12))
		row.Add(container.NewHBox(container.NewCenter(r), widget.NewLabel(t(name))))
	}
	return row
}

// stripPart is a partition on the whole-card strip.
type stripPart struct {
	name        string
	off, length int64
	state       byte // blockWritten (written to), blockKept, blockWaiting (left alone)
	text        *canvas.Text
}

// cardStrip is the whole card to scale, with a marker where the writer is.
type cardStrip struct {
	widget.BaseWidget
	size   int64
	pos    int64
	parts  []*stripPart
	raster *canvas.Raster
	marker *canvas.Raster
}

func newCardStrip() *cardStrip {
	c := &cardStrip{pos: -1}
	c.raster = canvas.NewRaster(c.paint)
	c.marker = canvas.NewRaster(c.paintMarker)
	c.ExtendBaseWidget(c)
	return c
}

func (c *cardStrip) setParts(size int64, parts []*stripPart) {
	c.size, c.parts = size, parts
	for _, p := range parts {
		p.text = canvas.NewText(p.name, color.White)
		p.text.TextSize = theme.TextSize() * 0.8
	}
	c.Refresh()
}

func (c *cardStrip) setPos(pos int64) {
	c.pos = pos
	c.marker.Refresh()
}

func (c *cardStrip) x(off int64, w int) int {
	if c.size <= 0 {
		return 0
	}
	return int(float64(off) / float64(c.size) * float64(w))
}

func (c *cardStrip) paint(w, h int) image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.NRGBA{60, 60, 60, 255}), image.Point{}, draw.Src)
	for _, p := range c.parts {
		col := color.NRGBA{95, 95, 95, 255}
		switch p.state {
		case blockWritten:
			col = color.NRGBA{30, 90, 210, 255}
		case blockKept:
			col = blockColors[blockKept]
		}
		x0, x1 := c.x(p.off, w), c.x(p.off+p.length, w)
		draw.Draw(im, image.Rect(x0, 0, max(x1-1, x0+1), h), image.NewUniform(col), image.Point{}, draw.Src)
	}
	return im
}

func (c *cardStrip) paintMarker(w, h int) image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	if c.pos < 0 {
		return im
	}
	x := c.x(c.pos, w)
	for y := 0; y < h; y++ {
		for dx := -y; dx <= y; dx++ {
			if x+dx >= 0 && x+dx < w {
				im.Set(x+dx, y, theme.Color(theme.ColorNameForeground))
			}
		}
	}
	return im
}

func (c *cardStrip) CreateRenderer() fyne.WidgetRenderer { return &stripRenderer{c} }

type stripRenderer struct{ c *cardStrip }

const stripH, markerH = 24, 9

func (r *stripRenderer) Layout(size fyne.Size) {
	c := r.c
	c.raster.Resize(fyne.NewSize(size.Width, stripH))
	c.marker.Move(fyne.NewPos(0, stripH+2))
	c.marker.Resize(fyne.NewSize(size.Width, markerH))
	for _, p := range c.parts {
		x0 := float32(c.x(p.off, int(size.Width)))
		x1 := float32(c.x(p.off+p.length, int(size.Width)))
		ts := p.text.MinSize()
		if x1-x0 < ts.Width+4 {
			p.text.Hide()
			continue
		}
		p.text.Show()
		p.text.Move(fyne.NewPos(x0+(x1-x0-ts.Width)/2, (stripH-ts.Height)/2))
	}
}

func (r *stripRenderer) MinSize() fyne.Size { return fyne.NewSize(100, stripH+2+markerH) }

func (r *stripRenderer) Refresh() {
	r.c.raster.Refresh()
	r.c.marker.Refresh()
	for _, p := range r.c.parts {
		p.text.Refresh()
	}
	r.Layout(r.c.Size())
}

func (r *stripRenderer) Objects() []fyne.CanvasObject {
	out := []fyne.CanvasObject{r.c.raster, r.c.marker}
	for _, p := range r.c.parts {
		out = append(out, p.text)
	}
	return out
}

func (r *stripRenderer) Destroy() {}

// partName is a partition as the user knows it.
func (s *screen) partName(name string, p *flash.Plan) string {
	switch name {
	case "BOOT":
		return s.t("Boot")
	case "system":
		return s.t("System")
	case "vendor":
		return s.t("Vendor")
	case "userdata":
		return s.t("Android")
	case "EASYROMS":
		return s.t("Games")
	case "cache":
		return s.t("Cache")
	case flash.GuestPartition:
		if p != nil && p.Guest != nil && p.Guest.DistributionName() != "" {
			return p.Guest.DistributionName()
		}
		return s.t("Game system")
	}
	return s.t(name)
}

// outsideName names what is written outside the partitions by what it is on the
// card: the partition tables at the start and the end, the boot loader between the
// first table and the first partition.
func (s *screen) outsideName(write string, off int64, p *flash.Plan) string {
	first, end := p.Size, int64(0)
	for _, part := range p.Table.Parts {
		first = min(first, part.Offset())
		end = max(end, part.Offset()+part.Size())
	}
	switch {
	case write == "Partition table" || write == "Backup partition table" || off < 34*512 || len(p.Table.Parts) > 0 && off >= end:
		return s.t("Partition table")
	case off < first:
		return s.t("Bootloader")
	}
	return s.t(write)
}

// mapPlan is what the block view shows for a plan: the strip, the groups of
// blocks, and the partitions that are set up empty.
func (s *screen) mapPlan(p *flash.Plan) (strip []*stripPart, groups []*blockGroup, empty []string) {
	type piece struct {
		name        string
		off, length int64
	}
	var pieces []piece
	written := map[string]bool{}
	for _, w := range p.Writes {
		// a partition only set up (zeros, an empty file system) is not drawn as copied
		setup := w.Zero || w.Data != nil
		rest := []piece{{w.Name, w.Offset, w.Length}}
		for _, part := range p.Table.Parts {
			var next []piece
			for _, r := range rest {
				a, b := max(r.off, part.Offset()), min(r.off+r.length, part.Offset()+part.Size())
				if a >= b {
					next = append(next, r)
					continue
				}
				if !setup {
					pieces = append(pieces, piece{s.partName(part.Name, p), a, b - a})
					written[part.Name] = true
				}
				if r.off < a {
					next = append(next, piece{r.name, r.off, a - r.off})
				}
				if b < r.off+r.length {
					next = append(next, piece{r.name, b, r.off + r.length - b})
				}
			}
			rest = next
		}
		for _, r := range rest {
			r.name = s.outsideName(w.Name, r.off, p)
			pieces = append(pieces, r)
		}
	}
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].off < pieces[j].off })
	var total int64
	for _, x := range pieces {
		total += x.length
	}
	// about ten rows of blocks for the whole write, at least a MiB each
	block := max(int64(1<<20), total/(mapCols*10))
	block = (block + (1<<20 - 1)) &^ (1<<20 - 1)
	byName := map[string]*blockGroup{}
	for _, x := range pieces {
		g := byName[x.name]
		if g == nil {
			g = &blockGroup{name: x.name}
			byName[x.name] = g
			groups = append(groups, g)
		}
		for off := x.off; off < x.off+x.length; off += block {
			g.blocks = append(g.blocks, mapBlock{off: off, length: min(block, x.off+x.length-off)})
		}
	}
	kept := map[string]bool{}
	for _, r := range p.Protected {
		if kept[r.Name] {
			continue
		}
		kept[r.Name] = true
		groups = append(groups, &blockGroup{name: s.partName(r.Name, p), kept: true})
	}
	for _, part := range p.Table.Parts {
		sp := &stripPart{name: s.partName(part.Name, p), off: part.Offset(), length: part.Size(), state: blockWaiting}
		switch {
		case kept[part.Name]:
			sp.state = blockKept
		case written[part.Name]:
			sp.state = blockWritten
		default:
			empty = append(empty, sp.name)
		}
		strip = append(strip, sp)
	}
	return
}

// gb is a position on the card for the user.
func gb(n int64) string {
	return fmt.Sprintf("%.2f GB", float64(n)/1e9)
}
