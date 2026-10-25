# what this
andr36oid now uses a Project Treble partition layout for the SD card. New users can flash with a tool like Rufus (losing the ability to set how big EASYROMS should be), but existing SD cards need to be repartitioned.

An ordinary flash would render the system unbootable or with data loss. This tool can alter the partition table of an existing install of andr36oid to the new format, as well as flesh any other SD card with our image.

The new partitioning layout enables two things: Project Treble compatibility (and thereby GSIs), Android versions greater than 11 (not implemented or tested to date), and noticeably faster bootup time.

## what is it not
an effin web browser pretending to be an app (looking at you etcher)

faster than rufus. this app will be slower than rufus. it validates what it writes as it writes to your card. this is because many cards, frankly, suck. this has led to many an inquiry in our telegram chat, and a better sd card usually solved the problem. I hope that readback verification will catch unsuitable SD cards before you put them in your console and be disappointed by the OS not booting.

## how use
Go to GitHub releases, and download the binary for your platform. You can use Windows, Linux, macOS and Android.

You do not need to download the andr36oid image. The flash tool will download it from GitHub for you (you can manually specify one if required).

To update to future releases of andr36oid, simply run the flash tool again.

Use **Apply hotfixes** to choose an existing card and copy fixes without reflashing. The flasher also offers hotfixes after a successful flash; you can skip that step. Bug fixes start checked, optional extras start unchecked, and you can change any selection. Downloads are checked for size and SHA-256 before they are copied to `BOOT/hotfix/`. The console applies them on its next boot.

Hotfixes must match the card's exact build. The card needs `BOOT/andr36oid-build.txt`, which this flasher preserves when flashing a recent release. Older cards without that file cannot be offered hotfixes. Checking for hotfixes needs an internet connection; a failed check does not prevent flashing.

## thx
to the Fyne Framework project for Go for multiplatform GUI
to EtchDroid for the logic to flash USB devices on unrooted phones
