package io.github.andr36oid.sdflasher;

import android.content.Context;
import android.os.Build;
import io.github.andr36oid.bindings.mobile.Mobile;
import java.util.Locale;

final class I18n {
  static final String[] CODES = {
    "en", "de", "ru", "uk", "es", "pt", "pt-BR", "hi", "ja", "ko", "zh-Hans"
  };
  static final String[] NAMES = {
    "🇬🇧 English",
    "🇩🇪 Deutsch",
    "🇷🇺 Русский",
    "🇺🇦 Українська",
    "🇪🇸 Español",
    "🇵🇹 Português",
    "🇧🇷 Português brasileiro",
    "🇮🇳 हिन्दी",
    "🇯🇵 日本語",
    "🇰🇷 한국어",
    "🇨🇳 简体中文"
  };

  static String code(Context c) {
    String saved = c.getSharedPreferences("settings", 0).getString("language", "");
    if (!saved.isEmpty()) return Mobile.resolveLocale(saved);
    Locale system =
        Build.VERSION.SDK_INT >= 24
            ? c.getResources().getConfiguration().getLocales().get(0)
            : c.getResources().getConfiguration().locale;
    return Mobile.resolveLocale(system.toLanguageTag());
  }

  static String text(Context c, String key, Object... args) {
    String value = Mobile.translate(code(c), key);
    return args.length == 0 ? value : String.format(Locale.getDefault(), value, args);
  }

  static String name(Context c) {
    String code = code(c);
    for (int n = 0; n < CODES.length; n++) if (CODES[n].equals(code)) return NAMES[n];
    return NAMES[0];
  }
}
