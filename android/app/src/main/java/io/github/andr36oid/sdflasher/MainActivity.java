package io.github.andr36oid.sdflasher;

import android.Manifest;
import android.app.*;
import android.content.*;
import android.database.Cursor;
import android.hardware.usb.*;
import android.net.Uri;
import android.os.*;
import android.provider.OpenableColumns;
import android.view.*;
import android.widget.*;
import io.github.andr36oid.bindings.mobile.Mobile;
import java.io.*;
import java.util.*;
import org.json.*;

public final class MainActivity extends Activity {
  private final Handler handler = new Handler(Looper.getMainLooper());
  private long rendered = -1;
  private LinearLayout body, target;
  // setup steps: image, card, console, options, write
  private static final String[] STEPS = {"Release", "Card", "Console", "Options", "Write"};
  private final LinearLayout[] sections = new LinearLayout[STEPS.length];
  private TextView stepLabel, modeHelp, summary, help;
  private Button back, next;
  private LinearLayout storage;
  private TextView sizeText;
  private SeekBar sizeBar;
  private Button sizeAuto;
  private int sizeMin = 4;
  private TextView systemLabel;
  private FrameLayout page;
  private Button updateNotice;
  private TextView imageLabel, cardLabel, status, customLabel, mapLegend;
  private Button language, console, profile, chooseCard, action;
  private final List<View> controls = new ArrayList<>();
  private CheckBox noROMs;
  private RadioButton install, update;
  private ProgressBar progress;
  private DiskMap map;
  private String lastError = "", lastResult = "";
  private boolean hotfixDialogShown;

  private String t(String key, Object... args) {
    return I18n.text(this, key, args);
  }

  private final Runnable poll =
      new Runnable() {
        public void run() {
          if (adviceAccepted() && WorkState.revision != rendered) {
            rendered = WorkState.revision;
            render();
          }
          if (adviceAccepted()) showAppUpdate();
          handler.postDelayed(this, 200);
        }
      };
  private final BroadcastReceiver usbEvents =
      new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
          if ((getPackageName() + ".USB_PERMISSION").equals(intent.getAction())) {
            UsbDevice device = intent.getParcelableExtra(UsbManager.EXTRA_DEVICE);
            UsbCard pendingReader = WorkState.pendingReader;
            if (pendingReader != null
                && device != null
                && device.getDeviceId() == pendingReader.device.getDeviceId()) {
              WorkState.pendingReader = null;
              if (intent.getBooleanExtra(UsbManager.EXTRA_PERMISSION_GRANTED, false))
                discover(pendingReader);
              else {
                WorkState.choosingHotfixCard = false;
                showError(t("USB permission was denied."));
              }
            }
          } else if (UsbManager.ACTION_USB_DEVICE_DETACHED.equals(intent.getAction())) {
            UsbDevice device = intent.getParcelableExtra(UsbManager.EXTRA_DEVICE);
            if (device != null
                && WorkState.pendingReader != null
                && device.getDeviceId() == WorkState.pendingReader.device.getDeviceId())
              WorkState.pendingReader = null;
            if (device != null
                && WorkState.selected != null
                && device.getDeviceId() == WorkState.selected.device.getDeviceId()) {
              WorkState.selected = null;
              WorkState.card = null;
              WorkState.identity = "";
              WorkState.revision++;
            }
          }
        }
      };

  @Override
  public void onCreate(Bundle state) {
    super.onCreate(state);
    try {
      WorkState.init(getApplicationContext());
    } catch (Exception e) {
      showError(e.toString());
      return;
    }
    IntentFilter filter = new IntentFilter(getPackageName() + ".USB_PERMISSION");
    filter.addAction(UsbManager.ACTION_USB_DEVICE_DETACHED);
    if (Build.VERSION.SDK_INT >= 33)
      registerReceiver(usbEvents, filter, Context.RECEIVER_NOT_EXPORTED);
    else registerReceiver(usbEvents, filter);
    if (adviceAccepted()) startSetup();
    else showCardAdvice();
  }

  private boolean adviceAccepted() {
    return getSharedPreferences("settings", 0).getBoolean("sd-card-advice-v1", false);
  }

  private void startSetup() {
    build();
    rendered = -1;
    WorkState.checkAppUpdate();
    if (Build.VERSION.SDK_INT >= 33
        && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
            != android.content.pm.PackageManager.PERMISSION_GRANTED)
      requestPermissions(new String[] {Manifest.permission.POST_NOTIFICATIONS}, 3);
  }

  @Override
  protected void onResume() {
    super.onResume();
    rendered = -1;
    handler.post(poll);
    UsbCard pendingReader = WorkState.pendingReader;
    if (pendingReader != null
        && ((UsbManager) getSystemService(USB_SERVICE)).hasPermission(pendingReader.device)) {
      WorkState.pendingReader = null;
      discover(pendingReader);
    }
  }

  @Override
  protected void onPause() {
    handler.removeCallbacks(poll);
    super.onPause();
  }

  @Override
  protected void onDestroy() {
    handler.removeCallbacksAndMessages(null);
    try {
      unregisterReceiver(usbEvents);
    } catch (IllegalArgumentException ignored) {
    }
    super.onDestroy();
  }

  private int dp(int n) {
    return Math.round(n * getResources().getDisplayMetrics().density);
  }

  private TextView label(String text, boolean heading) {
    TextView view = new TextView(this);
    view.setText(text);
    view.setTextSize(heading ? 19 : 15);
    view.setTextColor(getColor(R.color.body_text));
    if (heading) view.setTypeface(null, android.graphics.Typeface.BOLD);
    view.setPadding(0, dp(9), 0, dp(9));
    target.addView(view);
    return view;
  }

  private Button button(String key, View.OnClickListener action) {
    Button view = new Button(this);
    view.setText(t(key));
    view.setAllCaps(false);
    view.setOnClickListener(action);
    target.addView(view);
    controls.add(view);
    return view;
  }

  private void createPage() {
    controls.clear();
    ScrollView scroll = new ScrollView(this);
    scroll.setFillViewport(true);
    body = new LinearLayout(this);
    body.setOrientation(LinearLayout.VERTICAL);
    body.setPadding(dp(18), dp(12), dp(18), dp(24));
    body.setBackgroundColor(getColor(R.color.surface));
    target = body;
    scroll.addView(body);
    page = new FrameLayout(this);
    page.addView(scroll);
    setContentView(page);
    scroll.setOnApplyWindowInsetsListener(
        (view, insets) -> {
          view.setPadding(
              insets.getSystemWindowInsetLeft(),
              insets.getSystemWindowInsetTop(),
              insets.getSystemWindowInsetRight(),
              insets.getSystemWindowInsetBottom());
          return insets;
        });
  }

  private void showCardAdvice() {
    createPage();
    label("andr36oid SD Flasher", true);
    language = button(I18n.name(this), v -> languages());
    label(t("Your SD card matters"), true);
    for (String key :
        new String[] {
          "Android is much heavier on storage than ArkOS and similar Linux firmware. Use a"
              + " high-quality, fast microSD card for a smooth experience.",
          "Low-quality cards can fail early, behave unpredictably, cause crashes, or corrupt data.",
          "We don’t recommend specific brands: a brand label doesn’t prove a card is genuine. Buy"
              + " quality storage from reputable sellers.",
          "16 GB is the minimum. For Android, we recommend at least 32 GB. 128 GB is the sweet"
              + " spot for Android and EASYROMS to fit comfortably."
        }) label(t(key), false);
    button(
        "I understand",
        v -> {
          getSharedPreferences("settings", 0).edit().putBoolean("sd-card-advice-v1", true).apply();
          startSetup();
        });
  }

  void showAppUpdate() {
    if (updateNotice != null) updateNotice.setEnabled(!WorkState.busy);
    JSONObject update = WorkState.appUpdate;
    if (!adviceAccepted()
        || WorkState.busy
        || WorkState.updateShown
        || update == null
        || update.optString("apk").isEmpty()) return;
    WorkState.updateShown = true;
    Button notice = new Button(this);
    updateNotice = notice;
    notice.setAllCaps(false);
    notice.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
    notice.setText(
        t("Flasher %s is available", update.optString("version"))
            + "\n"
            + t("Tap to download the APK"));
    notice.setTextColor(getColor(R.color.notice_text));
    notice.setBackgroundTintList(
        android.content.res.ColorStateList.valueOf(getColor(R.color.notice_surface)));
    FrameLayout.LayoutParams params =
        new FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
            Gravity.BOTTOM);
    params.setMargins(dp(16), 0, dp(16), dp(72));
    page.addView(notice, params);
    notice.setOnClickListener(
        v -> {
          if (WorkState.busy) return;
          try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(update.optString("apk"))));
          } catch (ActivityNotFoundException e) {
            info(t("Download update"), update.optString("apk"));
          }
          if (notice.getParent() instanceof ViewGroup)
            ((ViewGroup) notice.getParent()).removeView(notice);
        });
    int duration = 20000;
    if (Build.VERSION.SDK_INT >= 29) {
      android.view.accessibility.AccessibilityManager accessibility =
          (android.view.accessibility.AccessibilityManager) getSystemService(ACCESSIBILITY_SERVICE);
      if (accessibility != null)
        duration =
            accessibility.getRecommendedTimeoutMillis(
                duration,
                android.view.accessibility.AccessibilityManager.FLAG_CONTENT_TEXT
                    | android.view.accessibility.AccessibilityManager.FLAG_CONTENT_CONTROLS);
    }
    handler.postDelayed(
        () -> {
          if (notice.getParent() instanceof ViewGroup)
            ((ViewGroup) notice.getParent()).removeView(notice);
        },
        duration);
  }

  private LinearLayout section(int step) {
    LinearLayout view = new LinearLayout(this);
    view.setOrientation(LinearLayout.VERTICAL);
    body.addView(view);
    sections[step] = view;
    target = view;
    return view;
  }

  private void build() {
    createPage();
    LinearLayout top = new LinearLayout(this);
    top.setOrientation(LinearLayout.HORIZONTAL);
    top.setGravity(Gravity.CENTER_VERTICAL);
    body.addView(top);
    target = top;
    label("andr36oid SD Flasher", true)
        .setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
    language = new Button(this);
    language.setAllCaps(false);
    language.setText(I18n.name(this));
    language.setOnClickListener(v -> languages());
    top.addView(language);
    target = body;
    label(t("Set up or update your andr36oid card"), false);
    stepLabel = label("", true);
    stepLabel.setTextColor(getColor(R.color.accent));

    section(0);
    label(t("Choose the andr36oid release"), true);
    label(
        t(
            "Download a release, or choose an image file you already have. Only Treble images can"
                + " be installed."),
        false);
    button("Download a release…", v -> releases());
    button("Choose image from disk…", v -> pick(1));
    imageLabel = label("", false);

    section(1);
    label(t("Choose the microSD card"), true);
    label(
        t("Connect a USB card reader directly to your Android device. No root is needed."), false);
    label(t("The card is only read now. Nothing is written before the last step."), false);
    chooseCard = button("Select a microSD card", v -> readers());
    cardLabel = label("", false);

    section(2);
    label(t("Choose your console"), true);
    label(t("Choose the board family first, then the board and screen."), false);
    console = button("Choose your console / board family", v -> chooseConsole());
    profile = button("Choose your board and screen", v -> chooseProfile());
    button(
        "I don’t know my board or panel",
        v ->
            info(
                "Finding your board and panel",
                "Use your console’s board markings and screen information. Similar-looking consoles"
                    + " can require different DTBs."));
    label(t("DTB"), true);
    customLabel = label("", false);
    button("Use my console’s own DTB file…", v -> chooseOwnDTB());
    button(
        "Advanced: custom DTB",
        v ->
            new AlertDialog.Builder(this)
                .setTitle(t("Custom Android DTB"))
                .setMessage(t(Mobile.customWarning()))
                .setPositiveButton(t("Choose custom Android DTB…"), (d, w) -> pick(2))
                .setNegativeButton(
                    t("Use shipped DTB"),
                    (d, w) -> {
                      WorkState.custom = "";
                      WorkState.customAccepted = false;
                      WorkState.convertDTB = false;
                      WorkState.revision++;
                    })
                .show());

    section(3);
    label(t("What should happen to the card?"), true);
    RadioGroup modes = new RadioGroup(this);
    install = new RadioButton(this);
    update = new RadioButton(this);
    install.setId(View.generateViewId());
    update.setId(View.generateViewId());
    install.setText(t("Install a new card (erase)"));
    update.setText(t("Update an existing installation"));
    modes.addView(install);
    modes.addView(update);
    target.addView(modes);
    controls.add(install);
    controls.add(update);
    modes.setOnCheckedChangeListener(
        (g, id) -> {
          WorkState.update = id == update.getId();
          if (WorkState.update && WorkState.card != null)
            WorkState.noROMs = WorkState.card.optBoolean("no_roms");
          WorkState.revision++;
        });
    modeHelp = label("", false);
    label(t("Storage"), true);
    noROMs = new CheckBox(this);
    noROMs.setText(t("Use all available storage for Android"));
    target.addView(noROMs);
    noROMs.setOnCheckedChangeListener(
        (b, checked) -> {
          WorkState.noROMs = checked;
          WorkState.revision++;
        });
    label(
        t(
            "Unchecked: choose below how much of the card Android gets. The rest becomes a"
                + " games partition readable on your computer. Updates keep the existing"
                + " arrangement."),
        false);
    storage = new LinearLayout(this);
    storage.setOrientation(LinearLayout.VERTICAL);
    target.addView(storage);
    sizeText = new TextView(this);
    sizeText.setTextColor(getColor(R.color.body_text));
    storage.addView(sizeText);
    sizeBar = new SeekBar(this);
    sizeBar.setOnSeekBarChangeListener(
        new SeekBar.OnSeekBarChangeListener() {
          public void onProgressChanged(SeekBar bar, int progress, boolean fromUser) {
            if (!fromUser) return;
            WorkState.userdataGiB = sizeMin + progress;
            WorkState.revision++;
          }

          public void onStartTrackingTouch(SeekBar bar) {}

          public void onStopTrackingTouch(SeekBar bar) {}
        });
    storage.addView(sizeBar);
    sizeAuto = new Button(this);
    sizeAuto.setText(t("Recommended size"));
    sizeAuto.setOnClickListener(
        v -> {
          WorkState.userdataGiB = 0;
          WorkState.revision++;
        });
    storage.addView(sizeAuto);
    systemLabel = label("", false);
    button(
        "Advanced: different system image (GSI)",
        v ->
            new AlertDialog.Builder(this)
                .setTitle(t("Advanced: different system image (GSI)"))
                .setMessage(t(Mobile.gsiWarning()))
                .setPositiveButton(t("Choose system image…"), (d, w) -> pick(4))
                .setNegativeButton(
                    t("Use the andr36oid system"),
                    (d, w) -> {
                      WorkState.clearSystem();
                      WorkState.revision++;
                    })
                .show());

    section(4);
    label(t("Check and write"), true);
    summary = label("", false);
    help = label("", false);
    action = button("Erase and write this card…", v -> review());
    map = new DiskMap(this);
    target.addView(map, new LinearLayout.LayoutParams(-1, dp(160)));
    mapLegend = label(t("Pending · Writing · Written · Verified · Preserved"), false);

    target = body;
    progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
    progress.setMax(1000);
    body.addView(progress);
    status = label("", false);
    LinearLayout nav = new LinearLayout(this);
    nav.setOrientation(LinearLayout.HORIZONTAL);
    body.addView(nav);
    target = nav;
    back = button("Back", v -> showStep(WorkState.step - 1));
    next = button("Next", v -> showStep(WorkState.step + 1));
    back.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
    next.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
    target = body;
    button("Restore an interrupted update…", v -> recover());
    button(
        "Apply hotfixes",
        v -> {
          if (WorkState.selected == null) {
            WorkState.choosingHotfixCard = true;
            readers();
            return;
          }
          fetchHotfixes(WorkState.selected, "");
        });
    button("About and licenses", v -> about());
    label(BuildConfig.VERSION_NAME + " · GPLv3", false);
    rendered = -1;
    render();
  }

  private void showStep(int step) {
    if (step < 0 || step >= STEPS.length || !reachable(step)) return;
    WorkState.step = step;
    WorkState.revision++;
  }

  // a card under the minimum for a new installation (Mobile.minCardBytes)
  private boolean tooSmall() {
    UsbCard c = WorkState.selected;
    return c != null && c.bytes > 0 && c.bytes < Mobile.minCardBytes();
  }

  private String tooSmallText() {
    return t(
        "This card is too small. andr36oid needs a card of at least 16 GB; this one has %.1f GB.",
        WorkState.selected.bytes / 1e9);
  }

  private boolean stepDone(int step) {
    switch (step) {
      case 0:
        return WorkState.image != null;
      case 1:
        return WorkState.card != null && WorkState.selected != null;
      case 2:
        return !WorkState.profile.isEmpty();
      case 3:
        return !WorkState.update
            || (WorkState.card != null && WorkState.card.optBoolean("installed"));
      default:
        return false;
    }
  }

  private boolean reachable(int step) {
    for (int n = 0; n < step; n++) if (!stepDone(n)) return false;
    return true;
  }

  private String dtbText() {
    if (WorkState.custom.isEmpty()) return t("Use the DTB shipped with the selected profile.");
    String name = new File(WorkState.custom).getName();
    return WorkState.convertDTB
        ? t("Converted from your console’s DTB: %s", name)
        : t("Custom Android DTB") + ": " + name;
  }

  private void languages() {
    String[] labels = new String[I18n.NAMES.length + 1];
    labels[0] = t("System language");
    System.arraycopy(I18n.NAMES, 0, labels, 1, I18n.NAMES.length);
    new AlertDialog.Builder(this)
        .setTitle(t("Language"))
        .setItems(
            labels,
            (d, n) -> {
              getSharedPreferences("settings", 0)
                  .edit()
                  .putString("language", n == 0 ? "" : I18n.CODES[n - 1])
                  .apply();
              if (adviceAccepted()) build();
              else showCardAdvice();
            })
        .show();
  }

  private void render() {
    if (imageLabel == null) return;
    boolean busy = WorkState.busy;
    if (WorkState.error.isEmpty()) lastError = "";
    if (WorkState.result.isEmpty()) lastResult = "";
    // "Update" gets its own state below: switching it on here and off there on every
    // refresh replayed the radio buttons' animation, as if they kept being tapped
    for (View view : controls) if (view != update) view.setEnabled(!busy);
    imageLabel.setText(
        WorkState.image == null
            ? t("Choose a release to see its supported consoles and panels.")
            : WorkState.image.optString("version"));
    customLabel.setText(dtbText());
    JSONObject selected = selectedProfile();
    console.setText(
        selected == null ? t("Choose your console / board family") : selected.optString("console"));
    profile.setText(
        selected == null ? t("Choose your board and screen") : selected.optString("name"));
    if (WorkState.selected != null) chooseCard.setText(WorkState.selected.label());
    else chooseCard.setText(t("Select a microSD card"));
    boolean installed = WorkState.card != null && WorkState.card.optBoolean("installed");
    cardLabel.setText(
        WorkState.card == null
            ? t("Insert a microSD card, then select it below.")
            : !installed && tooSmall()
                ? tooSmallText()
                : installed
                    ? t(
                        WorkState.card.optBoolean("treble")
                            ? "Treble installation"
                            : "Legacy andr36oid — will migrate to Treble")
                    : t("New installation required"));
    boolean modeChanged =
        install.isChecked() == WorkState.update
            || update.isChecked() != WorkState.update
            || update.isEnabled() != (!busy && installed);
    install.setChecked(!WorkState.update);
    update.setChecked(WorkState.update);
    update.setEnabled(!busy && installed);
    // changes made here (not by a tap) show at once, without the tap animation
    if (modeChanged) {
      install.jumpDrawablesToCurrentState();
      update.jumpDrawablesToCurrentState();
    }
    noROMs.setChecked(WorkState.noROMs);
    noROMs.setEnabled(!busy && !WorkState.update);
    renderStorage(busy);
    systemLabel.setText(systemText());
    boolean ready = true;
    for (int n = 0; n < 4; n++) ready &= stepDone(n);
    action.setEnabled(!busy && ready && !(!WorkState.update && tooSmall()));
    // a step that lost what it needs (card removed, new image) sends the user back
    while (WorkState.step > 0 && !reachable(WorkState.step)) WorkState.step--;
    int step = WorkState.step;
    for (int n = 0; n < sections.length; n++)
      sections[n].setVisibility(n == step ? View.VISIBLE : View.GONE);
    stepLabel.setText(t("Step %d of %d: %s", step + 1, STEPS.length, t(STEPS[step])));
    back.setEnabled(!busy && step > 0);
    next.setVisibility(step == STEPS.length - 1 ? View.INVISIBLE : View.VISIBLE);
    next.setEnabled(!busy && stepDone(step));
    int danger = android.graphics.Color.rgb(0xE5, 0x39, 0x35);
    modeHelp.setText(
        WorkState.update
            ? t("Apps, settings, saves, and games will be preserved.")
            : t("Everything currently on this card will be erased."));
    modeHelp.setTextColor(WorkState.update ? getColor(R.color.body_text) : danger);
    JSONObject chosen = selectedProfile();
    summary.setText(
        t("Release")
            + ": "
            + (WorkState.image == null ? "" : WorkState.image.optString("version"))
            + "\n"
            + t("microSD card")
            + ": "
            + (WorkState.selected == null ? "" : WorkState.selected.label())
            + "\n"
            + t("Device profile")
            + ": "
            + (chosen == null ? "" : chosen.optString("name"))
            + "\n"
            + t("DTB")
            + ": "
            + dtbText()
            + "\n"
            + t("Action")
            + ": "
            + t(WorkState.update ? "Update an existing installation" : "Install a new card (erase)")
            + (WorkState.system.isEmpty() ? "" : " · " + systemText())
            + "\n"
            + t("Storage")
            + ": "
            + storageText());
    help.setText(
        WorkState.update
            ? t(
                "BOOT, system, and vendor will be updated together. The dedicated cache will be"
                    + " cleared. Userdata and games stay in place. A recovery backup will be"
                    + " saved before writing.")
            : t("Everything currently on this card will be erased."));
    help.setTextColor(WorkState.update ? getColor(R.color.body_text) : danger);
    action.setText(t(WorkState.update ? "Update this card…" : "Erase and write this card…"));
    JSONObject p = WorkState.progress;
    String phase = p == null ? "" : p.optString("phase");
    status.setText(
        WorkState.error.isEmpty()
            ? t(busy ? (phase.isEmpty() ? "Working…" : phase) : WorkState.message)
                + (busy && p != null && p.has("name") ? ": " + t(p.optString("name")) : "")
            : t("Operation could not finish") + "\n" + WorkState.error);
    progress.setIndeterminate(busy && (p == null || p.optLong("total") == 0));
    if (p != null && p.optLong("total") > 0)
      progress.setProgress((int) (1000.0 * p.optLong("done") / p.optLong("total")));
    progress.setVisibility(busy || p != null ? View.VISIBLE : View.GONE);
    status.setVisibility(status.getText().length() > 0 ? View.VISIBLE : View.GONE);
    map.setVisibility(WorkState.plan != null ? View.VISIBLE : View.GONE);
    mapLegend.setVisibility(WorkState.plan != null ? View.VISIBLE : View.GONE);
    map.update(WorkState.plan, p);
    if (!busy && WorkState.discovered != null) {
      List<UsbCard> cards = WorkState.discovered;
      WorkState.discovered = null;
      chooseDiscovered(cards);
    }
    if (!busy && WorkState.reviewReady) {
      WorkState.reviewReady = false;
      if (WorkState.error.isEmpty()) confirmWrite();
    }
    if (!busy && WorkState.conversion != null) {
      JSONObject c = WorkState.conversion;
      WorkState.conversion = null;
      showConversion(c);
    }
    if (!busy && !WorkState.conversionRefused.isEmpty()) {
      String reason = WorkState.conversionRefused;
      WorkState.conversionRefused = "";
      new AlertDialog.Builder(this)
          .setTitle(t("This DTB can’t be converted"))
          .setMessage(reason)
          .setPositiveButton(t("OK"), null)
          .show();
    }
    if (!busy && WorkState.releases != null) {
      JSONArray assets = WorkState.releases;
      WorkState.releases = null;
      showReleases(assets);
    }
    if (!busy && !WorkState.error.isEmpty() && !WorkState.error.equals(lastError)) {
      lastError = WorkState.error;
      WorkState.choosingHotfixCard = false;
      showError(WorkState.error);
    }
    if (!busy && WorkState.hotfixAfterFlash) {
      WorkState.hotfixAfterFlash = false;
      fetchHotfixes(WorkState.hotfixTarget, WorkState.image.optString("build_identity"));
      return;
    }
    if (!busy && !hotfixDialogShown && WorkState.hotfixOffer != null) showHotfixes();
    if (!busy && !hotfixDialogShown && !WorkState.hotfixMessage.isEmpty()) {
      hotfixDialogShown = true;
      new AlertDialog.Builder(this)
          .setTitle(t("Apply hotfixes"))
          .setMessage(t(WorkState.hotfixMessage))
          .setPositiveButton(t("Continue"), null)
          .setOnDismissListener(
              d -> {
                WorkState.hotfixMessage = "";
                finishHotfixes();
              })
          .show();
    }
    if (!busy && !hotfixDialogShown && WorkState.hotfixResults != null) showHotfixResults();
    if (!busy && !WorkState.result.isEmpty() && !WorkState.result.equals(lastResult)) {
      lastResult = WorkState.result;
      new AlertDialog.Builder(this)
          .setTitle(t("Written and verified"))
          .setMessage(
              t(WorkState.message)
                  + (WorkState.note.isEmpty() ? "" : "\n\n" + t(WorkState.note))
                  + speedText())
          .setPositiveButton(t("OK"), null)
          .show();
    }
  }

  private JSONObject selectedProfile() {
    JSONArray ps = WorkState.image == null ? null : WorkState.image.optJSONArray("profiles");
    if (ps != null)
      for (int n = 0; n < ps.length(); n++) {
        JSONObject p = ps.optJSONObject(n);
        if (p != null && p.optString("id").equals(WorkState.profile)) return p;
      }
    return null;
  }

  /** The profile's own console, or one it is also listed under (a V12 wired like the G80). */
  private static boolean listedFor(JSONObject p, String family) {
    if (p.optString("console").equals(family)) return true;
    JSONArray also = p.optJSONArray("also_for");
    for (int i = 0; also != null && i < also.length(); i++) {
      if (also.optString(i).equals(family)) return true;
    }
    return false;
  }

  private void chooseConsole() {
    if (WorkState.image == null) {
      info("Choose a release", "Choose a release to see its supported consoles and panels.");
      return;
    }
    TreeSet<String> names = new TreeSet<>();
    JSONArray ps = WorkState.image.optJSONArray("profiles");
    for (int n = 0; n < ps.length(); n++) {
      JSONObject p = ps.optJSONObject(n);
      names.add(p.optString("console"));
      JSONArray also = p.optJSONArray("also_for");
      for (int i = 0; also != null && i < also.length(); i++) names.add(also.optString(i));
    }
    String[] labels = names.toArray(new String[0]);
    new AlertDialog.Builder(this)
        .setTitle(t("Choose your console / board family"))
        .setItems(labels, (d, n) -> profilesFor(labels[n]))
        .show();
  }

  private void chooseProfile() {
    JSONObject selected = selectedProfile();
    if (selected == null) chooseConsole();
    else profilesFor(selected.optString("console"));
  }

  private void profilesFor(String family) {
    List<JSONObject> profiles = new ArrayList<>();
    List<String> names = new ArrayList<>();
    JSONArray ps = WorkState.image.optJSONArray("profiles");
    for (int n = 0; n < ps.length(); n++) {
      JSONObject p = ps.optJSONObject(n);
      if (listedFor(p, family)) {
        profiles.add(p);
        names.add(
            p.optString("name")
                + (p.optBoolean("experimental") ? " (" + t("experimental") + ")" : ""));
      }
    }
    if (profiles.size() == 1) {
      // a console with one profile (R46H, R50S, ...) needs no second choice
      useProfile(profiles.get(0));
      return;
    }
    new AlertDialog.Builder(this)
        .setTitle(t("Choose your board and screen"))
        .setItems(names.toArray(new String[0]), (d, n) -> useProfile(profiles.get(n)))
        .show();
  }

  // An experimental profile is only used after its warning: a direct text, a checkbox
  // and an "I agree" button that works once the box is ticked. Asked once per profile
  // while the app runs.
  private void useProfile(JSONObject p) {
    String id = p.optString("id");
    if (!p.optBoolean("experimental") || WorkState.experimentalOK.contains(id)) {
      WorkState.profile = id;
      WorkState.revision++;
      return;
    }
    WorkState.profile = "";
    WorkState.revision++;
    String tracker = "https://github.com/andr36oid/clone_issue_tracker";
    LinearLayout box = new LinearLayout(this);
    box.setOrientation(LinearLayout.VERTICAL);
    box.setPadding(dp(22), dp(8), dp(22), 0);
    TextView name = new TextView(this);
    name.setText(p.optString("name"));
    name.setTypeface(null, android.graphics.Typeface.BOLD);
    name.setTextSize(16);
    box.addView(name);
    TextView body = new TextView(this);
    body.setText(
        t(
            "Nobody on the andr36oid team has tested this device tree on real hardware. It can"
                + " leave the screen black or the buttons dead, and in the worst case put a wrong"
                + " voltage on part of the board.\n\nSupport for experimental device trees is very"
                + " limited. If something doesn't work:\n• Report it only in the clone issue"
                + " tracker: %s\n• Attach all the boot files from a system that works on your"
                + " console, for example from the SD card it came with: every .dtb file and the"
                + " other files at the top of its first drive.\n• Answer every question in the"
                + " issue form.\n\nReports without those files will be closed.",
            tracker));
    body.setTextColor(android.graphics.Color.rgb(0xE5, 0x39, 0x35));
    body.setAutoLinkMask(android.text.util.Linkify.WEB_URLS);
    body.setPadding(0, dp(8), 0, dp(8));
    box.addView(body);
    CheckBox check = new CheckBox(this);
    check.setText(
        t(
            "I understand that this may not work, that support is very limited, and that I must"
                + " attach my console's working boot files to any report."));
    box.addView(check);
    ScrollView scroll = new ScrollView(this);
    scroll.addView(box);
    AlertDialog dialog =
        new AlertDialog.Builder(this)
            .setTitle(t("Experimental device tree"))
            .setView(scroll)
            .setNegativeButton(t("Cancel"), null)
            .setPositiveButton(
                t("I agree"),
                (d, w) -> {
                  WorkState.experimentalOK.add(id);
                  WorkState.profile = id;
                  WorkState.revision++;
                })
            .show();
    android.widget.Button agree = dialog.getButton(AlertDialog.BUTTON_POSITIVE);
    agree.setEnabled(false);
    check.setOnCheckedChangeListener((v, checked) -> agree.setEnabled(checked));
  }

  private void pick(int code) {
    Intent intent =
        new Intent(Intent.ACTION_OPEN_DOCUMENT)
            .setType("*/*")
            .addCategory(Intent.CATEGORY_OPENABLE);
    if (code == 1) {
      // release images come packed (.img.xz, .img.zip); an unpacked .img has no type of
      // its own and is listed as plain binary data
      intent.putExtra(
          Intent.EXTRA_MIME_TYPES,
          new String[] {
            "application/x-xz",
            "application/zip",
            "application/x-zip-compressed",
            "application/octet-stream",
            "application/x-raw-disk-image"
          });
    }
    startActivityForResult(intent, code);
  }

  @Override
  protected void onActivityResult(int request, int result, Intent data) {
    super.onActivityResult(request, result, data);
    if (result != RESULT_OK || data == null || data.getData() == null) return;
    Uri uri = data.getData();
    String name = "";
    try (Cursor cursor =
        getContentResolver()
            .query(uri, new String[] {OpenableColumns.DISPLAY_NAME}, null, null, null)) {
      if (cursor != null && cursor.moveToFirst()) name = cursor.getString(0);
    }
    String lower = name.toLowerCase(Locale.ROOT);
    if (request == 4 && !lower.endsWith(".img")) {
      showError(t("Choose an .img system image."));
      return;
    }
    if (request == 1
        && !(lower.endsWith(".img") || lower.endsWith(".zip") || lower.endsWith(".xz"))) {
      showError(t("Choose an .img, .img.xz or .img.zip release image."));
      return;
    }
    // a build too old for this flasher: refused by its name, before it is copied
    String oldBuild = request == 1 ? Mobile.tooOldImage(name) : "";
    if (!oldBuild.isEmpty()) {
      new AlertDialog.Builder(this)
          .setTitle(t("This image is too old"))
          .setMessage(
              t(
                  "This image was built on %s. Builds from before 22 October 2026 don't work"
                      + " with this flasher. Download a current release instead.",
                  oldBuild))
          .setPositiveButton(t("OK"), null)
          .show();
      return;
    }
    final String extension =
        request == 4
            ? ".img"
            : request >= 2
                ? ".dtb"
                : lower.endsWith(".zip") ? ".zip" : lower.endsWith(".xz") ? ".xz" : ".img";
    final String shown = name;
    final Context app = getApplicationContext();
    WorkState.start(
        app,
        false,
        () -> {
          if (request == 1) WorkState.clearImage();
          File file = File.createTempFile("import-", extension, app.getFilesDir());
          boolean keep = false;
          try (InputStream in = app.getContentResolver().openInputStream(uri);
              FileOutputStream out = new FileOutputStream(file)) {
            if (in == null) throw new IOException("Could not open selected file");
            byte[] buffer = new byte[1 << 20];
            long total = 0;
            int n;
            while ((n = in.read(buffer)) != -1) {
              total += n;
              if (total > (request == 4 ? 4L << 30 : request >= 2 ? 4L << 20 : 32L << 30))
                throw new IOException("Selected file exceeds the size limit");
              out.write(buffer, 0, n);
            }
            out.getFD().sync();
            if (request == 4) {
              WorkState.clearSystem();
              WorkState.system = file.getAbsolutePath();
              WorkState.systemName = shown;
              WorkState.systemAccepted = true;
            } else if (request == 2) {
              WorkState.custom = file.getAbsolutePath();
              WorkState.customAccepted = true;
              WorkState.convertDTB = false;
            } else if (request == 3) {
              try {
                WorkState.conversion =
                    new JSONObject(WorkState.engine.convertDTB(file.getAbsolutePath()));
                WorkState.conversionPath = file.getAbsolutePath();
              } catch (Exception e) {
                // a refusal explains itself; it is not an operation error
                WorkState.conversionRefused =
                    e.getMessage() == null ? e.toString() : e.getMessage();
                WorkState.message = "Ready";
                return;
              }
            } else {
              WorkState.image =
                  new JSONObject(WorkState.engine.load(file.getAbsolutePath(), WorkState::event));
              WorkState.profile = "";
              WorkState.autoProfile();
              if (WorkState.step == 0) WorkState.step = 1;
            }
            keep = true;
            WorkState.message = "Ready";
          } finally {
            if (!keep) file.delete();
          }
        });
  }

  private void chooseOwnDTB() {
    if (WorkState.image == null) {
      info(
          "Your console’s own DTB",
          "Choose a release first. The conversion uses the DTBs of that release.");
      return;
    }
    new AlertDialog.Builder(this)
        .setTitle(t("Your console’s own DTB"))
        .setMessage(
            t(
                "Choose the DTB file from your console’s original ArkOS, dArkOS or EmuELEC card."
                    + " It is in the BOOT partition, often named rk3326-r36s-linux.dtb. The"
                    + " flasher turns it into an andr36oid DTB: it copies your console’s screen,"
                    + " buttons, battery and other board details into the andr36oid DTB of the"
                    + " same board family. Your file and the files it replaces are kept in the"
                    + " andr36oid-dtb-backup folder on BOOT."))
        .setNegativeButton(t("Cancel"), null)
        .setPositiveButton(t("Continue"), (d, w) -> pick(3))
        .show();
  }

  private void showConversion(JSONObject c) {
    String profileName = c.optString("profile");
    JSONArray ps = WorkState.image == null ? null : WorkState.image.optJSONArray("profiles");
    if (ps != null)
      for (int n = 0; n < ps.length(); n++)
        if (ps.optJSONObject(n).optString("id").equals(c.optString("profile")))
          profileName = ps.optJSONObject(n).optString("name");
    StringBuilder text =
        new StringBuilder(
            t(
                "Board family: %s. Screen: %s. Device profile: %s.",
                c.optString("family"),
                c.optLong("width") + " × " + c.optLong("height"),
                profileName));
    text.append("\n\n")
        .append(
            t(
                "This DTB was made by a program from your console’s own DTB. It has not been"
                    + " tested on your console. If the screen stays dark or the buttons don’t"
                    + " work, write the card again without it."));
    JSONArray warnings = c.optJSONArray("warnings");
    if (warnings != null && warnings.length() > 0) {
      text.append("\n\n").append(t("Please check")).append(":");
      for (int n = 0; n < warnings.length(); n++) text.append("\n• ").append(warnings.optString(n));
    }
    String path = WorkState.conversionPath;
    new AlertDialog.Builder(this)
        .setTitle(t("Your console’s own DTB"))
        .setMessage(text.toString())
        .setNeutralButton(
            t("Full report"),
            (d, w) ->
                new AlertDialog.Builder(this)
                    .setTitle(t("Full report"))
                    .setMessage(c.optString("report"))
                    .setPositiveButton(t("OK"), (d2, w2) -> showConversion(c))
                    .show())
        .setNegativeButton(t("Cancel"), null)
        .setPositiveButton(
            t("Use this DTB"),
            (d, w) -> {
              WorkState.custom = path;
              WorkState.customAccepted = true;
              WorkState.convertDTB = true;
              if (!c.optString("profile").isEmpty()) WorkState.profile = c.optString("profile");
              WorkState.revision++;
            })
        .show();
  }

  private void releases() {
    WorkState.start(
        getApplicationContext(),
        false,
        () -> {
          WorkState.releases = new JSONArray(Mobile.releases());
          WorkState.message = "Ready";
        });
  }

  private void showReleases(JSONArray assets) {
    if (assets.length() == 0) {
      info(
          "No image downloads available",
          "No disk images are attached to andr36oid/releases yet. You can choose an image from"
              + " disk.");
      return;
    }
    String[] labels = new String[assets.length()];
    for (int n = 0; n < labels.length; n++) labels[n] = assets.optJSONObject(n).optString("name");
    Context app = getApplicationContext();
    new AlertDialog.Builder(this)
        .setTitle(t("Download a release"))
        .setItems(
            labels,
            (d, n) -> {
              WorkState.start(
                  app,
                  false,
                  () -> {
                    WorkState.clearImage();
                    WorkState.image =
                        new JSONObject(
                            WorkState.engine.download(
                                assets.getJSONObject(n).toString(), WorkState::event));
                    WorkState.profile = "";
                    WorkState.autoProfile();
                    if (WorkState.step == 0) WorkState.step = 1;
                    WorkState.message = "Ready";
                  });
            })
        .show();
  }

  private void readers() {
    List<UsbCard> readers = UsbCard.readers(this);
    if (readers.isEmpty()) {
      WorkState.choosingHotfixCard = false;
      info(
          "No removable cards found",
          "Connect a USB SD-card adapter directly. Internal SD slots, USB hubs, hard drives, and"
              + " SSDs are not supported.");
      return;
    }
    String[] labels = new String[readers.size()];
    for (int n = 0; n < labels.length; n++) labels[n] = readers.get(n).label();
    new AlertDialog.Builder(this)
        .setTitle(t("Select a microSD card"))
        .setItems(
            labels,
            (d, n) -> {
              UsbCard pendingReader = readers.get(n);
              WorkState.pendingReader = pendingReader;
              UsbManager manager = (UsbManager) getSystemService(USB_SERVICE);
              if (manager.hasPermission(pendingReader.device)) {
                WorkState.pendingReader = null;
                discover(pendingReader);
              } else {
                Intent permission =
                    new Intent(getPackageName() + ".USB_PERMISSION").setPackage(getPackageName());
                manager.requestPermission(
                    pendingReader.device,
                    PendingIntent.getBroadcast(
                        this,
                        0,
                        permission,
                        PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT));
              }
            })
        .setOnCancelListener(d -> WorkState.choosingHotfixCard = false)
        .show();
  }

  private void discover(UsbCard reader) {
    Context app = getApplicationContext();
    WorkState.start(
        app,
        true,
        () -> {
          WorkState.discovered = reader.discover(app);
          WorkState.message = "Ready";
        });
  }

  private void chooseDiscovered(List<UsbCard> cards) {
    if (cards.isEmpty()) {
      WorkState.choosingHotfixCard = false;
      info(
          "No removable cards found", "Insert a card with 512-byte logical sectors and try again.");
      return;
    }
    if (cards.size() == 1) {
      inspect(cards.get(0));
      return;
    }
    String[] labels = new String[cards.size()];
    for (int n = 0; n < labels.length; n++) labels[n] = cards.get(n).label();
    new AlertDialog.Builder(this)
        .setTitle(t("Select a microSD card"))
        .setItems(labels, (d, n) -> inspect(cards.get(n)))
        .setOnCancelListener(d -> WorkState.choosingHotfixCard = false)
        .show();
  }

  private void inspect(UsbCard target) {
    if (WorkState.choosingHotfixCard) {
      WorkState.choosingHotfixCard = false;
      fetchHotfixes(target, "");
      return;
    }
    Context app = getApplicationContext();
    WorkState.start(
        app,
        true,
        () -> {
          try (UsbCard.Connection disk = target.open(app)) {
            WorkState.card = new JSONObject(Mobile.inspectDisk(disk));
            WorkState.identity = disk.identity();
            WorkState.selected = target;
            WorkState.update = WorkState.card.optBoolean("installed");
            WorkState.noROMs = WorkState.card.optBoolean("no_roms");
            WorkState.autoProfile();
            WorkState.message = "Ready";
          }
        });
  }

  private void review() {
    try {
      WorkState.reviewOptions = WorkState.options().toString();
      UsbCard target = WorkState.selected;
      Context app = getApplicationContext();
      WorkState.reviewReady = false;
      WorkState.start(
          app,
          true,
          () -> {
            try (UsbCard.Connection disk = target.open(app)) {
              WorkState.plan = new JSONObject(WorkState.engine.plan(disk, WorkState.reviewOptions));
              WorkState.reviewReady = true;
            }
          });
    } catch (Exception e) {
      WorkState.reviewReady = false;
      showError(e.toString());
    }
  }

  private void confirmWrite() {
    UsbCard target = WorkState.selected;
    if (target == null) return;
    boolean updating = WorkState.update;
    String input = WorkState.reviewOptions;
    Context app = getApplicationContext();
    String message =
        t(
            updating
                ? "BOOT, system, and vendor will be updated together. The dedicated cache will be"
                    + " cleared. Userdata and games stay in place. A recovery backup will be"
                    + " saved before writing."
                : "Everything currently on this card will be erased.");
    if (WorkState.convertDTB)
      message +=
          "\n\n"
              + t(
                  "This DTB was made by a program from your console’s own DTB. It has not been"
                      + " tested on your console. If the screen stays dark or the buttons don’t"
                      + " work, write the card again without it.");
    else if (!WorkState.custom.isEmpty()) message += "\n\n" + t(Mobile.customWarning());
    new AlertDialog.Builder(this)
        .setTitle(t(updating ? "Update this card?" : "Erase this card?"))
        .setMessage(target.label() + "\n\n" + message)
        .setNegativeButton(t("Cancel"), null)
        .setPositiveButton(
            t(updating ? "Update" : "Erase and install"),
            (dialog, which) -> {
              WorkState.start(
                  app,
                  true,
                  () -> {
                    try (UsbCard.Connection disk = target.open(app)) {
                      WorkState.speed = null;
                      WorkState.result = WorkState.engine.write(disk, input, WorkState::event);
                      WorkState.message =
                          updating
                              ? "Your card has been written and verified. Apps, settings, saves,"
                                  + " and games were preserved."
                              : "Your card has been written and verified. Insert it into the"
                                  + " console and keep it powered on until Android setup"
                                  + " appears.";
                      WorkState.note =
                          updating
                              ? ""
                              : "Want to add your own apps? Before the first start, connect the"
                                  + " card to a computer and copy APK files to its AUTOAPK drive."
                                  + " The console offers them during setup.";
                      WorkState.card = null;
                      WorkState.hotfixTarget = target;
                      WorkState.hotfixAfterFlash = true;
                    }
                  });
            })
        .show();
  }

  private void fetchHotfixes(UsbCard target, String expectedBuild) {
    if (WorkState.busy) return;
    WorkState.hotfixTarget = target;
    WorkState.hotfixCompletion = WorkState.result;
    WorkState.hotfixCompletionMessage = WorkState.message;
    WorkState.hotfixCompletionNote = WorkState.note;
    Context app = getApplicationContext();
    WorkState.start(
        app,
        true,
        () -> {
          try (UsbCard.Connection disk = target.open(app)) {
            WorkState.hotfixOffer =
                new JSONObject(
                    WorkState.engine.fetchHotfixes(disk, expectedBuild, WorkState::event));
            if (WorkState.hotfixOffer.getJSONArray("hotfixes").length() == 0) {
              WorkState.hotfixOffer = null;
              WorkState.hotfixMessage = "No hotfixes are available for this exact build.";
            }
          } catch (Exception e) {
            WorkState.hotfixMessage = e.getMessage() == null ? e.toString() : e.getMessage();
          }
        });
  }

  private void finishHotfixes() {
    hotfixDialogShown = false;
    WorkState.hotfixOffer = null;
    WorkState.hotfixResults = null;
    WorkState.result = WorkState.hotfixCompletion;
    WorkState.message = WorkState.hotfixCompletionMessage;
    WorkState.note = WorkState.hotfixCompletionNote;
    WorkState.hotfixCompletion = "";
    WorkState.revision++;
  }

  private void showHotfixes() {
    JSONObject offer = WorkState.hotfixOffer;
    JSONArray list = offer.optJSONArray("hotfixes");
    if (list == null) {
      finishHotfixes();
      return;
    }
    hotfixDialogShown = true;
    String[] labels = new String[list.length()];
    boolean[] checked = new boolean[list.length()];
    for (int n = 0; n < list.length(); n++) {
      JSONObject h = list.optJSONObject(n);
      labels[n] =
          h.optString("title")
              + "\n"
              + h.optString("description").replaceAll("\\s+", " ")
              + "\n"
              + t("Size: %.1f MiB", h.optLong("size") / (double) (1 << 20));
      checked[n] = h.optString("type").equals("bugfix");
    }
    TextView hotfixTitle = new TextView(this);
    hotfixTitle.setPadding(dp(24), dp(18), dp(24), dp(9));
    hotfixTitle.setText(
        t("Apply hotfixes")
            + "\n\n"
            + t(
                "Bug fixes are selected. Optional extras are unchecked. Choose which hotfixes to"
                    + " copy to the card."));
    new AlertDialog.Builder(this)
        .setCustomTitle(hotfixTitle)
        .setMultiChoiceItems(labels, checked, (d, n, value) -> checked[n] = value)
        .setNegativeButton(t("Skip hotfixes"), (d, w) -> finishHotfixes())
        .setOnCancelListener(d -> finishHotfixes())
        .setPositiveButton(
            t("Apply selected hotfixes"),
            (d, w) -> {
              JSONArray selected = new JSONArray();
              for (int n = 0; n < checked.length; n++)
                if (checked[n]) selected.put(list.optJSONObject(n).optString("file"));
              WorkState.hotfixOffer = null;
              hotfixDialogShown = false;
              UsbCard target = WorkState.hotfixTarget;
              Context app = getApplicationContext();
              WorkState.start(
                  app,
                  true,
                  () -> {
                    try (UsbCard.Connection disk = target.open(app)) {
                      WorkState.hotfixResults =
                          new JSONArray(
                              WorkState.engine.applyHotfixes(
                                  disk, selected.toString(), WorkState::event));
                    } catch (Exception e) {
                      WorkState.hotfixMessage =
                          e.getMessage() == null ? e.toString() : e.getMessage();
                    }
                  });
            })
        .show();
  }

  private void showHotfixResults() {
    hotfixDialogShown = true;
    JSONArray results = WorkState.hotfixResults;
    StringBuilder text = new StringBuilder();
    if (results.length() == 0) text.append(t("No hotfixes selected.")).append("\n");
    for (int n = 0; n < results.length(); n++) {
      JSONObject result = results.optJSONObject(n);
      if (result.optString("error").isEmpty())
        text.append(t("Hotfix installed: %s", result.optString("file")));
      else
        text.append(t("Hotfix failed: %s", result.optString("file")))
            .append("\n")
            .append(t("Technical details"))
            .append(": ")
            .append(result.optString("error"));
      text.append("\n\n");
    }
    text.append(t("Hotfixes will be applied when the console next starts."));
    new AlertDialog.Builder(this)
        .setTitle(t("Hotfix results"))
        .setMessage(text)
        .setPositiveButton(t("Continue"), null)
        .setOnDismissListener(d -> finishHotfixes())
        .show();
  }

  private void recover() {
    if (WorkState.selected == null) {
      info("Choose a card", "Select the card that was being updated first.");
      return;
    }
    try {
      JSONArray backups = new JSONArray(WorkState.engine.recoveries());
      if (backups.length() == 0) {
        info("Recovery backups", "No recovery backups are available.");
        return;
      }
      String[] labels = new String[backups.length()];
      for (int n = 0; n < labels.length; n++) {
        JSONObject b = backups.getJSONObject(n);
        labels[n] = b.optString("created") + " · " + b.optString("version");
      }
      new AlertDialog.Builder(this)
          .setTitle(t("Recovery backups"))
          .setItems(
              labels,
              (d, n) -> {
                UsbCard target = WorkState.selected;
                Context app = getApplicationContext();
                String dir = backups.optJSONObject(n).optString("directory");
                new AlertDialog.Builder(this)
                    .setTitle(t("Restore previous installation"))
                    .setMessage(
                        t(
                                "Restore the saved operating system? Userdata and games remain"
                                    + " protected.")
                            + "\n\n"
                            + target.label())
                    .setNegativeButton(t("Cancel"), null)
                    .setPositiveButton(
                        t("Restore"),
                        (dialog, which) ->
                            WorkState.start(
                                app,
                                true,
                                () -> {
                                  try (UsbCard.Connection disk = target.open(app)) {
                                    WorkState.engine.restore(disk, dir, WorkState::event);
                                    WorkState.message =
                                        "The previous installation was restored and verified.";
                                    WorkState.result = dir;
                                    WorkState.card = null;
                                  }
                                }))
                    .show();
              })
          .show();
    } catch (Exception e) {
      showError(e.toString());
    }
  }

  private void about() {
    new AlertDialog.Builder(this)
        .setTitle(t("About and licenses"))
        .setMessage(
            "andr36oid SD Flasher\n"
                + BuildConfig.VERSION_NAME
                + "\nGPLv3\n\n"
                + t("USB access uses libaums and libusb, the same USB stack used by EtchDroid."))
        .setPositiveButton(
            t("Licenses"),
            (d, w) -> {
              try {
                String[] names = getAssets().list("licenses");
                new AlertDialog.Builder(this)
                    .setTitle(t("Licenses"))
                    .setItems(
                        names,
                        (dialog, n) -> {
                          try (InputStream in = getAssets().open("licenses/" + names[n])) {
                            ByteArrayOutputStream out = new ByteArrayOutputStream();
                            byte[] b = new byte[4096];
                            int count;
                            while ((count = in.read(b)) != -1) out.write(b, 0, count);
                            new AlertDialog.Builder(this)
                                .setTitle(names[n])
                                .setMessage(out.toString("UTF-8"))
                                .setPositiveButton(t("OK"), null)
                                .show();
                          } catch (Exception e) {
                            showError(e.toString());
                          }
                        })
                    .show();
              } catch (Exception e) {
                showError(e.toString());
              }
            })
        .setNegativeButton(t("Close"), null)
        .show();
  }

  private JSONObject storageLayout() {
    if (WorkState.selected == null) return null;
    try {
      return new JSONObject(Mobile.storageLayout(WorkState.selected.bytes, WorkState.userdataGiB));
    } catch (JSONException e) {
      return null;
    }
  }

  private String systemText() {
    if (WorkState.system.isEmpty()) return t("The andr36oid system is written.");
    return t("System image") + ": " + WorkState.systemName;
  }

  /** How a new card will be split between Android and games. */
  private String storageText() {
    if (WorkState.update) return t("Kept as it is");
    JSONObject l = storageLayout();
    if (WorkState.noROMs || (l != null && l.optInt("games") == 0))
      return t("Use all available storage for Android");
    if (l == null) return "";
    return t("Android %d GiB, games %d GiB", l.optInt("android"), l.optInt("games"));
  }

  /** The size slider: only for a new card that keeps a games partition. */
  private void renderStorage(boolean busy) {
    JSONObject l = storageLayout();
    int max = l == null ? 0 : l.optInt("max");
    if (l != null) sizeMin = l.optInt("min");
    if (WorkState.userdataGiB > max) {
      WorkState.userdataGiB = 0;
      l = storageLayout();
    }
    sizeText.setText(WorkState.update ? "" : storageText());
    sizeText.setVisibility(sizeText.getText().length() > 0 ? View.VISIBLE : View.GONE);
    boolean show = l != null && !WorkState.update && !WorkState.noROMs && max > sizeMin;
    sizeBar.setVisibility(show ? View.VISIBLE : View.GONE);
    sizeAuto.setVisibility(show ? View.VISIBLE : View.GONE);
    if (!show) return;
    int value = WorkState.userdataGiB == 0 ? l.optInt("android") : WorkState.userdataGiB;
    sizeBar.setMax(max - sizeMin);
    sizeBar.setProgress(Math.max(0, value - sizeMin));
    sizeBar.setEnabled(!busy);
    sizeAuto.setEnabled(!busy && WorkState.userdataGiB != 0);
  }

  /** The card speed measured while writing, with a warning for slow cards. */
  private String speedText() {
    JSONObject s = WorkState.speed;
    if (s == null || s.optDouble("write_mbps") <= 0) return "";
    String text =
        "\n\n"
            + t(
                "Card speed: %.0f MB/s writing, %.1f ms per small read, %.1f ms per small write",
                s.optDouble("write_mbps"),
                s.optDouble("read_latency_ms"),
                s.optDouble("write_latency_ms"));
    if (s.optBoolean("slow"))
      text +=
          "\n\n"
              + t(
                  "This card is slow. Android runs from the card, so apps may start slowly and"
                      + " games may stutter. For a smooth experience, use a card from a known"
                      + " brand marked A1 or A2.");
    return text;
  }

  private void info(String title, String message) {
    new AlertDialog.Builder(this)
        .setTitle(t(title))
        .setMessage(t(message))
        .setPositiveButton(t("OK"), null)
        .show();
  }

  private void showError(String message) {
    new AlertDialog.Builder(this)
        .setTitle(t("Operation could not finish"))
        .setMessage(
            t("Check the selected image and card, then try again.")
                + "\n\n"
                + t("Technical details")
                + ":\n"
                + message)
        .setPositiveButton(t("OK"), null)
        .show();
  }
}
