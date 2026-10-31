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
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface
import java.net.SocketTimeoutException

/**
 * Listens for UDP "hello" broadcasts sent by the BigPocket desktop app so the phone
 * can find the PC automatically without typing an IP address.
 */
object PcDiscovery {
    private const val TAG = "PcDiscovery"
    const val PORT = 47800

    /** Probe payload the desktop answers immediately (see `desktop/src-tauri/src/discovery.rs`). */
    private const val PROBE_TEXT = "bigpocket_discover"

    /** The desktop repeats its hello every 2s, so probing a little slower is enough. */
    private const val PROBE_INTERVAL_MS = 3_000L

    /** Receive timeout, so the probe keeps being sent while nothing is answered on the LAN. */
    private const val RECEIVE_TIMEOUT_MS = 1_500

    /**
     * A collector can start while the socket of the previous one is still being released (the
     * discovery flow is restarted on every connect/disconnect), where the first bind fails with
     * EADDRINUSE. Retry briefly instead of silently losing auto-discovery for the whole session.
     */
    private const val BIND_ATTEMPTS = 10
    private const val BIND_RETRY_DELAY_MS = 200L

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

        val socket = try {
            bindDiscoverySocket()
        } catch (e: Exception) {
            // Never leak the multicast lock when the socket cannot be bound.
            Log.w(TAG, "Discovery socket unavailable", e)
            try { lock?.release() } catch (_: Exception) { }
            close(e)
            return@callbackFlow
        }

        val thread = Thread {
            val buf = ByteArray(1024)
            var lastProbe = 0L
            while (!socket.isClosed) {
                // Actively probe every few seconds for a faster answer. The target list is rebuilt
                // on every round because the phone may have roamed to another subnet meanwhile.
                val now = System.currentTimeMillis()
                if (now - lastProbe > PROBE_INTERVAL_MS) {
                    lastProbe = now
                    val probe = PROBE_TEXT.toByteArray()
                    for (target in broadcastAddresses()) {
                        try {
                            socket.send(DatagramPacket(probe, probe.size, target, PORT))
                        } catch (_: Exception) { }
                    }
                }
                val packet = DatagramPacket(buf, buf.size)
                try {
                    socket.receive(packet)
                } catch (_: SocketTimeoutException) {
                    continue
                } catch (e: Exception) {
                    // Cancellation closes the socket, which surfaces here as a SocketException.
                    if (!socket.isClosed) Log.w(TAG, "Discovery receive error", e)
                    continue
                }
                try {
                    val text = String(packet.data, packet.offset, packet.length)
                    if (!text.startsWith("{")) continue
                    val json = JSONObject(text)
                    if (json.optString("app") != "bigpocket") continue
                    val ip = ipv4Host(packet.address) ?: continue
                    trySend(
                        DiscoveredPc(
                            ip = ip,
                            name = json.optString("name", "PC"),
                            port = json.optInt("port", 8085),
                            version = json.optString("version"),
                            needsPassword = json.optBoolean("needsPassword", false),
                        )
                    )
                } catch (e: Exception) {
                    // One malformed datagram must not kill the listener thread.
                    Log.w(TAG, "Malformed discovery packet", e)
                }
            }
        }.apply { isDaemon = true; start() }

        awaitClose {
            socket.close()
            thread.interrupt()
            try { lock?.release() } catch (_: Exception) { }
        }
    }.flowOn(Dispatchers.IO)

    /** Binds the IPv4 discovery socket, retrying while the previous collector releases the port. */
    private fun bindDiscoverySocket(): DatagramSocket {
        var lastError: Exception? = null
        for (attempt in 0 until BIND_ATTEMPTS) {
            val socket = DatagramSocket(null)
            try {
                socket.reuseAddress = true
                socket.broadcast = true
                socket.soTimeout = RECEIVE_TIMEOUT_MS
                // Bind the IPv4 wildcard explicitly: a dual-stack socket reports IPv4 senders as
                // IPv4-mapped IPv6 addresses, which cannot be used as a host in a `ws://` URL.
                socket.bind(InetSocketAddress(InetAddress.getByName("0.0.0.0"), PORT))
                return socket
            } catch (e: Exception) {
                lastError = e
                try { socket.close() } catch (_: Exception) { }
                if (attempt < BIND_ATTEMPTS - 1) {
                    try { Thread.sleep(BIND_RETRY_DELAY_MS) } catch (_: InterruptedException) { }
                }
            }
        }
        throw lastError ?: IllegalStateException("cannot bind UDP $PORT")
    }

    /**
     * Probe targets: the limited broadcast address plus every subnet-directed broadcast, since
     * some access points drop one of the two.
     */
    private fun broadcastAddresses(): List<InetAddress> {
        val targets = LinkedHashSet<InetAddress>()
        try {
            targets += InetAddress.getByName("255.255.255.255")
        } catch (_: Exception) { }
        try {
            // Platform type: may be null when the interface list cannot be read.
            val interfaces = NetworkInterface.getNetworkInterfaces() ?: return targets.toList()
            for (nif in interfaces) {
                if (!nif.isUp || nif.isLoopback) continue
                for (ifAddress in nif.interfaceAddresses) {
                    if (ifAddress.address !is Inet4Address) continue
                    ifAddress.broadcast?.let { targets += it }
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "Cannot enumerate network interfaces", e)
        }
        return targets.toList()
    }

    /**
     * Normalises a sender address to a dotted-quad IPv4 string; a dual-stack socket reports IPv4
     * senders as `::ffff:a.b.c.d`, which is not usable inside a `ws://` URL.
     */
    private fun ipv4Host(address: InetAddress?): String? {
        address ?: return null
        if (address is Inet4Address) return address.hostAddress
        val bytes = address.address
        val v4Mapped = bytes.size == 16 &&
            bytes[10] == 0xFF.toByte() && bytes[11] == 0xFF.toByte() &&
            (0 until 10).all { bytes[it] == 0.toByte() }
        if (address is Inet6Address && v4Mapped) {
            return "${bytes[12].toInt() and 0xFF}.${bytes[13].toInt() and 0xFF}." +
                "${bytes[14].toInt() and 0xFF}.${bytes[15].toInt() and 0xFF}"
        }
        return address.hostAddress
    }
}
