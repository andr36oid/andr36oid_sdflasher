package io.github.andr36oid.sdflasher;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;
import java.util.Arrays;
import org.json.*;

final class DiskMap extends View {
  // How much of the map the written image and preserved partitions keep when a big
  // unassigned tail is squashed into the "assigned on first boot" band.
  private static final double CONTENT_FRAC = 0.7;

  private final int[] cells = new int[36 * 14];
  private final int[] colors = {
    0xffeeeeee, 0xffaaaaaa, 0xff00cccc, 0xff163caf, 0xffed2020, 0xff0076ff, 0xff60aa6e
  };
  private final Paint paint = new Paint();
  private long size;
  private long contentEnd; // start of the unassigned tail; 0 when there is nothing to squash
  private boolean squash;
  private JSONObject currentPlan;

  DiskMap(Context context) {
    super(context);
    colors[0] = context.getColor(R.color.map_unused);
    colors[1] = context.getColor(R.color.map_pending);
    setMinimumHeight((int) (140 * getResources().getDisplayMetrics().density));
  }

  // span maps a byte offset to a fractional cell position. When a large unassigned
  // tail is present it is compressed into the last 1-CONTENT_FRAC of the cells, so the
  // real partitions near the start stay visible instead of a tiny sliver.
  private double span(long off) {
    int n = cells.length;
    if (size <= 0) return 0;
    if (off < 0) off = 0;
    if (off > size) off = size;
    if (!squash) return (double) off / size * n;
    double content = n * CONTENT_FRAC;
    if (off <= contentEnd) return (double) off / contentEnd * content;
    return content + (double) (off - contentEnd) / (size - contentEnd) * (n - content);
  }

  private void mark(long off, long length, int state) {
    if (size <= 0 || length <= 0) return;
    int start = (int) Math.max(0, Math.floor(span(off)));
    int end = (int) Math.min(cells.length, Math.ceil(span(off + length)));
    for (int n = start; n < end; n++) if (cells[n] != 2) cells[n] = state;
  }

  void update(JSONObject plan, JSONObject progress) {
    if (plan != null && plan != currentPlan) {
      currentPlan = plan;
      size = plan.optLong("size");
      // The content extent is the furthest real content, ignoring the tiny backup
      // partition table pinned to the very end of the card.
      contentEnd = 0;
      for (String type : new String[] {"writes", "protected"}) {
        JSONArray ranges = plan.optJSONArray(type);
        if (ranges == null) continue;
        for (int n = 0; n < ranges.length(); n++) {
          JSONObject range = ranges.optJSONObject(n);
          if (range == null) continue;
          if (type.equals("writes") && "Backup partition table".equals(range.optString("name")))
            continue;
          long end = range.optLong("offset") + range.optLong("length");
          if (end > contentEnd) contentEnd = end;
        }
      }
      // Only squash when the unassigned tail is a meaningful slice of the card; on an
      // update the preserved userdata already fills the end, so nothing is squashed.
      squash = contentEnd > 0 && size - contentEnd > size / 8;
      Arrays.fill(cells, 0);
      if (squash) mark(contentEnd, size - contentEnd, 6);
      for (String type : new String[] {"writes", "protected"}) {
        JSONArray ranges = plan.optJSONArray(type);
        if (ranges == null) continue;
        for (int n = 0; n < ranges.length(); n++) {
          JSONObject range = ranges.optJSONObject(n);
          if (range != null)
            mark(range.optLong("offset"), range.optLong("length"), type.equals("writes") ? 1 : 2);
        }
      }
    }
    if (progress != null) {
      String phase = progress.optString("phase");
      long off = progress.optLong("offset"), length = progress.optLong("length");
      if (phase.equals("writing")) {
        mark(off, length, 5);
        long block = Math.max(1, size / cells.length);
        mark(off + length - block, block, 4);
      }
      if (phase.equals("verifying")) mark(off, length, 3);
    }
    invalidate();
  }

  @Override
  protected void onDraw(Canvas c) {
    super.onDraw(c);
    float w = getWidth() / 36f, h = getHeight() / 14f;
    for (int n = 0; n < cells.length; n++) {
      paint.setColor(colors[cells[n]]);
      float x = (n % 36) * w, y = (n / 36) * h;
      c.drawRect(x + 1, y + 1, x + w - 1, y + h - 1, paint);
    }
  }
}
