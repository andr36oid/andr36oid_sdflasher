// Package dtbconv turns the device tree (DTB) a console shipped with
// (ArkOS / dArkOS or the stock EmuELEC firmware, Linux 4.4 BSP) into an
// andr36oid DTB. It starts from our own DTB for the same board family and
// copies over only the board facts: panel, buttons, stick wiring, battery,
// LEDs, a few rails and the second card slot. See Convert.
package dtbconv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	fdtMagic     = 0xd00dfeed
	fdtBeginNode = 1
	fdtEndNode   = 2
	fdtProp      = 3
	fdtNop       = 4
	fdtEnd       = 9
)

// Prop is one device tree property. Value is the raw big-endian bytes.
type Prop struct {
	Name  string
	Value []byte
}

// Node is one device tree node. Properties and children keep file order.
type Node struct {
	Name     string
	Props    []*Prop
	Children []*Node
	parent   *Node
}

// Tree is a parsed flattened device tree.
type Tree struct {
	Root     *Node
	Reserve  [][2]uint64
	BootCPU  uint32
	phandles map[uint32]*Node
}

// Parse reads a DTB (version 16 or 17).
func Parse(b []byte) (*Tree, error) {
	if len(b) < 40 || binary.BigEndian.Uint32(b) != fdtMagic {
		return nil, errors.New("not a device tree file")
	}
	h := func(i int) uint32 { return binary.BigEndian.Uint32(b[i*4:]) }
	total, offStruct, offStrings, offRsv, version := h(1), h(2), h(3), h(4), h(5)
	if int(total) > len(b) || total < 40 {
		return nil, errors.New("device tree file is truncated")
	}
	if version < 16 {
		return nil, fmt.Errorf("device tree version %d is too old", version)
	}
	b = b[:total]
	sizeStrings := h(8)
	if uint64(offStrings)+uint64(sizeStrings) > uint64(total) || offStruct >= total || offRsv >= total {
		return nil, errors.New("device tree header is damaged")
	}
	strs := b[offStrings : offStrings+sizeStrings]
	t := &Tree{BootCPU: h(7)}
	for p := int(offRsv); ; p += 16 {
		if p+16 > len(b) {
			return nil, errors.New("device tree reserve map is damaged")
		}
		a, s := binary.BigEndian.Uint64(b[p:]), binary.BigEndian.Uint64(b[p+8:])
		if a == 0 && s == 0 {
			break
		}
		t.Reserve = append(t.Reserve, [2]uint64{a, s})
	}
	var stack []*Node
	p := int(offStruct)
	word := func() (uint32, error) {
		if p+4 > len(b) {
			return 0, errors.New("device tree structure is truncated")
		}
		v := binary.BigEndian.Uint32(b[p:])
		p += 4
		return v, nil
	}
	for {
		tok, e := word()
		if e != nil {
			return nil, e
		}
		switch tok {
		case fdtBeginNode:
			end := bytes.IndexByte(b[p:], 0)
			if end < 0 {
				return nil, errors.New("device tree node name is damaged")
			}
			n := &Node{Name: string(b[p : p+end])}
			p = (p + end + 4) &^ 3
			if len(stack) > 0 {
				n.parent = stack[len(stack)-1]
				n.parent.Children = append(n.parent.Children, n)
			} else if t.Root != nil {
				return nil, errors.New("device tree has two roots")
			} else {
				t.Root = n
			}
			stack = append(stack, n)
		case fdtEndNode:
			if len(stack) == 0 {
				return nil, errors.New("device tree structure is unbalanced")
			}
			stack = stack[:len(stack)-1]
		case fdtProp:
			ln, e1 := word()
			no, e2 := word()
			if e1 != nil || e2 != nil || len(stack) == 0 || p+int(ln) > len(b) || int(no) >= len(strs) {
				return nil, errors.New("device tree property is damaged")
			}
			end := bytes.IndexByte(strs[no:], 0)
			if end < 0 {
				return nil, errors.New("device tree string table is damaged")
			}
			v := append([]byte(nil), b[p:p+int(ln)]...)
			stack[len(stack)-1].Props = append(stack[len(stack)-1].Props, &Prop{Name: string(strs[no : int(no)+end]), Value: v})
			p = (p + int(ln) + 3) &^ 3
		case fdtNop:
		case fdtEnd:
			if t.Root == nil || len(stack) != 0 {
				return nil, errors.New("device tree structure is unbalanced")
			}
			t.reindex()
			return t, nil
		default:
			return nil, fmt.Errorf("unknown device tree token %d", tok)
		}
	}
}

// Bytes writes the tree as a version 17 DTB.
func (t *Tree) Bytes() []byte {
	var strtab bytes.Buffer
	offs := map[string]uint32{}
	soff := func(s string) uint32 {
		if o, ok := offs[s]; ok {
			return o
		}
		o := uint32(strtab.Len())
		strtab.WriteString(s)
		strtab.WriteByte(0)
		offs[s] = o
		return o
	}
	var st bytes.Buffer
	u32 := func(v uint32) { binary.Write(&st, binary.BigEndian, v) }
	pad := func() {
		for st.Len()%4 != 0 {
			st.WriteByte(0)
		}
	}
	var emit func(n *Node)
	emit = func(n *Node) {
		u32(fdtBeginNode)
		st.WriteString(n.Name)
		st.WriteByte(0)
		pad()
		for _, pr := range n.Props {
			u32(fdtProp)
			u32(uint32(len(pr.Value)))
			u32(soff(pr.Name))
			st.Write(pr.Value)
			pad()
		}
		for _, c := range n.Children {
			emit(c)
		}
		u32(fdtEndNode)
	}
	emit(t.Root)
	u32(fdtEnd)
	var rsv bytes.Buffer
	for _, r := range t.Reserve {
		binary.Write(&rsv, binary.BigEndian, r)
	}
	binary.Write(&rsv, binary.BigEndian, [2]uint64{})
	offRsv := uint32(40)
	offStruct := offRsv + uint32(rsv.Len())
	offStrings := offStruct + uint32(st.Len())
	total := offStrings + uint32(strtab.Len())
	var out bytes.Buffer
	for _, v := range []uint32{fdtMagic, total, offStruct, offStrings, offRsv, 17, 16, t.BootCPU, uint32(strtab.Len()), uint32(st.Len())} {
		binary.Write(&out, binary.BigEndian, v)
	}
	out.Write(rsv.Bytes())
	out.Write(st.Bytes())
	out.Write(strtab.Bytes())
	return out.Bytes()
}

func (t *Tree) reindex() {
	t.phandles = map[uint32]*Node{}
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			c.parent = n
			walk(c)
		}
		if v, ok := n.U32("phandle"); ok {
			t.phandles[v] = n
		} else if v, ok := n.U32("linux,phandle"); ok {
			t.phandles[v] = n
		}
	}
	walk(t.Root)
}

// Node returns the node at an absolute path ("/dsi@ff450000/panel@0").
func (t *Tree) Node(path string) *Node {
	n := t.Root
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		n = n.Child(part)
		if n == nil {
			return nil
		}
	}
	return n
}

// Phandle returns the node a phandle points at.
func (t *Tree) Phandle(v uint32) *Node { return t.phandles[v] }

// PhandleOf returns the node's phandle, giving it a new one if it has none.
func (t *Tree) PhandleOf(n *Node) uint32 {
	if v, ok := n.U32("phandle"); ok {
		return v
	}
	var max uint32
	for v := range t.phandles {
		if v > max {
			max = v
		}
	}
	max++
	n.SetU32("phandle", max)
	t.phandles[max] = n
	return max
}

// Walk visits every node, parents first.
func (t *Tree) Walk(f func(n *Node)) {
	var walk func(n *Node)
	walk = func(n *Node) {
		f(n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Root)
}

// Path is the node's absolute path.
func (n *Node) Path() string {
	if n.parent == nil {
		return "/"
	}
	p := n.parent.Path()
	if p == "/" {
		return "/" + n.Name
	}
	return p + "/" + n.Name
}

func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	// a unit address may be left out when it is unique
	var found *Node
	for _, c := range n.Children {
		if strings.SplitN(c.Name, "@", 2)[0] == name {
			if found != nil {
				return nil
			}
			found = c
		}
	}
	return found
}

// AddChild appends a new empty child node, or returns the existing one.
func (n *Node) AddChild(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	c := &Node{Name: name, parent: n}
	n.Children = append(n.Children, c)
	return c
}

func (n *Node) RemoveChild(name string) {
	out := n.Children[:0]
	for _, c := range n.Children {
		if c.Name != name {
			out = append(out, c)
		}
	}
	n.Children = out
}

func (n *Node) Prop(name string) *Prop {
	for _, p := range n.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (n *Node) Get(name string) ([]byte, bool) {
	if p := n.Prop(name); p != nil {
		return p.Value, true
	}
	return nil, false
}

func (n *Node) Has(name string) bool { return n.Prop(name) != nil }

func (n *Node) Set(name string, v []byte) {
	if p := n.Prop(name); p != nil {
		p.Value = append([]byte(nil), v...)
		return
	}
	n.Props = append(n.Props, &Prop{Name: name, Value: append([]byte(nil), v...)})
}

func (n *Node) Delete(name string) {
	out := n.Props[:0]
	for _, p := range n.Props {
		if p.Name != name {
			out = append(out, p)
		}
	}
	n.Props = out
}

func (n *Node) U32(name string) (uint32, bool) {
	v, ok := n.Get(name)
	if !ok || len(v) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(v), true
}

func (n *Node) Cells(name string) ([]uint32, bool) {
	v, ok := n.Get(name)
	if !ok || len(v)%4 != 0 {
		return nil, false
	}
	return cells(v), true
}

func (n *Node) SetU32(name string, v uint32) { n.SetCells(name, v) }

func (n *Node) SetCells(name string, v ...uint32) { n.Set(name, cellBytes(v)) }

func (n *Node) String(name string) string {
	v, _ := n.Get(name)
	return string(bytes.TrimRight(v, "\x00"))
}

// Strings returns a string-list property (like compatible).
func (n *Node) Strings(name string) []string {
	v, ok := n.Get(name)
	if !ok {
		return nil
	}
	parts := strings.Split(string(bytes.TrimRight(v, "\x00")), "\x00")
	return parts
}

func (n *Node) SetString(name, s string) { n.Set(name, append([]byte(s), 0)) }

func (n *Node) Compatible(c string) bool {
	for _, s := range n.Strings("compatible") {
		if s == c {
			return true
		}
	}
	return false
}

// Enabled is true unless status says otherwise.
func (n *Node) Enabled() bool {
	s := n.String("status")
	return s == "" || s == "okay" || s == "ok"
}

func cells(v []byte) []uint32 {
	out := make([]uint32, len(v)/4)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(v[i*4:])
	}
	return out
}

func cellBytes(v []uint32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.BigEndian.PutUint32(b[i*4:], x)
	}
	return b
}
