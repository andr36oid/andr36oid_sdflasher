package io.github.andr36oid.sdflasher;

import android.content.Context;
import android.content.pm.PackageManager;
import android.hardware.usb.*;
import io.github.andr36oid.bindings.mobile.Disk;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Callable;
import me.jahnen.libaums.core.driver.BlockDeviceDriver;
import me.jahnen.libaums.core.driver.BlockDeviceDriverFactory;
import me.jahnen.libaums.core.driver.scsi.commands.sense.MediaNotInserted;
import me.jahnen.libaums.core.usb.UsbCommunication;
import me.jahnen.libaums.libusbcommunication.LibusbCommunication;

final class UsbCard {
  /** Where the desktop flashers live: the proven path when a phone can't do USB. */
  static final String DESKTOP_DOWNLOAD = "https://github.com/andr36oid/andr36oid-sdflasher/releases";

  final UsbDevice device;
  final UsbInterface iface;
  final UsbEndpoint input, output;
  final int lun;
  final long bytes;

  UsbCard(UsbDevice d, UsbInterface i, UsbEndpoint in, UsbEndpoint out, int l, long size) {
    device = d;
    iface = i;
    input = in;
    output = out;
    lun = l;
    bytes = size;
  }

  String label() {
    String name = device.getProductName();
    if (name == null || name.trim().isEmpty())
      name = "USB " + device.getVendorId() + ":" + device.getProductId();
    return name
        + " · "
        + device.getDeviceName()
        + (lun >= 0 ? " · LUN " + lun : "")
        + (bytes > 0 ? String.format(java.util.Locale.ROOT, " · %.1f GB", bytes / 1e9) : "");
  }

  /**
   * A failure of the USB path explained in plain language, with the raw cause kept for the
   * "Technical details" part of the error dialog. Every message recommends the desktop
   * flasher, because that is the proven way when a phone or reader won't cooperate.
   */
  static final class ReaderException extends IOException {
    ReaderException(Context context, String key, Throwable cause) {
      super(
          I18n.text(context, key, DESKTOP_DOWNLOAD)
              + (cause == null
                  ? ""
                  : "\n\n"
                      + I18n.text(context, "Technical details")
                      + ": "
                      + (cause.getMessage() == null ? cause.toString() : cause.getMessage())),
          cause);
    }
  }

  /** Whether this device can talk to USB accessories at all (USB host / OTG). */
  static boolean hostSupported(Context context) {
    return context.getPackageManager().hasSystemFeature(PackageManager.FEATURE_USB_HOST)
        && context.getSystemService(Context.USB_SERVICE) != null;
  }

  /** Plain-language reason why no card reader is listed right now. */
  static String noReaderReason(Context context) {
    if (!hostSupported(context))
      return I18n.text(
          context,
          "This phone or tablet does not support USB host mode (OTG), so no app on it can talk to"
              + " a card reader. Please use the Windows or Linux flasher instead: it is the"
              + " proven way to write the card. Download it from %s",
          DESKTOP_DOWNLOAD);
    UsbManager manager = (UsbManager) context.getSystemService(Context.USB_SERVICE);
    java.util.Collection<UsbDevice> devices = manager.getDeviceList().values();
    if (devices.isEmpty())
      return I18n.text(
          context,
          "Nothing is connected over USB. Plug the card reader straight into the phone or"
              + " tablet, with an OTG adapter if needed. Some devices have an OTG switch in"
              + " Settings that must be on. USB hubs are not supported.");
    StringBuilder names = new StringBuilder();
    for (UsbDevice d : devices) {
      String name = d.getProductName();
      if (name == null || name.trim().isEmpty())
        name = "USB " + d.getVendorId() + ":" + d.getProductId();
      if (names.length() > 0) names.append(", ");
      names.append(name.trim());
    }
    return I18n.text(
        context,
        "A USB device is connected (%s), but it is not a card reader this app can use: it does not"
            + " offer the standard USB mass storage interface. Try another reader, or use the"
            + " Windows or Linux flasher: %s",
        names.toString(),
        DESKTOP_DOWNLOAD);
  }

  static List<UsbCard> readers(Context context) {
    List<UsbCard> list = new ArrayList<>();
    if (!hostSupported(context)) return list;
    UsbManager manager = (UsbManager) context.getSystemService(Context.USB_SERVICE);
    for (UsbDevice device : manager.getDeviceList().values()) {
      for (int n = 0; n < device.getInterfaceCount(); n++) {
        UsbInterface iface = device.getInterface(n);
        if (iface.getInterfaceClass() != 8
            || iface.getInterfaceSubclass() != 6
            || iface.getInterfaceProtocol() != 80) continue;
        UsbEndpoint in = null, out = null;
        for (int k = 0; k < iface.getEndpointCount(); k++) {
          UsbEndpoint ep = iface.getEndpoint(k);
          if (ep.getType() != UsbConstants.USB_ENDPOINT_XFER_BULK) continue;
          if (ep.getDirection() == UsbConstants.USB_DIR_IN) in = ep;
          else out = ep;
        }
        if (in != null && out != null) list.add(new UsbCard(device, iface, in, out, -1, 0));
      }
    }
    return list;
  }

  UsbCommunication connect(Context context) throws IOException {
    UsbManager manager = (UsbManager) context.getSystemService(Context.USB_SERVICE);
    UsbDevice current = manager.getDeviceList().get(device.getDeviceName());
    if (current == null || current.getDeviceId() != device.getDeviceId())
      throw new ReaderException(
          context,
          "The card reader is no longer connected. Plug it in again and select the card once"
              + " more.",
          null);
    if (!manager.hasPermission(current))
      throw new ReaderException(
          context,
          "Android has withdrawn the app's permission to use the card reader. Select the card"
              + " again and allow access when asked.",
          null);
    try {
      return new LibusbCommunication(manager, current, iface, output, input);
    } catch (Exception e) {
      // openDevice returned null, libusb could not wrap the descriptor, or the
      // interface could not be claimed (a system driver or another app holds it).
      // Kotlin declares no checked exceptions, so this must be Exception, not IOException.
      throw new ReaderException(
          context,
          "Android did not let the app take over the card reader. Another app or a system"
              + " driver may be using it. Unplug the reader, plug it in again and try once more."
              + " If it keeps failing, use the Windows or Linux flasher: %s",
          e);
    }
  }

  private static BlockDeviceDriver initDriver(Context context, UsbCommunication usb, int lun)
      throws IOException {
    BlockDeviceDriver driver = BlockDeviceDriverFactory.INSTANCE.createBlockDevice(usb, (byte) lun);
    try {
      driver.init();
    } catch (MediaNotInserted empty) {
      throw empty;
    } catch (IOException | RuntimeException e) {
      throw new ReaderException(
          context,
          "The card reader does not answer the way the app expects, so it cannot be used from"
              + " this device. Try another reader, or use the Windows or Linux flasher: %s",
          e);
    }
    return driver;
  }

  List<UsbCard> discover(Context context) throws IOException {
    try (UsbCommunication usb = connect(context)) {
      byte[] max = new byte[1];
      // A stalled GET_MAX_LUN means a single logical unit in the BOT specification.
      try {
        if (usb.controlTransfer(0xa1, 0xfe, 0, iface.getId(), max, 1) != 1) max[0] = 0;
      } catch (IOException e) {
        max[0] = 0;
      }
      int count = (max[0] & 255);
      if (count > 15) throw new IOException("Invalid USB logical unit count");
      List<UsbCard> cards = new ArrayList<>();
      for (int l = 0; l <= count; l++) {
        BlockDeviceDriver driver;
        try {
          driver = initDriver(context, usb, l);
        } catch (MediaNotInserted empty) {
          continue;
        }
        if (driver.getBlockSize() != 512 || driver.getBlocks() == -1L) continue;
        long size = capacityBytes(driver);
        if (size >= 1L << 30) cards.add(new UsbCard(device, iface, input, output, l, size));
      }
      return cards;
    }
  }

  static long capacityBytes(BlockDeviceDriver driver) throws IOException {
    // This pinned libaums version exposes READ CAPACITY(10)'s last LBA,
    // including its signed Java representation, rather than a block count.
    long last = driver.getBlocks() & 0xffffffffL;
    if (driver.getBlockSize() != 512 || last == 0xffffffffL)
      throw new IOException("Unsupported logical sector size or READ CAPACITY(16) device");
    return (last + 1) * 512L;
  }

  Connection open(Context context) throws Exception {
    UsbCommunication usb = connect(context);
    try {
      BlockDeviceDriver driver = initDriver(context, usb, lun);
      if (driver.getBlockSize() != 512 || capacityBytes(driver) != bytes)
        throw new IOException("Card capacity changed; select it again");
      String serial = device.getSerialNumber();
      String id =
          device.getVendorId()
              + ":"
              + device.getProductId()
              + ":"
              + (serial == null ? "" : serial)
              + ":"
              + iface.getId()
              + ":"
              + lun;
      Connection connection = new Connection(usb, driver, bytes, id, lun);
      connection.stallMessage =
          I18n.text(
              context,
              "The card reader stopped responding and the operation could not finish. Unplug the"
                  + " reader, plug it in again and try once more, or use the Windows or Linux"
                  + " flasher: %s",
              DESKTOP_DOWNLOAD);
      connection.flush();
      return connection;
    } catch (Exception e) {
      usb.close();
      throw e;
    }
  }

  /** Told when a USB call has been waiting for seconds (0: it came back). */
  interface StallListener {
    void stalled(int seconds);
  }

  static final class Connection implements Disk, AutoCloseable {
    /**
     * How long a single reader call may take before the app calls it stalled. libaums
     * bounds each bulk transfer to 5 s but retries and recovers several times, and a
     * wedged reader can make libusb wait on a cancelled transfer indefinitely, so one
     * read or write of at most 4 MiB has no upper bound on its own. Nothing can safely
     * interrupt the native call; the stall is reported so the app can tell the user,
     * who unplugs the reader, which does end the call. Package-private for tests.
     */
    static volatile long stallAfterMs = 30_000;

    static volatile StallListener stallListener;

    private final UsbCommunication usb;
    private final BlockDeviceDriver driver;
    private final long bytes;
    private final String id;
    private final int lun;
    private volatile boolean closed = false;
    private boolean flushSupported = true;
    private int tag = 0x53444600;
    /** Translated text for a call that stalled; set by open(), English otherwise. */
    volatile String stallMessage;
    private volatile long busySince; // 0 while no reader call is in flight
    private volatile boolean stalledNow;
    private final Thread watchdog;

    Connection(UsbCommunication u, BlockDeviceDriver d, long size, String identity, int unit) {
      usb = u;
      driver = d;
      bytes = size;
      id = identity;
      lun = unit;
      watchdog = new Thread(this::watch, "usb-watchdog");
      watchdog.setDaemon(true);
      watchdog.start();
    }

    private void watch() {
      int reported = 0;
      while (!closed) {
        try {
          Thread.sleep(Math.max(50, Math.min(250, stallAfterMs / 4)));
        } catch (InterruptedException e) {
          return;
        }
        long since = busySince;
        long waited = since == 0 ? 0 : System.currentTimeMillis() - since;
        StallListener listener = stallListener;
        if (waited >= stallAfterMs) {
          int seconds = (int) (waited / 1000);
          stalledNow = true;
          if (seconds != reported && listener != null) listener.stalled(Math.max(1, seconds));
          reported = seconds;
        } else if (reported != 0) {
          reported = 0;
          if (listener != null) listener.stalled(0);
        }
      }
    }

    /** Runs one reader call under the watchdog; a call that stalled gets a clear error. */
    private <T> T guarded(Callable<T> op) throws Exception {
      stalledNow = false;
      busySince = System.currentTimeMillis();
      try {
        return op.call();
      } catch (IOException e) {
        if (stalledNow) {
          String text = stallMessage;
          if (text == null)
            text =
                "The card reader stopped responding and the operation could not finish. Unplug"
                    + " the reader, plug it in again and try once more, or use the Windows or"
                    + " Linux flasher: "
                    + DESKTOP_DOWNLOAD;
          throw new IOException(text + "\n" + e.getMessage(), e);
        }
        throw e;
      } finally {
        busySince = 0;
      }
    }

    @Override
    public long capacity() {
      return bytes;
    }

    @Override
    public String identity() {
      return id;
    }

    private void range(long off, long length) throws IOException {
      if (closed
          || off < 0
          || length < 0
          || off > bytes
          || length > bytes - off
          || off % 512 != 0
          || length % 512 != 0
          || length > 4L << 20) throw new IOException("Invalid USB transfer range");
    }

    @Override
    public synchronized byte[] read(long off, long length) throws Exception {
      range(off, length);
      byte[] result = new byte[(int) length];
      guarded(
          () -> {
            driver.read(off / 512, ByteBuffer.wrap(result));
            return null;
          });
      return result;
    }

    @Override
    public synchronized void write(long off, byte[] data) throws Exception {
      range(off, data.length);
      guarded(
          () -> {
            driver.write(off / 512, ByteBuffer.wrap(data));
            return null;
          });
    }

    private int command(byte[] cdb, byte[] data) throws IOException {
      ByteBuffer cbw = ByteBuffer.allocate(31).order(ByteOrder.LITTLE_ENDIAN);
      int current = ++tag;
      cbw.putInt(0x43425355)
          .putInt(current)
          .putInt(data.length)
          .put((byte) (data.length > 0 ? 0x80 : 0))
          .put((byte) lun)
          .put((byte) cdb.length)
          .put(cdb);
      cbw.rewind();
      if (usb.bulkOutTransfer(cbw) != 31) throw new IOException("Short SCSI command transfer");
      if (data.length > 0) {
        ByteBuffer buffer = ByteBuffer.wrap(data);
        while (buffer.hasRemaining())
          if (usb.bulkInTransfer(buffer) <= 0) throw new IOException("Short SCSI data transfer");
      }
      ByteBuffer csw = ByteBuffer.allocate(13).order(ByteOrder.LITTLE_ENDIAN);
      if (usb.bulkInTransfer(csw) != 13) throw new IOException("Short SCSI status transfer");
      csw.rewind();
      if (csw.getInt() != 0x53425355 || csw.getInt() != current || csw.getInt() != 0)
        throw new IOException("Invalid SCSI command status");
      return (csw.get() & 255);
    }

    @Override
    public synchronized void flush() throws Exception {
      if (closed) throw new IOException("USB connection closed");
      if (!flushSupported) return;
      guarded(
          () -> {
            byte[] cdb = new byte[10];
            cdb[0] = 0x35;
            int status = command(cdb, new byte[0]);
            if (status == 0) return null;
            if (status == 1) {
              byte[] sense = new byte[18];
              if (command(new byte[] {3, 0, 0, 0, 18, 0}, sense) == 0
                  && (sense[2] & 15) == 5
                  && (sense[12] == 0x20 || sense[12] == 0x24)) {
                // Some flash readers do not implement cache commands. All writes are
                // synchronous USB commands and read-back still bypasses host caches.
                flushSupported = false;
                return null;
              }
            }
            throw new IOException("The card could not flush its write cache");
          });
    }

    @Override
    public synchronized void close() throws IOException {
      if (!closed) {
        closed = true;
        watchdog.interrupt();
        usb.close();
      }
    }
  }
}
