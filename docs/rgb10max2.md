# the powkiddy rgb10 max 2

this is what it took to run andr36oid on the powkiddy rgb10 max 2, a 2021 rk3326
handheld that shares the odroid go super's board design but, unlike the og super,
ships with an internal wifi module. the port itself is small: a device tree, a
boot.ini line, a boot service. getting wifi to work was the interesting part and
needed something no device tree can express, explained further down.

status: every feature listed here was verified on hardware. wifi works with no
cables attached, on every cold boot.

## the console

- rockchip rk3326, 1 gb ram, no emmc (sd card only)
- 5 inch 854 x 480 ips, mipi dsi panel `elida,kd50t048a`, portrait panel used
  landscape, 2 lanes
- buttons on the odroid go super map (amux adc), plus start/select on gpio2 and
  l1/r1 on gpio3
- wifi + bluetooth: one realtek rtl8723bu combo module (`0bda:b720`),
  usb, sitting behind a genesys `05e3:0608` hub on the dwc2 usb bus. 2.4 ghz
  only: 802.11n, no 5 ghz, no wpa3 (see known limits)
- two usb-c ports: one marked power (charge only, no data lines), one marked
  otg (data, wired to the same dwc2 bus as the internal hub)

## the profile

the flasher reads console profiles from `BOOT/andr36oid-profiles.txt`; nothing
in this repo needs a new console. add one line and the matching folder:

```text
Devices/RGB10MAX2|RGB10 Max 2|Powkiddy RGB10 Max 2|
```

`BOOT/Devices/RGB10MAX2/` holds the same file set every r36s-derived profile
uses, built for this console:

- `rk3326-r36s-android.dtb` - the device tree described below
- `logo.bmp` - the boot logo
- `rg351mp-uboot.dtb`, `rg351p-uboot.dtb`, `rg351v-uboot.dtb` and the matching
  `*-kernel.dtb` files - the u-boot and kernel copies the bootloader picks from
  by board revision; the og super family uses the same names

the console identifies as `hwrev=v10-go3`, so the bootloader boots the
`rk3326-odroidgo3` path. the top-level files on BOOT are replaced with the
profile's files as usual.

## boot.ini

two lines matter on this console:

```text
setenv android "... androidboot.sf_orientation=ORIENTATION_270"
setenv bootrootfs "... usbcore.autosuspend=-1"
```

the panel is portrait and mounted landscape, so surfaceflinger needs the
orientation hint. `usbcore.autosuspend=-1` keeps the internal hub's usb port
awake; with autosuspend left on, the hub can idle off and wifi drops for a
moment during boot.

## the device tree

`docs/rgb10max2/rk3326-rgb10max2-android.dts` is the full source. it includes
`rk3326-r36s-android.dtsi` from the kernel tree and changes only these things:

- panel to `elida,kd50t048a` with the init sequence and timing from the stock
  go3 device tree, plus the 251 level backlight
- `invert-absrx` on the joypad and the amux channels made explicit
- leds: no vibrator, charge led on gpio0 pb5
- battery: design capacity 4000, charger values from the go3 tree
- `&rk817 { charger { /delete-property/ dc_det_gpio; }; }` - the base tree wires
  the rk817 charger's dc detect to gpio0 pb3 for the r36s pcb. this board has no
  dc jack and the pin floats high, so android shows "charging" with no cable.
  removing the property fixes the indicator; usb-c charging is unaffected
- the dwc2 otg controller stays in `dr_mode = "otg"` and the phy port stays
  enabled, so the otg connector keeps working as a normal usb-c data port
  (host devices, and android's usb modes)

build it exactly like the other andr36oid device trees:

```sh
cpp -nostdinc -I include -I arch/arm64/boot/dts/rockchip -undef -D__DTS__ \
    -x assembler-with-cpp rk3326-rgb10max2-android.dts -o out.dts
dtc -@ -I dts -O dtb -o rk3326-r36s-android.dtb out.dts
```

the `-undef` matters: the kernel's `linux` builtin macro mangles `linux,code`.

## wifi power, the part that no device tree can do

the internal hub and the wifi dongle are on the dwc2 bus. on this board their 5 v
comes from the otg port's vbus net, and the rk817 pmic's otg rail is what is
supposed to feed that net when the port is in host mode. two things make this
awkward:

1. the charger driver disables the otg rail during probe, and the phy event that
   would re-enable it is suppressed by the base device tree
   (`rockchip,no-vbus-en-extcon`)
2. more importantly, the rk817's rail on this board behaves as a pmic-side latch.
   kernel writes to the otg enable register (charger, regulator, phy vbus
   supply) never reach it: the kernel's register cache reads back "on"
   (`reg_b4=47 otg5v=1`) while the hardware register still reads `0x07`, and no
   power appears on the net. writing the register from u-boot does not survive
   either

what does work: a raw i2c write of `0xB4 = 0x47` from linux, once the charger
driver is not fighting it, flips the latch. the hub then enumerates from battery
power and the wifi module comes up. the latch survives warm reboots but resets on
a cold power-off, so it has to be set on every cold boot. writing it while
external 5 v is already on the otg net can trip the pmic, so the write must only
happen when the hub is not already powered.

`docs/rgb10max2/service/` does exactly that at boot, in this order:

1. preload `rtl8xxxu.ko` so the dongle binds the moment it enumerates
2. pin runtime pm on the usb controllers
3. if the hub is not enumerated: write `0xB4 = 0x47` with `rk817tool`. if that
   does not take, unbind the charger, write again, rebind, and write once more if
   rebinding killed it
4. after boot: disable wifi power save and set the ma regdomain with `wifitool`,
   then open adb over tcp
5. keep a small root command loop alive (the flasher's servicing hook, optional
   for a release build)

`docs/rgb10max2/tools/rk817tool.c` is a static aarch64 binary that reads and
writes rk817 registers over `/dev/i2c-0` (address 0x20).
`docs/rgb10max2/tools/wifitool.c` is a static aarch64 nl80211 helper (no libnl)
for power save and the regulatory domain, because the image ships no `iw`.

## what works

- display, orientation, touch-less ui, all buttons and both sticks
- wifi from battery on every cold boot (after the latch write), wpa2-psk
  networks including mixed wpa/wpa2 tkip ones
- bluetooth (the same combo module exposes hci0)
- the otg port as a normal usb-c data port; the power port charges
- battery percentage and an honest charging state

## known limits

- 2.4 ghz only. no 5 ghz networks, by hardware
- wpa3-only and wpa2/wpa3 transition networks that require pmf do not connect:
  the `rtl8xxxu` driver has no sae or management frame protection support. a
  wpa2-only access point (or forcing a wpa2 psk config) works
- weak links: the module's antenna is small. the ap can drop the client with
  "missing acks" below roughly -75 dbm; keeping wifi power save off helps

## evidence

the port was validated against the stock firmware's own kernel log from the same
unit (retrooz, kernel 4.4.189), which shows the same hub and dongle enumerating
on dwc2 in ~10 s, and against live adb sessions on android 11 where unplugging
the otg cable dropped the hub in under 3 s and plugging it back restored it. the
pmic behaviour above was measured with raw i2c reads on the running console.
