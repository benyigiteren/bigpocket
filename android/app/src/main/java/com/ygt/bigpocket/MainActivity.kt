package com.ygt.bigpocket

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.core.content.IntentCompat
import com.ygt.bigpocket.services.IncomingShare
import com.ygt.bigpocket.services.ShareInbox
import com.ygt.bigpocket.theme.BigPocketTheme

class MainActivity : ComponentActivity() {
  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)

    handleIncomingShare(intent)

    enableEdgeToEdge()
    setContent {
      BigPocketTheme {
        Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
          MainNavigation()
          com.ygt.bigpocket.update.UpdatePrompt()
        }
      }
    }
  }

  override fun onNewIntent(intent: Intent) {
    super.onNewIntent(intent)
    setIntent(intent)
    handleIncomingShare(intent)
  }

  /**
   * Receives "Paylaş → BigPocket ile PC'ye Gönder" payloads and hands them to the Compose tree,
   * which forwards the text/file to the connected PC.
   */
  private fun handleIncomingShare(intent: Intent?) {
    if (intent == null || intent.action != Intent.ACTION_SEND) return
    val sharedText = intent.getStringExtra(Intent.EXTRA_TEXT)
    val sharedStream = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
    if (sharedText.isNullOrBlank() && sharedStream == null) return
    ShareInbox.pending = IncomingShare(
      text = sharedText,
      uri = sharedStream?.toString()
    )
  }

  override fun onStop() {
    super.onStop()
    val prefs = getSharedPreferences("bigpocket_prefs", android.content.Context.MODE_PRIVATE)
    val bgCamera = prefs.getBoolean("bg_camera_enabled", true)
    val bgMic = prefs.getBoolean("bg_mic_enabled", true)
    val bgScreen = prefs.getBoolean("bg_screen_enabled", false)
    val bgService = prefs.getBoolean("bg_service_enabled", true)

    // Stop streams only if the user explicitly chose NOT to run them in the background
    if (!bgCamera) {
      com.ygt.bigpocket.services.SocketManager.stopCameraStream()
    }
    if (!bgMic) {
      com.ygt.bigpocket.services.SocketManager.stopAudioStream()
    }
    if (!bgScreen) {
      com.ygt.bigpocket.media.ScreenReceiver.stop()
    }

    val isMicRunning = com.ygt.bigpocket.services.SocketManager.isAudioStreaming
    val isCamRunning = com.ygt.bigpocket.services.SocketManager.isCameraStreaming
    if (bgService && (isMicRunning || isCamRunning || com.ygt.bigpocket.services.SocketManager.isConnected)) {
      com.ygt.bigpocket.services.BigPocketService.start(this, isMicRunning, isCamRunning)
    }
  }
  override fun onDestroy() {
    super.onDestroy()
    com.ygt.bigpocket.services.SocketManager.disconnect()
  }
}
