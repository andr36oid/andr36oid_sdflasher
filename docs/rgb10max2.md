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
- the charger's dc detect (gpio0 pb3) stays exactly as in the base tree. it is
  what lets the charger enable the charge path, and stock leaves it alone
- the dwc2 otg controller runs `dr_mode = "host"` with the phy port **disabled**
  (see the wifi section for why). the otg connector still works as a plain
  usb-c host port for devices; android's usb *device* modes need the phy port
  enabled and are outside this configuration

build it exactly like the other andr36oid device trees:

```sh
cpp -nostdinc -I include -I arch/arm64/boot/dts/rockchip -undef -D__DTS__ \
    -x assembler-with-cpp rk3326-rgb10max2-android.dts -o out.dts
dtc -@ -I dts -O dtb -o rk3326-r36s-android.dtb out.dts
```

the `-undef` matters: the kernel's `linux` builtin macro mangles `linux,code`.

## wifi power: the rk817 latch, and why the phy port stays disabled

the internal hub and the wifi dongle sit on the dwc2 bus, and their 5 v is the
otg port's vbus net, fed by the rk817 otg rail. on this board the rail behaves
as a pmic-side latch: kernel writes to the otg enable register (charger,
regulator, phy vbus supply) never reach it - the kernel register cache reads
back "on" (`reg_b4=47 otg5v=1`) while the hardware register reads `0x07` and no
power appears. a raw i2c write of `0xB4 = 0x47` from linux flips it, the hub
powers from the battery and the wifi module comes up. the latch survives warm
reboots but resets on a cold power-off, so the boot service writes it on every
cold boot.

the stock firmware ships the same topology: dwc2 runs phyless with the otg **phy
port disabled**. that one detail is load-bearing twice:

- dwc2: keeping `phys = <&u2phy_otg>` while the port is disabled makes the phy
  lookup defer until the u2phy driver is up (utmi clock on) and then return
  -ENODEV, so dwc2 probes phyless with the clock running. removing the `phys`
  property outright instead makes dwc2 probe before that clock exists and its
  core soft reset hangs with "GRSTCTL_CSFTRST timeout"
- charger: if the phy port is enabled it fires EXTCON_USB_VBUS_EN, the charger
  sets `otg_in` and then **disables its own plug/plug-out interrupts**. a
  charger plugged in afterwards is never detected and charging stops until
  reboot. with the phy port disabled the charger never hears about otg, keeps
  its plug detection, and charges normally

result, verified on hardware: charging and wifi run together. with a charger in
the power port its 5 v feeds the shared net, so the hub enumerates before the
latch is even needed; on battery the boot service arms the latch. a 30 s
watchdog re-arms the latch whenever the hub drops.

`docs/rgb10max2/service/` does this at boot, in this order:

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
- wifi from battery on every cold boot (latch written by the boot service),
  wpa2-psk networks including mixed wpa/wpa2 tkip ones
- bluetooth (the same combo module exposes hci0)
- **charging and wifi at the same time** - stock parity (verified live:
  battery charging at +869 mA with wlan0 connected)
- the otg port as a plain usb-c host port; the power port charges
- battery percentage

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
