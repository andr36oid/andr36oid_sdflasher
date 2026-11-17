package dtbconv

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Our joypad key codes (Android key layout of andr36oid) by function.
const (
	keyUp     = 103
	keyDown   = 108
	keyLeft   = 105
	keyRight  = 106
	keyA      = 304
	keyB      = 305
	keyX      = 307
	keyY      = 308
	keyL1     = 310
	keyR1     = 311
	keyL2     = 312
	keyR2     = 313
	keySelect = 314
	keyStart  = 315
	keyL3     = 158
	keyR3     = 125
	keyFN     = 172
)

var keyNames = map[uint32]string{keyUp: "up", keyDown: "down", keyLeft: "left", keyRight: "right", keyA: "A", keyB: "B", keyX: "X", keyY: "Y", keyL1: "L1", keyR1: "R1", keyL2: "L2", keyR2: "R2", keySelect: "select", keyStart: "start", keyL3: "L3", keyR3: "R3", keyFN: "FN"}

// ArkOS (and the stock 4.4 odroidgo3-joypad DTBs) report positions:
// east = 0x131 is our A, south = 0x130 our B, north = 0x133 X, west = 0x134 Y.
var arkosKeys = map[uint32]uint32{
	0x220: keyUp, 0x221: keyDown, 0x222: keyLeft, 0x223: keyRight,
	0x131: keyA, 0x130: keyB, 0x133: keyX, 0x134: keyY,
	0x136: keyL1, 0x137: keyR1, 0x138: keyL2, 0x139: keyR2,
	0x2c0: keySelect, 0x2c1: keyStart, 0x2c2: keyL3, 0x2c3: keyR3, 0x2c4: keyFN,
	0x13a: keySelect, 0x13b: keyStart, 0x13c: keyFN, 0x13d: keyL3, 0x13e: keyR3,
}

// EmuELEC's gamepad drivers take a plain list of pins. On every EE board we
// know the first 14 are in this order; FN, L3 and R3 follow in varying order.
var emuelecOrder = []struct {
	key uint32
	pin GPIO
}{
	{keyR1, GPIO{3, 11, 1}}, {keyR2, GPIO{3, 12, 1}}, {keyL1, GPIO{3, 13, 1}}, {keyL2, GPIO{3, 14, 1}},
	{keyA, GPIO{3, 16, 1}}, {keyB, GPIO{3, 17, 1}}, {keyX, GPIO{3, 18, 1}}, {keyY, GPIO{3, 19, 1}},
	{keyLeft, GPIO{3, 20, 1}}, {keyRight, GPIO{3, 21, 1}}, {keyUp, GPIO{3, 22, 1}}, {keyDown, GPIO{3, 23, 1}},
	{keySelect, GPIO{3, 24, 1}}, {keyStart, GPIO{3, 25, 1}},
}
var emuelecExtra = map[uint32]uint32{GPIO{3, 10, 0}.key(): keyFN, GPIO{2, 13, 0}.key(): keyL3, GPIO{2, 14, 0}.key(): keyR3}

// family fingerprints: where the d-pad up and the A button are wired
var familyPins = []struct {
	f      Family
	up, a  GPIO
	detail string
}{
	{Genuine, GPIO{1, 12, 0}, GPIO{1, 2, 0}, "buttons on GPIO1"},
	{EE, GPIO{3, 22, 0}, GPIO{3, 16, 0}, "buttons on GPIO3_C/D"},
	{Soy, GPIO{3, 7, 0}, GPIO{3, 14, 0}, "buttons on GPIO3_A/B"},
}

func findCompatible(t *Tree, compat ...string) *Node {
	var found *Node
	t.Walk(func(n *Node) {
		if found != nil {
			return
		}
		for _, c := range compat {
			if n.Compatible(c) {
				found = n
				return
			}
		}
	})
	return found
}

func (c *conv) findJoypad() error {
	if n := findCompatible(c.in, "odroidgo3-joypad"); n != nil {
		c.inPad, c.padKind = n, "arkos"
		c.buttons = map[uint32]GPIO{}
		for _, ch := range n.Children {
			code, ok := ch.U32("linux,code")
			if !ok {
				continue
			}
			g, has, e := c.in.GPIO(ch, "gpios")
			if e != nil {
				return refuse("Button %s: %v", ch.Name, e)
			}
			if !has {
				continue
			}
			if code == keyUp || code == keyDown || code == keyLeft || code == keyRight {
				return ErrAlreadyAndroid
			}
			our, ok := arkosKeys[code]
			if !ok {
				c.warn("Button %s (%s) sends key code 0x%x, which we don't know. It was left out.", ch.Name, g.Name(), code)
				continue
			}
			if old, dup := c.buttons[our]; dup {
				return refuse("Two buttons claim to be %s (%s and %s). This DTB can't be converted safely.", keyNames[our], old.Name(), g.Name())
			}
			c.buttons[our] = g
		}
		return nil
	}
	if n := findCompatible(c.in, "play_joystick", "micro,gamepad"); n != nil {
		c.inPad, c.padKind = n, "emuelec"
		keys, e := c.in.GPIOs(n, "key-gpios")
		if e != nil {
			return refuse("Gamepad buttons: %v", e)
		}
		if len(keys) < len(emuelecOrder) {
			return refuse("The gamepad in this DTB lists only %d buttons. This wiring is unknown to us.", len(keys))
		}
		c.buttons = map[uint32]GPIO{}
		for i, want := range emuelecOrder {
			if keys[i].key() != want.pin.key() {
				return refuse("The gamepad wiring in this DTB is not one we know (button %d is %s, expected %s). It can't be converted safely.", i+1, keys[i].Name(), want.pin.Name())
			}
			c.buttons[want.key] = keys[i]
		}
		for _, g := range keys[len(emuelecOrder):] {
			if k, ok := emuelecExtra[g.key()]; ok {
				c.buttons[k] = g
			}
		}
		return nil
	}
	if findCompatible(c.in, "adc-joystick") != nil || findCompatible(c.in, "rockchip,rk3326-odroid-go3", "hardkernel,rk3326-odroid-go3") != nil {
		return refuse("This DTB is made for the mainline Linux kernel (ROCKNIX, AmberELEC and others). Only ArkOS, dArkOS and stock EmuELEC DTBs can be converted.")
	}
	return refuse("No gamepad description found in this DTB. Only ArkOS, dArkOS and stock EmuELEC DTBs can be converted.")
}

func (c *conv) detectFamily() (Family, error) {
	up, okUp := c.buttons[keyUp]
	a, okA := c.buttons[keyA]
	if !okUp || !okA {
		return "", refuse("This DTB does not say where the d-pad and the A button are wired.")
	}
	for _, fp := range familyPins {
		if up.key() == fp.up.key() {
			if a.key() != fp.a.key() {
				return "", refuse("The buttons look like a %s board (d-pad on %s) but the A button is on %s, not %s. This wiring is unknown.", fp.f.Label(), up.Name(), a.Name(), fp.a.Name())
			}
			return fp.f, nil
		}
	}
	return "", refuse("The d-pad is wired to %s. That matches no board family we know (R36S, EE clone, Soy Sauce).", up.Name())
}

// ---- panel ----

var panelProps = []string{"prepare-delay-ms", "reset-delay-ms", "init-delay-ms", "enable-delay-ms", "disable-delay-ms", "unprepare-delay-ms", "width-mm", "height-mm", "dsi,flags", "dsi,format", "dsi,lanes", "panel-init-sequence", "panel-exit-sequence"}
var timingProps = []string{"clock-frequency", "hactive", "vactive", "hfront-porch", "hsync-len", "hback-porch", "vfront-porch", "vsync-len", "vback-porch", "hsync-active", "vsync-active", "de-active", "pixelclk-active"}

func (c *conv) nativeTiming(t *Tree) (*Node, error) {
	dt := t.Node(pathPanel + "/display-timings")
	if dt == nil {
		return nil, refuse("The panel has no display timings.")
	}
	if ph, ok := dt.U32("native-mode"); ok {
		if n := t.Phandle(ph); n != nil {
			return n, nil
		}
	}
	if len(dt.Children) == 1 {
		return dt.Children[0], nil
	}
	// a broken native-mode reference: take the 60 Hz entry if there is one
	for _, ch := range dt.Children {
		if strings.Contains(ch.Name, "60") {
			if c.res != nil {
				c.warn("The panel's chosen timing points at nothing; used the %q timing.", ch.Name)
			}
			return ch, nil
		}
	}
	return nil, refuse("The panel has several timings and none is marked as the one to use.")
}

func (c *conv) panel() error {
	ip, op := c.in.Node(pathPanel), c.out.Node(pathPanel)
	if ip == nil {
		return refuse("This DTB has no MIPI DSI panel. Only DSI screens are supported.")
	}
	if op == nil {
		return fmt.Errorf("base has no panel node")
	}
	if v, ok := ip.Get("panel-init-sequence"); !ok || len(v) < 3 {
		return refuse("The panel in this DTB has no start-up commands. It can't be converted.")
	}
	// remember the base panel: when the input has the very same panel, our
	// tested timing and DSI flags win over the stock ones (see samePanel)
	baseInit, _ := op.Get("panel-init-sequence")
	baseInit = append([]byte(nil), baseInit...)
	baseFlags, _ := op.Get("dsi,flags")
	baseFlags = append([]byte(nil), baseFlags...)
	for _, p := range panelProps {
		if v, ok := ip.Get(p); ok {
			op.Set(p, v)
		} else if p == "panel-exit-sequence" {
			op.Delete(p)
		}
	}
	for _, p := range []string{"dsi,lanes"} {
		if v, ok := op.U32(p); ok && (v < 1 || v > 4) {
			return refuse("The panel uses %d DSI lanes. The RK3326 has 1 to 4.", v)
		}
	}
	// reset and enable lines
	if g, ok, e := c.in.GPIO(ip, "reset-gpios"); e != nil {
		return refuse("Panel reset line: %v", e)
	} else if ok {
		if e := c.out.SetGPIOs(op, "reset-gpios", g); e != nil {
			return e
		}
	} else {
		op.Delete("reset-gpios")
		c.warn("The panel has no reset line in this DTB.")
	}
	if g, ok, e := c.in.GPIO(ip, "enable-gpios"); e != nil {
		return refuse("Panel enable line: %v", e)
	} else if ok {
		if e := c.out.SetGPIOs(op, "enable-gpios", g); e != nil {
			return e
		}
	} else {
		op.Delete("enable-gpios")
	}
	if e := c.panelPower(ip, op); e != nil {
		return e
	}
	// timing
	it, e := c.nativeTiming(c.in)
	if e != nil {
		return e
	}
	ot, e := c.nativeTiming(c.out)
	if e != nil {
		return fmt.Errorf("base: %w", e)
	}
	baseTiming := map[string][]byte{}
	for _, p := range timingProps {
		if v, ok := ot.Get(p); ok {
			baseTiming[p] = append([]byte(nil), v...)
		}
	}
	for i, p := range timingProps {
		v, ok := it.Get(p)
		if !ok {
			if i < 9 {
				return refuse("The panel timing has no %s.", p)
			}
			continue // polarity flags: keep the base value
		}
		ot.Set(p, v)
	}
	dt := ot.parent
	for _, ch := range append([]*Node(nil), dt.Children...) {
		if ch != ot {
			dt.RemoveChild(ch.Name)
		}
	}
	w, _ := ot.U32("hactive")
	h, _ := ot.U32("vactive")
	c.res.Width, c.res.Height = w, h
	switch {
	case w == 640 && h == 480:
	case w == 720 && h == 720:
		c.res.Square = true
		fw := c.out.Root.AddChild("firmware").AddChild("android")
		fw.SetString("compatible", "android,firmware")
		fw.SetString("lcd_density", "200")
		c.note("720x720 screen: added the square-screen density (ro.boot.lcd_density 200), like our R36 Ultra/Max and R36S Plus DTBs.")
	default:
		return refuse("The screen is %dx%d. Only 640x480 and 720x720 screens are supported.", w, h)
	}
	init, _ := ip.Get("panel-init-sequence")
	inClk, _ := it.U32("clock-frequency")
	if bytes.Equal(init, baseInit) && bytes.Equal(baseTiming["clock-frequency"], u32bytes(inClk)) &&
		bytes.Equal(baseTiming["hactive"], u32bytes(w)) && bytes.Equal(baseTiming["vactive"], u32bytes(h)) {
		// Same panel as our base DTB (for example EE "Panel 8", whose stock
		// timing differs from the one that was reported working).
		for p, v := range baseTiming {
			ot.Set(p, v)
		}
		if len(baseFlags) > 0 {
			op.Set("dsi,flags", baseFlags)
		}
		c.note("Screen: same panel as our %s DTB, so its tested timing and DSI flags were kept.", c.res.Family)
	} else {
		c.fixRefresh(ot)
	}
	c.note("Screen: %dx%d panel, %d bytes of start-up commands, timing and delays copied.", w, h, len(init))
	return nil
}

// fixRefresh shortens the vertical blanking so a slow screen runs at about
// 60 Hz with the same pixel clock. Faster screens keep their stock timing,
// except the proven R36S Panel 4 one.
func (c *conv) fixRefresh(t *Node) {
	get := func(p string) float64 { v, _ := t.U32(p); return float64(v) }
	clk := get("clock-frequency")
	ht := get("hactive") + get("hfront-porch") + get("hsync-len") + get("hback-porch")
	vt := get("vactive") + get("vfront-porch") + get("vsync-len") + get("vback-porch")
	if clk == 0 || ht == 0 || vt == 0 {
		return
	}
	hz := clk / (ht * vt)
	if math.Abs(hz-60)/60 <= 0.0025 {
		c.note("Screen refresh: %.2f Hz, left as it is.", hz)
		return
	}
	newVt := math.Round(clk / (ht * 60))
	// R36S Panel 4 timing (29 MHz, 640+46+2+44): keep its proven 165 lines
	// of front porch, which Panel 2/3 also use.
	proven := clk == 29000000 && get("hactive") == 640 && get("hfront-porch") == 46 && get("hsync-len") == 2 && get("hback-porch") == 44 &&
		get("vactive") == 480 && get("vsync-len") == 2 && get("vback-porch") == 14
	if proven {
		newVt = 480 + 165 + 2 + 14
	} else if newVt > vt {
		// Slowing a panel down means a longer front porch, and some panels
		// stay dark with it (an RX6H at 60 Hz instead of its stock 80 Hz).
		c.note("Screen refresh: %.2f Hz, left as it is (the stock timing).", hz)
		return
	}
	vfp := get("vfront-porch") + newVt - vt
	if vfp < 4 {
		c.warn("Screen refresh is %.2f Hz and can't be brought to 60 Hz with this timing. Left as it is.", hz)
		return
	}
	old, _ := t.U32("vfront-porch")
	t.SetU32("vfront-porch", uint32(vfp))
	c.note("Screen refresh: %.2f Hz changed to %.2f Hz (vertical front porch %d -> %d, same pixel clock).", hz, clk/(ht*newVt), old, uint32(vfp))
}

func isLDO(n *Node, name string) bool {
	return n != nil && n.Name == name && n.parent != nil && n.parent.Name == "regulators"
}

func (c *conv) panelPower(ip, op *Node) error {
	ph, ok := ip.U32("power-supply")
	if !ok {
		return refuse("The panel has no power supply in this DTB.")
	}
	src := c.in.Phandle(ph)
	if src == nil {
		return refuse("The panel power supply points at nothing.")
	}
	sw := c.out.Node("/vcc-lcd-sw")
	switch {
	case isLDO(src, "LDO_REG8"):
		ldo8 := c.out.Node(pathRegs + "/LDO_REG8")
		if ldo8 == nil {
			return fmt.Errorf("base has no LDO8")
		}
		op.SetU32("power-supply", c.out.PhandleOf(ldo8))
		for _, p := range []string{"regulator-min-microvolt", "regulator-max-microvolt"} {
			if v, ok := src.U32(p); ok {
				if v < 1800000 || v > 3300000 {
					return refuse("LDO8 (screen power) is set to %d uV in this DTB. That is outside 1.8-3.3 V.", v)
				}
				ldo8.SetU32(p, v)
			}
		}
		if s := ldo8.Child("regulator-state-mem"); s != nil {
			if v, ok := src.U32("regulator-max-microvolt"); ok && s.Has("regulator-suspend-microvolt") {
				s.SetU32("regulator-suspend-microvolt", v)
			}
		}
		if sw != nil {
			sw.SetString("status", "disabled")
			c.note("Screen power: RK817 LDO8 (the GPIO-switched LCD rail of the base is turned off).")
		} else {
			c.note("Screen power: RK817 LDO8.")
		}
		return nil
	case src.Compatible("regulator-fixed"):
		g, has, e := c.in.GPIO(src, "gpio")
		if e != nil {
			return refuse("Screen power switch: %v", e)
		}
		if !has {
			return refuse("The screen power rail in this DTB has no switch GPIO. This wiring is unknown to us.")
		}
		if sw == nil {
			return refuse("The screen is powered through a GPIO switch (%s), which %s boards don't have. This DTB doesn't match its board family.", g.Name(), c.res.Family.Label())
		}
		if e := c.out.SetGPIOs(sw, "gpio", g); e != nil {
			return e
		}
		sw.Delete("status")
		op.SetU32("power-supply", c.out.PhandleOf(sw))
		c.note("Screen power: GPIO-switched rail on %s.", g.Name())
		return nil
	}
	return refuse("The screen is powered from %s, which we don't know how to handle.", src.Path())
}

func (c *conv) backlight() error {
	ib, ob := c.in.Node("/backlight"), c.out.Node("/backlight")
	if ib == nil || ob == nil {
		return nil
	}
	ic, ok1 := ib.Cells("pwms")
	oc, ok2 := ob.Cells("pwms")
	if !ok1 || !ok2 || len(ic) < 3 || len(oc) < 3 {
		return nil
	}
	if pi, po := c.in.Phandle(ic[0]), c.out.Phandle(oc[0]); pi == nil || po == nil || pi.Path() != po.Path() {
		c.warn("The backlight uses another PWM in this DTB; kept ours (PWM1).")
		return nil
	}
	if ic[2] < 1000 || ic[2] > 10000000 {
		return nil
	}
	if oc[2] != ic[2] {
		oc[2] = ic[2]
		ob.SetCells("pwms", oc...)
	}
	c.note("Backlight PWM period: %d ns.", ic[2])
	return nil
}

// ---- joypad ----

func (c *conv) joypad() error {
	pad := findCompatible(c.out, "odroidgo3-joypad")
	if pad == nil {
		return fmt.Errorf("base has no joypad")
	}
	oldPins := c.buttonPins(pad)
	var missing []string
	for _, ch := range pad.Children {
		code, ok := ch.U32("linux,code")
		if !ok {
			continue
		}
		g, ok := c.buttons[code]
		if !ok {
			missing = append(missing, keyNames[code])
			continue
		}
		if e := c.out.SetGPIOs(ch, "gpios", g); e != nil {
			return e
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		c.warn("This DTB does not list these buttons: %s. They keep the usual pins of the board family.", strings.Join(missing, ", "))
	}
	// analog stick multiplexer
	switch c.padKind {
	case "arkos":
		for _, p := range []string{"amux-a-gpios", "amux-b-gpios", "amux-en-gpios"} {
			g, ok, e := c.in.GPIO(c.inPad, p)
			if e != nil {
				return refuse("Stick wiring: %v", e)
			}
			if !ok {
				return refuse("The stick wiring (%s) is missing in this DTB.", p)
			}
			if e := c.out.SetGPIOs(pad, p, g); e != nil {
				return e
			}
		}
		if v, ok := c.inPad.Get("amux-channel-mapping"); ok {
			pad.Set("amux-channel-mapping", v)
		} else {
			pad.Delete("amux-channel-mapping")
		}
		for _, p := range []string{"invert-absx", "invert-absy", "invert-absrx", "invert-absry"} {
			if c.inPad.Has(p) {
				pad.Set(p, nil)
			} else {
				pad.Delete(p)
			}
		}
	case "emuelec":
		for from, to := range map[string]string{"rocker0-gpios": "amux-en-gpios", "rocker1-gpios": "amux-a-gpios", "rocker-gpios": "amux-b-gpios"} {
			g, ok, e := c.in.GPIO(c.inPad, from)
			if e != nil {
				return refuse("Stick wiring: %v", e)
			}
			if !ok {
				return refuse("The stick wiring (%s) is missing in this DTB.", from)
			}
			if e := c.out.SetGPIOs(pad, to, g); e != nil {
				return e
			}
		}
		c.note("Stick channel order and direction: the EE board defaults (EmuELEC DTBs don't say).")
	}
	en, _, _ := c.out.GPIO(pad, "amux-en-gpios")
	c.note("Buttons: %d pins mapped to our key codes; stick multiplexer enable on %s.", len(c.buttons), en.Name())
	return c.updateButtonPins(pad, oldPins)
}

// buttonPins collects the pins of joypad buttons and GPIO volume keys.
func (c *conv) buttonPins(pad *Node) map[uint32]bool {
	pins := map[uint32]bool{}
	add := func(n *Node) {
		for _, ch := range n.Children {
			if g, ok, _ := c.out.GPIO(ch, "gpios"); ok {
				pins[g.key()] = true
			}
		}
	}
	add(pad)
	if k := findCompatible(c.out, "gpio-keys"); k != nil {
		add(k)
	}
	return pins
}

// updateButtonPins keeps the joypad's pull-up pin group in step with the pins.
func (c *conv) updateButtonPins(pad *Node, old map[uint32]bool) error {
	now := c.buttonPins(pad)
	same := len(now) == len(old)
	for k := range now {
		if !old[k] {
			same = false
		}
	}
	if same {
		return nil
	}
	ph, ok := pad.U32("pinctrl-0")
	if !ok {
		return nil
	}
	group := c.out.Phandle(ph)
	pins, e := c.out.Pins(group)
	if e != nil {
		return e
	}
	pullUp := c.out.pinConf("pcfg-pull-up")
	if pullUp == nil {
		return fmt.Errorf("base has no pull-up pin configuration")
	}
	var out []PinCfg
	have := map[uint32]bool{}
	for _, p := range pins {
		k := p.Bank<<8 | p.Pin
		if old[k] && !now[k] {
			continue
		}
		have[k] = true
		out = append(out, p)
	}
	var add []uint32
	for k := range now {
		if !have[k] {
			add = append(add, k)
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i] < add[j] })
	for _, k := range add {
		out = append(out, PinCfg{Bank: k >> 8, Pin: k & 0xff, Func: 0, Conf: pullUp})
	}
	c.out.SetPins(group, out)
	return nil
}

func (c *conv) volumeKeys() error {
	// GPIO volume keys (R36S, Soy Sauce)
	if ok := findCompatible(c.out, "gpio-keys"); ok != nil {
		ik := findCompatible(c.in, "gpio-keys")
		if ik == nil {
			c.warn("This DTB has no GPIO volume keys; kept the usual pins of the board family.")
			return nil
		}
		old := c.buttonPins(findCompatible(c.out, "odroidgo3-joypad"))
		n := 0
		for _, och := range ok.Children {
			code, _ := och.U32("linux,code")
			for _, ich := range ik.Children {
				if ic, _ := ich.U32("linux,code"); ic == code {
					if g, has, e := c.in.GPIO(ich, "gpios"); e == nil && has {
						if e := c.out.SetGPIOs(och, "gpios", g); e != nil {
							return e
						}
						n++
					}
				}
			}
		}
		c.note("Volume keys: %d GPIO keys copied.", n)
		return c.updateButtonPins(findCompatible(c.out, "odroidgo3-joypad"), old)
	}
	// ADC ladder (EE)
	if oa := findCompatible(c.out, "adc-keys"); oa != nil {
		ia := findCompatible(c.in, "adc-keys")
		if ia == nil {
			c.warn("This DTB has no ADC volume keys; kept the usual EE values (SARADC channel 2).")
			return nil
		}
		if ic, ok := ia.Cells("io-channels"); ok && len(ic) == 2 {
			if oc, ok := oa.Cells("io-channels"); ok && len(oc) == 2 {
				oc[1] = ic[1]
				oa.SetCells("io-channels", oc...)
			}
		}
		if v, ok := ia.U32("keyup-threshold-microvolt"); ok {
			oa.SetU32("keyup-threshold-microvolt", v)
		}
		for _, och := range oa.Children {
			code, _ := och.U32("linux,code")
			for _, ich := range ia.Children {
				if ic, _ := ich.U32("linux,code"); ic == code {
					if v, ok := ich.U32("press-threshold-microvolt"); ok {
						och.SetU32("press-threshold-microvolt", v)
					}
				}
			}
		}
		c.note("Volume keys: ADC thresholds copied.")
	}
	return nil
}

// ---- battery and charger ----

// The ODROID-GO2 table many firmwares carry for every board. It is not the
// battery of the console, so we keep the family's own table instead.
var genericOCV = []uint32{3165, 3220, 3280, 3330, 3380, 3430, 3480, 3530, 3580, 3630, 3670, 3710, 3750, 3790, 3830, 3870, 3910, 3950, 3990, 3995, 4000}

func equalCells(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *conv) battery() error {
	ib := findCompatible(c.in, "rk817,battery", "rk817,battery-5.10")
	ob := c.out.Node(pathRK817 + "/battery")
	if ob == nil {
		return fmt.Errorf("base has no battery node")
	}
	if ib == nil {
		c.warn("No battery description in this DTB; kept the board family's values.")
		return nil
	}
	ocv, ok := ib.Cells("ocv_table")
	if !ok {
		c.warn("No battery table in this DTB; kept the board family's values.")
		return nil
	}
	if equalCells(ocv, genericOCV) {
		c.note("Battery: this DTB carries the generic ODROID-GO2 table, not a table for this battery. Kept our values for the board family.")
		return nil
	}
	bad := len(ocv) != 21 || ocv[0] < 3000 || ocv[20] > 4450
	for i := 1; i < len(ocv) && !bad; i++ {
		if ocv[i] < ocv[i-1] {
			bad = true
		}
	}
	cap, _ := ib.U32("design_capacity")
	qmax, _ := ib.U32("design_qmax")
	off, _ := ib.U32("power_off_thresd")
	if bad || cap < 1000 || cap > 8000 || qmax < cap || off < 3000 || off > 3700 {
		c.warn("The battery values in this DTB look wrong; kept the board family's values.")
		return nil
	}
	baseOff, _ := ob.U32("power_off_thresd")
	ob.SetCells("ocv_table", ocv...)
	for _, p := range []string{"design_capacity", "design_qmax", "bat_res", "power_off_thresd", "zero_algorithm_vol"} {
		if v, ok := ib.U32(p); ok {
			ob.SetU32(p, v)
		}
	}
	// a lower shutdown voltage than our base drains the cell deeper; keep ours
	if off < baseOff {
		ob.SetU32("power_off_thresd", baseOff)
		c.note("Battery: %d mAh and charge table copied. Shutdown voltage kept at %d mV (the DTB says %d mV).", cap, baseOff, off)
		return nil
	}
	c.note("Battery: %d mAh, charge table and empty voltage (%d mV) copied.", cap, off)
	return nil
}

func (c *conv) charger() error {
	oc := c.out.Node(pathRK817 + "/charger")
	if oc == nil {
		return nil
	}
	// charge limits: a lower stock limit wins (small or weak batteries);
	// a higher one is ignored
	if ic := findCompatible(c.in, "rk817,charger"); ic != nil {
		for _, p := range []string{"max_input_current", "max_chrg_current", "max_chrg_voltage"} {
			iv, ok1 := ic.U32(p)
			ov, ok2 := oc.U32(p)
			if ok1 && ok2 && iv < ov && iv > 0 {
				oc.SetU32(p, iv)
				c.note("Charger: %s lowered from %d to %d, as in the input.", p, ov, iv)
			}
		}
	}
	for _, n := range []*Node{findCompatible(c.in, "rk817,charger"), findCompatible(c.in, "rk817,battery", "rk817,battery-5.10")} {
		if n == nil {
			continue
		}
		if g, ok, e := c.in.GPIO(n, "dc_det_gpio"); e == nil && ok {
			if e := c.out.SetGPIOs(oc, "dc_det_gpio", g); e != nil {
				return e
			}
			c.note("Charger plug detect: %s.", g.Name())
			return nil
		}
	}
	return nil
}

// ---- LEDs ----

func (c *conv) leds() error {
	if c.res.Family != EE {
		c.note("LEDs: the board family's defaults.")
		return nil
	}
	leds := c.out.Node("/gpio_leds")
	if leds == nil {
		return nil
	}
	type led struct {
		name, label, trigger string
		g                    GPIO
	}
	var add []led
	found := map[string]GPIO{}
	// EmuELEC: charger / battery properties
	for _, n := range []*Node{findCompatible(c.in, "rk817,charger"), findCompatible(c.in, "rk817,battery", "rk817,battery-5.10")} {
		if n == nil {
			continue
		}
		for _, p := range []string{"charge_blue_gpio", "charge_red_gpio", "work_blue_gpio", "work_red_gpio"} {
			if g, ok, e := c.in.GPIO(n, p); e == nil && ok {
				found[p] = g
			}
		}
	}
	// gamepad RGB (EmuELEC) or joy-* LEDs (ArkOS)
	if c.padKind == "emuelec" {
		for _, col := range []string{"red", "green", "blue"} {
			if g, ok, e := c.in.GPIO(c.inPad, col+"-gpio"); e == nil && ok {
				found["rgb-"+col] = g
			}
		}
	}
	if gl := findCompatible(c.in, "gpio-leds"); gl != nil {
		for _, ch := range gl.Children {
			g, ok, e := c.in.GPIO(ch, "gpios")
			if e != nil || !ok {
				continue
			}
			l := strings.ToLower(ch.Name + " " + ch.String("label"))
			for _, col := range []string{"red", "green", "blue"} {
				if strings.Contains(l, "joy") && strings.Contains(l, col) {
					found["rgb-"+col] = g
				}
			}
			// ArkOS K36 ports: blue/red "work" LEDs on GPIO0_B3/B4
			if g.Bank == 0 && (g.Pin == 11 || g.Pin == 12) {
				found[map[uint32]string{11: "work_blue_gpio", 12: "work_red_gpio"}[g.Pin]] = g
			}
		}
	}
	// charge LEDs: the base has them; move them if the DTB says otherwise
	for prop, child := range map[string]string{"charge_blue_gpio": "charge-blue", "charge_red_gpio": "charge-red"} {
		if g, ok := found[prop]; ok {
			if n := leds.Child(child); n != nil {
				if e := c.out.SetGPIOs(n, "gpios", GPIO{g.Bank, g.Pin, 0}); e != nil {
					return e
				}
			}
		}
	}
	if g, ok := found["work_blue_gpio"]; ok {
		add = append(add, led{"work-blue", "blue:work", "default-on", g})
	}
	if g, ok := found["work_red_gpio"]; ok {
		add = append(add, led{"work-red", "red:work", "none", g})
	}
	for _, col := range []string{"blue", "green", "red"} {
		if g, ok := found["rgb-"+col]; ok {
			add = append(add, led{"rgb-" + col, col + ":status", "none", g})
		}
	}
	var names []string
	used := map[uint32]bool{}
	for _, ch := range leds.Children {
		if g, ok, _ := c.out.GPIO(ch, "gpios"); ok {
			used[g.key()] = true
		}
	}
	for _, l := range add {
		if used[l.g.key()] {
			continue // same LED as a charge LED (some firmwares list it twice)
		}
		used[l.g.key()] = true
		n := leds.AddChild(l.name)
		n.SetString("label", l.label)
		if e := c.out.SetGPIOs(n, "gpios", GPIO{l.g.Bank, l.g.Pin, l.g.Flags & 1}); e != nil {
			return e
		}
		n.SetString("linux,default-trigger", l.trigger)
		if e := c.addLEDPin(l.g, "pcfg-pull-none"); e != nil {
			return e
		}
		names = append(names, l.label+" "+l.g.Name())
	}
	msg := "LEDs: charge LEDs (battery-full / battery-charging triggers)"
	if len(names) > 0 {
		msg += ", plus " + strings.Join(names, ", ")
	}
	c.note("%s.", msg)
	return nil
}

func (c *conv) addLEDPin(g GPIO, conf string) error {
	leds := c.out.Node("/gpio_leds")
	ph, ok := leds.U32("pinctrl-0")
	if !ok {
		return nil
	}
	group := c.out.Phandle(ph)
	pins, e := c.out.Pins(group)
	if e != nil {
		return e
	}
	cfg := c.out.pinConf(conf)
	if cfg == nil {
		return fmt.Errorf("base has no %s pin configuration", conf)
	}
	for _, p := range pins {
		if p.Bank == g.Bank && p.Pin == g.Pin {
			return nil
		}
	}
	pins = append(pins, PinCfg{g.Bank, g.Pin, 0, cfg})
	sort.SliceStable(pins, func(i, j int) bool { return pins[i].Bank<<8|pins[i].Pin < pins[j].Bank<<8|pins[j].Pin })
	c.out.SetPins(group, pins)
	return nil
}

// ---- rumble ----

func (c *conv) rumble() error {
	var motor *GPIO
	var where string
	c.in.Walk(func(n *Node) {
		for _, p := range n.Props {
			switch p.Name {
			case "rumble-gpio", "rumble-gpios", "motor-gpio", "motor-gpios", "moto-gpio", "moto-gpios":
				if motor == nil {
					if g, ok, e := c.in.GPIO(n, p.Name); e == nil && ok {
						motor, where = &g, n.Path()+"/"+p.Name
					}
				}
			}
		}
	})
	if motor == nil {
		return nil
	}
	pad := findCompatible(c.out, "odroidgo3-joypad")
	// Community ports drive these motors active high; the EmuELEC flag says
	// low on some boards. Active high matches our R36 Ultra/XF DTBs.
	g := GPIO{motor.Bank, motor.Pin, 0}
	if e := c.out.SetGPIOs(pad, "rumble-gpios", g); e != nil {
		return e
	}
	if c.out.Node("/gpio_leds") != nil && c.res.Family == EE {
		if e := c.addLEDPin(g, "pcfg-pull-down"); e != nil {
			return e
		}
	}
	c.note("Rumble motor on %s (from %s), driven as gamepad rumble, active high.", g.Name(), where)
	if motor.Flags&1 != 0 {
		c.warn("The DTB marks the rumble motor active low; we drive it active high like the community ports. If the motor runs all the time, this is why.")
	}
	return nil
}

// ---- regulators ----

func setRange(n *Node, min, max uint32) {
	n.SetU32("regulator-min-microvolt", min)
	n.SetU32("regulator-max-microvolt", max)
	if s := n.Child("regulator-state-mem"); s != nil && s.Has("regulator-suspend-microvolt") {
		s.SetU32("regulator-suspend-microvolt", max)
	}
}

func (c *conv) regulators() error {
	ir, or := c.in.Node(pathRegs), c.out.Node(pathRegs)
	if ir == nil || or == nil {
		return nil
	}
	// LDO4 feeds the PMU I/O domain: follow the board (3.0 V on clones).
	if i, o := ir.Child("LDO_REG4"), or.Child("LDO_REG4"); i != nil && o != nil {
		imin, ok1 := i.U32("regulator-min-microvolt")
		imax, ok2 := i.U32("regulator-max-microvolt")
		omax, _ := o.U32("regulator-max-microvolt")
		if ok1 && ok2 && imin == imax && imax >= 1800000 && imax <= 3300000 && imax != omax {
			setRange(o, imin, imax)
			c.note("LDO4 (PMU I/O): %.1f V as in this DTB.", float64(imax)/1e6)
		}
	}
	// LDO9: never run it higher than the board's own firmware does.
	if i, o := ir.Child("LDO_REG9"), or.Child("LDO_REG9"); i != nil && o != nil {
		imax, ok := i.U32("regulator-max-microvolt")
		omax, _ := o.U32("regulator-max-microvolt")
		if ok && imax >= 800000 && imax < omax {
			imin, _ := i.U32("regulator-min-microvolt")
			if imin == 0 || imin > imax {
				imin = imax
			}
			setRange(o, imin, imax)
			c.note("LDO9: lowered to %.2f V as in this DTB.", float64(imax)/1e6)
		}
	}
	return nil
}

func (c *conv) pmicIRQ() error {
	ip, op := c.in.Node(pathRK817), c.out.Node(pathRK817)
	if ip == nil || op == nil {
		return refuse("This DTB has no RK817 power chip where we expect it.")
	}
	ii, ok1 := ip.Cells("interrupts")
	oi, ok2 := op.Cells("interrupts")
	if !ok1 || !ok2 || len(ii) != 2 || len(oi) != 2 || ii[0] == oi[0] {
		return nil
	}
	if par, ok := ip.U32("interrupt-parent"); ok {
		if b, ok := gpioBank(c.in.Phandle(par)); !ok || b != 0 {
			return refuse("The power chip interrupt is not on GPIO0 in this DTB. This wiring is unknown.")
		}
	}
	// move the pin in the pmic_int pin group too
	if ph, ok := op.U32("pinctrl-0"); ok {
		g := c.out.Phandle(ph)
		pins, e := c.out.Pins(g)
		if e != nil {
			return e
		}
		for i := range pins {
			if pins[i].Bank == 0 && pins[i].Pin == oi[0] {
				pins[i].Pin = ii[0]
			}
		}
		c.out.SetPins(g, pins)
	}
	op.SetCells("interrupts", ii[0], oi[1])
	c.note("Power chip interrupt: %s.", GPIO{0, ii[0], 0}.Name())
	return nil
}

// ---- second card slot / Wi-Fi ----

func (c *conv) secondSlot() error {
	is, os := c.in.Node(pathSDIO), c.out.Node(pathSDIO)
	if is == nil || os == nil {
		return nil
	}
	wl := c.in.Node("/wireless-wlan")
	chip := ""
	if wl != nil {
		chip = wl.String("wifi_chip_type")
	}
	if is.Has("supports-rk912") || is.Has("supports-rk915") || chip == "rk915" || chip == "rk912" {
		return c.rk915(is, os, wl)
	}
	if !is.Enabled() {
		os.SetString("status", "disabled")
		c.note("Second card slot: off, as in this DTB.")
		return nil
	}
	if is.Has("supports-sdio") && !is.Has("supports-sd") && !is.Has("cd-gpios") {
		c.warn("The second SD controller drives an SDIO chip (%q) we don't support; the slot is turned off.", chip)
		os.SetString("status", "disabled")
		return nil
	}
	if g, ok, e := c.in.GPIO(is, "cd-gpios"); e == nil && ok {
		if e := c.out.SetGPIOs(os, "cd-gpios", g); e != nil {
			return e
		}
		c.note("Second card slot: card detect on %s.", g.Name())
	}
	if os.Has("no-1-8-v") {
		c.note("Second card slot: high speed without the 1.8 V UHS switch (its I/O rail can't switch on this board family).")
	} else {
		c.note("Second card slot: UHS allowed (switchable I/O rail on this board family).")
	}
	return nil
}

func (c *conv) rk915(is, os, wl *Node) error {
	if wl == nil {
		return refuse("The second SD controller is set up for RK915 Wi-Fi but the Wi-Fi power pins are missing.")
	}
	ow := c.out.Node("/wireless-wlan")
	if ow == nil {
		return fmt.Errorf("base has no wireless-wlan node")
	}
	pwr, ok1, e1 := c.in.GPIO(wl, "WIFI,poweren_gpio")
	wake, ok2, e2 := c.in.GPIO(wl, "WIFI,host_wake_irq")
	if e1 != nil || e2 != nil || !ok1 || !ok2 {
		return refuse("The RK915 Wi-Fi power or wake pin is missing or unreadable in this DTB.")
	}
	ow.SetString("wifi_chip_type", "rk915")
	if e := c.out.SetGPIOs(ow, "WIFI,poweren_gpio", pwr); e != nil {
		return e
	}
	if e := c.out.SetGPIOs(ow, "WIFI,host_wake_irq", GPIO{wake.Bank, wake.Pin, 0}); e != nil {
		return e
	}
	for _, p := range []string{"supports-sd", "cd-gpios", "card-detect-delay", "cap-mmc-highspeed", "sd-uhs-sdr12", "sd-uhs-sdr25", "sd-uhs-sdr50", "sd-uhs-sdr104", "vmmc-supply", "vqmmc-supply", "no-1-8-v"} {
		os.Delete(p)
	}
	os.SetU32("max-frequency", 50000000)
	for _, p := range []string{"supports-sdio", "supports-rk915", "keep-power-in-suspend"} {
		os.Set(p, nil)
	}
	for _, p := range []string{"no-sd", "no-mmc"} {
		if is.Has(p) {
			os.Set(p, nil)
		}
	}
	c.note("Wi-Fi: RK915 on the second SD controller (power %s, wake %s). Needs a kernel with the RK915 driver.", pwr.Name(), wake.Name())
	return nil
}

// ---- USB host power ----

func (c *conv) usbHost() error {
	var src *Node
	c.in.Walk(func(n *Node) {
		if src == nil && n.Compatible("regulator-fixed") && n.Enabled() && n.Has("gpio") {
			l := strings.ToLower(n.Name + " " + n.String("regulator-name"))
			if strings.Contains(l, "host") {
				src = n
			}
		}
	})
	if src == nil {
		return nil
	}
	g, ok, e := c.in.GPIO(src, "gpio")
	if e != nil || !ok {
		return nil
	}
	g.Flags = 0
	host := c.out.Node("/vcc-host")
	if host == nil {
		host = c.out.Root.AddChild("vcc-host")
		host.SetString("compatible", "regulator-fixed")
		host.SetString("regulator-name", "vcc_host")
		host.Set("enable-active-high", nil)
		host.Set("regulator-always-on", nil)
		host.SetU32("regulator-min-microvolt", 5000000)
		host.SetU32("regulator-max-microvolt", 5000000)
		if sys := c.out.Node("/vccsys"); sys != nil {
			host.SetU32("vin-supply", c.out.PhandleOf(sys))
		}
	}
	if e := c.out.SetGPIOs(host, "gpio", g); e != nil {
		return e
	}
	// GPIO0_B7 is the R36S rumble PWM pin: keep the PWM block off it.
	if g.Bank == 0 && g.Pin == 15 {
		pwm := c.out.Node("/pwm@ff200000")
		pc := c.out.Node("/pinctrl")
		none := c.out.pinConf("pcfg-pull-none")
		if pwm == nil || pc == nil || none == nil {
			return fmt.Errorf("base lacks pwm0 or pin configuration")
		}
		grp := pc.AddChild("usb").AddChild("vcc-host-en")
		c.out.SetPins(grp, []PinCfg{{0, 15, 0, none}})
		pwm.SetU32("pinctrl-0", c.out.PhandleOf(grp))
		c.note("USB host 5 V switch on GPIO0_B7; the rumble PWM is kept off that pin.")
		return nil
	}
	c.note("USB host 5 V switch on %s.", g.Name())
	return nil
}

// ---- audio ----

func (c *conv) audio() error {
	codec := c.out.Node(pathRK817 + "/codec")
	if codec == nil {
		return nil
	}
	var spk *GPIO
	if ic := c.in.Node(pathRK817 + "/codec"); ic != nil {
		if g, ok, e := c.in.GPIO(ic, "spk-ctl-gpios"); e == nil && ok {
			spk = &g
		}
		if ic.Has("use-ext-amplifier") {
			codec.Set("use-ext-amplifier", nil)
			c.note("Speaker: external amplifier, as in this DTB.")
		}
	}
	if spk == nil {
		if snd := c.in.Node("/rk817-sound"); snd != nil {
			if g, ok, e := c.in.GPIO(snd, "spk-con-gpio"); e == nil && ok {
				spk = &g
			}
		}
	}
	if spk != nil {
		if e := c.out.SetGPIOs(codec, "spk-ctl-gpios", GPIO{spk.Bank, spk.Pin, spk.Flags & 1}); e != nil {
			return e
		}
		c.note("Speaker switch on %s.", spk.Name())
	} else if codec.Has("spk-ctl-gpios") {
		g, _, _ := c.out.GPIO(codec, "spk-ctl-gpios")
		c.warn("This DTB has no speaker switch; kept the board family's %s.", g.Name())
	}
	return nil
}

func (c *conv) model() error {
	m := strings.TrimSpace(c.res.Model)
	if m == "" {
		m = "unknown board"
	}
	c.out.Root.SetString("model", m+" (converted for andr36oid)")
	return nil
}

func u32bytes(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }
