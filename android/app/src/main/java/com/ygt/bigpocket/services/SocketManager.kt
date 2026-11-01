package com.ygt.bigpocket.services

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import okhttp3.*
import org.json.JSONObject
import java.io.OutputStream
import java.net.Socket
import java.nio.ByteBuffer
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

object SocketManager {
    private const val TAG = "SocketManager"

    /**
     * Safety net for a socket that opens but never finishes the auth handshake (half-open TCP
     * connection after a Wi-Fi drop, server version mismatch, ...). The desktop answers an
     * unauthenticated socket after 5s, so this only fires when the link is truly wedged.
     */
    private const val CONNECT_TIMEOUT_MS = 12_000L

    // The keep-alive ping surfaces dead ("zombie") sockets within 15 seconds, which is what
    // triggers the automatic reconnection below.
    private val client = OkHttpClient.Builder()
        .pingInterval(15, TimeUnit.SECONDS)
        .connectTimeout(8, TimeUnit.SECONDS)
        .build()
    private var webSocket: WebSocket? = null

    var connectionStateListener: ((Boolean) -> Unit)? = null
    var clipboardListener: ((String) -> Unit)? = null
    var configUpdateListener: (() -> Unit)? = null
    var buttonStateListener: ((Int, Boolean, Int) -> Unit)? = null
    var pageSwitchedListener: ((Int) -> Unit)? = null
    var volumeChangedListener: ((String, Float) -> Unit)? = null
    var brightnessChangedListener: ((Int) -> Unit)? = null
    var systemStatsListener: ((Int, Int) -> Unit)? = null
    var detailedStatsListener: ((Int, Int, Int, Double, Double) -> Unit)? = null
    var networkStatsListener: ((Int, Int) -> Unit)? = null
    var extraStatsListener: ((SystemExtraStats) -> Unit)? = null
    var flowMessageListener: ((JSONObject) -> Unit)? = null
    var flowDeletedListener: ((String) -> Unit)? = null
    var flowClearedListener: (() -> Unit)? = null
    // Read from the OkHttp callback threads, the UI and the streaming threads.
    @Volatile
    var isConnected = false
        private set

    @Volatile
    var isConnecting = false
        private set

    /**
     * Set when the PC rejected the password (or temporarily blocked this phone after too many
     * attempts). While true neither the backoff loop nor UDP auto-discovery may retry, otherwise
     * the phone would keep hammering a rejected password and stay banned by the server's
     * brute-force guard. Cleared by the next explicit [connect].
     */
    @Volatile
    var isAuthRejected = false
        private set

    /**
     * Set when the user explicitly dropped the link (in-app button or the notification action) and
     * cleared by the next [connect]. Discovered PCs must not resurrect a link the user just closed.
     */
    @Volatile
    var isUserDisconnected = false
        private set

    @Volatile
    var lastError = ""
        private set

    @Volatile
    var lastErrorFriendly = ""
        private set
    // Automatic reconnection with exponential backoff: 1s, 2s, 4s, then capped at 5s.
    private val reconnectDelaysMs = longArrayOf(1_000L, 2_000L, 4_000L, 5_000L)
    private val mainHandler = Handler(Looper.getMainLooper())
    private var reconnectRunnable: Runnable? = null
    private var connectTimeoutRunnable: Runnable? = null
    @Volatile
    private var reconnectAttempt = 0
    @Volatile
    private var autoReconnect = false

    // Bumped for every new socket and every manual disconnect so stale callbacks are ignored.
    @Volatile
    private var generation = 0

    // Socket generation that already queued a retry, so a socket reporting both onClosed and
    // onFailure can not queue two reconnect attempts at once.
    private var scheduledForGeneration = -1

    private var audioSocket: Socket? = null
    private var audioOutputStream: OutputStream? = null
    private var audioRecord: AudioRecord? = null
    var isAudioStreaming = false
        private set

    private var cameraSocket: Socket? = null
    private var cameraOutputStream: OutputStream? = null
    var isCameraStreaming = false
        private set

    // Written on the main thread, read by the streaming/HTTP worker threads.
    @Volatile
    var currentIp = ""

    @Volatile
    var currentPassword = ""

    fun connect(ip: String, password: String) {
        currentIp = ip
        currentPassword = password
        isAuthRejected = false
        isUserDisconnected = false

        // Tear down the previous session (streams + socket) before opening the new link.
        reconnectAttempt = 0
        autoReconnect = true
        stopAudioStream()
        stopCameraStream()
        closeActiveSocket()
        setDisconnected()

        openSocket()
    }

    /**
     * True while the backoff loop is already retrying [ip]. UDP auto-discovery uses this so a
     * broadcast packet can not restart a running backoff, which would pin the retry delay at its
     * first (1s) step and turn the loop into a reconnect storm.
     */
    fun isRetryingFor(ip: String): Boolean = autoReconnect && ip == currentIp

    /** Opens the WebSocket for [currentIp]; safe to call from the reconnect handler. */
    private fun openSocket() {
        if (currentIp.isEmpty()) return
        // A dead socket must be fully closed before its replacement is opened, otherwise two live
        // sockets overlap and both write to the shared listener state.
        closeActiveSocket()

        val gen = generation
        isConnecting = true

        val request = Request.Builder()
            .url("ws://$currentIp:8085/ws")
            .build()

        webSocket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.d(TAG, "WebSocket opened, waiting for auth challenge")
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                if (gen != generation) return
                try {
                    val json = JSONObject(text)
                    val type = json.optString("type")
                    if (type == "auth_required") {
                        val authMsg = JSONObject().apply {
                            put("type", "auth_login")
                            put("password", currentPassword)
                        }
                        webSocket.send(authMsg.toString())
                    } else if (type == "auth_success") {
                        isConnected = true
                        isConnecting = false
                        isAuthRejected = false
                        lastError = ""
                        lastErrorFriendly = ""
                        reconnectAttempt = 0
                        cancelConnectTimeout()
                        sendControl(JSONObject().apply {
                            put("type", "client_connected")
                        })
                        notifyConnectionState()
                    } else if (type == "auth_failed") {
                        val failMsg = json.optString("message", "Şifre hatalı")
                        Log.e(TAG, "Authentication failed: $failMsg")
                        isConnected = false
                        isConnecting = false
                        isAuthRejected = true
                        lastError = "Kimlik doğrulama başarısız: $failMsg"
                        lastErrorFriendly = "Şifre hatalı veya PC bağlantıyı reddetti."
                        autoReconnect = false
                        cancelReconnect()
                        cancelConnectTimeout()
                        notifyConnectionState()
                        try {
                            webSocket.close(1000, "Auth failed")
                        } catch (e: Exception) {
                            Log.w(TAG, "Error closing rejected socket", e)
                        }
                    } else if (type == "clipboard_sync") {
                        // optString: the PC may send a null/absent text, which getString throws on.
                        val clipText = json.optString("text", "")
                        if (clipText.isNotEmpty()) {
                            clipboardListener?.invoke(clipText)
                        }
                    } else if (type == "flow_new") {
                        val msgObj = json.optJSONObject("message")
                        if (msgObj != null) {
                            flowMessageListener?.invoke(msgObj)
                        }
                    } else if (type == "flow_deleted") {
                        val id = json.optString("id")
                        if (id.isNotEmpty()) {
                            flowDeletedListener?.invoke(id)
                        }
                    } else if (type == "flow_cleared") {
                        flowClearedListener?.invoke()
                    } else if (type == "config_update") {
                        configUpdateListener?.invoke()
                    } else if (type == "button_state_changed") {
                        val btnId = json.optInt("button_id")
                        val state = json.optBoolean("state")
                        val page = json.optInt("page")
                        buttonStateListener?.invoke(btnId, state, page)
                    } else if (type == "page_switched") {
                        val activePage = json.optInt("active_page")
                        pageSwitchedListener?.invoke(activePage)
                    } else if (type == "volume_changed") {
                        val action = json.optString("action")
                        val level = json.optDouble("level", 50.0).toFloat()
                        volumeChangedListener?.invoke(action, level)
                    } else if (type == "brightness_changed") {
                        val level = json.optInt("level", 80)
                        brightnessChangedListener?.invoke(level)
                    } else if (type == "system_stats") {
                        val cpu = json.optInt("cpu")
                        val ram = json.optInt("ram")
                        val temp = json.optInt("cpu_temp", 45)
                        val ramUsed = json.optDouble("ram_used_gb", 0.0)
                        val ramTotal = json.optDouble("ram_total_gb", 0.0)
                        val netDown = json.optInt("net_down_kbps", 0)
                        val netUp = json.optInt("net_up_kbps", 0)
                        val extras = SystemExtraStats(
                            diskPercent = json.optInt("disk_percent", 0),
                            diskFreeGb = json.optDouble("disk_free_gb", 0.0),
                            diskTotalGb = json.optDouble("disk_total_gb", 0.0),
                            gpu = json.optInt("gpu", 0),
                            gpuTemp = json.optInt("gpu_temp", 0),
                            mediaTitle = json.optString("media_title", ""),
                            mediaArtist = json.optString("media_artist", ""),
                            mediaPlaying = json.optBoolean("media_playing", false),
                            mediaCover = json.optString("media_cover", ""),
                            mediaPosSec = json.optInt("media_pos_sec", 0),
                            mediaDurSec = json.optInt("media_dur_sec", 0),
                            mediaIsLive = json.optBoolean("media_is_live", false),
                            weatherCity = json.optString("weather_city", ""),
                            weatherTemp = json.optInt("weather_temp", 0),
                            weatherCode = json.optInt("weather_code", 0),
                            weatherDesc = json.optString("weather_desc", ""),
                            weatherWind = json.optInt("weather_wind", 0)
                        )
                        systemStatsListener?.invoke(cpu, ram)
                        detailedStatsListener?.invoke(cpu, ram, temp, ramUsed, ramTotal)
                        networkStatsListener?.invoke(netDown, netUp)
                        extraStatsListener?.invoke(extras)
                    }
                } catch (e: Exception) {
                    Log.e(TAG, "Error parsing WS message", e)
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (gen != generation) return
                Log.d(TAG, "WebSocket closed: $code $reason")
                cancelConnectTimeout()
                setDisconnected()
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (gen != generation) return
                val rawMsg = t.message ?: t.javaClass.simpleName
                lastError = "$rawMsg (${t.javaClass.simpleName})"
                lastErrorFriendly = when {
                    t is java.net.ConnectException || rawMsg.contains("failed to connect") || rawMsg.contains("ECONNREFUSED") ->
                        "PC'ye bağlanılamadı. BigPocket sunucusunun açık olduğundan ve Windows Güvenlik Duvarı'nın engellemediğinden emin olun."
                    t is java.net.SocketTimeoutException || rawMsg.contains("timed out") ->
                        "Zaman aşımı. Telefon ile PC'nin aynı ağda olduğunu veya USB Tethering'i kontrol edin."
                    rawMsg.contains("EHOSTUNREACH") || rawMsg.contains("No route to host") ->
                        "Ağ rotası bulunamadı. IP adresini ($currentIp) kontrol edin."
                    else ->
                        "Bağlantı hatası: $rawMsg"
                }
                Log.e(TAG, "WebSocket failure: $rawMsg -> $lastErrorFriendly")
                cancelConnectTimeout()
                setDisconnected()
                scheduleReconnect()
            }
        })

        scheduleConnectTimeout(gen)
    }

    /**
     * Retries the last connection with exponential backoff (1s, 2s, 4s, max 5s). Never runs after
     * a deliberate [disconnect] or a rejected password, only after an unexpected drop.
     */
    private fun scheduleReconnect() {
        if (!autoReconnect || currentIp.isEmpty()) return
        // onClosed and onFailure can both report the same dead socket; queue a single retry.
        if (scheduledForGeneration == generation) return
        scheduledForGeneration = generation
        val delayMs = reconnectDelaysMs[reconnectAttempt.coerceIn(0, reconnectDelaysMs.size - 1)]
        reconnectAttempt++
        cancelReconnect()
        Log.d(TAG, "Auto-reconnecting to $currentIp in ${delayMs}ms (attempt $reconnectAttempt)")
        val runnable = Runnable {
            reconnectRunnable = null
            scheduledForGeneration = -1
            // openSocket() closes any leftover socket before it opens the replacement.
            if (autoReconnect && !isConnected) openSocket()
        }
        reconnectRunnable = runnable
        mainHandler.postDelayed(runnable, delayMs)
    }

    private fun cancelReconnect() {
        reconnectRunnable?.let { mainHandler.removeCallbacks(it) }
        reconnectRunnable = null
    }

    /**
     * Fails a socket that opened but never authenticated so the retry loop keeps making progress
     * instead of sitting in "connecting" forever (the classic post-Wi-Fi-drop stuck state).
     */
    private fun scheduleConnectTimeout(gen: Int) {
        cancelConnectTimeout()
        val runnable = Runnable {
            connectTimeoutRunnable = null
            if (gen != generation || isConnected) return@Runnable
            Log.w(TAG, "No authenticated socket for $currentIp after ${CONNECT_TIMEOUT_MS}ms, retrying")
            lastError = "Zaman aşımı ($currentIp:8085 el sıkışma yanıtı vermedi)"
            lastErrorFriendly = "Sunucu yanıt vermedi. Port 8085 Windows Güvenlik Duvarı tarafından engelleniyor olabilir."
            closeActiveSocket()
            setDisconnected()
            scheduleReconnect()
        }
        connectTimeoutRunnable = runnable
        mainHandler.postDelayed(runnable, CONNECT_TIMEOUT_MS)
    }

    private fun cancelConnectTimeout() {
        connectTimeoutRunnable?.let { mainHandler.removeCallbacks(it) }
        connectTimeoutRunnable = null
    }

    /**
     * Invalidates the callbacks of the current socket and hard-closes it. [WebSocket.cancel] is
     * used rather than a graceful close because the case that triggers a reconnect is usually a
     * dead link, where a close handshake would linger until the timeout.
     */
    private fun closeActiveSocket() {
        generation++
        cancelReconnect()
        cancelConnectTimeout()
        val previous = webSocket
        webSocket = null
        if (previous != null) {
            try {
                previous.cancel()
            } catch (e: Exception) {
                Log.w(TAG, "Error closing WebSocket", e)
            }
        }
    }

    /** Clears both connection flags and pushes the new state to the UI. */
    private fun setDisconnected() {
        isConnected = false
        isConnecting = false
        notifyConnectionState()
    }

    /**
     * Delivers the connection flag to [connectionStateListener] on the main thread, so Compose
     * reflects a connect/drop in the same frame instead of one extra dispatch hop later.
     */
    private fun notifyConnectionState() {
        if (connectionStateListener == null) return
        val connected = isConnected
        val deliver = Runnable {
            // Resolved at delivery time: the owning composable may have replaced its listener.
            val listener = connectionStateListener ?: return@Runnable
            try {
                listener(connected)
            } catch (e: Exception) {
                Log.e(TAG, "Connection state listener failed", e)
            }
        }
        if (Looper.myLooper() == Looper.getMainLooper()) deliver.run() else mainHandler.post(deliver)
    }

    fun disconnect() {
        autoReconnect = false
        isAuthRejected = false
        isUserDisconnected = true
        reconnectAttempt = 0
        stopAudioStream()
        stopCameraStream()
        closeActiveSocket()
        setDisconnected()
    }

    fun sendControl(json: JSONObject) {
        if (isConnected) {
            webSocket?.send(json.toString())
        }
    }

    // ----------------- Audio Streaming (PCM 16-bit 16kHz Mono) -----------------
    @SuppressLint("MissingPermission")
    fun startAudioStream() {
        if (isAudioStreaming || currentIp.isEmpty()) return
        isAudioStreaming = true

        thread(start = true) {
            val sampleRate = 44100
            val channelConfig = AudioFormat.CHANNEL_IN_MONO
            val audioFormat = AudioFormat.ENCODING_PCM_16BIT
            val bufferSize = AudioRecord.getMinBufferSize(sampleRate, channelConfig, audioFormat)
            
            try {
                val s = Socket()
                s.tcpNoDelay = true
                s.setPerformancePreferences(0, 2, 1)
                s.connect(java.net.InetSocketAddress(currentIp, 8084), 3000)
                audioSocket = s
                audioOutputStream = s.getOutputStream()
                
                audioRecord = AudioRecord(
                    MediaRecorder.AudioSource.MIC,
                    sampleRate,
                    channelConfig,
                    audioFormat,
                    bufferSize
                )
                
                audioRecord?.startRecording()
                val buffer = ByteArray(bufferSize)

                Log.d(TAG, "Audio streaming started")
                while (isAudioStreaming) {
                    val read = audioRecord?.read(buffer, 0, buffer.size) ?: -1
                    if (read > 0) {
                        audioOutputStream?.write(buffer, 0, read)
                    }
                }
            } catch (e: Exception) {
                Log.e(TAG, "Audio stream error", e)
            } finally {
                cleanupAudio()
            }
        }
    }

    fun stopAudioStream() {
        isAudioStreaming = false
    }

    private fun cleanupAudio() {
        isAudioStreaming = false
        try {
            audioRecord?.stop()
            audioRecord?.release()
        } catch (e: Exception) {
            Log.e(TAG, "Error releasing AudioRecord", e)
        }
        audioRecord = null

        try {
            audioOutputStream?.close()
            audioSocket?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing audio socket", e)
        }
        audioOutputStream = null
        audioSocket = null
        Log.d(TAG, "Audio streaming stopped")
    }

    // ----------------- Camera Streaming (TCP Socket 8083) -----------------
    fun startCameraStream() {
        if (isCameraStreaming || currentIp.isEmpty()) return
        isCameraStreaming = true

        thread(start = true) {
            try {
                val s = Socket()
                s.tcpNoDelay = true
                s.setPerformancePreferences(0, 2, 1)
                s.sendBufferSize = 256 * 1024
                s.connect(java.net.InetSocketAddress(currentIp, 8083), 3000)
                cameraSocket = s
                cameraOutputStream = s.getOutputStream()
                Log.d(TAG, "Camera TCP socket connected with zero-latency options")
            } catch (e: Exception) {
                Log.e(TAG, "Camera TCP socket error", e)
                isCameraStreaming = false
            }
        }
    }

    fun sendCameraFrame(jpegBytes: ByteArray) {
        if (!isCameraStreaming || cameraOutputStream == null) return
        try {
            val size = jpegBytes.size
            // Write size prefix (4 bytes, big endian)
            val sizeBuffer = ByteBuffer.allocate(4).putInt(size).array()
            cameraOutputStream?.write(sizeBuffer)
            cameraOutputStream?.write(jpegBytes)
            cameraOutputStream?.flush()
        } catch (e: Exception) {
            Log.e(TAG, "Error sending camera frame", e)
            stopCameraStream()
        }
    }

    fun stopCameraStream() {
        isCameraStreaming = false
        try {
            cameraOutputStream?.close()
            cameraSocket?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing camera socket", e)
        }
        cameraOutputStream = null
        cameraSocket = null
        Log.d(TAG, "Camera streaming stopped")
    }

    fun sendFlowMessage(text: String, msgType: String = "text", fileName: String = "", fileUrl: String = "", fileSize: Long = 0) {
        val json = JSONObject().apply {
            put("type", "flow_send")
            put("sender", "phone")
            put("text", text)
            put("msg_type", msgType)
            put("file_name", fileName)
            put("file_url", fileUrl)
            put("file_size", fileSize)
        }
        sendControl(json)
    }

    fun getFullDiagnosticReport(context: android.content.Context): String {
        val sb = StringBuilder()
        sb.append("=== BigPocket Android Teşhis Raporu ===\n")
        sb.append("Tarih: ").append(java.text.SimpleDateFormat("yyyy-MM-dd HH:mm:ss", java.util.Locale.getDefault()).format(java.util.Date())).append("\n")
        sb.append("Cihaz: ").append(android.os.Build.MANUFACTURER).append(" ").append(android.os.Build.MODEL).append("\n")
        sb.append("Android Sürümü: ").append(android.os.Build.VERSION.RELEASE).append(" (API ").append(android.os.Build.VERSION.SDK_INT).append(")\n")
        sb.append("Bağlantı Durumu: ").append(if (isConnected) "BAĞLI" else if (isConnecting) "BAĞLANILIYOR" else "BAĞLI DEĞİL").append("\n")
        sb.append("Hedef IP: ").append(currentIp.ifEmpty { "Belirtilmemiş" }).append(":8085\n")
        sb.append("Şifre Tanımlı: ").append(currentPassword.isNotEmpty()).append("\n")
        sb.append("Son Teknik Hata: ").append(lastError.ifEmpty { "Kayıtlı hata yok" }).append("\n")
        sb.append("Açıklama: ").append(lastErrorFriendly.ifEmpty { "Normal" }).append("\n")
        try {
            val cm = context.getSystemService(android.content.Context.CONNECTIVITY_SERVICE) as? android.net.ConnectivityManager
            @Suppress("DEPRECATION")
            val active = cm?.activeNetworkInfo
            sb.append("Ağ Bağlantısı: ").append(active?.typeName ?: "Bilinmiyor").append(" (Bağlı: ").append(active?.isConnected ?: false).append(")\n")
        } catch (e: Exception) {
            sb.append("Ağ Bilgisi: ").append(e.message).append("\n")
        }
        return sb.toString()
    }

    fun deleteFlowMessage(id: String) {
        val json = JSONObject().apply {
            put("type", "flow_delete")
            put("id", id)
        }
        // cached flow mid-reconnect) fall back to the REST endpoint so the delete still lands.
        if (isConnected) {
            sendControl(json)
        } else {
            deleteFlowMessageOverHttp(id)
        }
    }

    /** `DELETE /api/flow/<id>` fallback used while the WebSocket is down. */
    private fun deleteFlowMessageOverHttp(id: String) {
        if (currentIp.isEmpty()) {
            Log.w(TAG, "Cannot delete flow message: no host known")
            return
        }
        val request = Request.Builder()
            .url("http://$currentIp:8085/api/flow/${Uri.encode(id)}")
            .addHeader("X-Password", currentPassword)
            .delete()
            .build()
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: java.io.IOException) {
                Log.e(TAG, "HTTP flow delete failed", e)
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (!response.isSuccessful) {
                        Log.w(TAG, "HTTP flow delete returned ${response.code}")
                    }
                }
            }
        })
    }
}

/** Disk, GPU, media and weather telemetry bundled into the `system_stats` broadcast. */
data class SystemExtraStats(
    val diskPercent: Int = 0,
    val diskFreeGb: Double = 0.0,
    val diskTotalGb: Double = 0.0,
    val gpu: Int = 0,
    val gpuTemp: Int = 0,
    val mediaTitle: String = "",
    val mediaArtist: String = "",
    val mediaPlaying: Boolean = false,
    val mediaCover: String = "",
    val mediaPosSec: Int = 0,
    val mediaDurSec: Int = 0,
    val mediaIsLive: Boolean = false,
    val weatherCity: String = "",
    val weatherTemp: Int = 0,
    val weatherCode: Int = 0,
    val weatherDesc: String = "",
    val weatherWind: Int = 0
)

/** Payload handed over by another app through the Android share sheet (Intent.ACTION_SEND). */
data class IncomingShare(
    val text: String? = null,
    val uri: String? = null
)

/**
 * One-slot mailbox that carries a share-sheet payload from [android.app.Activity] into the
 * Compose tree. Backed by Compose snapshot state so MainScreen reacts the moment it arrives.
 */
object ShareInbox {
    var pending by mutableStateOf<IncomingShare?>(null)
}

