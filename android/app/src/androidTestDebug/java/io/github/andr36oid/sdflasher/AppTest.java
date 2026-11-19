package io.github.andr36oid.sdflasher;

import static org.junit.Assert.*;

import android.content.Context;
import android.content.res.Configuration;
import android.os.Build;
import android.view.*;
import android.widget.TextView;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import io.github.andr36oid.bindings.mobile.*;
import java.io.File;
import java.util.Locale;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.json.JSONObject;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class AppTest {
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
  public void languageChoiceSurvivesRecreation() {
    try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
      for (String code : I18n.CODES) {
        context.getSharedPreferences("settings", 0).edit().putString("language", code).commit();
        scenario.recreate();
        scenario.onActivity(
            activity -> {
              assertEquals(code, I18n.code(activity));
              assertTrue(
                  contains(
                      activity.getWindow().getDecorView(),
                      Mobile.translate(code, "Set up or update your andr36oid card")));
              assertTrue(contains(activity.getWindow().getDecorView(), I18n.name(activity)));
            });
      }
    }
  }

  @Test
  public void detectedProfileTakesPriorityWithoutGuessingMissingPanels() throws Exception {
    JSONObject oldImage = WorkState.image, oldCard = WorkState.card;
    String oldProfile = WorkState.profile;
    try {
      WorkState.image =
          new JSONObject("{\"profiles\":[{\"id\":\"Panels/Panel4\"},{\"id\":\"Panels/Panel3\"}]}");
      WorkState.card = null;
      WorkState.profile = "";
      WorkState.autoProfile();
      assertEquals("Panels/Panel4", WorkState.profile);
      WorkState.profile = "Panels/Panel3";
      WorkState.autoProfile();
      assertEquals("Panels/Panel3", WorkState.profile);
      WorkState.card = new JSONObject("{\"profile\":\"Panels/Panel4\"}");
      WorkState.autoProfile();
      assertEquals("Panels/Panel4", WorkState.profile);
      WorkState.card = new JSONObject("{\"profile\":\"Devices/Other\"}");
      WorkState.autoProfile();
      assertEquals("", WorkState.profile);
      WorkState.card = null;
      WorkState.image = new JSONObject("{\"profiles\":[{\"id\":\"Panels/Panel3\"}]}");
      WorkState.autoProfile();
      assertEquals("", WorkState.profile);
    } finally {
      WorkState.image = oldImage;
      WorkState.card = oldCard;
      WorkState.profile = oldProfile;
    }
  }

  @Test
  public void automaticLocaleUsesBrazilianPortugueseAndEnglishFallback() {
    Configuration config = new Configuration(context.getResources().getConfiguration());
    config.setLocale(new Locale("pt", "BR"));
    assertEquals("pt-BR", I18n.code(context.createConfigurationContext(config)));
    config.setLocale(Locale.FRENCH);
    assertEquals("en", I18n.code(context.createConfigurationContext(config)));
    config.setLocale(Locale.SIMPLIFIED_CHINESE);
    assertEquals("zh-Hans", I18n.code(context.createConfigurationContext(config)));
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
  public void foregroundWorkSurvivesActivityRecreation() throws Exception {
    CountDownLatch started = new CountDownLatch(1), finish = new CountDownLatch(1);
    try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
      scenario.onActivity(
          activity ->
              WorkState.start(
                  activity.getApplicationContext(),
                  false,
                  () -> {
                    // progress like a real write: a determinate bar. An indeterminate one
                    // keeps animating on Android 6 even with animations off, so the main
                    // thread never idles and recreate() would wait until this work ends.
                    WorkState.event("{\"phase\":\"Writing\",\"done\":1,\"total\":2}");
                    started.countDown();
                    finish.await(30, TimeUnit.SECONDS);
                  }));
      assertTrue(started.await(10, TimeUnit.SECONDS));
      scenario.recreate();
      scenario.onActivity(activity -> assertTrue(WorkState.busy));
    } finally {
      finish.countDown();
    }
    long limit = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
    while (WorkState.busy && System.nanoTime() < limit) Thread.sleep(50);
    assertFalse(WorkState.busy);
  }

  @Test
  public void cardAdviceRequiresAgreementAndRemembersIt() {
    context
        .getSharedPreferences("settings", 0)
        .edit()
        .putBoolean("sd-card-advice-v1", false)
        .commit();
    try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
      scenario.onActivity(
          activity -> {
            View root = activity.getWindow().getDecorView();
            assertTrue(contains(root, "Your SD card matters"));
            assertFalse(contains(root, "Download a release..."));
            assertFalse(
                context.getSharedPreferences("settings", 0).getBoolean("sd-card-advice-v1", false));
            findText(root, "I understand").performClick();
            assertTrue(contains(activity.getWindow().getDecorView(), "Download a release..."));
          });
      scenario.recreate();
      scenario.onActivity(
          activity ->
              assertFalse(contains(activity.getWindow().getDecorView(), "Your SD card matters")));
    }
  }

  @Test
  public void updateToastOpensTheOfficialApk() throws Exception {
    String apk =
        "https://github.com/andr36oid/andr36oid-sdflasher/releases/download/v9.0.0/andr36oid-sdflasher-v9.0.0-android-universal.apk";
    WorkState.appUpdate = new JSONObject().put("version", "v9.0.0").put("apk", apk);
    WorkState.updateShown = false;
    android.content.IntentFilter filter =
        new android.content.IntentFilter(android.content.Intent.ACTION_VIEW);
    filter.addDataScheme("https");
    filter.addDataAuthority("github.com", null);
    filter.addDataPath(
        android.net.Uri.parse(apk).getPath(), android.os.PatternMatcher.PATTERN_LITERAL);
    android.app.Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
    android.app.Instrumentation.ActivityMonitor monitor =
        instrumentation.addMonitor(
            filter,
            new android.app.Instrumentation.ActivityResult(android.app.Activity.RESULT_OK, null),
            true);
    try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
      scenario.onActivity(
          activity -> {
            activity.showAppUpdate();
            TextView notice =
                findText(
                    activity.getWindow().getDecorView(),
                    "Flasher v9.0.0 is available\nTap to download the APK");
            assertNotNull(notice);
            notice.performClick();
          });
      assertEquals(1, monitor.getHits());
    } finally {
      instrumentation.removeMonitor(monitor);
      WorkState.appUpdate = null;
      WorkState.updateShown = false;
    }
  }
}
