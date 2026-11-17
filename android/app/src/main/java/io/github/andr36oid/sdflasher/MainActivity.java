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
  // the one warning colour, for erase texts and the erase button
  private static final int DANGER = android.graphics.Color.rgb(0xE5, 0x39, 0x35);
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
  private TextView imageLabel, cardLabel, status, customLabel, mapLegend, usbNotice;
  private Button language, console, profile, chooseCard, action, stop;
  private final List<View> controls = new ArrayList<>();
  private CheckBox noROMs;
  private RadioGroup guestGroup;
  private final java.util.List<RadioButton> guestButtons = new java.util.ArrayList<>();
  private final java.util.List<String> guestIds = new java.util.ArrayList<>();
  private boolean guestUpdating;
  private TextView guestInfo, guestIntro;
  private LinearLayout guestBox;
  private RadioButton install, update;
  private BootOptionsView bootOptionsView;
 private TextView dtbLabel;
  private static final String[] BOOTLOADER_CHOICES={"image","standard","new"};
  private static final String BOOTLOADER_WARNING="Test builds are tried on few consoles. If the console no longer starts, write the card again with \"As in the image\".";
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
            // no device in the answer: it is still the answer to the pending request
            if (pendingReader != null
                && (device == null || device.getDeviceId() == pendingReader.device.getDeviceId())) {
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
    // headings get air above them so sections read as groups
    view.setPadding(0, dp(heading ? 16 : 9), 0, dp(heading ? 6 : 9));
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

  /** A filled button in the given colour: the one action to take next (or the dangerous one). */
  private void emphasize(Button view, int color) {
    view.setBackgroundTintList(android.content.res.ColorStateList.valueOf(color));
    view.setTextColor(getColor(R.color.notice_text));
  }

  /** A borderless button for the occasional tools, so they stay out of the main flow. */
  private void quiet(Button view) {
    view.setBackgroundTintList(
        android.content.res.ColorStateList.valueOf(android.graphics.Color.TRANSPARENT));
    view.setTextColor(getColor(R.color.accent));
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
    // Capability preflight: a device without USB host (OTG) can never see a card
    // reader. Say so at once, in plain words, and point to the proven path.
    if (!UsbCard.hostSupported(this)) {
      usbNotice = label(t("This device cannot connect to a USB card reader"), true);
      usbNotice.setTextColor(android.graphics.Color.rgb(0xE5, 0x39, 0x35));
      TextView why = label(UsbCard.noReaderReason(this), false);
      why.setAutoLinkMask(android.text.util.Linkify.WEB_URLS);
    }
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
    dtbLabel=label("",false);
    button("Change...",v->{if(WorkState.image!=null)pick(5);});
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
    guestBox = new LinearLayout(this);
    guestBox.setOrientation(LinearLayout.VERTICAL);
    guestIntro =
        label(t("Choose one system to put on the card now. You can add more later on the console."), false);
    for (TextView view : new TextView[] {label(t("Game system"), true), guestIntro}) {
      target.removeView(view);
      guestBox.addView(view);
    }
    guestGroup = new RadioGroup(this);
    for (String id : new String[] {"dArkOS", "dArkOSRE", "dArkOSen", "none"}) {
      RadioButton button = new RadioButton(this);
      button.setId(View.generateViewId());
      guestGroup.addView(button);
      guestButtons.add(button);
      guestIds.add(id);
      controls.add(button);
    }
    guestBox.addView(guestGroup);
    guestGroup.setOnCheckedChangeListener(
        (g, id) -> {
          if (guestUpdating) return;
          for (int n = 0; n < guestButtons.size(); n++)
            if (guestButtons.get(n).getId() == id) {
              WorkState.guestChoice = guestIds.get(n);
              WorkState.guestChosen = true;
            }
          WorkState.revision++;
        });
    guestInfo = new TextView(this);
    guestInfo.setTextColor(getColor(R.color.body_text));
    guestBox.addView(guestInfo);
    target.addView(guestBox);
    try{bootOptionsView=new BootOptionsView(WorkState.bootloaderOptions(),bootContext(null),c->{WorkState.bootloaderChoice=c.optString("choice","image");WorkState.bootChain=c.optString("chain","standard");WorkState.sleepFirmware=c.optString("sleep","image");WorkState.forceBootOptions=c.optBoolean("force_boot_options");WorkState.revision++;});target.addView(bootOptionsView);}catch(Exception e){showError(e.toString());}
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
    // Stop is the one control that stays live while a write runs; it is not in
    // controls, which render() disables when busy.
    stop = new Button(this);
    stop.setAllCaps(false);
    stop.setText(t("Stop writing"));
    stop.setOnClickListener(v -> confirmStop());
    stop.setVisibility(View.GONE);
    body.addView(stop);
    LinearLayout nav = new LinearLayout(this);
    nav.setOrientation(LinearLayout.HORIZONTAL);
    body.addView(nav);
    target = nav;
    back = button("Back", v -> showStep(WorkState.step - 1));
    next = button("Next", v -> showStep(WorkState.step + 1));
    emphasize(next, getColor(R.color.accent));
    back.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
    next.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
    target = body;
    quiet(button("Restore an interrupted update…", v -> recover()));
    quiet(button("Update device trees",v->{if(WorkState.busy)return;WorkState.choosingDTBCard=true;readers();}));
    quiet(button("Boot options", v -> { if(WorkState.busy)return; WorkState.choosingBootloaderCard=true; readers(); }));
    quiet(
        button(
            "Apply hotfixes",
            v -> {
              if (WorkState.selected == null) {
                WorkState.choosingHotfixCard = true;
                readers();
                return;
              }
              fetchHotfixes(WorkState.selected, "");
            }));
    quiet(button("Free up space", v -> freeSpace()));
    quiet(button("About and licenses", v -> about()));
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
    for (View view : controls) {
      if (view != update) view.setEnabled(!busy);
      // tinted and borderless buttons lose the platform's disabled look; dim them
      view.setAlpha(view.isEnabled() ? 1f : 0.45f);
    }
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
    renderGuest(busy);
    renderBootloader(busy);
    dtbLabel.setText(WorkState.image==null||WorkState.image.optString("dtb_version").isEmpty()?"":t("Using device trees from %s",WorkState.image.optString("dtb_version")));
    renderStorage(busy);
    systemLabel.setText(systemText());
    boolean ready = true;
    for (int n = 0; n < 4; n++) ready &= stepDone(n);
    action.setEnabled(!busy && ready && !(!WorkState.update && tooSmall()) && !WorkState.guestBlocked());
    // red for an erase, the accent for an update: the same emphasis the desktop app uses
    emphasize(action, WorkState.update ? getColor(R.color.accent) : DANGER);
    action.setAlpha(action.isEnabled() ? 1f : 0.45f);
    next.setAlpha(next.isEnabled() ? 1f : 0.45f);
    // a step that lost what it needs (card removed, new image) sends the user back
    while (WorkState.step > 0 && !reachable(WorkState.step)) WorkState.step--;
    int step = WorkState.step;
    if(step==3) WorkState.lookupBootloaders();
    for (int n = 0; n < sections.length; n++)
      sections[n].setVisibility(n == step ? View.VISIBLE : View.GONE);
    stepLabel.setText(t("Step %d of %d: %s", step + 1, STEPS.length, t(STEPS[step])));
    back.setEnabled(!busy && step > 0);
    next.setVisibility(step == STEPS.length - 1 ? View.INVISIBLE : View.VISIBLE);
    next.setEnabled(!busy && stepDone(step));
    int danger = DANGER;
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
            + storageText() + "\n" + bootloaderSummary(null));
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
    int stall = WorkState.stall;
    status.setText(
        !WorkState.error.isEmpty()
            ? stopped()
                ? WorkState.error.startsWith(Mobile.StoppedIncomplete)
                    ? t("Writing stopped. The card is incomplete and has to be written again before you use it.")
                    : WorkState.error.startsWith(Mobile.StoppedDownload)
                        ? t("The game system was not downloaded. Nothing was written to the card.")
                        : t("Writing stopped")
                : t("Operation could not finish") + "\n" + explained(WorkState.error)
            : busy && stall > 0
                ? t(
                    "The card reader has not answered for %d seconds. If this does not change,"
                        + " unplug the reader: the operation then stops with an error and you"
                        + " can try again, or use the flasher for Windows, macOS or Linux.",
                    stall)
                : t(busy ? (phase.isEmpty() ? "Working…" : phase) : WorkState.message)
                    + (busy && p != null && p.has("name") ? ": " + t(p.optString("name")) : ""));
    status.setTextColor(
        busy && stall > 0
            ? android.graphics.Color.rgb(0xE5, 0x39, 0x35)
            : getColor(R.color.body_text));
    stop.setVisibility(busy && WorkState.cancelable ? View.VISIBLE : View.GONE);
    progress.setIndeterminate(busy && (p == null || p.optLong("total") == 0));
    if (p != null && p.optLong("total") > 0)
      progress.setProgress(
          WorkState.progressValue(
              phase, p.optLong("done"), p.optLong("total"), p.optBoolean("guest")));
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
      if (stopped())
        new AlertDialog.Builder(this)
            .setTitle(t("Writing stopped"))
            .setMessage(
                t(
                    "Nothing more was written. The card is incomplete and must be written again"
                        + " from the start before it can be used. An interrupted update can be"
                        + " restored with \"Restore an interrupted update\"."))
            .setPositiveButton(t("OK"), null)
            .show();
      else showError(WorkState.error);
    }
    if (!busy && WorkState.hotfixAfterFlash) {
      WorkState.hotfixAfterFlash = false;
      fetchHotfixes(WorkState.hotfixTarget, WorkState.image.optString("build_identity"));
      return;
    }
    if(!busy&&WorkState.dtbMessagePending){WorkState.dtbMessagePending=false;dtbInfo(t(WorkState.dtbMessageTitle),t(WorkState.dtbMessageBody));}
    if(!busy&&WorkState.dtbSourcePending){WorkState.dtbSourcePending=false;showDTBSource();}
    if(!busy&&WorkState.dtbPreview!=null){JSONObject preview=WorkState.dtbPreview;WorkState.dtbPreview=null;showDTBPreview(preview);}
    if(!busy&&WorkState.step==2){
      if(WorkState.dtbOnlineOffer!=null){JSONObject offer=WorkState.dtbOnlineOffer;WorkState.dtbOnlineOffer=null;WorkState.start(getApplicationContext(),false,()->{WorkState.image=new JSONObject(WorkState.engine.imageDTBReference(offer.getString("path"),offer.optString("manifest_path")));WorkState.autoProfile();WorkState.message="Ready";});}
      else WorkState.lookupImageDTBs();
    }
    if (!busy && WorkState.bootloaderInfo != null) showBootloaderCard();
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
                  + "\n\n"
                  + t("It is safe to unplug the card reader now.")
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
    long size = -1;
    try (Cursor cursor =
        getContentResolver()
            .query(
                uri,
                new String[] {OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE},
                null,
                null,
                null)) {
      if (cursor != null && cursor.moveToFirst()) {
        name = cursor.getString(0);
        if (!cursor.isNull(1)) size = cursor.getLong(1);
      }
    }
    // The file is copied into app storage first (SAF gives a stream, not a path).
    // A phone that is nearly full would otherwise fail halfway with a bare "No
    // space left on device"; say it up front, with the numbers.
    long free = getFilesDir().getUsableSpace();
    if (size > 0 && free > 0 && size + (64L << 20) > free) {
      new AlertDialog.Builder(this)
          .setTitle(t("Not enough free space"))
          .setMessage(
              t(
                  "This device does not have enough free storage to hold the file: %.1f GB"
                      + " needed, %.1f GB free. Free up space, or use the flasher for Windows,"
                      + " macOS or Linux.",
                  size / 1e9,
                  free / 1e9))
          .setPositiveButton(t("OK"), null)
          .show();
      return;
    }
    String lower = name.toLowerCase(Locale.ROOT);
    if ((request==5||request==6)&&!lower.endsWith(".zip")){showError(t("This file is not a complete device tree set. Download it again."));return;}
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
        request==5||request==6?".zip":request == 4
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
              if (total > (request==5||request==6?256L<<20:request == 4 ? 4L << 30 : request >= 2 ? 4L << 20 : 32L << 30))
                throw new IOException("Selected file exceeds the size limit");
              out.write(buffer, 0, n);
            }
            out.getFD().sync();
            if(request==5){WorkState.image=new JSONObject(WorkState.engine.imageDTBs(file.getAbsolutePath()));WorkState.autoProfile();}
            else if(request==6){WorkState.dtbFile=file.getAbsolutePath();WorkState.dtbSourcePending=true;}
            else if (request == 4) {
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
      // Say which of the three things it is: no USB host, nothing plugged in, or a
      // device that is not a mass-storage reader. Each names the desktop flasher as
      // the proven alternative.
      TextView text = new TextView(this);
      text.setText(UsbCard.noReaderReason(this));
      text.setTextSize(15);
      text.setPadding(dp(24), dp(12), dp(24), dp(12));
      text.setAutoLinkMask(android.text.util.Linkify.WEB_URLS);
      new AlertDialog.Builder(this)
          .setTitle(
              t(
                  UsbCard.hostSupported(this)
                      ? "No removable cards found"
                      : "This device cannot connect to a USB card reader"))
          .setView(text)
          .setPositiveButton(t("OK"), null)
          .show();
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
                        // Android adds the answer (granted, device) to this intent, so
                        // it must be mutable on API 31+; it is explicit (this package
                        // only), as Android 14 requires for a mutable one
                        Build.VERSION.SDK_INT >= 31
                            ? PendingIntent.FLAG_MUTABLE | PendingIntent.FLAG_UPDATE_CURRENT
                            : PendingIntent.FLAG_UPDATE_CURRENT));
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
    if(WorkState.choosingDTBCard){
      WorkState.choosingDTBCard=false;Context app=getApplicationContext();
      WorkState.start(app,true,()->{try(UsbCard.Connection disk=target.open(app)){WorkState.dtbCardInfo=new JSONObject(WorkState.engine.dtbInfo(disk));WorkState.dtbTarget=target;WorkState.dtbIdentity=disk.identity();WorkState.dtbSourcePending=true;WorkState.message="Ready";}});return;
    }
    if (WorkState.choosingBootloaderCard) {
      WorkState.choosingBootloaderCard=false;
      Context app=getApplicationContext();
      WorkState.start(app,true,()->{
        try(UsbCard.Connection disk=target.open(app)) {
          if(!WorkState.bootloaderLookup) {
            WorkState.bootloaderLookup=true;
            try {WorkState.bootOptionsOffer=new JSONObject(WorkState.engine.bootOptionsOffers());WorkState.bootloaderOffers=WorkState.bootOptionsOffer.optJSONArray("builds");WorkState.bootloaderFailed=WorkState.bootOptionsOffer.optBoolean("builds_failed");}
            catch(Exception e) {WorkState.bootloaderFailed=true;}
          }
          WorkState.bootloaderTarget=target;
          WorkState.bootloaderInfo=new JSONObject(WorkState.engine.bootloaderInfo(disk));
        }
      });
      return;
    }
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

  private void confirmWrite(){
    if(bootOptionsView==null||WorkState.card!=null&&WorkState.card.optBoolean("data_card")){confirmWriteAccepted();return;}
    bootOptionsView.confirm(c->{try{WorkState.reviewOptions=new JSONObject(WorkState.reviewOptions).put("bootloader",c).toString();confirmWriteAccepted();}catch(Exception e){showError(e.toString());}});
  }
  private void confirmWriteAccepted() {
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
              withDTBFreshScreen(() -> WorkState.start(
                  app,
                  true,
                  () -> {
                    // the download can be stopped too (nothing is written yet)
                    WorkState.downloading = true;
                    WorkState.cancelable = true;
                    WorkState.revision++;
                    try {
                      WorkState.prepareGuest(input);
                    } finally {
                      WorkState.downloading = false;
                    }
                    try (UsbCard.Connection disk = target.open(app)) {
                      WorkState.speed = null;
                      WorkState.cancelable = true;
                      WorkState.revision++;
                      try {
                        WorkState.result = WorkState.engine.write(disk, input, WorkState::event);
                      } catch (Exception e) {
                        throw withHints(e, WorkState.engine.lastFailureHints());
                      }
                      WorkState.cancelable = false;
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
                      JSONObject loaderResult=new JSONObject(WorkState.result).optJSONObject("bootloader");
                      if(loaderResult!=null) WorkState.note += "\n" +
                        (!loaderResult.optString("warning").isEmpty()?t(loaderResult.optString("warning")):bootloaderSummary(loaderResult.optJSONObject("record")));
                      WorkState.card = null;
                      WorkState.hotfixTarget = target;
                      WorkState.hotfixAfterFlash = true;
                    } catch (java.io.IOException e) {
                      // the reader could not be opened or talked to
                      throw withHints(e, Mobile.readerFailureHints());
                    }
                  }));
            })
        .show();
  }

  // e with the hint block after its message: hints are English lines (from the engine),
  // shown translated. The message's start is kept (the stopped check reads it).
  private Exception withHints(Exception e, String hints) {
    if (hints == null || hints.isEmpty()) return e;
    StringBuilder text = new StringBuilder(e.getMessage() == null ? e.toString() : e.getMessage());
    text.append("\n");
    for (String line : hints.split("\n")) text.append("\n").append(t(line));
    return new Exception(text.toString(), e);
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
                                    JSONObject loaderResult=new JSONObject(WorkState.result).optJSONObject("bootloader");
                      if(loaderResult!=null) WorkState.note += "\n" +
                        (!loaderResult.optString("warning").isEmpty()?t(loaderResult.optString("warning")):bootloaderSummary(loaderResult.optJSONObject("record")));
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
      if (WorkState.image == null)
        return new JSONObject(
            Mobile.storageLayout(WorkState.selected.bytes, WorkState.userdataGiB));
      long guestBytes =
          WorkState.guestSelected() != null ? WorkState.guestSelected().optLong("partition_bytes") : 0;
      return new JSONObject(
          Mobile.storageLayoutForImage(
              WorkState.image.toString(),
              WorkState.selected.bytes,
              WorkState.userdataGiB,
              WorkState.noROMs,
              guestBytes));
    } catch (Exception e) {
      return null;
    }
  }

  private static String humanBytes(long n) {
    if (n >= 1L << 30)
      return String.format(Locale.getDefault(), "%.1f GiB", (double) n / (1L << 30));
    if (n >= 1L << 20)
      return String.format(Locale.getDefault(), "%.1f MiB", (double) n / (1L << 20));
    if (n >= 1L << 10)
      return String.format(Locale.getDefault(), "%.0f KiB", (double) n / (1L << 10));
    return String.format(Locale.getDefault(), "%d B", n);
  }

  // One choice of game system: dArkOS (preselected), dArkOSRE, dArkOSen or none, as
  // in the desktop flasher. Each says why it can't be chosen.
  private void renderGuest(boolean busy) {
    WorkState.lookupGuestOffer();
    // an update keeps the system that is on the card
    String kept = guestKept();
    if (WorkState.update && !kept.isEmpty()) {
      guestBox.setVisibility(View.VISIBLE);
      guestGroup.setVisibility(View.GONE);
      guestIntro.setVisibility(View.GONE);
      guestInfo.setText(kept);
      return;
    }
    guestGroup.setVisibility(View.VISIBLE);
    guestIntro.setVisibility(View.VISIBLE);
    boolean show = !WorkState.update && (WorkState.guestLooking || WorkState.guestOffers != null);
    guestBox.setVisibility(show ? View.VISIBLE : View.GONE);
    if (!show) return;
    WorkState.guestDefault();
    String info = t("No system is put on the card. You can add one later on the console.");
    guestUpdating = true;
    for (int n = 0; n < guestIds.size(); n++) {
      String id = guestIds.get(n);
      RadioButton button = guestButtons.get(n);
      button.setEnabled(!busy);
      boolean chosen = id.equals(WorkState.guestChoice);
      if (chosen != button.isChecked()) button.setChecked(chosen);
      if ("none".equals(id)) {
        button.setText(t("None"));
        continue;
      }
      JSONObject offer = WorkState.guestOffer(id);
      String state = offer == null ? (WorkState.guestLooking ? "" : "missing") : offer.optString("state");
      String label = id;
      String text;
      if ("too_old".equals(state)) {
        label += " · " + t("needs a newer andr36oid release");
        text = t("This system needs a newer andr36oid release than the one you chose. Choose another system or none, or use a newer release.");
      } else if ("none".equals(state)) {
        label += " · " + t("no release yet");
        text = t("This system has no release yet. Choose another one, or none.");
      } else if ("flasher".equals(state)) {
        label += " · " + t("needs a newer flasher");
        text = t("This system's newest release needs a newer version of this flasher. Update the flasher, or choose another system or none.");
      } else if ("missing".equals(state)) {
        label += " · " + t("not available right now");
        text = t("This system can't be downloaded right now. Choose another one, or none.");
      } else if (offer == null) {
        text = t("Looking for the newest version…");
      } else {
        label += " · " + offer.optString("darkosen") + " · " + humanBytes(offer.optLong("download_bytes"));
        boolean fits = WorkState.guestFitsCard(offer);
        if (chosen) WorkState.guestFits = fits;
        text =
            fits
                ? t(
                    "Download %s. It gets %s of its own at the end of the card and is ready on the"
                        + " console's first start.",
                    humanBytes(offer.optLong("download_bytes")),
                    humanBytes(offer.optLong("partition_bytes")))
                : t("This card is too small for Android and %s together.", id);
      }
      button.setText(label);
      if (chosen) info = text;
    }
    guestUpdating = false;
    guestInfo.setText(info);
  }

  /** What an update says of the game system on the card, "" when it has none. */
  private String guestKept() {
    JSONObject card = WorkState.card;
    JSONObject g = card == null ? null : card.optJSONObject("guest");
    if (g == null) return "";
    String name = g.optString("distribution"), version = g.optString("version");
    if (name.isEmpty()) return t("This card has a game system on it. The update keeps it as it is.");
    if (version.isEmpty()) return t("This card has %s on it. The update keeps it as it is.", name);
    return t("This card has %s (%s) on it. The update keeps it as it is.", name, version);
  }

  /** An error from the engine with its message for the user, translated, first. */
  private String explained(String error) {
    String message = Mobile.explain(I18n.code(this), error);
    return message.isEmpty() ? error : message + "\n\n" + t("Technical details") + ": " + error;
  }

  /** Frees the app's storage: downloads and unpacked files, never recovery backups. */
  private void freeSpace() {
    if (WorkState.busy) return;
    JSONObject usage;
    try {
      usage = new JSONObject(WorkState.engine.cacheUsage());
    } catch (Exception e) {
      showError(e.toString());
      return;
    }
    long free = usage.optLong("free");
    long backups = usage.optLong("backups");
    long count = usage.optLong("backup_count");
    String message =
        t(
            "This deletes the downloaded releases and the unpacked images and game systems (%s)."
                + " You choose the release again afterwards.",
            humanBytes(free));
    if (count > 0)
      message +=
          "\n\n"
              + t(
                  "Your %d recovery backup(s) (%s) are kept.",
                  (int) count,
                  humanBytes(backups));
    new AlertDialog.Builder(this)
        .setTitle(t("Free up space"))
        .setMessage(message)
        .setNegativeButton(t("Cancel"), null)
        .setPositiveButton(
            t("Delete"),
            (d, w) -> {
              if (WorkState.busy) return;
              try {
                WorkState.engine.clearCache();
                File[] imports = getFilesDir().listFiles((dir, name) -> name.startsWith("import-"));
                if (imports != null) for (File f : imports) f.delete();
              } catch (Exception e) {
                showError(e.toString());
              }
              WorkState.clearImage();
              WorkState.clearSystem();
            })
        .show();
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

  /** The last error is the engine's "stopped by the user", not a failure. */
  private boolean stopped() {
    return WorkState.error.startsWith(Mobile.Stopped);
  }

  private void confirmStop() {
    if (!WorkState.busy || !WorkState.cancelable) return;
    if (WorkState.downloading) {
      new AlertDialog.Builder(this)
          .setTitle(t("Stop the download?"))
          .setMessage(t("The game system was not downloaded. Nothing was written to the card."))
          .setNegativeButton(t("Cancel"), null)
          .setPositiveButton(
              t("Stop"),
              (d, w) -> {
                if (WorkState.busy && WorkState.downloading) WorkState.engine.cancel();
              })
          .show();
      return;
    }
    new AlertDialog.Builder(this)
        .setTitle(t("Stop writing?"))
        .setMessage(
            t(
                "The card will be left incomplete and must be written again from the start. An"
                    + " interrupted update keeps its recovery backup."))
        .setNegativeButton(t("Cancel"), null)
        .setPositiveButton(
            t("Stop writing"),
            (d, w) -> {
              if (WorkState.busy && WorkState.cancelable) WorkState.engine.cancel();
            })
        .show();
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
  // All compatibility decisions come from the Go table, also checked by the writer.
  private interface BootChoiceCallback {void accept(JSONObject value);}
  private final class BootOptionsView extends LinearLayout {
    JSONObject choice, context, evaluation=new JSONObject();
 JSONArray resetNotes=new JSONArray();
 String lastContext="",lastOffers="";
    final LinearLayout rows; final Spinner[] picks=new Spinner[3];
    final TextView[] notes=new TextView[3]; final TextView changed;
    final BootChoiceCallback update;
    boolean drawing,unsupported;
    BootOptionsView(JSONObject initial,JSONObject targetContext,BootChoiceCallback callback) {
      super(MainActivity.this);setOrientation(VERTICAL);choice=initial;context=targetContext;update=callback;
      Button toggle=new Button(MainActivity.this);toggle.setText(t("Advanced boot options"));addView(toggle);
      text(this,t("For consoles that don't start or don't wake up. Leave these alone otherwise."));
      changed=text(this,""); rows=new LinearLayout(MainActivity.this);rows.setOrientation(VERTICAL);addView(rows);rows.setVisibility(GONE);
      toggle.setOnClickListener(v->rows.setVisibility(rows.getVisibility()==GONE?VISIBLE:GONE));
      String[] titles={"Bootloader","Sleep firmware","Boot chain"};
      for(int n=0;n<3;n++){
        final int row=n;text(rows,t(titles[n]));picks[n]=new Spinner(MainActivity.this);rows.addView(picks[n]);notes[n]=text(rows,"");
        picks[n].setOnItemSelectedListener(new AdapterView.OnItemSelectedListener(){
          public void onNothingSelected(AdapterView<?> p){}
          public void onItemSelected(AdapterView<?> p,View v,int pos,long id){
            if(drawing)return;BootMenu a=(BootMenu)p.getAdapter();if(!a.isEnabled(pos))return;
            String field=new String[]{"choice","sleep","chain"}[row],key=a.ids.get(pos);
            if(key.equals(choice.optString(field,row==2?"standard":"image")))return;
            try{choice.put(field,key).remove("force_boot_options");check(new String[]{"bootloader","sleep","chain"}[row]);update.accept(choice);refresh(false);}catch(Exception e){showError(e.toString());}
          }
        });
        if(n==1)text(rows,t("Runs while the console sleeps and switches it off."));
        if(n==2)text(rows,t("\"Fully mainline\" replaces the first start-up stage too."));
      }
      text(rows,t("Test builds. If the console doesn't start, flash again with \"Standard\"."));
      refresh(false);
    }
    TextView text(LinearLayout parent,String text){TextView v=new TextView(MainActivity.this);v.setText(text);v.setTextColor(getColor(R.color.body_text));parent.addView(v);return v;}
    JSONObject evaluate(JSONObject c,String changed) throws Exception{
      JSONObject r=new JSONObject().put("choice",c).put("target",context).put("changed",changed);
      if(WorkState.bootOptionsOffer!=null)r.put("rules",WorkState.bootOptionsOffer.optJSONArray("rules"));
      return new JSONObject(WorkState.engine.checkBootOptions(r.toString()));
    }
    void check(String changedRow)throws Exception{
      evaluation=evaluate(choice,changedRow);if(!changedRow.isEmpty())resetNotes=evaluation.optJSONArray("notes");evaluation.put("notes",resetNotes);choice=evaluation.getJSONObject("choice");
      if(!"image".equals(choice.optString("choice","image"))||!"image".equals(choice.optString("sleep","image")))choice.put("chain",choice.optString("chain","standard").isEmpty()?"standard":choice.optString("chain"));
      // Builds are verified again by the helper. Do not keep a stale offer when reset.
      JSONObject build=loaderBuild(choice.optString("choice","image"));choice.remove("build");if(build!=null)choice.put("build",build);
    }
    void refresh(boolean busy){
      try {
        drawing=true;String nowContext=context.toString(),nowOffers=WorkState.bootOptionsOffer==null?"":WorkState.bootOptionsOffer.toString();
 boolean reset=!nowContext.equals(lastContext)||!nowOffers.equals(lastOffers);lastContext=nowContext;lastOffers=nowOffers;String oldChoice=choice.toString();check(reset?"target":"");if(!oldChoice.equals(choice.toString()))update.accept(choice);unsupported=!"sd".equals(context.optString("storage","sd"));
        boolean mainline="mainline".equals(choice.optString("chain"));
        for(int row=0;row<3;row++){
          ArrayList<String> ids=new ArrayList<>(),labels=new ArrayList<>(),why=new ArrayList<>();
          if(row==0){Collections.addAll(ids,"image","standard","new");Collections.addAll(labels,t("As in the image"),t("Standard U-Boot, newest test build"),t("New U-Boot, newest test build"));}
          if(row==1){ids.add("image");labels.add(t("As in the image"));JSONObject cat=WorkState.bootOptionsOffer==null?null:WorkState.bootOptionsOffer.optJSONObject("catalog");JSONArray entries=cat==null?null:cat.optJSONArray("entries");if(entries!=null)for(int i=0;i<entries.length();i++){JSONObject e=entries.getJSONObject(i);ids.add(e.getString("id"));labels.add(t(e.getString("label_key")));}}
          if(row==2){Collections.addAll(ids,"standard","mainline");Collections.addAll(labels,t("Standard"),t("Fully mainline"));}
          String field=new String[]{"choice","sleep","chain"}[row], selected=choice.optString(field,row==2?"standard":"image");
          for(String id:ids){
            JSONObject candidate=new JSONObject(choice.toString()).put(field,id);JSONObject result=evaluate(candidate,new String[]{"bootloader","sleep","chain"}[row]);String reason="";
            JSONArray issues=result.optJSONArray("issues");if(issues!=null)for(int i=0;i<issues.length();i++){JSONObject v=issues.getJSONObject(i);if("impossible".equals(v.optString("level"))){reason=t(v.optString("reason"));break;}}
            if(row==0&&"image".equals(id)&&!context.optBoolean("image_available"))reason=t("Choose the image file to put its bootloader back.");
            if((row==0&&!"image".equals(id)&&loaderBuild(id)==null)||(row==2&&"mainline".equals(id)&&loaderBuild("new")==null))reason=t("Looking for bootloader builds…");
            why.add(reason);
          }
          if(row<2&&mainline){ids=new ArrayList<>(Arrays.asList("image"));labels=new ArrayList<>(Arrays.asList(t("Part of the mainline chain")));why=new ArrayList<>(Arrays.asList(t("Part of the mainline chain")));selected="image";}
          picks[row].setAdapter(new BootMenu(ids,labels,why));picks[row].setSelection(Math.max(0,ids.indexOf(selected)),false);picks[row].setEnabled(!busy&&!unsupported&&!(row<2&&mainline));
          StringBuilder note=new StringBuilder();boolean warning=false;
          for(String list:new String[]{"notes","issues"}){JSONArray a=evaluation.optJSONArray(list);if(a!=null)for(int i=0;i<a.length();i++){JSONObject v=a.getJSONObject(i);if(!new String[]{"bootloader","sleep","chain"}[row].equals(v.optString("row")))continue;if(note.length()>0)note.append('\n');note.append(bootReason(v.optString("reason")));warning|="broken".equals(v.optString("level"))||"impossible".equals(v.optString("level"));}}
          notes[row].setText(note.toString());notes[row].setTextColor(warning?DANGER:getColor(R.color.body_text));
        }
        ArrayList<String> parts=new ArrayList<>();String[] fields={"choice","sleep","chain"},defaults={"image","image","standard"},titles={"Bootloader","Sleep firmware","Boot chain"};
        for(int n=0;n<3;n++){String id=choice.optString(fields[n],defaults[n]);if(!defaults[n].equals(id)){BootMenu menu=(BootMenu)picks[n].getAdapter();int at=menu.ids.indexOf(id);parts.add(t(titles[n])+": "+(at<0?id:menu.getItem(at)));}}
        String s=android.text.TextUtils.join(", ",parts);JSONArray issues=evaluation.optJSONArray("issues");if(!s.isEmpty()&&issues!=null)for(int n=0;n<issues.length();n++){if("untested".equals(issues.getJSONObject(n).optString("level"))){s+=" "+t("(untested combination)");break;}}
        if(!s.isEmpty())s=t("Advanced boot options changed: %s",s);changed.setText(s);changed.setVisibility(s.isEmpty()?GONE:VISIBLE);
      }catch(Exception e){changed.setText(e.toString());}finally{drawing=false;}
    }
    void confirm(BootChoiceCallback action){
      try{check("");StringBuilder bad=new StringBuilder();JSONArray a=evaluation.getJSONArray("issues");for(int i=0;i<a.length();i++){JSONObject v=a.getJSONObject(i);String level=v.getString("level");if("impossible".equals(level)){showError(bootReason(v.getString("reason")));return;}if("broken".equals(level)){if(bad.length()>0)bad.append('\n');bad.append(bootReason(v.getString("reason")));}}
        if(bad.length()==0){action.accept(choice);return;}
        new AlertDialog.Builder(MainActivity.this).setTitle(t("These options don't work together")).setMessage(t("%s\nThe console will probably not start or not wake up with this card.",bad.toString())).setNegativeButton(t("Go back"),null).setPositiveButton(t("Flash anyway"),(d,w)->{try{choice.put("force_boot_options",true);action.accept(choice);}catch(Exception e){showError(e.toString());}}).show();
      }catch(Exception e){showError(e.toString());}
    }
  }
  private String bootReason(String reason){if(reason.startsWith("Reset: doesn't work with "))return t("Reset: doesn't work with %s",reason.substring("Reset: doesn't work with ".length()).replace("mainline",t("Fully mainline")));if(reason.startsWith("Doesn't work with "))return t("Doesn't work with %s.",reason.substring("Doesn't work with ".length(),reason.length()-1));return t(reason);}
  private final class BootMenu extends ArrayAdapter<String>{
    final List<String> ids,why;
    BootMenu(List<String> ids,List<String> labels,List<String> why){super(MainActivity.this,android.R.layout.simple_spinner_item,labels);this.ids=ids;this.why=why;setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);}
    public boolean areAllItemsEnabled(){return false;}public boolean isEnabled(int position){return why.get(position).isEmpty();}
    public View getDropDownView(int pos,View old,ViewGroup parent){TextView v=(TextView)super.getDropDownView(pos,old,parent);v.setEnabled(isEnabled(pos));v.setText(getItem(pos)+(isEnabled(pos)?"":" — "+why.get(pos)));return v;}
  }
  private JSONObject bootContext(JSONObject setup){
    JSONObject c=new JSONObject();try{
      c.put("storage",WorkState.card!=null&&WorkState.card.optBoolean("data_card")?"emmc":"sd").put("image_available",WorkState.image!=null);
      JSONObject facts=WorkState.image==null?null:WorkState.image.optJSONObject("facts");c.put("kernel_line",setup!=null?setup.optString("kernel_line"):facts==null?"":facts.optString("kernel_line"));
      String profile=setup==null?WorkState.profile:setup.optString("profile");c.put("profile",profile);
      JSONArray ps=WorkState.image==null?null:WorkState.image.optJSONArray("profiles");if(ps!=null)for(int i=0;i<ps.length();i++){JSONObject p=ps.getJSONObject(i);if(profile.equals(p.optString("id")))c.put("console",p.optString("console"));}
      if(setup!=null&&setup.optJSONObject("boot_options")!=null)c.put("current_chain",setup.getJSONObject("boot_options").optString("chain"));
    }catch(Exception ignored){}return c;
  }

  private JSONObject loaderBuild(String choice) {
    JSONArray builds=WorkState.bootloaderOffers;
    if(builds!=null) for(int n=0;n<builds.length();n++) {JSONObject b=builds.optJSONObject(n);if(b!=null&&choice.equals(b.optString("choice")))return b;}
    return null;
  }
  private String bootOutcome(JSONObject out){
    if(!out.optString("warning").isEmpty())return t(out.optString("warning"));
    JSONObject o=out.optJSONObject("options");if(o==null)return bootloaderSummary(out.optJSONObject("record"));
    String chain="mainline".equals(o.optString("chain"))?"Fully mainline":"Standard";
    return t("Boot chain")+": "+t(chain)+"; "+t("Sleep firmware")+": "+("image".equals(o.optString("sleep"))?t("As in the image"):o.optString("sleep"));
  }
  private String bootloaderSummary(JSONObject record) {
 if(record==null&&bootOptionsView!=null&&!bootOptionsView.changed.getText().toString().isEmpty())return bootOptionsView.changed.getText().toString();
    String choice=record==null?WorkState.bootloaderChoice:record.optString("choice");
    JSONObject build=record==null?loaderBuild(choice):record;
    if(build!=null&&("standard".equals(choice)||"new".equals(choice)))
      return "new".equals(choice)?t("Bootloader: New U-Boot %s (%s)",build.optString("version"),build.optString("tag")):t("Bootloader: Standard U-Boot %s (%s)",build.optString("version"),build.optString("tag"));
    return t("Bootloader: as in the image");
  }
  private void renderBootloader(boolean busy){if(bootOptionsView!=null){bootOptionsView.context=bootContext(null);bootOptionsView.refresh(busy);}}
  private void showBootloaderCard(){
    JSONObject info=WorkState.bootloaderInfo;WorkState.bootloaderInfo=null;UsbCard card=WorkState.bootloaderTarget;
    JSONObject layout=info.optJSONObject("card"),setup=info.optJSONObject("setup");
    try{
      JSONObject context=info.optJSONObject("boot_target");if(context==null)context=bootContext(setup);context.put("image_available",WorkState.image!=null);context.put("storage",layout==null||layout.optBoolean("data_card")?"emmc":"sd");
      JSONObject initial=new JSONObject().put("choice","image").put("chain","standard").put("sleep","image");
      BootOptionsView view=new BootOptionsView(initial,context,c->{});
      new AlertDialog.Builder(this).setTitle(t("Boot options")).setView(view).setNegativeButton(t("Cancel"),null).setNeutralButton(t("Choose image from disk…"),(d,w)->pick(1)).setPositiveButton(t("Apply"),(d,w)->view.confirm(choice->{
        WorkState.start(getApplicationContext(),true,()->{try(UsbCard.Connection disk=card.open(getApplicationContext())){
          JSONObject req=new JSONObject().put("identity",disk.identity()).put("fingerprint",layout.getString("fingerprint")).put("build",info.getString("build_identity")).put("bootloader",choice);
          JSONObject out=new JSONObject(WorkState.engine.applyBootloader(disk,req.toString(),WorkState::event));
          WorkState.note=bootOutcome(out);WorkState.message="Ready";
        }});
      })).show();
    }catch(Exception e){showError(e.toString());}
    WorkState.lookupBootloaders();
  }

  private void showDTBSource(){
    JSONObject info=WorkState.dtbCardInfo;if(info==null)return;
    LinearLayout box=new LinearLayout(this);box.setPadding(dp(24),dp(8),dp(24),0);box.setOrientation(LinearLayout.VERTICAL);
    TextView intro=new TextView(this);intro.setText(t("Device trees tell the system which screen, buttons and sound chip your console has. A newer set can fix or add support for a console without writing the whole card again. Your games, saves and settings stay."));box.addView(intro);
    TextView card=new TextView(this);card.setText(t("Card")+": "+WorkState.dtbTarget.label());box.addView(card);
    RadioGroup rows=new RadioGroup(this);box.addView(rows);List<String> modes=new ArrayList<>();modes.add("online");modes.add("file");
    JSONObject setup=info.optJSONObject("setup");if(setup!=null&&setup.optJSONArray("history")!=null&&setup.optJSONArray("history").length()>0)modes.add("restore");
    String[] labels={"Look for the set that belongs to this card online","Use a file I downloaded","Put the previous files back"};
    final String[] selected={WorkState.dtbFile.isEmpty()?"online":"file"};
    android.net.ConnectivityManager connectivity=(android.net.ConnectivityManager)getSystemService(CONNECTIVITY_SERVICE);
    if(WorkState.dtbFile.isEmpty()&&(connectivity==null||connectivity.getActiveNetworkInfo()==null||!connectivity.getActiveNetworkInfo().isConnected()))selected[0]="file";
    for(int n=0;n<modes.size();n++){final String mode=modes.get(n);RadioButton row=new RadioButton(this);row.setId(View.generateViewId());row.setText(t(labels[n]));rows.addView(row);row.setChecked(mode.equals(selected[0]));row.setOnClickListener(v->selected[0]=mode);}
    TextView file=new TextView(this);file.setText(WorkState.dtbFile.isEmpty()?"":new File(WorkState.dtbFile).getName());box.addView(file);
    AlertDialog dialog=new AlertDialog.Builder(this).setTitle(t("Update device trees")).setView(box).setNegativeButton(t("Cancel"),null)
      .setNeutralButton(t("Choose file..."),(d,w)->pick(6)).setPositiveButton(t("Continue"),null).create();
    dialog.setOnShowListener(d->{Button next=dialog.getButton(AlertDialog.BUTTON_POSITIVE);Runnable ready=()->next.setEnabled(!"file".equals(selected[0])||!WorkState.dtbFile.isEmpty());
      rows.setOnCheckedChangeListener((g,id)->{int n=rows.indexOfChild(g.findViewById(id));if(n>=0)selected[0]=modes.get(n);ready.run();});ready.run();
      next.setOnClickListener(v->{try{JSONObject layout=info.getJSONObject("card");JSONObject req=new JSONObject().put("identity",WorkState.dtbIdentity).put("fingerprint",layout.getString("fingerprint")).put("build",info.getString("build_identity"))
        .put("file",WorkState.dtbFile).put("online","online".equals(selected[0])).put("restore","restore".equals(selected[0])).put("options",new JSONObject());dialog.dismiss();runDTBRequest(req,false);}catch(Exception e){showError(e.toString());}});
    });dialog.show();
  }
  private void runDTBRequest(JSONObject req,boolean apply){
    WorkState.dtbRequest=req.toString();UsbCard target=WorkState.dtbTarget;Context app=getApplicationContext();
    WorkState.start(app,true,()->{try(UsbCard.Connection disk=target.open(app)){
      if(apply){JSONObject result=new JSONObject(WorkState.engine.applyDTBPack(disk,req.toString(),WorkState::event));boolean restore=req.optBoolean("restore");
        WorkState.message=result.optBoolean("already")?"This card already has these device trees.":restore?"Previous device trees are back":"Device trees updated";
        WorkState.note=result.optBoolean("already")?"This card already has these device trees.":result.optBoolean("screen_changed")?Mobile.dtbScreenSuccess():restore?"The card starts with the files it had before the update.":"Put the card back into the console and start it. If the picture or the buttons are wrong afterwards, open this screen again and choose \"Put the previous files back\".";
        WorkState.dtbMessageTitle=WorkState.message;WorkState.dtbMessageBody=WorkState.note;WorkState.dtbMessagePending=true;
      }else{WorkState.dtbPreview=new JSONObject(WorkState.engine.dtbPlan(disk,req.toString()));WorkState.message="Ready";}
    }catch(Exception e){String reason=e.getMessage()==null?e.toString():e.getMessage();
      if(apply&&!reason.contains("device tree write and rollback failed"))reason=t("Writing stopped: %s. The card still starts with its previous files.",t(reason));
      WorkState.dtbMessageTitle="Update device trees";WorkState.dtbMessageBody=reason;WorkState.dtbMessagePending=true;WorkState.message="Ready";
    }});
  }
  private void showDTBPreview(JSONObject preview){
    try{
      JSONObject plan=preview.getJSONObject("plan");
      JSONObject pending=new JSONObject(WorkState.dtbRequest);
      if(pending.optBoolean("restore")){confirmDTBScreen(plan,pending);return;}
      JSONObject pack=preview.getJSONObject("pack"),setup=plan.getJSONObject("setup");
      if(plan.optBoolean("already")&&!plan.optBoolean("manual")){dtbInfo(t("Update device trees"),t("This card already has these device trees."));return;}
      JSONObject req=new JSONObject(WorkState.dtbRequest).put("file",pack.getString("path")).put("manifest",pack.optString("manifest_path")).put("online",false);
      String current=plan.optString("profile").isEmpty()?t("default console (no profile chosen)"):t(plan.optBoolean("changed")?"files change":"no change");
      String other=plan.optInt("updated")==0&&plan.optInt("added")==0?t("nothing to update"):t("%d updated, %d new",plan.optInt("updated"),plan.optInt("added"));
      LinearLayout box=new LinearLayout(this);box.setPadding(dp(24),dp(8),dp(24),0);box.setOrientation(LinearLayout.VERTICAL);
      TextView details=new TextView(this);details.setText(t("Card")+": "+setup.optString("build")+", "+setup.optString("kernel_line")+"\n"+t("Your console")+": "+current+"\n"+t("Other consoles")+": "+other+"\n\n"+t("The files in use now are kept on the card, so this can be undone."));box.addView(details);
      if(setup.optInt("manifest_revision")>0&&pack.getJSONObject("manifest").optInt("revision")>setup.optInt("manifest_revision")){TextView newer=new TextView(this);newer.setText(t("A newer set exists."));box.addView(newer);}
      Button console=new Button(this);console.setText(t("Console")+": "+plan.optString("profile"));box.addView(console);
      AlertDialog dialog=new AlertDialog.Builder(this).setTitle(t("Device trees for %s",pack.getJSONObject("manifest").getString("version"))+" · r"+pack.getJSONObject("manifest").optInt("revision")).setView(box).setNegativeButton(t("Cancel"),null)
        .setPositiveButton(t("Apply"),(d,w)->{if(plan.optBoolean("manual"))new AlertDialog.Builder(this).setTitle(t("The console files on this card were changed by hand"))
          .setMessage(t("The files this card starts with are not the ones its console profile normally uses. Applying the set replaces them with the standard files for %s. The current files are kept on the card.",plan.optString("profile")))
          .setNegativeButton(t("Cancel"),null).setPositiveButton(t("Replace them"),(x,y)->{try{req.getJSONObject("options").put("replace_anyway",true);confirmDTBScreen(plan,req);}catch(Exception e){showError(e.toString());}}).show();else confirmDTBScreen(plan,req);}).create();
      console.setOnClickListener(v->showDTBProfiles(plan,req,dialog));dialog.show();

    }catch(Exception e){showError(e.toString());}
  }

  private void confirmDTBScreen(JSONObject plan,JSONObject req){
    if(!plan.optBoolean("screen_changed")){runDTBRequest(req,true);return;}
    new AlertDialog.Builder(this).setTitle(t("Watch the screen at the first start")).setMessage(t(Mobile.dtbScreenWarning()))
      .setNegativeButton(t("Cancel"),null).setPositiveButton(t("I'll watch the screen"),(d,w)->{try{req.getJSONObject("options").put("screen_accepted",true);runDTBRequest(req,true);}catch(Exception e){showError(e.toString());}}).show();
  }
  private void showDTBProfiles(JSONObject plan,JSONObject req,AlertDialog previous){
    JSONArray profiles=plan.optJSONArray("profiles");if(profiles==null)return;
    JSONObject setup=plan.optJSONObject("setup");String current=setup==null?"":setup.optString("profile");String console="";
    for(int n=0;n<profiles.length();n++){JSONObject p=profiles.optJSONObject(n);if(p!=null&&current.equals(p.optString("id")))console=p.optString("console");}
    List<JSONObject> known=new ArrayList<>(),all=new ArrayList<>();for(int n=0;n<profiles.length();n++){JSONObject p=profiles.optJSONObject(n);if(p==null)continue;all.add(p);boolean fits=!console.isEmpty()&&console.equals(p.optString("console"));JSONArray aliases=p.optJSONArray("also_for");if(aliases!=null&&!console.isEmpty())for(int a=0;a<aliases.length();a++)if(console.equals(aliases.optString(a)))fits=true;if(fits)known.add(p);}
    showDTBProfileGroup(known,all,req,previous,false);
  }
  private void showDTBProfileGroup(List<JSONObject> choices,List<JSONObject> all,JSONObject req,AlertDialog previous,boolean expanded){
    String[] names=new String[choices.size()];for(int n=0;n<names.length;n++){JSONObject p=choices.get(n);names[n]=p.optString("name")+(p.optBoolean("experimental")?" ("+t("experimental")+")":"");}
    AlertDialog.Builder dialog=new AlertDialog.Builder(this).setTitle(t(expanded?"All screens":"Known to fit this console"));
    if(expanded){TextView header=new TextView(this);header.setText(t("All screens")+"\n"+t("Only choose from here if you know which screen your console has."));header.setPadding(dp(24),dp(16),dp(24),dp(8));dialog.setCustomTitle(header);}
    dialog.setItems(names,(d,n)->{try{req.getJSONObject("options").put("profile",choices.get(n).getString("id"));previous.dismiss();runDTBRequest(req,false);}catch(Exception e){showError(e.toString());}}).setNegativeButton(t("Cancel"),null);
    if(!expanded)dialog.setNeutralButton(t("Show all screens"),(d,w)->showDTBProfileGroup(all,all,req,previous,true));dialog.show();
  }

  private void dtbInfo(String title,String message){new AlertDialog.Builder(this).setTitle(title).setMessage(message).setPositiveButton(t("Close"),null).show();}
  private void withDTBFreshScreen(Runnable run){
    JSONObject profile=selectedProfile();if(profile==null||!profile.optBoolean("dtb_screen_changed")||!WorkState.custom.isEmpty()){run.run();return;}
    new AlertDialog.Builder(this).setTitle(t("Watch the screen at the first start")).setMessage(t(Mobile.dtbScreenWarning())).setNegativeButton(t("Cancel"),null).setPositiveButton(t("I'll watch the screen"),(d,w)->run.run()).show();
  }

}
