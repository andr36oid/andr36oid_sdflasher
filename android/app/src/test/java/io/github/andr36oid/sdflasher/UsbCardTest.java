package io.github.andr36oid.sdflasher;

import static org.junit.Assert.*;

import android.hardware.usb.*;
import java.io.IOException;
import java.nio.*;
import me.jahnen.libaums.core.driver.BlockDeviceDriver;
import me.jahnen.libaums.core.usb.PipeException;
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
    // stalls of the reader, for SYNCHRONIZE CACHE only: the CBW, the sense data
    // phase, or this many status reads in a row
    boolean stallCommand, stallData, noSense;
    int stallStatus, statusStalls, residue, resets, halts;

    public UsbEndpoint getInEndpoint() {
      return null;
    }

    public UsbEndpoint getOutEndpoint() {
      return null;
    }

    public UsbInterface getUsbInterface() {
      return null;
    }

    public int bulkOutTransfer(ByteBuffer b) throws IOException {
      b.order(ByteOrder.LITTLE_ENDIAN);
      assertEquals(31, b.remaining());
      assertEquals(0x43425355, b.getInt());
      tag = b.getInt();
      b.position(15);
      command = b.get() & 255;
      commands++;
      sense = command == 3;
      statusStalls = 0;
      b.position(31);
      if (stallCommand && command == 0x35) throw new PipeException();
      return 31;
    }

    public int bulkInTransfer(ByteBuffer b) throws IOException {
      if (sense && stallData) {
        sense = false;
        throw new PipeException();
      }
      if (sense) {
        byte[] data = new byte[18];
        data[0] = 0x70;
        data[2] = (byte) (mediaError ? 3 : noSense ? 0 : 5);
        data[7] = 10;
        data[12] = 0x20;
        b.put(data);
        sense = false;
        return 18;
      }
      if (shortStatus) return 0;
      if (command == 0x35 && statusStalls < stallStatus) {
        statusStalls++;
        throw new PipeException();
      }
      boolean failed = (command == 0x35 && (unsupported || mediaError)) || (command == 3 && stallData);
      b.order(ByteOrder.LITTLE_ENDIAN)
          .putInt(0x53425355)
          .putInt(badTag ? tag + 1 : tag)
          .putInt(failed && command == 3 ? residue : 0)
          .put((byte) (failed ? 1 : 0));
      return 13;
    }

    public int controlTransfer(int a, int b, int c, int d, byte[] e, int f) {
      assertEquals(0x21, a);
      assertEquals(0xff, b);
      resets++;
      return 0;
    }

    public void resetDevice() {}

    public void clearFeatureHalt(UsbEndpoint e) {
      halts++;
    }

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
  /** A reader that takes the cache command works as before: sent every time, no recovery. */
  @Test
  public void acceptedFlushIsSentEveryTime() throws Exception {
    Transport u = new Transport();
    UsbCard.Connection c = new UsbCard.Connection(u, new Driver(), 1L << 30, "test", 0);
    c.flush();
    c.flush();
    assertEquals(2, u.commands);
    assertEquals(0x35, u.command);
    assertEquals(0, u.halts);
    assertEquals(0, u.resets);
    c.close();
  }

  /** Stalled sense data, then a valid failed CSW with a residue: the status is used. */
  @Test
  public void stalledDataPhaseReadsTheStatus() throws Exception {
    Transport u = new Transport();
    UsbCard.Connection c = new UsbCard.Connection(u, new Driver(), 1L << 30, "test", 0);
    u.unsupported = true;
    u.stallData = true;
    u.residue = 18;
    IOException e = assertThrows(IOException.class, c::flush);
    assertEquals("The card could not flush its write cache", e.getMessage());
    assertEquals(1, u.halts);
    assertEquals(0, u.resets);
    // the reader is still usable: the next flush gets its sense data
    u.stallData = false;
    c.flush();
    int count = u.commands;
    c.flush();
    assertEquals(count, u.commands);
    c.close();
  }

  /** A stalled status phase is cleared and read again (BOT 5.3.3). */
  @Test
  public void stalledStatusIsReadAgain() throws Exception {
    Transport u = new Transport();
    UsbCard.Connection c = new UsbCard.Connection(u, new Driver(), 1L << 30, "test", 0);
    u.stallStatus = 1;
    c.flush();
    assertEquals(1, u.halts);
    assertEquals(0, u.resets);
    c.flush(); // passed: still supported, sent again
    assertEquals(2, u.commands);
    // stalled and failed with a medium error: still a failure
    u.mediaError = true;
    assertThrows(IOException.class, c::flush);
    // failed with no clear sense: an error without a stall, a refusal with one
    u.mediaError = false;
    u.unsupported = true;
    u.noSense = true;
    u.stallStatus = 0;
    assertThrows(IOException.class, c::flush);
    u.stallStatus = 1;
    c.flush();
    int count = u.commands;
    c.flush();
    assertEquals(count, u.commands);
    c.close();
  }

  /** Two stalled status reads, or a stalled CBW: reset recovery, then the command is skipped. */
  @Test
  public void repeatedStallResetsTheReader() throws Exception {
    Transport u = new Transport();
    UsbCard.Connection c = new UsbCard.Connection(u, new Driver(), 1L << 30, "test", 0);
    u.stallStatus = 2;
    c.flush();
    assertEquals(1, u.resets);
    assertEquals(3, u.halts); // bulk-in after the first stall, then both by the reset
    int count = u.commands;
    c.flush();
    assertEquals(count, u.commands);
    c.close();

    Transport v = new Transport();
    c = new UsbCard.Connection(v, new Driver(), 1L << 30, "test", 0);
    v.stallCommand = true;
    c.flush();
    assertEquals(1, v.resets);
    assertEquals(2, v.halts);
    count = v.commands;
    c.flush();
    assertEquals(count, v.commands);
    c.close();
  }

  /** A card behind a reader that stalls the cache command is still written and read back. */
  @Test
  public void writeCompletesWhenFlushIsRefusedByStall() throws Exception {
    java.util.Map<Long, byte[]> card = new java.util.HashMap<>();
    Driver d =
        new Driver() {
          @Override
          public void read(long lba, ByteBuffer b) {
            for (long s = lba; b.hasRemaining(); s++) {
              byte[] sector = card.get(s);
              b.put(sector == null ? new byte[512] : sector);
            }
          }

          @Override
          public void write(long lba, ByteBuffer b) {
            writes++;
            for (long s = lba; b.hasRemaining(); s++) {
              byte[] sector = new byte[512];
              b.get(sector);
              card.put(s, sector);
            }
          }
        };
    Transport u = new Transport();
    u.stallStatus = 2;
    UsbCard.Connection c = new UsbCard.Connection(u, d, 1L << 30, "test", 0);
    c.flush(); // as open() does
    byte[] data = new byte[64 << 10];
    new java.util.Random(17).nextBytes(data);
    for (long off = 0; off < 1 << 20; off += data.length) {
      c.write(off, data);
      c.flush();
      assertArrayEquals(data, c.read(off, data.length));
    }
    assertEquals(16, d.writes);
    assertEquals(1, u.resets);
    c.close();
  }
}
