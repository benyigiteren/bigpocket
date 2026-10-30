package com.ygt.bigpocket.services

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.ygt.bigpocket.MainActivity
import com.ygt.bigpocket.R

/**
 * Foreground service that keeps the BigPocket link to the PC alive while the app runs in the
 * background, and that keeps live camera / microphone streaming from being frozen by Doze.
 *
 * It holds a partial [PowerManager.WakeLock] plus a high-performance [WifiManager.WifiLock],
 * publishes the "BigPocket PC'ye Bağlı" status in the notification shade and offers a
 * "Bağlantıyı Kes" quick action that drops the socket without opening the app.
 */
class BigPocketService : Service() {

    companion object {
        private const val TAG = "BigPocketService"
        private const val CHANNEL_ID = "bigpocket_connection"
        private const val NOTIFICATION_ID = 4711

        /**
         * Safety cap for the partial wake lock. A held lock with no timeout keeps draining the
         * battery indefinitely if the service is ever left running; four hours comfortably
         * covers a long session while still guaranteeing the lock is released.
         */
        private const val WAKE_LOCK_TIMEOUT_MS = 4 * 60 * 60 * 1000L

        const val ACTION_START = "com.ygt.bigpocket.action.START_FOREGROUND"
        const val ACTION_STOP = "com.ygt.bigpocket.action.STOP_FOREGROUND"

        private const val EXTRA_MIC = "extra_mic_streaming"
        private const val EXTRA_CAMERA = "extra_camera_streaming"

        /**
         * Brings the service up (or refreshes its notification) so the PC link and any running
         * stream survive the app being sent to the background.
         */
        fun start(context: Context, micStreaming: Boolean, cameraStreaming: Boolean) {
            val intent = Intent(context, BigPocketService::class.java).apply {
                action = ACTION_START
                putExtra(EXTRA_MIC, micStreaming)
                putExtra(EXTRA_CAMERA, cameraStreaming)
            }
            try {
                ContextCompat.startForegroundService(context, intent)
            } catch (e: Exception) {
                Log.w(TAG, "Could not start foreground service", e)
            }
        }

        fun stop(context: Context) {
            try {
                context.stopService(Intent(context, BigPocketService::class.java))
            } catch (e: Exception) {
                Log.w(TAG, "Could not stop foreground service", e)
            }
        }
    }

    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var isStreaming = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            Log.d(TAG, "Disconnect requested from the notification action")
            SocketManager.disconnect()
            releaseLocks()
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
            return START_NOT_STICKY
        }

        val micStreaming = intent?.getBooleanExtra(EXTRA_MIC, false) ?: false
        val cameraStreaming = intent?.getBooleanExtra(EXTRA_CAMERA, false) ?: false
        isStreaming = micStreaming || cameraStreaming

        acquireLocks()
        startForegroundSafely(micStreaming, cameraStreaming)
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        releaseLocks()
        super.onDestroy()
    }

    private fun startForegroundSafely(micStreaming: Boolean, cameraStreaming: Boolean) {
        val notification = buildNotification()
        try {
            ServiceCompat.startForeground(
                this,
                NOTIFICATION_ID,
                notification,
                foregroundServiceType(micStreaming, cameraStreaming)
            )
        } catch (e: Exception) {
            // A camera/microphone foreground type is rejected when the app is not in the
            // foreground (or the runtime permission was revoked); the plain device link stays.
            Log.e(TAG, "startForeground with media types failed, retrying as device link", e)
            try {
                ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, baseForegroundServiceType())
            } catch (inner: Exception) {
                Log.e(TAG, "startForeground failed", inner)
                releaseLocks()
                stopSelf()
            }
        }
    }

    private fun baseForegroundServiceType(): Int =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE
        } else {
            0
        }

    private fun foregroundServiceType(micStreaming: Boolean, cameraStreaming: Boolean): Int {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return 0
        var type = ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE
        if (micStreaming && hasPermission(Manifest.permission.RECORD_AUDIO)) {
            type = type or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
        }
        if (cameraStreaming && hasPermission(Manifest.permission.CAMERA)) {
            type = type or ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
        }
        return type
    }

    private fun hasPermission(permission: String): Boolean =
        ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED

    private fun buildNotification(): Notification {
        ensureChannel()

        val openAppIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java).apply {
                action = Intent.ACTION_MAIN
                addCategory(Intent.CATEGORY_LAUNCHER)
                flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
            },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val disconnectIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, BigPocketService::class.java).apply { action = ACTION_STOP },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val title = getString(
            if (isStreaming) R.string.notif_streaming_title else R.string.notif_connected_title
        )
        val text = getString(
            if (isStreaming) R.string.notif_streaming_text else R.string.notif_connected_text
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(openAppIntent)
            .setOngoing(true)
            .setShowWhen(false)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .addAction(0, getString(R.string.notif_disconnect_action), disconnectIntent)
            .build()
    }

    private fun ensureChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager ?: return
        if (manager.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notif_channel_name),
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = getString(R.string.notif_channel_desc)
            setShowBadge(false)
        }
        manager.createNotificationChannel(channel)
    }

    @Suppress("DEPRECATION")
    private fun acquireLocks() {
        if (wakeLock == null) {
            val powerManager = getSystemService(Context.POWER_SERVICE) as? PowerManager
            wakeLock = powerManager?.newWakeLock(
                PowerManager.PARTIAL_WAKE_LOCK,
                "$packageName:bigpocket-link"
            )?.apply {
                setReferenceCounted(false)
                try {
                    // A 4-hour cap stops the partial wake lock from pinning the CPU forever
                    // (and draining the battery) if the link is never torn down.
                    acquire(WAKE_LOCK_TIMEOUT_MS)
                } catch (e: Exception) {
                    Log.w(TAG, "WakeLock acquire failed", e)
                }
            }
        }
        if (wifiLock == null) {
            val wifiManager = applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
            wifiLock = wifiManager?.createWifiLock(
                WifiManager.WIFI_MODE_FULL_HIGH_PERF,
                "$packageName:bigpocket-wifi"
            )?.apply {
                setReferenceCounted(false)
                try {
                    acquire()
                } catch (e: Exception) {
                    Log.w(TAG, "WifiLock acquire failed", e)
                }
            }
        }
    }

    private fun releaseLocks() {
        try {
            wakeLock?.takeIf { it.isHeld }?.release()
        } catch (e: Exception) {
            Log.w(TAG, "WakeLock release failed", e)
        }
        wakeLock = null
        try {
            wifiLock?.takeIf { it.isHeld }?.release()
        } catch (e: Exception) {
            Log.w(TAG, "WifiLock release failed", e)
        }
        wifiLock = null
    }
}
