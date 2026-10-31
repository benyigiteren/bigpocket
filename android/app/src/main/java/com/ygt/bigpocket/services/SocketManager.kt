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

    // The keep-alive ping surfaces dead ("zombie") sockets within 15 seconds, which is what
    // triggers the automatic reconnection below.
    private val client = OkHttpClient.Builder()
        .pingInterval(15, TimeUnit.SECONDS)
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
    var isConnected = false
        private set
    var isConnecting = false
        private set

    // Automatic reconnection with exponential backoff: 2s, 4s, 8s, then capped at 15s.
    private val reconnectDelaysMs = longArrayOf(2_000L, 4_000L, 8_000L, 15_000L)
    private val reconnectHandler = Handler(Looper.getMainLooper())
    private var reconnectRunnable: Runnable? = null
    private var reconnectAttempt = 0
    private var autoReconnect = false

    // Bumped for every new socket and every manual disconnect so stale callbacks are ignored.
    private var generation = 0

    private var audioSocket: Socket? = null
    private var audioOutputStream: OutputStream? = null
    private var audioRecord: AudioRecord? = null
    var isAudioStreaming = false
        private set

    private var cameraSocket: Socket? = null
    private var cameraOutputStream: OutputStream? = null
    var isCameraStreaming = false
        private set

    var currentIp = ""
    var currentPassword = ""

    fun connect(ip: String, password: String) {
        currentIp = ip
        currentPassword = password

        // Tear down the previous session (streams + socket) before opening the new link.
        generation++
        cancelReconnect()
        reconnectAttempt = 0
        stopAudioStream()
        stopCameraStream()
        try {
            webSocket?.close(1000, "Reconnecting")
        } catch (e: Exception) {
            Log.e(TAG, "Error closing WS", e)
        }
        webSocket = null
        isConnected = false
        isConnecting = false
        connectionStateListener?.invoke(false)

        autoReconnect = true
        openSocket()
    }

    /** Opens the WebSocket for [currentIp]; safe to call from the reconnect handler. */
    private fun openSocket() {
        if (currentIp.isEmpty()) return
        generation++
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
                        reconnectAttempt = 0
                        Log.d(TAG, "WebSocket authenticated successfully")
                        sendControl(JSONObject().apply {
                            put("type", "client_connected")
                        })
                        connectionStateListener?.invoke(true)
                    } else if (type == "auth_failed") {
                        Log.e(TAG, "Authentication failed: ${json.optString("message")}")
                        isConnected = false
                        isConnecting = false
                        // A rejected password is never retried automatically.
                        autoReconnect = false
                        cancelReconnect()
                        connectionStateListener?.invoke(false)
                        webSocket.close(1000, "Auth failed")
                    } else if (type == "clipboard_sync") {
                        val clipText = json.getString("text")
                        clipboardListener?.invoke(clipText)
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
                isConnected = false
                isConnecting = false
                connectionStateListener?.invoke(false)
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (gen != generation) return
                Log.e(TAG, "WebSocket failure: ${t.message}")
                isConnected = false
                isConnecting = false
                connectionStateListener?.invoke(false)
                scheduleReconnect()
            }
        })
    }

    /**
     * Retries the last connection with exponential backoff (2s, 4s, 8s, max 15s). Never runs
     * after a deliberate [disconnect], only after an unexpected drop.
     */
    private fun scheduleReconnect() {
        if (!autoReconnect || currentIp.isEmpty()) return
        val delayMs = reconnectDelaysMs[reconnectAttempt.coerceIn(0, reconnectDelaysMs.size - 1)]
        reconnectAttempt++
        cancelReconnect()
        Log.d(TAG, "Auto-reconnecting to $currentIp in ${delayMs}ms (attempt $reconnectAttempt)")
        val runnable = Runnable {
            reconnectRunnable = null
            if (autoReconnect && !isConnected && !isConnecting) openSocket()
        }
        reconnectRunnable = runnable
        reconnectHandler.postDelayed(runnable, delayMs)
    }

    private fun cancelReconnect() {
        reconnectRunnable?.let { reconnectHandler.removeCallbacks(it) }
        reconnectRunnable = null
    }

    fun disconnect() {
        autoReconnect = false
        generation++
        cancelReconnect()
        reconnectAttempt = 0
        stopAudioStream()
        stopCameraStream()

        try {
            webSocket?.close(1000, "Goodbye")
        } catch (e: Exception) {
            Log.e(TAG, "Error closing WS", e)
        }
        webSocket = null
        isConnected = false
        isConnecting = false
        connectionStateListener?.invoke(false)
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

    fun deleteFlowMessage(id: String) {
        val json = JSONObject().apply {
            put("type", "flow_delete")
            put("id", id)
        }
        // The live socket is the fast path; when it is not connected (e.g. the app is showing a
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

