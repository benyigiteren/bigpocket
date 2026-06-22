package com.ygt.bigpocket.services

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.util.Log
import org.json.JSONObject

class NotificationReceiver : NotificationListenerService() {
    private val TAG = "NotificationReceiver"

    override fun onNotificationPosted(sbn: StatusBarNotification) {
        if (!SocketManager.isConnected) return

        try {
            val packageName = sbn.packageName
            // Skip system or our own notifications to avoid recursion
            if (packageName == "android" || packageName == "com.android.systemui" || packageName == applicationContext.packageName) {
                return
            }

            val extras = sbn.notification.extras
            val title = extras.getString(Notification.EXTRA_TITLE) ?: ""
            val text = extras.getCharSequence(Notification.EXTRA_TEXT)?.toString() ?: ""

            if (title.isEmpty() && text.isEmpty()) return

            // Get app label
            val pm = packageManager
            val appLabel = try {
                val ai = pm.getApplicationInfo(packageName, 0)
                pm.getApplicationLabel(ai).toString()
            } catch (e: Exception) {
                packageName
            }

            val json = JSONObject().apply {
                put("type", "notification")
                put("app", appLabel)
                put("title", title)
                put("text", text)
            }

            Log.d(TAG, "Sending notification from $appLabel: $title - $text")
            SocketManager.sendControl(json)
        } catch (e: Exception) {
            Log.e(TAG, "Error processing notification", e)
        }
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification) {
        // Optional: handle notification removal if needed
    }
}

