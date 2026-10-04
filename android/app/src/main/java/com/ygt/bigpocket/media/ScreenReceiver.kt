package com.ygt.bigpocket.media

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import java.io.InputStream
import java.net.Socket
import java.nio.ByteBuffer
import kotlin.concurrent.thread

object ScreenReceiver {
    private const val TAG = "ScreenReceiver"
    var latestFrame by mutableStateOf<Bitmap?>(null)
    private var socket: Socket? = null
    private var inputStream: InputStream? = null
    private var isRunning = false

    fun start(ip: String) {
        if (isRunning) return
        isRunning = true
        thread(start = true) {
            try {
                val s = Socket()
                s.tcpNoDelay = true
                s.setPerformancePreferences(0, 2, 1)
                s.receiveBufferSize = 512 * 1024
                s.connect(java.net.InetSocketAddress(ip, 8086), 3000)
                socket = s
                inputStream = s.getInputStream()
                Log.d(TAG, "Connected to Screen TCP stream with zero-latency TCP options")
                
                val sizeBuffer = ByteArray(4)
                while (isRunning) {
                    // Read 4 bytes length
                    var readBytes = 0
                    while (readBytes < 4) {
                        val read = inputStream?.read(sizeBuffer, readBytes, 4 - readBytes) ?: -1
                        if (read == -1) break
                        readBytes += read
                    }
                    if (readBytes < 4) break
                    
                    val length = ByteBuffer.wrap(sizeBuffer).int
                    if (length <= 0 || length > 10 * 1024 * 1024) continue // ignore crazy sizes
                    
                    // Read length bytes
                    val imgBuffer = ByteArray(length)
                    var imgRead = 0
                    while (imgRead < length) {
                        val read = inputStream?.read(imgBuffer, imgRead, length - imgRead) ?: -1
                        if (read == -1) break
                        imgRead += read
                    }
                    if (imgRead < length) break
                    
                    // Decode bitmap
                    val bitmap = BitmapFactory.decodeByteArray(imgBuffer, 0, length)
                    if (bitmap != null) {
                        latestFrame = bitmap
                    }
                }
            } catch (e: Exception) {
                Log.e(TAG, "Screen stream error", e)
            } finally {
                stop()
            }
        }
    }

    fun stop() {
        isRunning = false
        try {
            inputStream?.close()
            socket?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing screen socket", e)
        }
        inputStream = null
        socket = null
        latestFrame = null
        Log.d(TAG, "Screen stream stopped")
    }
}

