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

## windows without an OpenGL driver
The Windows zip ships `opengl32.dll`, Mesa's llvmpipe software renderer, beside the exe. Keep the two together: it is what lets the window open on a fresh Windows 11 install with only the Microsoft Basic Display Adapter, in a VM, or over Remote Desktop, where Windows has no OpenGL at all. Rendering happens on the CPU, which is plenty for this UI. Delete the DLL if you would rather use your GPU driver's OpenGL. The DLL is third-party (Mesa, MIT licence; LLVM, Apache-2.0 with LLVM Exceptions), see `third_party/mesa/` and the `licenses/` folder in the zip.

## text mode (no graphics needed at all)
There is a full text-mode flasher that needs no GPU, no graphics driver and no OpenGL. It is the same program: it drives the same verified-write code as the window, with prompts instead of buttons. It is useful on a bare Windows install, in a VM, over Remote Desktop, or on a plain SSH session on Linux and macOS.

You never have to reach for it by hand. If the window cannot open, the flasher explains why in plain language and continues in text mode automatically. On the GUI build of Windows it attaches (or opens) a console first, so you see the prompts even though the app is a windowed program.

To start text mode yourself, run the exe with `-cli` from Command Prompt or PowerShell:

```
andr36oid-sdflasher.exe -cli
```

On Linux and macOS it is the same flag: `./andr36oid-sdflasher -cli`. You can also set the environment variable `A36_UI=cli`.

Every job the window offers is reachable in text mode with `-action`:

- `-action flash` (the default): set up a new card, or update/repartition an existing install in place. This is the same operation that makes this tool the patcher. It walks through choosing the image, the console profile, install or update, then writes and verifies.
- `-action hotfix`: apply hotfixes to an existing card. Reads the card's exact build, lists the matching hotfixes (bug fixes selected by default, extras not), and copies the chosen ones to `BOOT/hotfix` without reflashing.
- `-action kernel`: check for and install a kernel update into `BOOT`, keeping apps, settings, saves and games in place.
- `-action restore`: roll an interrupted update back from the `recovery.json` it left behind.
- `-action list`: just print the drives the flasher sees, with the path to pass to `-drive`.

Handy flags (see `andr36oid-sdflasher.exe -cli -h` for all): `-image <path>` or `-download` to pick the release; `-drive <path>` to preselect the card; `-profile <id or name>` to preselect the console; `-mode install|update`; `-no-roms` and `-userdata <GiB>` for the install storage layout; `-recovery <path>` for a restore; and `-y` to skip confirmations where it is safe (never for a non-removable disk). A non-removable disk always asks for its path to be typed, even with `-y`.

Progress is printed as a percentage for the long steps (verify, download, write, readback). The write is verified by reading the card back, exactly as the window does.

## thx
to the Fyne Framework project for Go for multiplatform GUI

to EtchDroid for the logic to flash USB devices on unrooted phones
