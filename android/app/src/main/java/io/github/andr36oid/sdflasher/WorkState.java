package io.github.andr36oid.sdflasher;

import android.content.Context;
import android.content.Intent;
import android.os.Build;
import io.github.andr36oid.bindings.mobile.Mobile;
import io.github.andr36oid.bindings.mobile.Session;
import java.io.File;
import java.util.List;
import org.json.JSONObject;

final class WorkState {
  interface Work {
    void run() throws Exception;
  }

  static volatile Session engine;
  static volatile JSONObject appUpdate;
  static boolean updateCheckStarted, updateShown;
  static volatile JSONObject image, card, progress, plan, speed;
  static volatile JSONObject hotfixOffer;
  static volatile org.json.JSONArray hotfixResults;
  static volatile String hotfixMessage = "";
  static volatile boolean hotfixAfterFlash;
  static volatile boolean choosingHotfixCard;
  static volatile UsbCard hotfixTarget;
  static String hotfixCompletion = "", hotfixCompletionMessage = "", hotfixCompletionNote = "";
  static volatile org.json.JSONArray releases;
  static volatile String reviewOptions = "";
  static volatile boolean reviewReady = false;
  static volatile UsbCard selected, pendingReader;
  static volatile List<UsbCard> discovered;
  static volatile String identity = "",
      profile = "",
      custom = "",
      error = "",
      message = "",
      // shown under message when the work is done (the AUTOAPK note after an install)
      note = "",
      result = "";
  static volatile boolean customAccepted = false, update = false, noROMs = false, busy = false;
  // Android size for a new card in GiB, 0: the recommended size
  static volatile int userdataGiB = 0;
  // a different system image (GSI) copied into app storage, "" for andr36oid's own
  static volatile String system = "", systemName = "";
  static volatile boolean systemAccepted = false;
  // experimental profiles whose warning was agreed to
  static final java.util.Set<String> experimentalOK =
      java.util.Collections.synchronizedSet(new java.util.HashSet<>());

  static void clearSystem() {
    if (!system.isEmpty()) new java.io.File(system).delete();
    system = "";
    systemName = "";
    systemAccepted = false;
  }

  // The console's own DTB: converted while writing (convert_dtb). conversion is
  // the preview the user still has to accept; conversionRefused its reason.
  static volatile boolean convertDTB = false;
  static volatile JSONObject conversion;
  static volatile String conversionPath = "", conversionRefused = "";
  static volatile long revision = 0;
  static volatile int step = 0;
  static Work pending;
  static boolean usbJob;

  static void clearImage() {
    image = null;
    profile = "";
    custom = "";
    customAccepted = false;
    convertDTB = false;
    conversion = null;
    revision++;
  }

  static void autoProfile() {
    if (image == null) return;
    String detected = card == null ? "" : card.optString("profile");
    String preferred =
        !detected.isEmpty() ? detected : profile.isEmpty() ? "Panels/Panel4" : profile;
    org.json.JSONArray profiles = image.optJSONArray("profiles");
    profile = "";
    if (profiles != null)
      for (int n = 0; n < profiles.length(); n++)
        if (profiles.optJSONObject(n).optString("id").equals(preferred)) {
          profile = preferred;
          return;
        }
  }

  static synchronized void checkAppUpdate() {
    if (updateCheckStarted) return;
    updateCheckStarted = true;
    new Thread(
            () -> {
              try {
                String value = Mobile.checkAppUpdate(BuildConfig.VERSION_NAME);
                if (!value.isEmpty()) appUpdate = new JSONObject(value);
              } catch (Exception ignored) {
              }
            },
            "app-update-check")
        .start();
  }

  static synchronized void init(Context context) throws Exception {
    if (engine == null)
      engine =
          Mobile.newSession(
              new File(context.getFilesDir(), "images-and-recovery").getAbsolutePath());
  }

  static synchronized void start(Context context, boolean usb, Work work) {
    if (busy) return;
    busy = true;
    error = "";
    message = "Working…";
    note = "";
    result = "";
    progress = null;
    plan = null;
    pending = work;
    usbJob = usb;
    revision++;
    Intent intent = new Intent(context, FlashService.class);
    try {
      if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent);
      else context.startService(intent);
    } catch (RuntimeException e) {
      busy = false;
      pending = null;
      error = e.getMessage();
      revision++;
    }
  }

  static void event(String event) {
    try {
      JSONObject p = new JSONObject(event);
      if (p.has("speed")) {
        speed = p.getJSONObject("speed");
        return;
      }
      progress = p;
      if (progress.has("plan")) plan = progress.getJSONObject("plan");
      revision++;
    } catch (Exception ignored) {
    }
  }

  static JSONObject options() throws Exception {
    return new JSONObject()
        .put("identity", identity)
        .put("fingerprint", card.getString("fingerprint"))
        .put("mode", update ? "update" : "install")
        .put("profile", profile)
        .put("no_roms", noROMs)
        .put("userdata_gib", update || noROMs ? 0 : userdataGiB)
        .put("custom", custom)
        .put("custom_accepted", customAccepted)
        .put("convert_dtb", convertDTB)
        .put("system", system)
        .put("system_accepted", systemAccepted);
  }
}
