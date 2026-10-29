package com.ygt.bigpocket.media

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import java.io.IOException
import java.io.InputStream
import java.net.InetSocketAddress
import java.net.Socket
import kotlin.concurrent.thread

/**
 * Live screen stream receiver.
 *
 * Steady-state allocation is zero: frames are read into pooled byte buffers and
 * decoded straight into a double-buffered, reusable [Bitmap] (`inBitmap` +
 * `inMutable`), so the 30 FPS stream produces no per-frame garbage and no
 * Bitmap GC pauses/jank.
 */
object ScreenReceiver {
    private const val TAG = "ScreenReceiver"
    private const val MAX_FRAME_SIZE = 10 * 1024 * 1024
    private const val INITIAL_BUFFER_SIZE = 256 * 1024
    private const val POOLED_BUFFERS = 2

    var latestFrame by mutableStateOf<Bitmap?>(null)
        private set

    private var socket: Socket? = null
    private var inputStream: InputStream? = null

    @Volatile
    private var isRunning = false

    // ----------------- Reusable frame memory -----------------

    /** Stack of recycled read buffers; grown on demand, never freed per frame. */
    private val bufferPool = ArrayDeque<ByteArray>()

    private fun obtainBuffer(size: Int): ByteArray {
        val pooled = bufferPool.removeLastOrNull()
        if (pooled != null && pooled.size >= size) return pooled

        // Grow geometrically so a slowly growing stream does not reallocate
        // on every frame.
        var capacity = if (pooled != null && pooled.isNotEmpty()) pooled.size else INITIAL_BUFFER_SIZE
        while (capacity < size) capacity = capacity shl 1
        return ByteArray(capacity)
    }

    private fun recycleBuffer(buffer: ByteArray) {
        if (bufferPool.size < POOLED_BUFFERS) bufferPool.addLast(buffer)
    }

    // ---- Double-buffered Bitmap decode ----

    /** Decode target the decoder writes into (never the published frame). */
    private var backBuffer: Bitmap? = null

    /** Bitmap currently published through [latestFrame]. */
    private var frontBuffer: Bitmap? = null

    /** Reused options object: no allocation per decoded frame. */
    private val decodeOptions = BitmapFactory.Options()

    /**
     * Decodes a JPEG frame into [target] when possible so no new [Bitmap] is
     * allocated. Returns null when the payload is not a decodable image.
     */
    private fun decodeFrame(buffer: ByteArray, length: Int, target: Bitmap?): Bitmap? {
        val options = decodeOptions
        options.inJustDecodeBounds = false
        options.inMutable = true
        options.inPreferredConfig = Bitmap.Config.ARGB_8888
        options.inBitmap = target?.takeIf { !it.isRecycled && it.isMutable }

        if (options.inBitmap != null) {
            try {
                val reused = BitmapFactory.decodeByteArray(buffer, 0, length, options)
                if (reused != null) return reused
                // Some API levels return null instead of throwing when reuse
                // fails; fall through to a plain allocation.
            } catch (e: IllegalArgumentException) {
                Log.w(TAG, "Bitmap reuse rejected (geometry change?), allocating new buffer", e)
            }
            options.inBitmap = null
        }

        return try {
            BitmapFactory.decodeByteArray(buffer, 0, length, options)
        } catch (e: IllegalArgumentException) {
            Log.w(TAG, "Frame decode failed", e)
            null
        }
    }

    // ----------------- Stream lifecycle -----------------

    fun start(ip: String) {
        if (isRunning) return
        isRunning = true
        thread(start = true) {
            try {
                val s = Socket()
                s.tcpNoDelay = true
                s.setPerformancePreferences(0, 2, 1)
                s.receiveBufferSize = 512 * 1024
                s.connect(InetSocketAddress(ip, 8086), 3000)
                val input = s.getInputStream()
                socket = s
                inputStream = input
                Log.d(TAG, "Connected to Screen TCP stream with zero-latency TCP options")

                val sizeBuffer = ByteArray(4)
                while (isRunning) {
                    // Read 4 bytes length (big endian, no ByteBuffer allocation).
                    if (!readFully(input, sizeBuffer, 4)) break
                    val length = ((sizeBuffer[0].toInt() and 0xFF) shl 24) or
                        ((sizeBuffer[1].toInt() and 0xFF) shl 16) or
                        ((sizeBuffer[2].toInt() and 0xFF) shl 8) or
                        (sizeBuffer[3].toInt() and 0xFF)
                    if (length <= 0 || length > MAX_FRAME_SIZE) continue // ignore crazy sizes

                    val imgBuffer = obtainBuffer(length)
                    if (!readFully(input, imgBuffer, length, offset = 0)) {
                        recycleBuffer(imgBuffer)
                        break
                    }

                    val target = backBuffer
                    val frame = decodeFrame(imgBuffer, length, target)
                    recycleBuffer(imgBuffer)
                    if (frame == null) continue

                    // Double buffering: publish the fresh bitmap and hand the
                    // previously published one back as the next decode target
                    // (a full frame interval has passed, so Compose has drawn it).
                    backBuffer = frontBuffer
                    frontBuffer = frame
                    latestFrame = frame
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
        // Buffers are intentionally dropped (not recycled) so an in-flight
        // Compose draw can never touch a recycled bitmap; the next start()
        // rebuilds the double buffer.
        frontBuffer = null
        backBuffer = null
        latestFrame = null
        Log.d(TAG, "Screen stream stopped")
    }

    private fun readFully(stream: InputStream, buffer: ByteArray, length: Int, offset: Int = 0): Boolean {
        var read = 0
        while (read < length) {
            val n = try {
                stream.read(buffer, offset + read, length - read)
            } catch (e: IOException) {
                Log.e(TAG, "Screen stream read failed", e)
                return false
            }
            if (n < 0) return false
            read += n
        }
        return true
    }
}
