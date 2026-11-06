package io.github.andr36oid.sdflasher;

import android.app.*;
import android.content.*;
import android.content.pm.ServiceInfo;
import android.os.*;

public final class FlashService extends Service {
  private static final String CHANNEL = "card-operations";
  private PowerManager.WakeLock wake;
  private boolean running;

  @Override
  public int onStartCommand(Intent intent, int flags, int startId) {
    if (running) return START_NOT_STICKY;
    WorkState.Work job = WorkState.pending;
    if (job == null) {
      stopSelf();
      return START_NOT_STICKY;
    }
    running = true;
    NotificationManager manager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
    if (Build.VERSION.SDK_INT >= 26)
      manager.createNotificationChannel(
          new NotificationChannel(
              CHANNEL, I18n.text(this, "Card operations"), NotificationManager.IMPORTANCE_LOW));
    Intent open = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
    PendingIntent content =
        PendingIntent.getActivity(
            this, 0, open, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    Notification.Builder builder =
        Build.VERSION.SDK_INT >= 26
            ? new Notification.Builder(this, CHANNEL)
            : new Notification.Builder(this);
    Notification notification =
        builder
            .setSmallIcon(R.drawable.ic_card)
            .setContentTitle("andr36oid SD Flasher")
            .setContentText(
                I18n.text(this, "Keep the card connected until the operation finishes."))
            .setContentIntent(content)
            .setOngoing(true)
            .build();
    if (Build.VERSION.SDK_INT >= 29)
      startForeground(
          1,
          notification,
          WorkState.usbJob
              ? ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE
              : ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
    else startForeground(1, notification);
    wake =
        ((PowerManager) getSystemService(POWER_SERVICE))
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, getPackageName() + ":card-operation");
    wake.acquire(6 * 60 * 60 * 1000L);
    new Thread(
            () -> {
              try {
                job.run();
              } catch (Throwable e) {
                // Throwable, not Exception: a StackOverflowError or OutOfMemoryError from
                // the USB stack must end the job with a message, not kill the app.
                WorkState.error = e.getMessage() == null ? e.toString() : e.getMessage();
              } finally {
                WorkState.cancelable = false;
                WorkState.stall = 0;
                new Handler(Looper.getMainLooper())
                    .post(
                        () -> {
                          if (wake.isHeld()) wake.release();
                          stopForeground(true);
                          stopSelf(startId);
                          running = false;
                          WorkState.pending = null;
                          WorkState.busy = false;
                          WorkState.revision++;
                        });
              }
            },
            "card-operation")
        .start();
    return START_NOT_STICKY;
  }

  @Override
  public void onTimeout(int startId, int foregroundServiceType) {
    // Android 15 limits background data-sync time. Card writes use the
    // connected-device service type and are not subject to that limit.
    if (!WorkState.usbJob) {
      stopForeground(true);
      stopSelf(startId);
    }
  }

  @Override
  public IBinder onBind(Intent intent) {
    return null;
  }

  @Override
  public void onDestroy() {
    if (wake != null && wake.isHeld() && !WorkState.busy) wake.release();
    super.onDestroy();
  }
}
