#!/usr/bin/env bash
set -euo pipefail
usb_revision=cef48e29a57974d4e5b09369a14df55d2fc0f9b3
usb_dir=build/usb-source
if [ ! -d "$usb_dir/.git" ]; then
  git clone --no-checkout https://github.com/magnusja/libaums.git "$usb_dir"
fi
git -C "$usb_dir" fetch --depth 1 origin "$usb_revision"
git -C "$usb_dir" checkout --detach "$usb_revision"
git -C "$usb_dir" submodule update --init --depth 1 vendor/libusb

# JNI release modes are integers. Older libaums used NULL, rejected by NDK 26.
python3 - <<'PYTHON'
from pathlib import Path
p = Path('build/usb-source/libusbcommunication/src/c/usb.c')
s = p.read_text().replace('body, NULL)', 'body, 0)').replace('c_data, NULL)', 'c_data, 0)')
p.write_text(s)
PYTHON

# Keep the retry buffer at its original bounds and reject stalled data phases.
# Apply to the pinned source on every fetch, including an already patched tree.
python3 - <<'PYTHON'
from pathlib import Path

p = Path('build/usb-source/libaums/src/main/java/me/jahnen/libaums/core/driver/scsi/ScsiBlockDevice.kt')
s = p.read_text()

def patch(old, new):
    global s
    if s.count(old) == 1:
        s = s.replace(old, new, 1)
    elif s.count(old) != 0 or s.count(new) != 1:
        raise SystemExit('Pinned ScsiBlockDevice.kt no longer matches the USB safety patch')

patch('''    private fun transferCommand(command: CommandBlockWrapper, inBuffer: ByteBuffer) {
        var lastException: Exception? = null
        for(i in 0..MAX_RECOVERY_ATTEMPTS) {
            try {
                val result = transferOneCommand(command, inBuffer)''',
'''    private fun transferCommand(command: CommandBlockWrapper, inBuffer: ByteBuffer) {
        val originalPosition = inBuffer.position()
        val originalLimit = inBuffer.limit()
        var lastException: Exception? = null
        for(i in 0..MAX_RECOVERY_ATTEMPTS) {
            try {
                // A failed data phase or CSW may have advanced the buffer.
                inBuffer.limit(originalLimit)
                inBuffer.position(originalPosition)
                val result = transferOneCommand(command, inBuffer)''')

patch('''                    read += usbCommunication.bulkInTransfer(inBuffer)''',
'''                    val transferred = usbCommunication.bulkInTransfer(inBuffer)
                    if (transferred <= 0) throw IOException("Short SCSI data transfer")
                    read += transferred''')

patch('''                    written += usbCommunication.bulkOutTransfer(inBuffer)''',
'''                    val transferred = usbCommunication.bulkOutTransfer(inBuffer)
                    if (transferred <= 0) throw IOException("Short SCSI data transfer")
                    written += transferred''')

p.write_text(s)
PYTHON
