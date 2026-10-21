package io.github.andr36oid.sdflasher;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.view.*;
import android.widget.TextView;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import io.github.andr36oid.bindings.mobile.*;
import java.io.File;
import org.json.JSONObject;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class ReleaseTest {
  private Context context;

  @Before
  public void prepare() throws Exception {
    context = InstrumentationRegistry.getInstrumentation().getTargetContext();
    context
        .getSharedPreferences("settings", 0)
        .edit()
        .clear()
        .putBoolean("sd-card-advice-v1", true)
        .commit();
    if (Build.VERSION.SDK_INT >= 33) {
      InstrumentationRegistry.getInstrumentation()
          .getUiAutomation()
          .executeShellCommand(
              "pm grant " + context.getPackageName() + " android.permission.POST_NOTIFICATIONS")
          .close();
    }
  }

  /** The colour behind a view: the nearest parent with a plain colour background. */
  private static int pageColor(View view) {
    for (android.view.ViewParent p = view.getParent(); p instanceof View; p = p.getParent()) {
      android.graphics.drawable.Drawable d = ((View) p).getBackground();
      if (d instanceof android.graphics.drawable.ColorDrawable)
        return ((android.graphics.drawable.ColorDrawable) d).getColor();
    }
    throw new AssertionError("no background colour behind " + view);
  }

  private TextView findText(View view, String text) {
    if (view instanceof TextView && ((TextView) view).getText().toString().equals(text))
      return (TextView) view;
    if (view instanceof ViewGroup) {
      ViewGroup group = (ViewGroup) view;
      for (int n = 0; n < group.getChildCount(); n++) {
        TextView found = findText(group.getChildAt(n), text);
        if (found != null) return found;
      }
    }
    return null;
  }

  private boolean contains(View view, String text) {
    if (view instanceof TextView && ((TextView) view).getText().toString().equals(text))
      return true;
    if (view instanceof ViewGroup) {
      ViewGroup group = (ViewGroup) view;
      for (int n = 0; n < group.getChildCount(); n++)
        if (contains(group.getChildAt(n), text)) return true;
    }
    return false;
  }

  @Test
  public void appearanceFollowsSystemMode() {
    try (ActivityScenario<Activity> scenario =
        ActivityScenario.launch(
            new Intent().setClassName(context, "io.github.andr36oid.sdflasher.MainActivity"))) {
      scenario.onActivity(
          activity -> {
            boolean dark =
                (activity.getResources().getConfiguration().uiMode
                        & android.content.res.Configuration.UI_MODE_NIGHT_MASK)
                    == android.content.res.Configuration.UI_MODE_NIGHT_YES;
            assertThemeIsDark(activity, dark);
            TextView title = findText(activity.getWindow().getDecorView(), "andr36oid SD Flasher");
            assertNotNull(title);
            int background = pageColor(title);
            assertEquals(dark, android.graphics.Color.red(title.getCurrentTextColor()) > 128);
            assertEquals(!dark, android.graphics.Color.red(background) > 128);
            android.app.AlertDialog dialog =
                new android.app.AlertDialog.Builder(activity)
                    .setMessage("Theme check")
                    .setPositiveButton("OK", null)
                    .create();
            dialog.show();
            assertThemeIsDark(dialog.getContext(), dark);
            dialog.dismiss();
          });
    }
  }

  private void assertThemeIsDark(Context themed, boolean dark) {
    android.content.res.TypedArray colors =
        themed.obtainStyledAttributes(new int[] {android.R.attr.textColorPrimary});
    try {
      assertEquals(
          dark, android.graphics.Color.red(colors.getColorStateList(0).getDefaultColor()) > 128);
    } finally {
      colors.recycle();
    }
  }

  @Test
  public void languageChoiceSurvivesRecreation() {
    try (ActivityScenario<Activity> scenario =
        ActivityScenario.launch(
            new Intent(Intent.ACTION_MAIN)
                .setClassName(
                    context.getPackageName(), "io.github.andr36oid.sdflasher.MainActivity"))) {
      for (String code :
          new String[] {"en", "de", "ru", "uk", "es", "pt", "pt-BR", "hi", "ja", "ko", "zh-Hans"}) {
        context.getSharedPreferences("settings", 0).edit().putString("language", code).commit();
        scenario.recreate();
        scenario.onActivity(
            activity -> {
              assertTrue(
                  contains(
                      activity.getWindow().getDecorView(),
                      Mobile.translate(code, "Set up or update your andr36oid card")));
            });
      }
    }
  }

  static class MemoryDisk implements Disk {
    int writes, reads;

    public long capacity() {
      return 1L << 30;
    }

    public String identity() {
      return "emulated-usb-reader";
    }

    public byte[] read(long offset, long length) {
      reads++;
      assertEquals(0, offset % 512);
      assertEquals(0, length % 512);
      return new byte[(int) length];
    }

    public void write(long offset, byte[] data) {
      writes++;
    }

    public void flush() {}
  }

  @Test
  public void nativeBridgeInspectsWithoutWritingAndRejectsMissingImage() throws Exception {
    MemoryDisk disk = new MemoryDisk();
    JSONObject card = new JSONObject(Mobile.inspectDisk(disk));
    assertFalse(card.getBoolean("installed"));
    assertTrue(disk.reads > 0);
    Session session =
        Mobile.newSession(new File(context.getFilesDir(), "test-engine").getAbsolutePath());
    try {
      session.write(disk, "{}", null);
      fail("write without source succeeded");
    } catch (Exception expected) {
      assertTrue(expected.getMessage().contains("Treble"));
    }
    assertEquals(0, disk.writes);
  }

  @Test
  public void nativeLibrariesAndUsbEntryPointsSurviveOptimization() throws Exception {
    for (String library : new String[] {"errno-lib", "usb-lib", "libusb", "libusbcom"}) {
      System.loadLibrary(library);
    }
    Class<?> usb = Class.forName("me.jahnen.libaums.libusbcommunication.LibusbCommunication");
    assertTrue(
        java.lang.reflect.Modifier.isNative(
            usb.getDeclaredMethod("nativeInit", int.class, long[].class).getModifiers()));
    assertTrue(
        java.lang.reflect.Modifier.isNative(
            usb.getDeclaredMethod(
                    "nativeBulkTransfer",
                    long.class,
                    int.class,
                    byte[].class,
                    int.class,
                    int.class,
                    int.class)
                .getModifiers()));
  }

  @Test
  public void nativeProgressCallbackSurvivesOptimization() throws Exception {
    File archive = new File(context.getCacheDir(), "invalid.img.zip");
    try (java.util.zip.ZipOutputStream zip =
        new java.util.zip.ZipOutputStream(new java.io.FileOutputStream(archive))) {
      zip.putNextEntry(new java.util.zip.ZipEntry("invalid.img"));
      zip.write(new byte[1024 * 1024]);
      zip.closeEntry();
    }
    java.util.concurrent.atomic.AtomicInteger events =
        new java.util.concurrent.atomic.AtomicInteger();
    Session session =
        Mobile.newSession(new File(context.getFilesDir(), "test-progress").getAbsolutePath());
    try {
      session.load(archive.getAbsolutePath(), event -> events.incrementAndGet());
      fail("invalid image accepted");
    } catch (Exception expected) {
      assertNotNull(expected.getMessage());
    } finally {
      archive.delete();
    }
    assertTrue("Go must invoke the Java progress callback", events.get() > 0);
  }

  @Test
  public void cardAdviceRequiresAgreementAndRemembersIt() {
    context
        .getSharedPreferences("settings", 0)
        .edit()
        .putBoolean("sd-card-advice-v1", false)
        .commit();
    try (ActivityScenario<Activity> scenario =
        ActivityScenario.launch(
            new Intent(Intent.ACTION_MAIN)
                .setClassName(
                    context.getPackageName(), "io.github.andr36oid.sdflasher.MainActivity"))) {
      scenario.onActivity(
          activity -> {
            View root = activity.getWindow().getDecorView();
            assertTrue(contains(root, "Your SD card matters"));
            assertFalse(contains(root, "Download a release…"));
            assertFalse(
                context.getSharedPreferences("settings", 0).getBoolean("sd-card-advice-v1", false));
            findText(root, "I understand").performClick();
            assertTrue(contains(activity.getWindow().getDecorView(), "Download a release…"));
          });
      scenario.recreate();
      scenario.onActivity(
          activity ->
              assertFalse(contains(activity.getWindow().getDecorView(), "Your SD card matters")));
    }
  }
}
