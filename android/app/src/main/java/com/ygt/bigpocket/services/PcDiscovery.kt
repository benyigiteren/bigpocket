package com.ygt.bigpocket.services

import android.content.Context
import android.net.wifi.WifiManager
import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.flowOn
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.SocketTimeoutException

/**
 * Listens for UDP "hello" broadcasts sent by the BigPocket desktop app so the phone
 * can find the PC automatically without typing an IP address.
 */
object PcDiscovery {
    private const val TAG = "PcDiscovery"
    const val PORT = 47800

    data class DiscoveredPc(
        val ip: String,
        val name: String,
        val port: Int,
        val version: String,
        val needsPassword: Boolean,
    )

    fun discover(context: Context): Flow<DiscoveredPc> = callbackFlow {
        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
        val lock = wifi?.createMulticastLock("bigpocket-discovery")?.apply {
            setReferenceCounted(false)
            try { acquire() } catch (e: Exception) { Log.w(TAG, "Multicast lock failed", e) }
        }

        val socket = DatagramSocket(null).apply {
            reuseAddress = true
            broadcast = true
            soTimeout = 1500
            bind(InetSocketAddress(PORT))
        }

        val thread = Thread {
            val buf = ByteArray(1024)
            var lastProbe = 0L
            while (!socket.isClosed) {
                // Actively probe every few seconds for a faster answer.
                val now = System.currentTimeMillis()
                if (now - lastProbe > 3000) {
                    lastProbe = now
                    try {
                        val probe = "bigpocket_discover".toByteArray()
                        socket.send(DatagramPacket(probe, probe.size, InetAddress.getByName("255.255.255.255"), PORT))
                    } catch (_: Exception) { }
                }
                try {
                    val packet = DatagramPacket(buf, buf.size)
                    socket.receive(packet)
                    val text = String(packet.data, 0, packet.length)
                    if (!text.startsWith("{")) continue
                    val json = JSONObject(text)
                    if (json.optString("app") != "bigpocket") continue
                    trySend(
                        DiscoveredPc(
                            ip = packet.address.hostAddress ?: continue,
                            name = json.optString("name", "PC"),
                            port = json.optInt("port", 8085),
                            version = json.optString("version"),
                            needsPassword = json.optBoolean("needsPassword", false),
                        )
                    )
                } catch (_: SocketTimeoutException) {
                } catch (e: Exception) {
                    if (!socket.isClosed) Log.w(TAG, "Discovery receive error", e)
                }
            }
        }.apply { isDaemon = true; start() }

        awaitClose {
            socket.close()
            thread.interrupt()
            try { lock?.release() } catch (_: Exception) { }
        }
    }.flowOn(Dispatchers.IO)
}
