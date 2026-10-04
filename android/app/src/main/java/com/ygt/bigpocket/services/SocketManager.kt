package com.ygt.bigpocket.services

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.util.Log
import okhttp3.*
import org.json.JSONObject
import java.io.OutputStream
import java.net.Socket
import java.nio.ByteBuffer
import kotlin.concurrent.thread

object SocketManager {
    private const val TAG = "SocketManager"
    private val client = OkHttpClient()
    private var webSocket: WebSocket? = null
    
    var connectionStateListener: ((Boolean) -> Unit)? = null
    var clipboardListener: ((String) -> Unit)? = null
    var configUpdateListener: (() -> Unit)? = null
    var buttonStateListener: ((Int, Boolean, Int) -> Unit)? = null
    var pageSwitchedListener: ((Int) -> Unit)? = null
    var volumeChangedListener: ((String, Float) -> Unit)? = null
    var systemStatsListener: ((Int, Int) -> Unit)? = null
    var isConnected = false
        private set

    private var audioSocket: Socket? = null
    private var audioOutputStream: OutputStream? = null
    private var audioRecord: AudioRecord? = null
    private var isAudioStreaming = false

    private var cameraSocket: Socket? = null
    private var cameraOutputStream: OutputStream? = null
    private var isCameraStreaming = false

    var currentIp = ""
    var currentPassword = ""

    fun connect(ip: String, password: String) {
        currentIp = ip
        currentPassword = password
        disconnect()

        val request = Request.Builder()
            .url("ws://$ip:8085/ws")
            .build()

        webSocket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.d(TAG, "WebSocket opened, waiting for auth challenge")
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
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
                        Log.d(TAG, "WebSocket authenticated successfully")
                        sendControl(JSONObject().apply {
                            put("type", "client_connected")
                        })
                        connectionStateListener?.invoke(true)
                    } else if (type == "auth_failed") {
                        Log.e(TAG, "Authentication failed: ${json.optString("message")}")
                        isConnected = false
                        connectionStateListener?.invoke(false)
                        webSocket.close(1000, "Auth failed")
                    } else if (type == "clipboard_sync") {
                        val clipText = json.getString("text")
                        clipboardListener?.invoke(clipText)
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
                    } else if (type == "system_stats") {
                        val cpu = json.optInt("cpu")
                        val ram = json.optInt("ram")
                        systemStatsListener?.invoke(cpu, ram)
                    }
                } catch (e: Exception) {
                    Log.e(TAG, "Error parsing WS message", e)
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                isConnected = false
                connectionStateListener?.invoke(false)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                Log.e(TAG, "WebSocket failure: ${t.message}")
                isConnected = false
                connectionStateListener?.invoke(false)
            }
        })
    }

    fun disconnect() {
        stopAudioStream()
        stopCameraStream()
        
        try {
            webSocket?.close(1000, "Goodbye")
        } catch (e: Exception) {
            Log.e(TAG, "Error closing WS", e)
        }
        webSocket = null
        isConnected = false
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
}

