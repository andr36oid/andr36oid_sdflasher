package io.github.andr36oid.sdflasher;

import static org.junit.Assert.*;

import android.hardware.usb.*;
import java.io.IOException;
import java.nio.*;
import me.jahnen.libaums.core.driver.BlockDeviceDriver;
import me.jahnen.libaums.core.usb.UsbCommunication;
import org.junit.Test;

public class UsbCardTest {
  static class Driver implements BlockDeviceDriver {
    long last = (1L << 30) / 512 - 1, offset = -1;
    int writes = 0;

    public int getBlockSize() {
      return 512;
    }

    public long getBlocks() {
      return last;
    }

    public void init() {}

    public void read(long lba, ByteBuffer b) throws IOException {
      offset = lba;
      while (b.hasRemaining()) b.put((byte) 0x5a);
    }

    public void write(long lba, ByteBuffer b) throws IOException {
      offset = lba;
      writes++;
      b.position(b.limit());
    }
  }

  static class Transport implements UsbCommunication {
    int tag, command, commands;
    boolean shortStatus, badTag, unsupported, mediaError, sense;

    public UsbEndpoint getInEndpoint() {
      return null;
    }

    public UsbEndpoint getOutEndpoint() {
      return null;
    }

    public UsbInterface getUsbInterface() {
      return null;
    }

    public int bulkOutTransfer(ByteBuffer b) {
      b.order(ByteOrder.LITTLE_ENDIAN);
      assertEquals(31, b.remaining());
      assertEquals(0x43425355, b.getInt());
      tag = b.getInt();
      b.position(15);
      command = b.get() & 255;
      commands++;
      sense = command == 3;
      b.position(31);
      return 31;
    }

    public int bulkInTransfer(ByteBuffer b) {
      if (sense) {
        byte[] data = new byte[18];
        data[0] = 0x70;
        data[2] = (byte) (mediaError ? 3 : 5);
        data[7] = 10;
        data[12] = 0x20;
        b.put(data);
        sense = false;
        return 18;
      }
      if (shortStatus) return 0;
      b.order(ByteOrder.LITTLE_ENDIAN)
          .putInt(0x53425355)
          .putInt(badTag ? tag + 1 : tag)
          .putInt(0)
          .put((byte) (command == 0x35 && (unsupported || mediaError) ? 1 : 0));
      return 13;
    }

    public int controlTransfer(int a, int b, int c, int d, byte[] e, int f) {
      return 0;
    }

    public void resetDevice() {}

    public void clearFeatureHalt(UsbEndpoint e) {}

    public void close() {}
  }

  @Test
  public void capacityIncludesFinalSector() throws Exception {
    Driver d = new Driver();
    assertEquals(1L << 30, UsbCard.capacityBytes(d));
    d.last = (int) 0x80000000L;
    assertEquals(0x80000001L * 512, UsbCard.capacityBytes(d));
    d.last = -1;
    assertThrows(IOException.class, () -> UsbCard.capacityBytes(d));
  }

  @Test
  public void writesUseSectorOffsetsAndRejectInvalidRanges() throws Exception {
    Driver d = new Driver();
    UsbCard.Connection c = new UsbCard.Connection(new Transport(), d, 1L << 30, "test", 0);
    c.write(512, new byte[512]);
    assertEquals(1, d.offset);
    assertArrayEquals(new byte[] {0x5a}, new byte[] {c.read(1024, 512)[0]});
    assertEquals(2, d.offset);
    assertThrows(IOException.class, () -> c.write(1, new byte[512]));
    assertThrows(IOException.class, () -> c.write(1L << 30, new byte[512]));
    assertEquals(1, d.writes);
    c.close();
    assertThrows(IOException.class, () -> c.read(0, 512));
  }

  /** A reader call that takes too long is reported as a stall, then cleared when it returns. */
  @Test
  public void watchdogReportsStalledReaderCallsAndClearsThem() throws Exception {
    long saved = UsbCard.Connection.stallAfterMs;
    UsbCard.Connection.stallAfterMs = 300;
    java.util.concurrent.CountDownLatch stalled = new java.util.concurrent.CountDownLatch(1);
    java.util.concurrent.CountDownLatch cleared = new java.util.concurrent.CountDownLatch(1);
    java.util.concurrent.atomic.AtomicInteger seconds = new java.util.concurrent.atomic.AtomicInteger(-1);
    UsbCard.Connection.stallListener =
        s -> {
          if (s > 0) {
            seconds.set(s);
            stalled.countDown();
          } else if (stalled.getCount() == 0) cleared.countDown();
        };
    Driver slow =
        new Driver() {
          @Override
          public void read(long lba, ByteBuffer b) throws IOException {
            try {
              Thread.sleep(1200);
            } catch (InterruptedException ignored) {
            }
            super.read(lba, b);
          }
        };
    UsbCard.Connection c = new UsbCard.Connection(new Transport(), slow, 1L << 30, "test", 0);
    try {
      long start = System.currentTimeMillis();
      c.read(0, 512);
      assertTrue(System.currentTimeMillis() - start >= 1200);
      assertTrue("stall was reported", stalled.await(0, java.util.concurrent.TimeUnit.SECONDS));
      assertTrue(seconds.get() >= 1);
      assertTrue(
          "stall was cleared after the call returned",
          cleared.await(2, java.util.concurrent.TimeUnit.SECONDS));
      // a quick call after the stall is not reported as stalled
      c.read(512, 512);
    } finally {
      c.close();
      UsbCard.Connection.stallListener = null;
      UsbCard.Connection.stallAfterMs = saved;
    }
  }

  /** A call that fails after stalling gets the plain-language stall text first. */
  @Test
  public void stalledFailureExplainsItself() throws Exception {
    long saved = UsbCard.Connection.stallAfterMs;
    UsbCard.Connection.stallAfterMs = 200;
    Driver broken =
        new Driver() {
          @Override
          public void write(long lba, ByteBuffer b) throws IOException {
            try {
              Thread.sleep(700);
            } catch (InterruptedException ignored) {
            }
            throw new IOException("MAX_RECOVERY_ATTEMPTS Exceeded");
          }
        };
    UsbCard.Connection c = new UsbCard.Connection(new Transport(), broken, 1L << 30, "test", 0);
    c.stallMessage = "stopped responding";
    try {
      c.write(0, new byte[512]);
      fail("write succeeded");
    } catch (IOException e) {
      assertTrue(e.getMessage(), e.getMessage().startsWith("stopped responding"));
      assertTrue(e.getMessage().contains("MAX_RECOVERY_ATTEMPTS"));
    } finally {
      c.close();
      UsbCard.Connection.stallAfterMs = saved;
    }
    // a failure without a stall keeps its own message
    Driver quick =
        new Driver() {
          @Override
          public void write(long lba, ByteBuffer b) throws IOException {
            throw new IOException("plain failure");
          }
        };
    c = new UsbCard.Connection(new Transport(), quick, 1L << 30, "test", 0);
    c.stallMessage = "stopped responding";
    try {
      c.write(0, new byte[512]);
      fail("write succeeded");
    } catch (IOException e) {
      assertEquals("plain failure", e.getMessage());
    } finally {
      c.close();
    }
  }

  @Test
  public void flushValidatesStatusAndOnlyIgnoresUnsupportedCommands() throws Exception {
    Driver d = new Driver();
    Transport u = new Transport();
    UsbCard.Connection c = new UsbCard.Connection(u, d, 1L << 30, "test", 0);
    c.flush();
    assertEquals(0x35, u.command);
    u.badTag = true;
    assertThrows(IOException.class, c::flush);
    u.badTag = false;
    u.shortStatus = true;
    assertThrows(IOException.class, c::flush);
    u.shortStatus = false;
    u.mediaError = true;
    assertThrows(IOException.class, c::flush);
    u.mediaError = false;
    u.unsupported = true;
    c.flush();
    int count = u.commands;
    c.flush();
    assertEquals(count, u.commands);
  }
}
