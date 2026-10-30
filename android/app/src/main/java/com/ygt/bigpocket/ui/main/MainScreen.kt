package com.ygt.bigpocket.ui.main

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.ImageFormat
import android.graphics.Rect
import android.graphics.YuvImage
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.util.Log
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.ui.layout.ContentScale
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.*
import androidx.compose.ui.platform.LocalConfiguration
import android.content.res.Configuration
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.draw.scale
import androidx.compose.animation.core.*
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import android.content.pm.ActivityInfo
import com.ygt.bigpocket.theme.*
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import coil.compose.AsyncImage
import com.ygt.bigpocket.media.ScreenReceiver
import com.ygt.bigpocket.services.SocketManager
import com.ygt.bigpocket.theme.BigPocketTheme
import com.ygt.bigpocket.ui.settings.SettingsDialog
import com.ygt.bigpocket.update.UpdatePrompt
import com.ygt.bigpocket.update.UpdateDialog
import com.ygt.bigpocket.update.UpdateManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.async
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.catch
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.InputStream
import androidx.compose.foundation.shape.CircleShape
import java.util.Calendar
import java.util.concurrent.Executors
import kotlin.math.hypot
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.activity.compose.BackHandler
import androidx.camera.core.Preview
import androidx.camera.view.PreviewView
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.viewinterop.AndroidView
import com.ygt.bigpocket.services.BigPocketService
import com.ygt.bigpocket.services.ShareInbox
import okio.Buffer
import okio.BufferedSink
import okio.source

data class StreamDeckButtonInfo(
    val id: Int,
    val label: String,
    val type: String,
    val value: String,
    val icon: String,
    val page: Int = 0,
    val state: Boolean = false,
    val color: String = "",
    val rowSpan: Int = 1,
    val colSpan: Int = 1,
    val bgStyle: String = "default",
    val clockStyle: String = "digital",
    val fontFamily: String = "mono",
    /** Point size of the clock digits, `0` means "use the responsive default". */
    val clockFontSize: Int = 0,
    /** Explicit bento row (0-based); `-1` means "let the packer pick the first free cell". */
    val gridRow: Int = -1,
    /** Explicit bento column (0-based); `-1` means "let the packer pick the first free cell". */
    val gridCol: Int = -1
)

/**
 * Immutable snapshot of every live value the PC pushes over the `system_stats` broadcast
 * (CPU/RAM/thermal, disk, GPU, media and weather). One record keeps all Stream Deck
 * widgets in sync and lets callers thread a single parameter through the compose tree.
 */
data class StreamDeckLiveStats(
    val cpu: Int = 0,
    val ram: Int = 0,
    val cpuTemp: Int = 45,
    val ramUsedGb: Double = 0.0,
    val ramTotalGb: Double = 0.0,
    val netDownKbps: Int = 0,
    val netUpKbps: Int = 0,
    val diskPercent: Int = 0,
    val diskFreeGb: Double = 0.0,
    val diskTotalGb: Double = 0.0,
    val gpu: Int = 0,
    val gpuTemp: Int = 0,
    val mediaTitle: String = "",
    val mediaArtist: String = "",
    val mediaPlaying: Boolean = false,
    val weatherCity: String = "",
    val weatherTemp: Int = 0,
    val weatherCode: Int = 0,
    val weatherDesc: String = "",
    val weatherWind: Int = 0,
    /** Rolling sample windows (~2s apart) that feed the widgets' sparkline curves. */
    val cpuHistory: List<Float> = emptyList(),
    val ramHistory: List<Float> = emptyList(),
    val netDownHistory: List<Float> = emptyList(),
    val netUpHistory: List<Float> = emptyList()
)

/** Flat monochrome card palette shared 1:1 with the PC preview (Editorial Monochrome Zinc). */
data class DeckPalette(
    val surface: Color,
    val surfaceActive: Color,
    val accent: Color,
    val border: Color
)

/**
 * Every Stream Deck widget — clock, weather, telemetry, now-playing, shortcuts, sliders —
 * uses exactly this palette: `#18181B` matte surface, `#222226` icon badge, `#27272A`
 * hairline and `#F4F4F5` / `#A1A1AA` typography. Colours picked in the editor only change
 * the icon glyph tint inside this same shell, never the card surface.
 */
val DeckMono = DeckPalette(MinimalistSurface, MinimalistSurfaceElevated, MinimalistPrimary, MinimalistBorder)

/** Card corner radius shared by every card and widget (PC `border-radius: 20px`). */
val DeckCorner = 20.dp

fun deckPaletteForType(type: String, value: String): DeckPalette = DeckMono

fun deckPaletteFor(type: String, value: String, color: String): DeckPalette = DeckMono

/** WMO weather code to a pure Material vector glyph (never an emoji). */
fun weatherIcon(code: Int): androidx.compose.ui.graphics.vector.ImageVector = when (code) {
    0 -> Icons.Default.WbSunny
    1, 2 -> Icons.Default.Cloud
    3 -> Icons.Default.Cloud
    45, 48 -> Icons.Default.Air
    in 51..57 -> Icons.Default.WaterDrop
    in 61..67 -> Icons.Default.WaterDrop
    in 71..77, 85, 86 -> Icons.Default.AcUnit
    in 80..82 -> Icons.Default.WaterDrop
    in 95..99 -> Icons.Default.Thunderstorm
    else -> Icons.Default.Cloud
}

/** Compact human status label for the action / toggle cards. */
fun deckStatusLabel(button: StreamDeckButtonInfo): String = when (button.type) {
    "toggle" -> if (button.state) "Açık" else "Kapalı"
    "command" -> "Uygulama"
    "url" -> "Bağlantı"
    "hotkey" -> "Kısayol"
    else -> "Kısayol"
}

data class StreamDeckPageInfo(
    val id: Int,
    val title: String
)

data class InstalledAppInfo(
    val name: String,
    val path: String
)

private fun fetchInstalledApps(ip: String, password: String, onSuccess: (List<InstalledAppInfo>) -> Unit) {
    val client = OkHttpClient()
    val request = Request.Builder()
        .url("http://$ip:8085/installed_apps")
        .addHeader("X-Password", password)
        .build()
    client.newCall(request).enqueue(object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) {
            Log.e("MainScreen", "Failed to fetch installed apps", e)
        }
        override fun onResponse(call: Call, response: Response) {
            response.use {
                if (!response.isSuccessful) return
                try {
                    val body = response.body?.string() ?: return
                    val json = JSONObject(body)
                    val appsArray = json.getJSONArray("apps")
                    val list = mutableListOf<InstalledAppInfo>()
                    for (i in 0 until appsArray.length()) {
                        val item = appsArray.getJSONObject(i)
                        list.add(InstalledAppInfo(
                            name = item.getString("name"),
                            path = item.getString("path")
                        ))
                    }
                    onSuccess(list)
                } catch (e: Exception) {
                    Log.e("MainScreen", "Error parsing installed apps JSON", e)
                }
            }
        }
    })
}

private fun fetchStreamDeckConfig(
    ip: String,
    password: String,
    onSuccess: (List<StreamDeckButtonInfo>, Int, Int, List<StreamDeckPageInfo>, Int) -> Unit
) {
    val client = OkHttpClient()
    val request = Request.Builder()
        .url("http://$ip:8085/config")
        .addHeader("X-Password", password)
        .build()
    client.newCall(request).enqueue(object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) {
            Log.e("MainScreen", "Failed to fetch stream deck config", e)
        }
        override fun onResponse(call: Call, response: Response) {
            response.use {
                if (!response.isSuccessful) return
                try {
                    val body = response.body?.string() ?: return
                    val json = JSONObject(body)
                    val configObj = json.getJSONObject("config")
                    val rows = configObj.optInt("stream_deck_rows", 2)
                    val cols = configObj.optInt("stream_deck_cols", 4)
                    val activePage = configObj.optInt("active_page", 0)

                    val pagesList = mutableListOf<StreamDeckPageInfo>()
                    val pagesArray = configObj.optJSONArray("stream_deck_pages")
                    if (pagesArray != null) {
                        for (i in 0 until pagesArray.length()) {
                            val pObj = pagesArray.getJSONObject(i)
                            pagesList.add(StreamDeckPageInfo(
                                id = pObj.optInt("id", i),
                                title = pObj.optString("title", "Sayfa ${i + 1}")
                            ))
                        }
                    }
                    if (pagesList.isEmpty()) {
                        pagesList.add(StreamDeckPageInfo(0, "Ana Sayfa"))
                        pagesList.add(StreamDeckPageInfo(1, "Medya & Ses"))
                        pagesList.add(StreamDeckPageInfo(2, "Sistem & PC"))
                    }

                    val buttonsArray = configObj.getJSONArray("stream_deck_buttons")
                    val list = mutableListOf<StreamDeckButtonInfo>()
                    for (i in 0 until buttonsArray.length()) {
                        val item = buttonsArray.getJSONObject(i)
                        list.add(StreamDeckButtonInfo(
                            id = item.getInt("id"),
                            label = item.getString("label"),
                            type = item.getString("type"),
                            value = item.getString("value"),
                            icon = item.optString("icon", ""),
                            page = item.optInt("page", 0),
                            state = item.optBoolean("state", false),
                            color = item.optString("color", ""),
                            rowSpan = item.optInt("row_span", 1),
                            colSpan = item.optInt("col_span", 1),
                            bgStyle = item.optString("bg_style", "default"),
                            clockStyle = item.optString("clock_style", "digital"),
                            fontFamily = item.optString("font_family", "mono"),
                            clockFontSize = item.optInt("clock_font_size", 0),
                            gridRow = item.optInt("grid_row", -1),
                            gridCol = item.optInt("grid_col", -1)
                        ))
                    }
                    onSuccess(list, rows, cols, pagesList, activePage)
                } catch (e: Exception) {
                    Log.e("MainScreen", "Error parsing config JSON", e)
                }
            }
        }
    })
}

private fun saveStreamDeckConfig(
    ip: String,
    password: String,
    buttons: List<StreamDeckButtonInfo>,
    rows: Int,
    cols: Int,
    activePage: Int = 0,
    pages: List<StreamDeckPageInfo> = emptyList(),
    onSuccess: () -> Unit
) {
    val client = OkHttpClient()
    try {
        val configObj = JSONObject()
        configObj.put("theme", "pro")
        configObj.put("password", password)
        configObj.put("stream_deck_rows", rows)
        configObj.put("stream_deck_cols", cols)
        configObj.put("active_page", activePage)

        if (pages.isNotEmpty()) {
            val pagesArr = org.json.JSONArray()
            for (p in pages) {
                val pObj = JSONObject()
                pObj.put("id", p.id)
                pObj.put("title", p.title)
                pagesArr.put(pObj)
            }
            configObj.put("stream_deck_pages", pagesArr)
        }
        
        val buttonsArray = org.json.JSONArray()
        for (b in buttons) {
            val bObj = JSONObject()
            bObj.put("id", b.id)
            bObj.put("label", b.label)
            bObj.put("type", b.type)
            bObj.put("value", b.value)
            bObj.put("icon", b.icon)
            bObj.put("page", b.page)
            bObj.put("state", b.state)
            bObj.put("color", b.color)
            bObj.put("row_span", b.rowSpan)
            bObj.put("col_span", b.colSpan)
            bObj.put("bg_style", b.bgStyle)
            bObj.put("clock_style", b.clockStyle)
            bObj.put("font_family", b.fontFamily)
            bObj.put("clock_font_size", b.clockFontSize)
            bObj.put("grid_row", b.gridRow)
            bObj.put("grid_col", b.gridCol)
            buttonsArray.put(bObj)
        }
        configObj.put("stream_deck_buttons", buttonsArray)

        val requestBody = configObj.toString().toRequestBody("application/json".toMediaTypeOrNull())
        val request = Request.Builder()
            .url("http://$ip:8085/config")
            .post(requestBody)
            .addHeader("X-Password", password)
            .build()
            
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: java.io.IOException) {
                Log.e("MainScreen", "Failed to save stream deck config", e)
            }
            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (response.isSuccessful) {
                        onSuccess()
                    }
                }
            }
        })
    } catch (e: Exception) {
        Log.e("MainScreen", "Error preparing save config JSON", e)
    }
}

data class SharedFileInfo(
    val name: String,
    val size: Long,
    val modified: Long
)
data class FlowMessageItem(
    val id: String,
    val sender: String,
    val type: String,
    val text: String,
    val fileName: String = "",
    val fileSize: Long = 0,
    val fileUrl: String = "",
    val timestamp: Long = System.currentTimeMillis()
)

private fun fetchFlowHistory(
    ip: String,
    password: String,
    onSuccess: (List<FlowMessageItem>) -> Unit,
    onFailure: (Exception) -> Unit
) {
    val client = OkHttpClient()
    val request = Request.Builder()
        .url("http://$ip:8085/api/flow")
        .addHeader("X-Password", password)
        .build()
    client.newCall(request).enqueue(object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) {
            onFailure(e)
        }
        override fun onResponse(call: Call, response: Response) {
            response.use {
                if (!response.isSuccessful) {
                    onFailure(Exception("Server returned code ${response.code}"))
                    return
                }
                try {
                    val body = response.body?.string() ?: return
                    val json = JSONObject(body)
                    val arr = json.optJSONArray("messages") ?: org.json.JSONArray()
                    val list = mutableListOf<FlowMessageItem>()
                    for (i in 0 until arr.length()) {
                        val item = arr.getJSONObject(i)
                        list.add(FlowMessageItem(
                            id = item.optString("id"),
                            sender = item.optString("sender", "pc"),
                            type = item.optString("type", "text"),
                            text = item.optString("text"),
                            fileName = item.optString("file_name"),
                            fileSize = item.optLong("file_size", 0L),
                            fileUrl = item.optString("file_url"),
                            timestamp = item.optLong("timestamp", System.currentTimeMillis())
                        ))
                    }
                    onSuccess(list)
                } catch (e: Exception) {
                    onFailure(e)
                }
            }
        }
    })
}

private fun fetchSharedFiles(
    ip: String,
    password: String,
    onSuccess: (List<SharedFileInfo>) -> Unit,
    onFailure: (Exception) -> Unit
) {
    val client = OkHttpClient()
    val request = Request.Builder()
        .url("http://$ip:8085/files")
        .addHeader("X-Password", password)
        .build()
    client.newCall(request).enqueue(object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) {
            onFailure(e)
        }
        override fun onResponse(call: Call, response: Response) {
            response.use {
                if (!response.isSuccessful) {
                    onFailure(Exception("Server returned code ${response.code}"))
                    return
                }
                try {
                    val body = response.body?.string() ?: return
                    val json = JSONObject(body)
                    val filesArray = json.getJSONArray("files")
                    val list = mutableListOf<SharedFileInfo>()
                    for (i in 0 until filesArray.length()) {
                        val item = filesArray.getJSONObject(i)
                        list.add(SharedFileInfo(
                            name = item.getString("name"),
                            size = item.getLong("size"),
                            modified = item.getLong("modified")
                        ))
                    }
                    onSuccess(list)
                } catch (e: Exception) {
                    onFailure(e)
                }
            }
        }
    })
}

private fun deleteSharedFile(
    ip: String,
    password: String,
    filename: String,
    onSuccess: () -> Unit,
    onFailure: (Exception) -> Unit
) {
    val client = OkHttpClient()
    val request = Request.Builder()
        .url("http://$ip:8085/files/${Uri.encode(filename)}")
        .delete()
        .addHeader("X-Password", password)
        .build()
    client.newCall(request).enqueue(object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) {
            onFailure(e)
        }
        override fun onResponse(call: Call, response: Response) {
            response.use {
                if (response.isSuccessful) {
                    onSuccess()
                } else {
                    onFailure(Exception("Server returned code ${response.code}"))
                }
            }
        }
    })
}

private fun downloadFileUsingManager(context: Context, ip: String, password: String, filename: String) {
    try {
        val downloadManager = context.getSystemService(Context.DOWNLOAD_SERVICE) as android.app.DownloadManager
        val url = "http://$ip:8085/download/${Uri.encode(filename)}?password=${Uri.encode(password)}"
        val uri = Uri.parse(url)
        
        val request = android.app.DownloadManager.Request(uri)
            .setTitle(filename)
            .setDescription("BigPocket'tan indiriliyor")
            .setNotificationVisibility(android.app.DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            .setDestinationInExternalPublicDir(android.os.Environment.DIRECTORY_DOWNLOADS, filename)
            .setAllowedOverMetered(true)
            .setAllowedOverRoaming(true)
            
        downloadManager.enqueue(request)
        Toast.makeText(context, "İndirme başlatıldı: $filename", Toast.LENGTH_SHORT).show()
    } catch (e: Exception) {
        Log.e("FileDownload", "Download failed", e)
        Toast.makeText(context, "İndirme başlatılamadı: ${e.message}", Toast.LENGTH_LONG).show()
    }
}

private suspend fun autoDetectPCIP(): String? = withContext(Dispatchers.IO) {
    val subnets = mutableListOf<String>()
    try {
        val interfaces = java.net.NetworkInterface.getNetworkInterfaces()
        while (interfaces.hasMoreElements()) {
            val element = interfaces.nextElement()
            val name = element.name.lowercase()
            if (name.contains("rndis") || name.contains("usb") || name.contains("wlan") || name.contains("ap") || name.contains("eth")) {
                val addresses = element.inetAddresses
                while (addresses.hasMoreElements()) {
                    val addr = addresses.nextElement()
                    if (!addr.isLoopbackAddress) {
                        val ip = addr.hostAddress ?: ""
                        if (ip.contains(".") && (ip.startsWith("192.168.") || ip.startsWith("172."))) {
                            val parts = ip.split(".")
                            if (parts.size == 4) {
                                subnets.add("${parts[0]}.${parts[1]}.${parts[2]}")
                            }
                        }
                    }
                }
            }
        }
    } catch (e: Exception) {
        Log.e("AutoDetect", "Error getting interfaces", e)
    }

    if (subnets.isEmpty()) return@withContext null

    for (subnetPrefix in subnets.distinct()) {
        val deferreds = (1..254).map { host ->
            async {
                val targetIp = "$subnetPrefix.$host"
                try {
                    val socket = java.net.Socket()
                    socket.connect(java.net.InetSocketAddress(targetIp, 8085), 150) // 150ms timeout
                    socket.close()
                    targetIp
                } catch (e: Exception) {
                    null
                }
            }
        }
        val results = deferreds.awaitAll().filterNotNull()
        if (results.isNotEmpty()) {
            return@withContext results.first()
        }
    }
    return@withContext null
}

private fun formatFileSize(size: Long): String {
    if (size <= 0) return "0 B"
    val units = arrayOf("B", "KB", "MB", "GB", "TB")
    val digitGroups = (Math.log10(size.toDouble()) / Math.log10(1024.0)).toInt()
    return String.format(java.util.Locale.US, "%.1f %s", size / Math.pow(1024.0, digitGroups.toDouble()), units[digitGroups])
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(
    onItemClick: (androidx.navigation3.runtime.NavKey) -> Unit = {},
    modifier: Modifier = Modifier
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val coroutineScope = rememberCoroutineScope()
    val focusManager = LocalFocusManager.current
    
    val prefs = remember { context.getSharedPreferences("bigpocket_prefs", Context.MODE_PRIVATE) }
    // Critical screen state survives configuration changes and process death.
    var ipAddress by rememberSaveable { mutableStateOf(prefs.getString("ip_address", "192.168.1.100") ?: "192.168.1.100") }
    var password by rememberSaveable { mutableStateOf(prefs.getString("password", "") ?: "") }
    var isGamerTheme by remember { mutableStateOf(false) }
    var keepScreenOn by rememberSaveable { mutableStateOf(prefs.getBoolean("keep_screen_on", true)) }

    // Remembers IPs the user explicitly connected to, so UDP auto-discovery is only allowed to
    // reuse the stored password towards a host the user has already approved.
    val trustIp: (String) -> Unit = { approvedIp ->
        if (approvedIp.isNotBlank()) {
            val trusted = (prefs.getStringSet("trusted_ips", emptySet()) ?: emptySet()).toMutableSet()
            if (trusted.add(approvedIp)) {
                prefs.edit().putStringSet("trusted_ips", trusted).apply()
            }
        }
    }

    // Keep screen awake (FLAG_KEEP_SCREEN_ON)
    DisposableEffect(keepScreenOn) {
        val window = (context as? Activity)?.window
        if (keepScreenOn) {
            window?.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        } else {
            window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
        onDispose {
            window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
    }

    // Connection state
    var isConnected by remember { mutableStateOf(SocketManager.isConnected) }
    var selectedTab by rememberSaveable { mutableStateOf(0) }
    var showQrScanner by remember { mutableStateOf(false) }
    var streamDeckButtons by remember { mutableStateOf<List<StreamDeckButtonInfo>>(emptyList()) }
    var streamDeckRows by remember { mutableIntStateOf(2) }
    var streamDeckCols by remember { mutableIntStateOf(4) }
    var streamDeckPages by remember { mutableStateOf<List<StreamDeckPageInfo>>(listOf(
        StreamDeckPageInfo(0, "Ana Sayfa"),
        StreamDeckPageInfo(1, "Medya & Ses"),
        StreamDeckPageInfo(2, "Sistem & PC")
    )) }
    var activeDeckPage by rememberSaveable { mutableStateOf(0) }
    var liveStats by remember { mutableStateOf(StreamDeckLiveStats()) }
    var currentMasterVolume by remember { mutableFloatStateOf(50f) }
    var currentBrightness by remember { mutableIntStateOf(80) }
    var lastVolSentTime by remember { mutableLongStateOf(0L) }
    var lastBrightSentTime by remember { mutableLongStateOf(0L) }

    // Camera FPS & Quality preferences
    var cameraFps by rememberSaveable { mutableStateOf(prefs.getInt("camera_fps", 30)) }
    var cameraQuality by rememberSaveable { mutableStateOf(prefs.getString("camera_quality", "720p") ?: "720p") }
    var isMonitorLandscape by rememberSaveable { mutableStateOf(prefs.getBoolean("monitor_landscape", false)) }
    var isMonitorFullscreen by rememberSaveable { mutableStateOf(prefs.getBoolean("monitor_fullscreen", false)) }
    var installedApps by remember { mutableStateOf<List<InstalledAppInfo>>(emptyList()) }
    var showEditDialog by remember { mutableStateOf(false) }
    var editingButton by remember { mutableStateOf<StreamDeckButtonInfo?>(null) }
    var showGridSettingsDialog by remember { mutableStateOf(false) }
    var showAddWidgetDialog by remember { mutableStateOf(false) }
    var targetAddSlotIndex by remember { mutableIntStateOf(0) }
    var showSettingsDialog by remember { mutableStateOf(false) }
    var manualUpdateInfo by remember { mutableStateOf<UpdateManager.UpdateInfo?>(null) }

    // Media streaming states
    var isMicStreaming by remember { mutableStateOf(false) }
    var isCamStreaming by remember { mutableStateOf(false) }
    var useFrontCamera by rememberSaveable { mutableStateOf(prefs.getBoolean("use_front_camera", false)) }

    // Clipboard State
    var clipboardText by remember { mutableStateOf("") }

    // File Upload State
    var uploadStatus by remember { mutableStateOf("") }

    // System Back: go back to the main tab instead of closing the app.
    BackHandler(enabled = selectedTab != 0) { selectedTab = 0 }

    // Single PreviewView shared with the Media tab, so the live camera preview keeps its
    // surface while the user switches tabs.
    val cameraPreviewView = remember {
        PreviewView(context).apply {
            implementationMode = PreviewView.ImplementationMode.COMPATIBLE
            scaleType = PreviewView.ScaleType.FILL_CENTER
        }
    }

    // Screen rotation and fullscreen controller based on tab selection. The Deck follows the
    // physical device orientation; only the second monitor can be forced to landscape.
    LaunchedEffect(selectedTab, isMonitorLandscape, isMonitorFullscreen) {
        val activity = context as? Activity
        // The Stream Deck is always edge-to-edge full screen; only the second monitor is toggled.
        val shouldBeFullscreen = selectedTab == 1 || (selectedTab == 3 && isMonitorFullscreen)

        activity?.requestedOrientation = when (selectedTab) {
            1 -> ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
            3 -> if (isMonitorLandscape) ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE else ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            else -> ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
        }

        activity?.window?.let { window ->
            if ((selectedTab == 1 || selectedTab == 3) && shouldBeFullscreen) {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    window.insetsController?.hide(
                        WindowInsets.Type.statusBars() or WindowInsets.Type.navigationBars()
                    )
                    window.insetsController?.systemBarsBehavior = WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                } else {
                    @Suppress("DEPRECATION")
                    window.decorView.systemUiVisibility = (
                        View.SYSTEM_UI_FLAG_FULLSCREEN
                        or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        or View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    )
                }
            } else {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    window.insetsController?.show(
                        WindowInsets.Type.statusBars() or WindowInsets.Type.navigationBars()
                    )
                } else {
                    @Suppress("DEPRECATION")
                    window.decorView.systemUiVisibility = View.SYSTEM_UI_FLAG_VISIBLE
                }
            }
        }
    }
    
    // Listen for WebSocket connection changes
    DisposableEffect(Unit) {
        SocketManager.connectionStateListener = { connected ->
            coroutineScope.launch(Dispatchers.Main) {
                isConnected = connected
                if (!connected) {
                    isMicStreaming = false
                    isCamStreaming = false
                    ScreenReceiver.stop()
                    streamDeckButtons = emptyList()
                    streamDeckRows = 2
                    streamDeckCols = 4
                } else {
                    fetchStreamDeckConfig(ipAddress, password) { buttons, rows, cols, pages, activePage ->
                        coroutineScope.launch(Dispatchers.Main) {
                            streamDeckButtons = buttons
                            streamDeckRows = rows
                            streamDeckCols = cols
                            if (pages.isNotEmpty()) streamDeckPages = pages
                            activeDeckPage = activePage
                        }
                    }
                }
            }
        }
        
        SocketManager.clipboardListener = { text ->
            coroutineScope.launch(Dispatchers.Main) {
                clipboardText = text
                try {
                    val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                    val clip = android.content.ClipData.newPlainText("BigPocket Sync", text)
                    clipboard.setPrimaryClip(clip)
                    Toast.makeText(context, "Pano bilgisayarla eşitlendi!", Toast.LENGTH_SHORT).show()
                } catch (e: Exception) {
                    Log.e("MainScreen", "Clipboard sync error", e)
                }
            }
        }

        SocketManager.configUpdateListener = {
            fetchStreamDeckConfig(ipAddress, password) { buttons, rows, cols, pages, activePage ->
                coroutineScope.launch(Dispatchers.Main) {
                    streamDeckButtons = buttons
                    streamDeckRows = rows
                    streamDeckCols = cols
                    if (pages.isNotEmpty()) streamDeckPages = pages
                    activeDeckPage = activePage
                }
            }
        }

        SocketManager.buttonStateListener = { btnId, state, page ->
            coroutineScope.launch(Dispatchers.Main) {
                streamDeckButtons = streamDeckButtons.map { b ->
                    if (b.id == btnId) b.copy(state = state) else b
                }
            }
        }

        SocketManager.pageSwitchedListener = { page ->
            coroutineScope.launch(Dispatchers.Main) {
                activeDeckPage = page
            }
        }

        SocketManager.volumeChangedListener = { action, level ->
            coroutineScope.launch(Dispatchers.Main) {
                currentMasterVolume = level
            }
        }

        SocketManager.brightnessChangedListener = { level ->
            coroutineScope.launch(Dispatchers.Main) {
                currentBrightness = level
            }
        }

        SocketManager.systemStatsListener = { cpu, ram ->
            coroutineScope.launch(Dispatchers.Main) {
                liveStats = liveStats.copy(cpu = cpu, ram = ram)
            }
        }

        SocketManager.detailedStatsListener = { cpu, ram, temp, ramUsed, ramTotal ->
            coroutineScope.launch(Dispatchers.Main) {
                liveStats = liveStats.copy(
                    cpu = cpu,
                    ram = ram,
                    cpuTemp = temp,
                    ramUsedGb = ramUsed,
                    ramTotalGb = ramTotal,
                    // History is appended exactly once per `system_stats` frame — the sibling
                    // simple-stats listener above only refreshes the plain values.
                    cpuHistory = liveStats.cpuHistory.withSparkSample(cpu.toFloat()),
                    ramHistory = liveStats.ramHistory.withSparkSample(ram.toFloat())
                )
            }
        }

        SocketManager.networkStatsListener = { downKbps, upKbps ->
            coroutineScope.launch(Dispatchers.Main) {
                liveStats = liveStats.copy(
                    netDownKbps = downKbps,
                    netUpKbps = upKbps,
                    netDownHistory = liveStats.netDownHistory.withSparkSample(downKbps.toFloat()),
                    netUpHistory = liveStats.netUpHistory.withSparkSample(upKbps.toFloat())
                )
            }
        }

        SocketManager.extraStatsListener = { extras ->
            coroutineScope.launch(Dispatchers.Main) {
                liveStats = liveStats.copy(
                    diskPercent = extras.diskPercent,
                    diskFreeGb = extras.diskFreeGb,
                    diskTotalGb = extras.diskTotalGb,
                    gpu = extras.gpu,
                    gpuTemp = extras.gpuTemp,
                    mediaTitle = extras.mediaTitle,
                    mediaArtist = extras.mediaArtist,
                    mediaPlaying = extras.mediaPlaying,
                    weatherCity = extras.weatherCity,
                    weatherTemp = extras.weatherTemp,
                    weatherCode = extras.weatherCode,
                    weatherDesc = extras.weatherDesc,
                    weatherWind = extras.weatherWind
                )
            }
        }

        onDispose {
            SocketManager.connectionStateListener = null
            SocketManager.clipboardListener = null
            SocketManager.configUpdateListener = null
            SocketManager.buttonStateListener = null
            SocketManager.pageSwitchedListener = null
            SocketManager.volumeChangedListener = null
            SocketManager.brightnessChangedListener = null
            SocketManager.systemStatsListener = null
            SocketManager.detailedStatsListener = null
            SocketManager.networkStatsListener = null
            SocketManager.extraStatsListener = null
        }
    }

    // Refresh config when IP or connection state changes
    LaunchedEffect(isConnected, ipAddress) {
        if (isConnected) {
            fetchStreamDeckConfig(ipAddress, password) { buttons, rows, cols, pages, activePage ->
                coroutineScope.launch(Dispatchers.Main) {
                    streamDeckButtons = buttons
                    streamDeckRows = rows
                    streamDeckCols = cols
                    if (pages.isNotEmpty()) streamDeckPages = pages
                    activeDeckPage = activePage
                }
            }
        }
    }

    // Fetch installed apps when button editing dialog is opened
    LaunchedEffect(showEditDialog) {
        if (showEditDialog && isConnected) {
            fetchInstalledApps(ipAddress, password) { apps ->
                coroutineScope.launch(Dispatchers.Main) {
                    installedApps = apps
                }
            }
        }
    }

    // Camera Stream Controller with FPS & Quality
    LaunchedEffect(isCamStreaming, useFrontCamera, cameraFps, cameraQuality) {
        if (isCamStreaming) {
            SocketManager.startCameraStream()
            startCameraAnalysis(context, lifecycleOwner, useFrontCamera, cameraFps, cameraQuality, cameraPreviewView) { jpegBytes ->
                SocketManager.sendCameraFrame(jpegBytes)
            }
        } else {
            SocketManager.stopCameraStream()
            try {
                val cameraProvider = ProcessCameraProvider.getInstance(context).get()
                cameraProvider.unbindAll()
            } catch (e: Exception) {
                // ignore
            }
        }
    }

    // Safety Camera Unbind when leaving screen
    DisposableEffect(Unit) {
        onDispose {
            try {
                val cameraProvider = ProcessCameraProvider.getInstance(context).get()
                cameraProvider.unbindAll()
            } catch (e: Exception) {
                // ignore
            }
        }
    }

    // Permission launcher
    val permissionsLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.RequestMultiplePermissions()
    ) { permissions ->
        val cameraGranted = permissions[Manifest.permission.CAMERA] ?: false
        val audioGranted = permissions[Manifest.permission.RECORD_AUDIO] ?: false
        if (!cameraGranted || !audioGranted) {
            Toast.makeText(context, "Özellikleri kullanmak için izin vermelisiniz.", Toast.LENGTH_LONG).show()
        }
    }

    LaunchedEffect(Unit) {
        permissionsLauncher.launch(
            arrayOf(
                Manifest.permission.CAMERA,
                Manifest.permission.RECORD_AUDIO,
                Manifest.permission.POST_NOTIFICATIONS
            )
        )
    }

    // File picker launcher
    val filePickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        if (uri != null) {
            uploadStatus = "Yükleniyor..."
            coroutineScope.launch(Dispatchers.IO) {
                val success = uploadFileToServer(context, uri, ipAddress, password)
                coroutineScope.launch(Dispatchers.Main) {
                    uploadStatus = if (success) "Dosya başarıyla gönderildi!" else "Yükleme başarısız."
                }
            }
        }
    }

    // Custom Button Icon picker launcher
    var longPressedButtonId by remember { mutableIntStateOf(-1) }
    val buttonIconPickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        if (uri != null && longPressedButtonId != -1) {
            val btnId = longPressedButtonId
            uploadStatus = "İkon yükleniyor..."
            coroutineScope.launch(Dispatchers.IO) {
                val success = uploadIconToServer(context, uri, ipAddress, password, btnId)
                coroutineScope.launch(Dispatchers.Main) {
                    if (success) {
                        Toast.makeText(context, "Buton ikonu başarıyla güncellendi!", Toast.LENGTH_SHORT).show()
                        fetchStreamDeckConfig(ipAddress, password) { buttons, rows, cols, pages, activePage ->
                            coroutineScope.launch(Dispatchers.Main) {
                                streamDeckButtons = buttons
                                streamDeckRows = rows
                                streamDeckCols = cols
                                if (pages.isNotEmpty()) streamDeckPages = pages
                                activeDeckPage = activePage
                            }
                        }
                    } else {
                        Toast.makeText(context, "İkon yüklenemedi.", Toast.LENGTH_SHORT).show()
                    }
                }
            }
        }
    }

    // Auto discovery: while disconnected, listen for BigPocket PCs on the LAN and connect automatically.
    LaunchedEffect(isConnected) {
        if (isConnected) return@LaunchedEffect
        val lastAttempt = mutableMapOf<String, Long>()
        var warnedPassword = false
        var warnedUntrusted = false
        com.ygt.bigpocket.services.PcDiscovery.discover(context)
            .catch { e -> Log.w("MainScreen", "Discovery unavailable", e) }
            .collect { pc ->
            if (SocketManager.isConnected || SocketManager.isConnecting) return@collect
            val now = System.currentTimeMillis()
            if (now - (lastAttempt[pc.ip] ?: 0L) < 8000) return@collect
            lastAttempt[pc.ip] = now

            // A broadcast can be spoofed by any device on the network, so the stored password
            // is only ever sent to an IP the user connected to before.
            val trustedIps = prefs.getStringSet("trusted_ips", emptySet()) ?: emptySet()
            if (pc.needsPassword && pc.ip !in trustedIps) {
                ipAddress = pc.ip
                prefs.edit().putString("ip_address", pc.ip).apply()
                if (!warnedUntrusted) {
                    warnedUntrusted = true
                    Toast.makeText(
                        context,
                        "${pc.name} şifre korumalı — bağlanmak için IP'yi onaylayın",
                        Toast.LENGTH_LONG
                    ).show()
                }
                return@collect
            }

            if (pc.needsPassword && password.isEmpty()) {
                if (!warnedPassword) {
                    warnedPassword = true
                    ipAddress = pc.ip
                    Toast.makeText(context, "${pc.name} bulundu — bağlanmak için şifre girin", Toast.LENGTH_LONG).show()
                }
                return@collect
            }
            ipAddress = pc.ip
            prefs.edit().putString("ip_address", pc.ip).apply()
            Toast.makeText(context, "${pc.name} bulundu, bağlanılıyor…", Toast.LENGTH_SHORT).show()
            SocketManager.connect(pc.ip, password)
        }
    }

    // Foreground service: keeps the PC link and any live stream alive in the background.
    LaunchedEffect(isConnected, isMicStreaming, isCamStreaming) {
        if (isConnected || isMicStreaming || isCamStreaming) {
            BigPocketService.start(context, isMicStreaming, isCamStreaming)
        } else {
            BigPocketService.stop(context)
        }
    }

    // "Paylaş -> BigPocket ile PC'ye Gönder": text and files handed over by other apps. The
    // payload stays queued until the PC link is up and is then forwarded automatically.
    val pendingShare = ShareInbox.pending
    LaunchedEffect(isConnected, pendingShare) {
        val share = pendingShare ?: return@LaunchedEffect
        if (!isConnected) {
            Toast.makeText(context, "Paylaşım hazır — bağlantı kurulunca gönderilecek.", Toast.LENGTH_LONG).show()
            return@LaunchedEffect
        }
        ShareInbox.pending = null
        val sharedText = share.text
        if (!sharedText.isNullOrBlank()) {
            SocketManager.sendControl(JSONObject().apply {
                put("type", "clipboard_sync")
                put("text", sharedText)
            })
            Toast.makeText(context, "Paylaşılan metin bilgisayara gönderildi!", Toast.LENGTH_SHORT).show()
        }
        val sharedUri = share.uri
        if (sharedUri != null) {
            uploadStatus = "Yükleniyor..."
            val targetIp = ipAddress
            val targetPassword = password
            val uploaded = withContext(Dispatchers.IO) {
                uploadFileToServer(context, Uri.parse(sharedUri), targetIp, targetPassword)
            }
            uploadStatus = if (uploaded) "Dosya başarıyla gönderildi!" else "Yükleme başarısız."
            Toast.makeText(context, uploadStatus, Toast.LENGTH_SHORT).show()
        }
    }

    BigPocketTheme(isGamerTheme = false) {
        // In-App Auto Update on Launch
        UpdatePrompt()

        // Manual Update Dialog (From Settings)
        manualUpdateInfo?.let { uInfo ->
            UpdateDialog(
                info = uInfo,
                onDismiss = { manualUpdateInfo = null }
            )
        }

        // Settings Dialog
        if (showSettingsDialog) {
            SettingsDialog(
                onDismiss = { showSettingsDialog = false },
                keepScreenOn = keepScreenOn,
                onKeepScreenOnChange = { checked ->
                    keepScreenOn = checked
                    prefs.edit().putBoolean("keep_screen_on", checked).apply()
                },
                onUpdateAvailable = { uInfo ->
                    manualUpdateInfo = uInfo
                }
            )
        }

        if (showQrScanner) {
            QrScannerDialog(
                onQrCodeScanned = { rawVal ->
                    showQrScanner = false
                    try {
                        if (rawVal.startsWith("{")) {
                            val jsonObj = JSONObject(rawVal)
                            val ip = jsonObj.getString("ip")
                            val pw = jsonObj.optString("password", "")
                            if (ip.isNotEmpty()) {
                                ipAddress = ip
                                password = pw
                                prefs.edit().putString("ip_address", ip).putString("password", pw).apply()
                                trustIp(ip)
                                SocketManager.connect(ip, pw)
                            }
                        } else {
                            ipAddress = rawVal
                            prefs.edit().putString("ip_address", rawVal).apply()
                            trustIp(rawVal)
                            SocketManager.connect(rawVal, password)
                        }
                    } catch (e: Exception) {
                        Log.e("MainScreen", "QR Scan Parse Error", e)
                        ipAddress = rawVal
                        prefs.edit().putString("ip_address", rawVal).apply()
                        trustIp(rawVal)
                        SocketManager.connect(rawVal, password)
                    }
                },
                onDismiss = { showQrScanner = false }
            )
        }
        if (showEditDialog && editingButton != null) {
            val currentBtn = editingButton!!
            var editLabel by remember(currentBtn) { mutableStateOf(currentBtn.label) }
            var editType by remember(currentBtn) { mutableStateOf(currentBtn.type) }
            var editValue by remember(currentBtn) { mutableStateOf(currentBtn.value) }
            var editIcon by remember(currentBtn) { mutableStateOf(currentBtn.icon) }
            var editColSpan by remember(currentBtn) { mutableIntStateOf(currentBtn.colSpan) }
            var editRowSpan by remember(currentBtn) { mutableIntStateOf(currentBtn.rowSpan) }
            var editClockStyle by remember(currentBtn) { mutableStateOf(normalizeClockStyle(currentBtn.clockStyle)) }
            var editFontFamily by remember(currentBtn) { mutableStateOf(currentBtn.fontFamily) }
            var appSearchQuery by remember(currentBtn) { mutableStateOf("") }

            LaunchedEffect(streamDeckButtons) {
                val updatedBtn = streamDeckButtons.find { it.id == currentBtn.id }
                if (updatedBtn != null) {
                    editIcon = updatedBtn.icon
                }
            }

            AlertDialog(
                onDismissRequest = { showEditDialog = false },
                shape = RoundedCornerShape(DeckCorner),
                containerColor = MinimalistSurface,
                title = { Text("Butonu Düzenle (Buton ${currentBtn.id + 1})") },
                text = {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .verticalScroll(rememberScrollState()),
                        verticalArrangement = Arrangement.spacedBy(12.dp)
                    ) {
                        OutlinedTextField(
                            value = editLabel,
                            onValueChange = { editLabel = it },
                            label = { Text("Buton Başlığı") },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )

                        Text("Izgara Boyutu (Kaplama)", fontWeight = FontWeight.Bold, fontSize = 13.sp)
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(6.dp)
                        ) {
                            val sizes = listOf(
                                Triple("1×1", 1, 1),
                                Triple("2×1", 2, 1),
                                Triple("1×2", 1, 2),
                                Triple("2×2", 2, 2)
                            )
                            for ((sLabel, cSpan, rSpan) in sizes) {
                                val isSelected = editColSpan == cSpan && editRowSpan == rSpan
                                Box(
                                    modifier = Modifier
                                        .weight(1f)
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(if (isSelected) MinimalistPrimary else MinimalistSurfaceElevated)
                                        .border(1.dp, if (isSelected) MinimalistPrimary else MinimalistBorder, RoundedCornerShape(12.dp))
                                        .clickable {
                                            editColSpan = cSpan
                                            editRowSpan = rSpan
                                        }
                                        .padding(vertical = 7.dp),
                                    contentAlignment = Alignment.Center
                                ) {
                                    Text(
                                        text = sLabel,
                                        fontSize = 12.sp,
                                        fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                                        color = if (isSelected) MinimalistBackground else MinimalistSecondary
                                    )
                                }
                            }
                        }

                        Text("Eylem / Widget Türü", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .horizontalScroll(rememberScrollState()),
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            val types = listOf(
                                "hotkey" to "Kısayol",
                                "command" to "Uygulama",
                                "toggle" to "Aç/Kapa (LED)",
                                "volume_slider" to "Ses Barı",
                                "brightness_slider" to "Parlaklık",
                                "clock_widget" to "Saat",
                                "weather" to "Hava Durumu",
                                "now_playing" to "Şimdi Çalıyor",
                                "live_info" to "Donanım & Disk",
                                "media_widget" to "Medya Masası",
                                "net_speed" to "Ağ Hızı",
                                "pomodoro" to "Pomodoro"
                            )
                            for ((typeKey, typeLabel) in types) {
                                val isSelected = editType == typeKey
                                Box(
                                    modifier = Modifier
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(if (isSelected) MinimalistPrimary else MinimalistSurfaceElevated)
                                        .border(1.dp, if (isSelected) MinimalistPrimary else MinimalistBorder, RoundedCornerShape(12.dp))
                                        .clickable {
                                             editType = typeKey
                                             if (typeKey == "volume_slider" && editLabel.isEmpty()) editLabel = "Ana Ses"
                                             if (typeKey == "brightness_slider" && editLabel.isEmpty()) editLabel = "Parlaklık"
                                             if (typeKey == "clock_widget") {
                                                 if (editLabel.isEmpty()) editLabel = "Saat"
                                                 if (editValue.isBlank()) editValue = "digital_clean"
                                             }
                                             if (typeKey == "weather") {
                                                 if (editLabel.isEmpty()) editLabel = "Hava Durumu"
                                                 if (editValue.isBlank()) editValue = "Istanbul"
                                             }
                                             if (typeKey == "now_playing") {
                                                 if (editLabel.isEmpty()) editLabel = "Şimdi Çalıyor"
                                                 editValue = "media_session"
                                             }
                                             if (typeKey == "live_info") {
                                                 if (editLabel.isEmpty()) editLabel = "CPU / RAM"
                                                 if (editValue.isBlank()) editValue = "cpu"
                                             }
                                             if (typeKey == "media_widget" && editLabel.isEmpty()) editLabel = "Medya Masası"
                                             if (typeKey == "net_speed" && editLabel.isEmpty()) editLabel = "Ağ Hızı"
                                             if (typeKey == "pomodoro" && editLabel.isEmpty()) editLabel = "Pomodoro"
                                         }
                                        .padding(horizontal = 12.dp, vertical = 8.dp),
                                    contentAlignment = Alignment.Center
                                ) {
                                    Text(
                                        text = typeLabel,
                                        fontSize = 12.sp,
                                        fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                                        color = if (isSelected) MinimalistBackground else MinimalistSecondary
                                    )
                                }
                            }
                        }

                        OutlinedTextField(
                            value = editValue,
                            onValueChange = { editValue = it },
                            label = {
                                Text(
                                    when (editType) {
                                        "hotkey" -> "Tuş Kombinasyonu (Örn. ctrl+c)"
                                        "weather" -> "Şehir (Örn. Istanbul)"
                                        "now_playing" -> "Medya Kaynağı"
                                        else -> "Uygulama / Komut Yolu"
                                    }
                                )
                            },
                            placeholder = {
                                Text(
                                    when (editType) {
                                        "hotkey" -> "ctrl+shift+esc veya volumeup"
                                        "weather" -> "Istanbul, Ankara, London..."
                                        "now_playing" -> "media_session"
                                        else -> "notepad.exe veya cmd.exe"
                                    }
                                )
                            },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )

                        // Live value pickers for the new widget families.
                        when (editType) {
                            "live_info" -> EditorChoiceRow(
                                title = "Canlı Metrik",
                                options = listOf(
                                    "cpu" to "CPU %",
                                    "cpu_temp" to "PC Sıcaklık",
                                    "ram" to "RAM",
                                    "disk" to "Disk Doluluk",
                                    "gpu" to "GPU Yükü",
                                    "net_speed" to "Ağ Hızı",
                                    "combo" to "Kombo"
                                ),
                                selected = editValue,
                                onSelect = { editValue = it }
                            )
                            "weather" -> EditorChoiceRow(
                                title = "Şehir",
                                options = listOf(
                                    "Istanbul" to "Istanbul",
                                    "Ankara" to "Ankara",
                                    "Izmir" to "Izmir",
                                    "Bursa" to "Bursa",
                                    "Antalya" to "Antalya",
                                    "London" to "London",
                                    "New York" to "New York"
                                ),
                                selected = editValue,
                                onSelect = { editValue = it }
                            )
                            "clock_widget" -> {
                                EditorChoiceRow(
                                    title = "Saat Stili",
                                    options = listOf(
                                        "apple_minimal" to "Apple Minimal",
                                        "flip_clock" to "Flip",
                                        "neon_cyberpunk" to "Neon",
                                        "word_clock" to "Kelime",
                                        "swiss_serif" to "İsviçre",
                                        "retro_terminal" to "Terminal"
                                    ),
                                    selected = editClockStyle,
                                    onSelect = { editClockStyle = it }
                                )
                                EditorChoiceRow(
                                    title = "Yazı Tipi",
                                    options = listOf(
                                        "mono" to "Mono",
                                        "serif" to "Serif",
                                        "sans" to "Sans"
                                    ),
                                    selected = editFontFamily,
                                    onSelect = { editFontFamily = it }
                                )
                            }
                        }

                        if (editType == "command") {
                            Text("Hazır PC Uygulamaları", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MinimalistPrimary)
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .horizontalScroll(rememberScrollState()),
                                horizontalArrangement = Arrangement.spacedBy(8.dp)
                            ) {
                                val appPresets = listOf(
                                    "Notepad" to "notepad.exe",
                                    "Chrome" to "cmd /c start chrome",
                                    "Spotify" to "cmd /c start spotify",
                                    "Hesap Makinesi" to "calc.exe",
                                    "Ekranı Kilitle" to "rundll32.exe user32.dll,LockWorkStation",
                                    "CMD" to "cmd.exe",
                                    "Görev Yöneticisi" to "taskmgr.exe",
                                    "Paint" to "mspaint.exe"
                                )
                                for (preset in appPresets) {
                                    Box(
                                        modifier = Modifier
                                            .clip(RoundedCornerShape(12.dp))
                                            .background(MinimalistSurfaceElevated)
                                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                                            .clickable {
                                                editValue = preset.second
                                                editLabel = preset.first
                                                editIcon = when (preset.first) {
                                                    "Notepad" -> "task"
                                                    "Chrome" -> "web"
                                                    "Spotify" -> "play"
                                                    "Hesap Makinesi" -> "calc"
                                                    "Ekranı Kilitle" -> "lock"
                                                    "Paint" -> "calc"
                                                    else -> "task"
                                                }
                                            }
                                            .padding(horizontal = 12.dp, vertical = 6.dp),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Text(preset.first, fontSize = 12.sp)
                                    }
                                }
                            }
                            Spacer(modifier = Modifier.height(12.dp))
                            Text("Bilgisayardaki Uygulamalar", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MinimalistPrimary)
                            Spacer(modifier = Modifier.height(4.dp))
                            OutlinedTextField(
                                value = appSearchQuery,
                                onValueChange = { appSearchQuery = it },
                                label = { Text("PC Uygulamalarında Ara") },
                                singleLine = true,
                                modifier = Modifier.fillMaxWidth()
                            )
                            Spacer(modifier = Modifier.height(4.dp))
                            val filteredApps = installedApps.filter { it.name.contains(appSearchQuery, ignoreCase = true) }
                            Column(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .heightIn(max = 120.dp)
                                    .verticalScroll(rememberScrollState())
                            ) {
                                if (filteredApps.isEmpty()) {
                                    Text("Uygulama bulunamadı.", fontSize = 11.sp, color = MinimalistSecondary, modifier = Modifier.padding(vertical = 4.dp))
                                } else {
                                    for (app in filteredApps.take(15)) {
                                        Box(
                                            modifier = Modifier
                                                .fillMaxWidth()
                                                .clickable {
                                                    editValue = app.path
                                                    editLabel = app.name
                                                    editIcon = when {
                                                        app.name.contains("chrome", ignoreCase = true) || app.name.contains("browser", ignoreCase = true) -> "web"
                                                        app.name.contains("music", ignoreCase = true) || app.name.contains("spotify", ignoreCase = true) -> "play"
                                                        app.name.contains("volume", ignoreCase = true) || app.name.contains("sound", ignoreCase = true) -> "volup"
                                                        app.name.contains("mic", ignoreCase = true) -> "mic"
                                                        app.name.contains("lock", ignoreCase = true) -> "lock"
                                                        app.name.contains("task", ignoreCase = true) || app.name.contains("manager", ignoreCase = true) -> "task"
                                                        app.name.contains("settings", ignoreCase = true) -> "settings"
                                                        else -> "star"
                                                    }
                                                }
                                                .padding(vertical = 6.dp, horizontal = 4.dp)
                                        ) {
                                            Text(app.name, fontSize = 12.sp)
                                        }
                                        Divider(color = MinimalistBorder)
                                    }
                                }
                            }
                        } else {
                            Text("Hazır Kısayollar", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MinimalistPrimary)
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .horizontalScroll(rememberScrollState()),
                                horizontalArrangement = Arrangement.spacedBy(8.dp)
                            ) {
                                val hotkeyPresets = listOf(
                                    "Sesi Kapat" to "mute",
                                    "Ses Artır" to "volumeup",
                                    "Ses Azalt" to "volumedown",
                                    "Oynat/Duraklat" to "playpause",
                                    "Sonraki Şarkı" to "nexttrack",
                                    "Önceki Şarkı" to "prevtrack",
                                    "Kopyala" to "ctrl+c",
                                    "Yapıştır" to "ctrl+v",
                                    "Geri Al" to "ctrl+z",
                                    "Kilit (Win+L)" to "meta+l",
                                    "Görev Yöneticisi" to "ctrl+shift+esc"
                                )
                                for (preset in hotkeyPresets) {
                                    Box(
                                        modifier = Modifier
                                            .clip(RoundedCornerShape(12.dp))
                                            .background(MinimalistSurfaceElevated)
                                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                                            .clickable {
                                                editValue = preset.second
                                                editLabel = preset.first
                                                editIcon = when (preset.second) {
                                                    "mute" -> "mic"
                                                    "volumeup" -> "volup"
                                                    "volumedown" -> "voldown"
                                                    "playpause", "nexttrack", "prevtrack" -> "play"
                                                    "ctrl+shift+esc" -> "task"
                                                    "meta+l" -> "lock"
                                                    else -> "star"
                                                }
                                            }
                                            .padding(horizontal = 12.dp, vertical = 6.dp),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Text(preset.first, fontSize = 12.sp)
                                    }
                                }
                            }
                        }

                        Text("Simge Seçin", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .horizontalScroll(rememberScrollState()),
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            val builtInIcons = listOf("mic", "volup", "voldown", "play", "calc", "web", "lock", "task", "settings", "info", "star", "refresh")
                            for (iconName in builtInIcons) {
                                Box(
                                    modifier = Modifier
                                        .size(44.dp)
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(if (editIcon == iconName) MinimalistPrimary.copy(alpha = 0.2f) else MinimalistSurfaceElevated)
                                        .border(
                                            1.dp,
                                            if (editIcon == iconName) MinimalistPrimary else MinimalistBorder,
                                            RoundedCornerShape(12.dp)
                                        )
                                        .clickable { editIcon = iconName }
                                        .padding(8.dp),
                                    contentAlignment = Alignment.Center
                                ) {
                                    Icon(
                                        imageVector = getVectorIcon(iconName),
                                        contentDescription = iconName,
                                        tint = if (editIcon == iconName) MinimalistPrimary else MinimalistSecondary,
                                        modifier = Modifier.size(24.dp)
                                    )
                                }
                            }
                        }

                        Button(
                            onClick = {
                                longPressedButtonId = currentBtn.id
                                buttonIconPickerLauncher.launch("image/*")
                            },
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistSurfaceElevated,
                                contentColor = MinimalistSecondary
                            ),
                            modifier = Modifier.fillMaxWidth(),
                            shape = RoundedCornerShape(12.dp)
                        ) {
                            Icon(Icons.Default.Add, contentDescription = "Upload", modifier = Modifier.size(16.dp))
                            Spacer(modifier = Modifier.width(8.dp))
                            Text("Galeriden Özel Görsel Yükle", fontSize = 12.sp)
                        }

                        Divider(color = MinimalistBorder)
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Text("Aktif Simge Önizleme:", fontSize = 13.sp, modifier = Modifier.weight(1f))
                            Box(
                                modifier = Modifier
                                    .size(44.dp)
                                    .clip(RoundedCornerShape(12.dp))
                                    .background(MinimalistSurfaceElevated)
                                    .border(1.dp, MinimalistPrimary.copy(alpha = 0.15f), RoundedCornerShape(12.dp)),
                                contentAlignment = Alignment.Center
                            ) {
                                if (editIcon.startsWith("custom:") && editIcon.length > 7) {
                                    val iconPath = editIcon.substring(7)
                                    val imageUrl = "http://$ipAddress:8085/$iconPath?password=${Uri.encode(password)}"
                                    AsyncImage(
                                        model = imageUrl,
                                        contentDescription = "Custom Icon",
                                        modifier = Modifier.size(32.dp).clip(RoundedCornerShape(8.dp))
                                    )
                                } else {
                                    Icon(
                                        imageVector = getVectorIcon(editIcon),
                                        contentDescription = "Icon Preview",
                                        tint = MinimalistPrimary,
                                        modifier = Modifier.size(24.dp)
                                    )
                                }
                            }
                        }
                    }
                },
                confirmButton = {
                    Button(
                        onClick = {
                            val updatedButtons = streamDeckButtons.map {
                                if (it.id == currentBtn.id) {
                                    currentBtn.copy(
                                        label = editLabel,
                                        type = editType,
                                        value = editValue,
                                        icon = editIcon,
                                        colSpan = editColSpan,
                                        rowSpan = editRowSpan,
                                        clockStyle = if (editType == "clock_widget") editClockStyle else currentBtn.clockStyle,
                                        fontFamily = if (editType == "clock_widget") editFontFamily else currentBtn.fontFamily
                                    )
                                } else {
                                    it
                                }
                            }
                            saveStreamDeckConfig(ipAddress, password, updatedButtons, streamDeckRows, streamDeckCols, activeDeckPage, streamDeckPages) {
                                coroutineScope.launch(Dispatchers.Main) {
                                    Toast.makeText(context, "Buton başarıyla güncellendi!", Toast.LENGTH_SHORT).show()
                                    streamDeckButtons = updatedButtons
                                }
                            }
                            showEditDialog = false
                        },
                        shape = RoundedCornerShape(12.dp)
                    ) {
                        Text("Kaydet")
                    }
                },
                dismissButton = {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(4.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        TextButton(
                            onClick = {
                                val updatedButtons = streamDeckButtons.filter { it.id != currentBtn.id }
                                saveStreamDeckConfig(ipAddress, password, updatedButtons, streamDeckRows, streamDeckCols, activeDeckPage, streamDeckPages) {
                                    coroutineScope.launch(Dispatchers.Main) {
                                        Toast.makeText(context, "Buton silindi!", Toast.LENGTH_SHORT).show()
                                        streamDeckButtons = updatedButtons
                                    }
                                }
                                showEditDialog = false
                            },
                            colors = ButtonDefaults.textButtonColors(contentColor = StatusRedText)
                        ) {
                            Text("Sil")
                        }
                        TextButton(onClick = { showEditDialog = false }) {
                            Text("İptal")
                        }
                    }
                }
            )
        }

        if (showGridSettingsDialog) {
            StreamDeckGridSettingsDialog(
                currentRows = streamDeckRows,
                currentCols = streamDeckCols,
                onDismiss = { showGridSettingsDialog = false },
                onApply = { newRows, newCols ->
                    streamDeckRows = newRows
                    streamDeckCols = newCols
                    SocketManager.sendControl(JSONObject().apply {
                        put("type", "set_grid_layout")
                        put("rows", newRows)
                        put("cols", newCols)
                    })
                    saveStreamDeckConfig(ipAddress, password, streamDeckButtons, newRows, newCols, activeDeckPage, streamDeckPages) {
                        coroutineScope.launch(Dispatchers.Main) {
                            Toast.makeText(context, "Matris düzeni güncellendi: ${newCols}×${newRows}", Toast.LENGTH_SHORT).show()
                        }
                    }
                    showGridSettingsDialog = false
                }
            )
        }

        if (showAddWidgetDialog) {
            AddWidgetDialog(
                slotIndex = targetAddSlotIndex,
                onDismiss = { showAddWidgetDialog = false },
                onSelectPreset = { pLabel, pType, pVal, pIcon, pColSpan, pRowSpan, pColor, pClockStyle ->
                    val newId = (streamDeckButtons.maxOfOrNull { it.id } ?: -1) + 1
                    val newBtn = StreamDeckButtonInfo(
                        id = newId,
                        label = pLabel,
                        type = pType,
                        value = pVal,
                        icon = pIcon,
                        page = activeDeckPage,
                        colSpan = pColSpan,
                        rowSpan = pRowSpan,
                        color = pColor,
                        clockStyle = pClockStyle
                    )
                    val updated = streamDeckButtons + newBtn
                    saveStreamDeckConfig(ipAddress, password, updated, streamDeckRows, streamDeckCols, activeDeckPage, streamDeckPages) {
                        coroutineScope.launch(Dispatchers.Main) {
                            Toast.makeText(context, "$pLabel bileşeni eklendi!", Toast.LENGTH_SHORT).show()
                            streamDeckButtons = updated
                        }
                    }
                    showAddWidgetDialog = false
                },
                onCustomCreate = {
                    val newId = (streamDeckButtons.maxOfOrNull { it.id } ?: -1) + 1
                    val newBtn = StreamDeckButtonInfo(
                        id = newId,
                        label = "Yeni Buton",
                        type = "hotkey",
                        value = "",
                        icon = "",
                        page = activeDeckPage,
                        colSpan = 1,
                        rowSpan = 1,
                        color = "#818CF8"
                    )
                    val updated = streamDeckButtons + newBtn
                    streamDeckButtons = updated
                    editingButton = newBtn
                    showAddWidgetDialog = false
                    showEditDialog = true
                }
            )
        }
        // The Stream Deck owns the full screen, so its tab always hides the navigation bar; the
        // second monitor keeps its explicit fullscreen toggle.
        val hideBars = selectedTab == 1 || (selectedTab == 3 && isMonitorFullscreen)

        Scaffold(
            bottomBar = {
                if (!hideBars) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .navigationBarsPadding()
                            .padding(horizontal = 16.dp, vertical = 10.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Surface(
                            modifier = Modifier.fillMaxWidth(),
                            shape = RoundedCornerShape(DeckCorner),
                            color = MinimalistSurface,
                            border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                            shadowElevation = 8.dp
                        ) {
                            NavigationBar(
                                containerColor = Color.Transparent,
                                contentColor = MinimalistPrimary,
                                tonalElevation = 0.dp
                            ) {
                                NavigationBarItem(
                                    selected = selectedTab == 0,
                                    onClick = { selectedTab = 0 },
                                    icon = { Icon(Icons.Default.Home, contentDescription = "Dashboard") },
                                    label = { Text("Giriş") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                                NavigationBarItem(
                                    selected = selectedTab == 1,
                                    onClick = { selectedTab = 1 },
                                    icon = { Icon(Icons.Default.GridView, contentDescription = "Stream Deck") },
                                    label = { Text("Deck") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                                NavigationBarItem(
                                    selected = selectedTab == 2,
                                    onClick = { selectedTab = 2 },
                                    icon = { Icon(Icons.Default.Mouse, contentDescription = "Trackpad") },
                                    label = { Text("Mouse") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                                NavigationBarItem(
                                    selected = selectedTab == 3,
                                    onClick = { 
                                        selectedTab = 3
                                        if (isConnected && !ScreenReceiver.latestFrame.let { false }) {
                                            ScreenReceiver.start(ipAddress)
                                        }
                                    },
                                    icon = { Icon(Icons.Default.Monitor, contentDescription = "Second Screen") },
                                    label = { Text("Ekran") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                                NavigationBarItem(
                                    selected = selectedTab == 4,
                                    onClick = { selectedTab = 4 },
                                    icon = { Icon(Icons.Default.Videocam, contentDescription = "Media") },
                                    label = { Text("Medya") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                                NavigationBarItem(
                                    selected = selectedTab == 5,
                                    onClick = { selectedTab = 5 },
                                    icon = { Icon(Icons.Default.Handyman, contentDescription = "Tools") },
                                    label = { Text("Araçlar") },
                                    colors = NavigationBarItemDefaults.colors(
                                        indicatorColor = MinimalistSurfaceElevated,
                                        selectedIconColor = MinimalistPrimary,
                                        selectedTextColor = MinimalistPrimary,
                                        unselectedIconColor = MinimalistSecondary,
                                        unselectedTextColor = MinimalistSecondary
                                    )
                                )
                            }
                        }
                    }
                }
            }
        ) { paddingValues ->
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(MinimalistBackground)
                    .padding(if (hideBars) PaddingValues(0.dp) else paddingValues)
            ) {
                when (selectedTab) {
                    0 -> ConnectionTab(
                        ipAddress = ipAddress,
                        onIpChange = {
                            ipAddress = it
                            prefs.edit().putString("ip_address", it).apply()
                        },
                        password = password,
                        onPasswordChange = {
                            password = it
                            prefs.edit().putString("password", it).apply()
                        },
                        isConnected = isConnected,
                        onConnectClick = {
                            focusManager.clearFocus()
                            if (isConnected) {
                                SocketManager.disconnect()
                            } else {
                                // Manual connect = explicit user approval of this host.
                                trustIp(ipAddress)
                                SocketManager.connect(ipAddress, password)
                            }
                        },
                        onScanQrClick = { showQrScanner = true },
                        context = context,
                        keepScreenOn = keepScreenOn,
                        onKeepScreenOnChange = { checked ->
                            keepScreenOn = checked
                            prefs.edit().putBoolean("keep_screen_on", checked).apply()
                        },
                        onOpenSettings = { showSettingsDialog = true }
                    )
                    1 -> StreamDeckTab(
                        isConnected = isConnected,
                        buttons = streamDeckButtons,
                        rows = streamDeckRows,
                        cols = streamDeckCols,
                        pages = streamDeckPages,
                        activePage = activeDeckPage,
                        onPageChange = { p ->
                            activeDeckPage = p
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "switch_page")
                                put("page", p)
                            })
                        },
                        live = liveStats,
                        masterVolume = currentMasterVolume,
                        onVolumeChange = { lvl ->
                            currentMasterVolume = lvl
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "set_volume")
                                put("action", "set")
                                put("level", lvl.toDouble())
                            })
                        },
                        brightness = currentBrightness,
                        onBrightnessChange = { lvl ->
                            currentBrightness = lvl
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "set_brightness")
                                put("level", lvl)
                            })
                        },
                        ipAddress = ipAddress,
                        password = password,
                        onButtonLongClick = { btn ->
                            editingButton = btn
                            showEditDialog = true
                        },
                        onApplyGridLayout = { newRows, newCols ->
                            streamDeckRows = newRows
                            streamDeckCols = newCols
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "set_grid_layout")
                                put("rows", newRows)
                                put("cols", newCols)
                            })
                            saveStreamDeckConfig(
                                ipAddress,
                                password,
                                streamDeckButtons,
                                newRows,
                                newCols,
                                activeDeckPage,
                                streamDeckPages
                            ) {}
                        },
                        onOpenGridSettings = { showGridSettingsDialog = true },
                        onAddWidgetClick = { slotIndex ->
                            targetAddSlotIndex = slotIndex
                            showAddWidgetDialog = true
                        },
                        onAddPage = {
                            val newPageId = (streamDeckPages.maxOfOrNull { it.id } ?: -1) + 1
                            val newPages = streamDeckPages + StreamDeckPageInfo(newPageId, "Sayfa " + (streamDeckPages.size + 1))
                            streamDeckPages = newPages
                            saveStreamDeckConfig(
                                ipAddress,
                                password,
                                streamDeckButtons,
                                streamDeckRows,
                                streamDeckCols,
                                newPageId,
                                newPages
                            ) {
                                coroutineScope.launch(Dispatchers.Main) {
                                    activeDeckPage = newPageId
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "switch_page")
                                        put("page", newPageId)
                                    })
                                }
                            }
                        },
                        onButtonsUpdated = { updated ->
                            streamDeckButtons = updated
                            saveStreamDeckConfig(
                                ipAddress,
                                password,
                                updated,
                                streamDeckRows,
                                streamDeckCols,
                                activeDeckPage,
                                streamDeckPages
                            ) {}
                        }
                    )
                    2 -> TrackpadTab(isConnected = isConnected)
                    3 -> MonitorTab(
                        isConnected = isConnected,
                        ipAddress = ipAddress,
                        onExitTab = { selectedTab = 0 },
                        isLandscapeMode = isMonitorLandscape,
                        onToggleLandscapeMode = {
                            val newVal = !isMonitorLandscape
                            isMonitorLandscape = newVal
                            prefs.edit().putBoolean("monitor_landscape", newVal).apply()
                        },
                        isMonitorFullscreen = isMonitorFullscreen,
                        onToggleFullscreen = {
                            val newVal = !isMonitorFullscreen
                            isMonitorFullscreen = newVal
                            prefs.edit().putBoolean("monitor_fullscreen", newVal).apply()
                        }
                    )
                    4 -> MediaTab(
                        isConnected = isConnected,
                        previewView = cameraPreviewView,
                        isMicStreaming = isMicStreaming,
                        onMicToggle = { active ->
                            isMicStreaming = active
                            if (active) SocketManager.startAudioStream() else SocketManager.stopAudioStream()
                        },
                        isCamStreaming = isCamStreaming,
                        onCamToggle = { active ->
                            isCamStreaming = active
                        },
                        useFrontCamera = useFrontCamera,
                        onCameraSelect = { active ->
                            useFrontCamera = active
                            prefs.edit().putBoolean("use_front_camera", active).apply()
                        },
                        cameraFps = cameraFps,
                        onFpsChange = { fps ->
                            cameraFps = fps
                            prefs.edit().putInt("camera_fps", fps).apply()
                        },
                        cameraQuality = cameraQuality,
                        onQualityChange = { q ->
                            cameraQuality = q
                            prefs.edit().putString("camera_quality", q).apply()
                        }
                    )
                    5 -> ToolsTab(
                        isConnected = isConnected,
                        clipboardText = clipboardText,
                        onSendClipboard = {
                            try {
                                val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                                val text = clipboard.primaryClip?.getItemAt(0)?.text?.toString() ?: ""
                                if (text.isNotEmpty()) {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "clipboard_sync")
                                        put("text", text)
                                    })
                                    Toast.makeText(context, "Pano PC'ye gönderildi!", Toast.LENGTH_SHORT).show()
                                }
                            } catch (e: Exception) {
                                Toast.makeText(context, "Pano kopyalanamadı.", Toast.LENGTH_SHORT).show()
                            }
                        },
                        uploadStatus = uploadStatus,
                        onSelectFile = {
                            filePickerLauncher.launch("*/*")
                        },
                        ipAddress = ipAddress,
                        password = password,
                        onOpenSettings = { showSettingsDialog = true }
                    )
                }
            }
        }
    }
}

// ----------------- Shared shell -----------------

/**
 * Shared page header used by all six tabs: title + optional subtitle on the left, then an
 * optional `BAĞLI / KOPUK` badge and extra actions on the right. Always inset 20.dp from the
 * screen edge so every tab's title sits on exactly the same line.
 */
@Composable
fun TopHeader(
    title: String,
    subtitle: String? = null,
    isConnected: Boolean? = null,
    actions: @Composable (RowScope.() -> Unit)? = null
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 20.dp, end = 20.dp, top = 14.dp, bottom = 10.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold,
                color = MinimalistPrimary,
                letterSpacing = (-0.3).sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
            if (!subtitle.isNullOrBlank()) {
                Text(
                    text = subtitle,
                    fontSize = 11.5.sp,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            if (isConnected != null) {
                ConnectionBadge(isConnected = isConnected)
            }
            actions?.invoke(this)
        }
    }
}

/** `BAĞLI / KOPUK` pill — the only place a status colour is allowed outside the deck widgets. */
@Composable
private fun ConnectionBadge(isConnected: Boolean) {
    val tint = if (isConnected) StatusGreenText else StatusRedText
    val background = if (isConnected) StatusGreenBg else StatusRedBg
    Row(
        modifier = Modifier
            .clip(CircleShape)
            .background(background)
            .border(1.dp, tint.copy(alpha = 0.3f), CircleShape)
            .padding(horizontal = 10.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp)
    ) {
        Box(
            modifier = Modifier
                .size(6.dp)
                .clip(CircleShape)
                .background(tint)
        )
        Text(
            text = if (isConnected) "BAĞLI" else "KOPUK",
            fontSize = 10.sp,
            fontWeight = FontWeight.Bold,
            color = tint
        )
    }
}

// ----------------- Tab Components -----------------

@Composable
fun ConnectionTab(
    ipAddress: String,
    onIpChange: (String) -> Unit,
    password: String,
    onPasswordChange: (String) -> Unit,
    isConnected: Boolean,
    onConnectClick: () -> Unit,
    onScanQrClick: () -> Unit,
    context: Context,
    keepScreenOn: Boolean = false,
    onKeepScreenOnChange: (Boolean) -> Unit = {},
    onOpenSettings: () -> Unit = {}
) {
    Column(
        modifier = Modifier.fillMaxSize()
    ) {
        TopHeader(
            title = "Giriş Paneli",
            subtitle = "PC bağlantısını kurun ve durumunu yönetin",
            isConnected = isConnected,
            actions = {
                IconButton(
                    onClick = onOpenSettings,
                    modifier = Modifier
                        .clip(RoundedCornerShape(12.dp))
                        .background(MinimalistSurfaceElevated)
                        .size(40.dp)
                ) {
                    Icon(
                        imageVector = Icons.Default.Settings,
                        contentDescription = "Ayarlar",
                        tint = MinimalistPrimary,
                        modifier = Modifier.size(20.dp)
                    )
                }
            }
        )

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center
        ) {
            BigPocketLogo(
                modifier = Modifier.size(72.dp)
            )
            
            Spacer(modifier = Modifier.height(18.dp))

            // Connection Card (Glassy & Ultra-Soft)
            Card(
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(DeckCorner),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(20.dp),
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    // IP Field and QR Scan Row
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        OutlinedTextField(
                            value = ipAddress,
                            onValueChange = onIpChange,
                            label = { Text("Bilgisayar IP Adresi") },
                            placeholder = { Text("Örn. 192.168.1.100") },
                            singleLine = true,
                            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                            modifier = Modifier.weight(1f),
                            shape = RoundedCornerShape(12.dp),
                            colors = OutlinedTextFieldDefaults.colors(
                                focusedBorderColor = MinimalistAccent,
                                unfocusedBorderColor = MinimalistBorder,
                                focusedContainerColor = MinimalistBackground,
                                unfocusedContainerColor = MinimalistBackground
                            )
                        )
                        Spacer(modifier = Modifier.width(10.dp))
                        IconButton(
                            onClick = onScanQrClick,
                            modifier = Modifier
                                .size(54.dp)
                                .background(MinimalistBackground, RoundedCornerShape(12.dp))
                                .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                        ) {
                            Icon(
                                imageVector = Icons.Default.QrCode2,
                                contentDescription = "QR Kodunu Tarat",
                                tint = MinimalistPrimary
                            )
                        }
                    }
            
                    Spacer(modifier = Modifier.height(14.dp))

                    // Password Field
                    OutlinedTextField(
                        value = password,
                        onValueChange = onPasswordChange,
                        label = { Text("Erişim Şifresi") },
                        placeholder = { Text("Sunucuda şifre yoksa boş bırakın") },
                        singleLine = true,
                        visualTransformation = androidx.compose.ui.text.input.PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(
                            imeAction = ImeAction.Done,
                            keyboardType = androidx.compose.ui.text.input.KeyboardType.Password
                        ),
                        keyboardActions = KeyboardActions(onDone = { onConnectClick() }),
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(12.dp),
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = MinimalistAccent,
                            unfocusedBorderColor = MinimalistBorder,
                            focusedContainerColor = MinimalistBackground,
                            unfocusedContainerColor = MinimalistBackground
                        )
                    )

                    Spacer(modifier = Modifier.height(20.dp))

                    // Connect Button
                    Button(
                        onClick = onConnectClick,
                        colors = ButtonDefaults.buttonColors(
                            containerColor = if (isConnected) MinimalistSurfaceElevated else MinimalistPrimary,
                            contentColor = if (isConnected) MinimalistPrimary else MinimalistBackground
                        ),
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(52.dp),
                        shape = RoundedCornerShape(12.dp)
                    ) {
                        Text(
                            text = if (isConnected) "Bağlantıyı Kes" else "Bağlan",
                            fontWeight = FontWeight.Bold,
                            fontSize = 15.sp
                        )
                    }

                    if (!isConnected) {
                        Spacer(modifier = Modifier.height(12.dp))
                        val coroutineScope = rememberCoroutineScope()
                        var isSearching by remember { mutableStateOf(false) }
                        Button(
                            onClick = {
                                isSearching = true
                                coroutineScope.launch {
                                    val detectedIp = autoDetectPCIP()
                                    isSearching = false
                                    if (detectedIp != null) {
                                        onIpChange(detectedIp)
                                        Toast.makeText(context, "Bilgisayar bulundu: $detectedIp", Toast.LENGTH_SHORT).show()
                                        onConnectClick()
                                    } else {
                                        Toast.makeText(context, "Bilgisayar bulunamadı. Lütfen USB Tethering'in açık olduğundan emin olun.", Toast.LENGTH_LONG).show()
                                    }
                                }
                            },
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistSurfaceElevated,
                                contentColor = MinimalistPrimary
                            ),
                            modifier = Modifier
                                .fillMaxWidth()
                                .height(52.dp),
                            shape = RoundedCornerShape(12.dp),
                            enabled = !isSearching
                        ) {
                            if (isSearching) {
                                CircularProgressIndicator(modifier = Modifier.size(18.dp), color = MinimalistPrimary, strokeWidth = 2.dp)
                                Spacer(modifier = Modifier.width(10.dp))
                                Text("Ağda PC Aranıyor...")
                            } else {
                                Icon(Icons.Default.Refresh, contentDescription = "Auto Detect", modifier = Modifier.size(18.dp))
                                Spacer(modifier = Modifier.width(10.dp))
                                Text("Ağdaki PC'yi Otomatik Bul")
                            }
                        }

                        Spacer(modifier = Modifier.height(10.dp))

                        Button(
                            onClick = {
                                onIpChange("127.0.0.1")
                                onConnectClick()
                            },
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistSurfaceElevated,
                                contentColor = MinimalistPrimary
                            ),
                            modifier = Modifier
                                .fillMaxWidth()
                                .height(52.dp),
                            shape = RoundedCornerShape(12.dp),
                            border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
                        ) {
                            Icon(Icons.Default.Bolt, contentDescription = "USB 1ms", tint = MinimalistPrimary, modifier = Modifier.size(18.dp))
                            Spacer(modifier = Modifier.width(10.dp))
                            Text("USB Sıfır Gecikme (127.0.0.1 - 1ms)", fontWeight = FontWeight.Bold, fontSize = 13.sp)
                        }
                    }

                    Spacer(modifier = Modifier.height(16.dp))

                    // Keep Screen Awake Option
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(12.dp))
                            .background(MinimalistBackground)
                            .padding(horizontal = 14.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween
                    ) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text("Ekran Asla Kararmasın", fontSize = 13.sp, fontWeight = FontWeight.SemiBold, color = MinimalistPrimary)
                            Text("Kullanım esnasında telefon açık kalsın", fontSize = 11.sp, color = MinimalistSecondary)
                        }
                        Switch(
                            checked = keepScreenOn,
                            onCheckedChange = onKeepScreenOnChange,
                            colors = bigPocketSwitchColors()
                        )
                    }
                }
            }

            Spacer(modifier = Modifier.height(20.dp))

            // USB Tethering Guide Card
            Card(
                shape = RoundedCornerShape(DeckCorner),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.padding(18.dp)) {
                    Text(
                        text = "Sıfır Gecikmeli Kablolu Bağlantı",
                        fontWeight = FontWeight.SemiBold,
                        fontSize = 13.sp,
                        color = MinimalistPrimary
                    )
                    Spacer(modifier = Modifier.height(6.dp))
                    Text(
                        text = "1. Telefonu USB kablosu ile bilgisayara bağlayın.\n" +
                               "2. Telefon Ayarlarından USB Tethering özelliğini aktif edin.\n" +
                               "3. Yukarıdaki IP kutusuna tethering IP'sini girin veya QR okutun.",
                        fontSize = 11.sp,
                        color = MinimalistSecondary,
                        lineHeight = 16.sp
                    )
                }
            }
        }
    }
}

// Map standard config string icons to compose Vector drawables
private fun getVectorIcon(iconName: String): androidx.compose.ui.graphics.vector.ImageVector {
    val clean = iconName.substringBefore(":")
    return when (clean) {
        "mic" -> Icons.Default.Mic
        "volup", "volume_up" -> Icons.Default.VolumeUp
        "voldown", "volume_down" -> Icons.Default.VolumeDown
        "volume_mute", "mute", "sound_mute" -> Icons.Default.VolumeMute
        "volume", "master_volume" -> Icons.Default.VolumeUp
        "bright", "brightness", "sun", "light" -> Icons.Default.LightMode
        "play", "playpause" -> Icons.Default.PlayArrow
        "skip", "nexttrack" -> Icons.Default.SkipNext
        "prev", "prevtrack" -> Icons.Default.SkipPrevious
        "calc" -> Icons.Default.Calculate
        "web", "globe" -> Icons.Default.Public
        "lock" -> Icons.Default.Lock
        "task", "taskmgr" -> Icons.Default.Assessment
        "settings" -> Icons.Default.Settings
        "info" -> Icons.Default.Info
        "refresh" -> Icons.Default.Refresh
        "cpu" -> Icons.Default.Memory
        "ram" -> Icons.Default.Storage
        "monitor", "desktop", "virtual_monitor" -> Icons.Default.Tv
        "camera" -> Icons.Default.CameraAlt
        "terminal" -> Icons.Default.Code
        "home" -> Icons.Default.Home
        "spotify" -> Icons.Default.MusicNote
        "link", "url" -> Icons.Default.Share
        "copy", "text", "text_snippet" -> Icons.Default.ContentCopy
        "headphones" -> Icons.Default.Headphones
        "speaker" -> Icons.Default.Speaker
        "folder" -> Icons.Default.Folder
        "bell" -> Icons.Default.Notifications
        "wifi" -> Icons.Default.Wifi
        "weather", "cloud" -> Icons.Default.Cloud
        "disk", "storage" -> Icons.Default.Storage
        "gpu" -> Icons.Default.Memory
        "now_playing", "music" -> Icons.Default.MusicNote
        "add" -> Icons.Default.Add
        "delete" -> Icons.Default.Delete
        "edit" -> Icons.Default.Edit
        "close" -> Icons.Default.Close
        else -> Icons.Default.Star
    }
}

/**
 * Template 5 — capsule slider (volume / brightness): soft tonal fill on the flat card surface,
 * round accent badge with the label on the left and the live percentage on the right.
 * Adapts between the horizontal and vertical pill from the cell aspect ratio.
 */
@Composable
fun AppleCapsuleSlider(
    type: String,
    value: Float,
    label: String,
    accentColor: Color,
    palette: DeckPalette? = null,
    onValueChange: (Float) -> Unit,
    onLongClick: (() -> Unit)? = null,
    modifier: Modifier = Modifier
) {
    val haptic = LocalHapticFeedback.current
    val coroutineScope = rememberCoroutineScope()
    val p = palette ?: deckPaletteFor(if (type == "brightness") "brightness_slider" else "volume_slider", "", "")
    val accent = if (accentColor == Color.Unspecified) p.accent else accentColor
    val isBrightness = type == "brightness"

    var debounceJob by remember { mutableStateOf<kotlinx.coroutines.Job?>(null) }
    var localValue by remember(value) { mutableFloatStateOf(value) }
    var isDragging by remember { mutableStateOf(false) }

    val clampedValue = (if (isDragging) localValue else value).coerceIn(0f, 100f)
    val fillFraction = clampedValue / 100f

    val commitNow: (Float) -> Unit = { target ->
        debounceJob?.cancel()
        onValueChange(target)
    }
    val scheduleCommit: (Float) -> Unit = { target ->
        debounceJob?.cancel()
        debounceJob = coroutineScope.launch {
            kotlinx.coroutines.delay(350L)
            onValueChange(target)
        }
    }
    val sliderIcon = when {
        isBrightness -> Icons.Default.LightMode
        clampedValue == 0f -> Icons.Default.VolumeMute
        clampedValue < 50f -> Icons.Default.VolumeDown
        else -> Icons.Default.VolumeUp
    }

    BoxWithConstraints(
        modifier = modifier
            .fillMaxSize()
            .pointerInput(Unit) {
                detectTapGestures(
                    onLongPress = { onLongClick?.invoke() },
                    onTap = { offset ->
                        val horizontal = size.width > size.height * 1.25f
                        val fraction = if (horizontal) {
                            offset.x / size.width.toFloat()
                        } else {
                            1f - (offset.y / size.height.toFloat())
                        }
                        val level = fraction.coerceIn(0f, 1f) * 100f
                        localValue = level
                        haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                        commitNow(level)
                    }
                )
            }
            .pointerInput(Unit) {
                detectDragGestures(
                    onDragStart = {
                        isDragging = true
                        debounceJob?.cancel()
                    },
                    onDragEnd = {
                        isDragging = false
                        commitNow(localValue)
                    },
                    onDragCancel = {
                        isDragging = false
                        commitNow(localValue)
                    },
                    onDrag = { change, _ ->
                        change.consume()
                        val horizontal = size.width > size.height * 1.25f
                        val fraction = if (horizontal) {
                            change.position.x / size.width.toFloat()
                        } else {
                            1f - (change.position.y / size.height.toFloat())
                        }
                        val level = fraction.coerceIn(0f, 1f) * 100f
                        localValue = level
                        scheduleCommit(level)
                    }
                )
            }
    ) {
        val horizontal = maxWidth > maxHeight * 1.25f
        val density = androidx.compose.ui.platform.LocalDensity.current

        if (horizontal) {
            val fillWidth = with(density) { (fillFraction * constraints.maxWidth.toFloat()).toDp() }
            Box(
                modifier = Modifier
                    .align(Alignment.CenterStart)
                    .width(fillWidth)
                    .fillMaxHeight()
                    .background(accent.copy(alpha = 0.22f))
            )
        } else {
            val fillHeight = with(density) { (fillFraction * constraints.maxHeight.toFloat()).toDp() }
            Box(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .height(fillHeight)
                    .background(accent.copy(alpha = 0.22f))
            )
        }

        if (horizontal) {
            Row(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 14.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(10.dp)
                ) {
                    Box(
                        modifier = Modifier
                            .size(36.dp)
                            .clip(CircleShape)
                            .background(MinimalistSurfaceElevated),
                        contentAlignment = Alignment.Center
                    ) {
                        Icon(
                            imageVector = sliderIcon,
                            contentDescription = label,
                            tint = accent,
                            modifier = Modifier.size(18.dp)
                        )
                    }
                    Text(
                        text = label,
                        fontSize = 13.5.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = MinimalistPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
                Text(
                    text = clampedValue.toInt().toString() + "%",
                    fontSize = 15.sp,
                    fontWeight = FontWeight.Light,
                    color = MinimalistPrimary
                )
            }
        } else {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(12.dp),
                horizontalAlignment = Alignment.Start,
                verticalArrangement = Arrangement.SpaceBetween
            ) {
                Box(
                    modifier = Modifier
                        .size(36.dp)
                        .clip(CircleShape)
                        .background(MinimalistSurfaceElevated),
                    contentAlignment = Alignment.Center
                ) {
                    Icon(
                        imageVector = sliderIcon,
                        contentDescription = label,
                        tint = accent,
                        modifier = Modifier.size(18.dp)
                    )
                }
                Text(
                    text = clampedValue.toInt().toString() + "%",
                    fontSize = 22.sp,
                    fontWeight = FontWeight.Light,
                    color = MinimalistPrimary
                )
                Text(
                    text = label,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
    }
}

/**
 * Canonicalises a stored `clock_style` to one of the six known faces; legacy values such as
 * `digital`, `digital_clean` or `apple_lockscreen` fall back to the Apple minimal face.
 */
private fun normalizeClockStyle(style: String): String = when (style) {
    "flip_clock", "neon_cyberpunk", "word_clock", "swiss_serif", "retro_terminal", "apple_minimal" -> style
    else -> "apple_minimal"
}

/**
 * Everything a clock face renderer needs, threaded as one record so each `clock_style` face is a
 * pure, previewable composable that never re-reads the wall clock or the palette itself.
 */
private data class ClockFaceSpec(
    val timeText: String,
    val dateText: String,
    val wordText: String,
    val secondary: String,
    val accent: Color,
    val compact: Boolean,
    val digitSize: TextUnit,
    val digitsFont: FontFamily,
    val maxWidth: Dp,
    val maxHeight: Dp,
    val seconds: Int
)

/**
 * Clock card with six pickable faces: the Pixel Hub / Apple minimal default plus the flip, neon
 * cyberpunk, Turkish word clock, Swiss serif and retro CRT terminal styles selected by
 * `clock_style`. Every face fits the card bounds and is redrawn once a second.
 */
@Composable
fun StreamDeckClockTile(
    label: String,
    accentColor: Color,
    colSpan: Int = 1,
    clockStyle: String = "digital",
    fontFamilyStr: String = "mono",
    clockFontSize: Int = 0,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("clock_widget", "", "")
    val accent = if (accentColor == Color.Unspecified) p.accent else accentColor
    val dateFormat = remember { java.text.SimpleDateFormat("EEE, d MMM", java.util.Locale("tr")) }
    val timeFormat = remember { java.text.SimpleDateFormat("HH:mm", java.util.Locale.getDefault()) }
    var now by remember { mutableStateOf(java.util.Date()) }
    LaunchedEffect(Unit) {
        while (true) {
            kotlinx.coroutines.delay(1000)
            now = java.util.Date()
        }
    }
    val calendar = remember(now) { Calendar.getInstance().apply { time = now } }
    val seconds = calendar.get(Calendar.SECOND)
    val dateText = dateFormat.format(now).replaceFirstChar { it.uppercase() }
    val timeText = timeFormat.format(now)
    val wordText = turkishClockWords(calendar.get(Calendar.HOUR_OF_DAY), calendar.get(Calendar.MINUTE))
    val secondary = if (label.isNotBlank()) label else "İstanbul · yerel saat"
    val digitsFont = when (fontFamilyStr) {
        "serif" -> FontFamily.Serif
        "sans", "sans_serif" -> FontFamily.SansSerif
        else -> FontFamily.Monospace
    }

    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val compact = maxHeight < 92.dp
        val digitSize = if (clockFontSize > 0) clockFontSize.sp else (maxHeight.value * 0.38f).coerceIn(20f, 54f).sp
        val spec = ClockFaceSpec(
            timeText = timeText,
            dateText = dateText,
            wordText = wordText,
            secondary = secondary,
            accent = accent,
            compact = compact,
            digitSize = digitSize,
            digitsFont = digitsFont,
            maxWidth = maxWidth,
            maxHeight = maxHeight,
            seconds = seconds
        )
        when (clockStyle) {
            "flip_clock" -> ClockFaceFlip(spec)
            "neon_cyberpunk" -> ClockFaceNeon(spec)
            "word_clock" -> ClockFaceWord(spec)
            "swiss_serif" -> ClockFaceSwissSerif(spec)
            "retro_terminal" -> ClockFaceRetro(spec)
            else -> ClockFaceAppleMinimal(spec)
        }
    }
}

/** `apple_minimal` (and every unknown style): accent date, thin tabular digits, locality line. */
@Composable
private fun ClockFaceAppleMinimal(spec: ClockFaceSpec) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            text = spec.dateText,
            fontSize = 12.sp,
            fontWeight = FontWeight.Medium,
            color = spec.accent,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
        Text(
            text = spec.timeText,
            fontSize = spec.digitSize,
            fontWeight = FontWeight.Light,
            color = MinimalistPrimary,
            fontFamily = spec.digitsFont,
            letterSpacing = (-0.5).sp,
            style = TextStyle(fontFeatureSettings = "tnum"),
            maxLines = 1
        )
        if (!spec.compact) {
            Text(
                text = spec.secondary,
                fontSize = 11.sp,
                fontWeight = FontWeight.Medium,
                color = MinimalistSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/** `flip_clock`: two split-flap cards with a hinge seam, plus the date line underneath. */
@Composable
private fun ClockFaceFlip(spec: ClockFaceSpec) {
    val parts = spec.timeText.split(":")
    val hours = parts.getOrElse(0) { "--" }
    val minutes = parts.getOrElse(1) { "--" }
    val cardFont = (spec.maxHeight.value * 0.30f).coerceIn(18f, 46f).sp
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 8.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp)
        ) {
            FlipClockCard(digits = hours, fontSize = cardFont)
            Text(
                text = ":",
                fontSize = cardFont,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Bold,
                color = spec.accent
            )
            FlipClockCard(digits = minutes, fontSize = cardFont)
        }
        if (!spec.compact) {
            Spacer(modifier = Modifier.height(6.dp))
            Text(
                text = spec.dateText,
                fontSize = 10.sp,
                fontWeight = FontWeight.Medium,
                color = MinimalistSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/** One flap: matte dark plate with a hairline hinge and a soft top-lit gradient. */
@Composable
private fun FlipClockCard(digits: String, fontSize: TextUnit) {
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(7.dp))
            .background(Brush.verticalGradient(listOf(Color(0xFF17171B), Color(0xFF0A0A0C))))
            .border(1.dp, MinimalistBorder, RoundedCornerShape(7.dp)),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = digits,
            fontSize = fontSize,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.Bold,
            color = MinimalistPrimary,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp)
        )
        // Split-flap hinge: a dark seam with a hairline highlight just under it.
        Canvas(modifier = Modifier.matchParentSize()) {
            val hingeY = size.height / 2f
            val hairline = 1.dp.toPx()
            drawLine(
                color = Color.Black.copy(alpha = 0.70f),
                start = Offset(0f, hingeY),
                end = Offset(size.width, hingeY),
                strokeWidth = hairline
            )
            drawLine(
                color = Color.White.copy(alpha = 0.06f),
                start = Offset(0f, hingeY + hairline),
                end = Offset(size.width, hingeY + hairline),
                strokeWidth = hairline
            )
        }
    }
}

/** `neon_cyberpunk`: cyan glowing monospace digits with an amber seconds / date readout. */
@Composable
private fun ClockFaceNeon(spec: ClockFaceSpec) {
    val cyan = Color(0xFF22E1FF)
    val amber = Color(0xFFFFB020)
    val neonSize = (spec.maxHeight.value * 0.40f).coerceIn(22f, 56f).sp
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.Start
    ) {
        Text(
            text = spec.timeText,
            fontSize = neonSize,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.Bold,
            color = cyan,
            letterSpacing = 1.sp,
            maxLines = 1,
            style = TextStyle(shadow = Shadow(color = cyan.copy(alpha = 0.9f), blurRadius = 22f))
        )
        if (!spec.compact) {
            Spacer(modifier = Modifier.height(2.dp))
            Text(
                text = "%02d".format(spec.seconds) + "  " + spec.dateText.uppercase(),
                fontSize = 10.sp,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Bold,
                color = amber,
                letterSpacing = 2.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                style = TextStyle(shadow = Shadow(color = amber.copy(alpha = 0.7f), blurRadius = 12f))
            )
        }
    }
}

/** `word_clock`: the time spelled out in Turkish, rounded to the nearest five minutes. */
@Composable
private fun ClockFaceWord(spec: ClockFaceSpec) {
    val wordSize = (spec.maxWidth.value / 10f).coerceIn(13f, 30f).sp
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 10.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Text(
            text = spec.wordText,
            fontSize = wordSize,
            fontWeight = FontWeight.SemiBold,
            color = MinimalistPrimary,
            fontFamily = FontFamily.SansSerif,
            letterSpacing = 1.sp,
            textAlign = TextAlign.Center,
            maxLines = 3,
            overflow = TextOverflow.Clip
        )
        if (!spec.compact) {
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                text = "· " + spec.dateText + " ·",
                fontSize = 9.5.sp,
                fontWeight = FontWeight.Medium,
                color = spec.accent,
                letterSpacing = 2.sp,
                maxLines = 1
            )
        }
    }
}

/** `swiss_serif`: light serif numerals inside a thin seconds ring. */
@Composable
private fun ClockFaceSwissSerif(spec: ClockFaceSpec) {
    val ringSize = (minOf(spec.maxWidth.value, spec.maxHeight.value) * 0.66f).coerceIn(56f, 150f).dp
    val serifSize = (spec.maxHeight.value * 0.30f).coerceIn(20f, 44f).sp
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(8.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Box(modifier = Modifier.size(ringSize), contentAlignment = Alignment.Center) {
            Canvas(modifier = Modifier.fillMaxSize()) {
                val strokeWidth = 1.5.dp.toPx()
                drawArc(
                    color = MinimalistBorder,
                    startAngle = -90f,
                    sweepAngle = 360f,
                    useCenter = false,
                    style = Stroke(width = strokeWidth)
                )
                drawArc(
                    color = spec.accent,
                    startAngle = -90f,
                    sweepAngle = (spec.seconds / 60f) * 360f,
                    useCenter = false,
                    style = Stroke(width = strokeWidth, cap = StrokeCap.Round)
                )
            }
            Text(
                text = spec.timeText,
                fontSize = serifSize,
                fontFamily = FontFamily.Serif,
                fontWeight = FontWeight.Light,
                color = MinimalistPrimary,
                letterSpacing = 0.5.sp,
                maxLines = 1
            )
        }
        if (!spec.compact) {
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                text = spec.dateText,
                fontSize = 10.sp,
                fontFamily = FontFamily.Serif,
                color = MinimalistSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/** `retro_terminal`: green CRT monochrome `[HH:MM:SS]` readout with a console prompt line. */
@Composable
private fun ClockFaceRetro(spec: ClockFaceSpec) {
    val green = Color(0xFF33FF66)
    val termSize = (spec.maxHeight.value * 0.30f).coerceIn(18f, 46f).sp
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 10.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.Start
    ) {
        Text(
            text = "[" + spec.timeText + ":" + "%02d".format(spec.seconds) + "]",
            fontSize = termSize,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.Bold,
            color = green,
            letterSpacing = 1.sp,
            maxLines = 1,
            style = TextStyle(shadow = Shadow(color = green.copy(alpha = 0.8f), blurRadius = 14f))
        )
        if (!spec.compact) {
            Spacer(modifier = Modifier.height(2.dp))
            Text(
                text = "> " + spec.dateText.lowercase(),
                fontSize = 9.5.sp,
                fontFamily = FontFamily.Monospace,
                color = green.copy(alpha = 0.6f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/** Turkish hour names indexed by the 12-hour clock (`0` and `12` both read `ON İKİ`). */
private val TurkishHourWords = arrayOf(
    "ON İKİ", "BİR", "İKİ", "ÜÇ", "DÖRT", "BEŞ",
    "ALTI", "YEDİ", "SEKİZ", "DOKUZ", "ON", "ON BİR"
)

/** Rounds to the nearest five minutes and spells the time out, e.g. `11:45` → `ON BİR KIRK BEŞ`. */
private fun turkishClockWords(hour24: Int, minute: Int): String {
    val hourWord = TurkishHourWords[((hour24 % 12) + 12) % 12]
    val minuteWord = when ((minute / 5) * 5) {
        0 -> "TAM"
        5 -> "BEŞ"
        10 -> "ON"
        15 -> "ON BEŞ"
        20 -> "YİRMİ"
        25 -> "YİRMİ BEŞ"
        30 -> "OTUZ"
        35 -> "OTUZ BEŞ"
        40 -> "KIRK"
        45 -> "KIRK BEŞ"
        50 -> "ELLİ"
        else -> "ELLİ BEŞ"
    }
    return "$hourWord $minuteWord"
}

/**
 * Rolling window length for the hardware widgets' history curves: one sample per `system_stats`
 * frame (~2s), so 24 points cover roughly the last minute of load.
 */
private const val DECK_HISTORY_POINTS = 24

/** Appends one sample to a rolling window, dropping the oldest point once the cap is reached. */
private fun List<Float>.withSparkSample(value: Float): List<Float> =
    if (size < DECK_HISTORY_POINTS) this + value else drop(size - DECK_HISTORY_POINTS + 1) + value

/** Stroke tint of the history curve (line) and of the area fading out under it. */
private val DeckSparkStroke = MinimalistPrimary.copy(alpha = 0.6f)
private val DeckSparkAreaFill = MinimalistPrimary.copy(alpha = 0.06f)
private val DeckSparkGhostStroke = MinimalistPrimary.copy(alpha = 0.28f)

/**
 * Thin monochrome history curve for the live telemetry widgets: a smooth cubic path through the
 * rolling sample window plus a very faint gradient area under it. Nothing is drawn until the
 * window holds at least two points, so a freshly opened deck just shows the flat readouts.
 */
@Composable
private fun DeckSparkline(
    values: List<Float>,
    modifier: Modifier = Modifier,
    domainMax: Float = 100f,
    ghostValues: List<Float> = emptyList()
) {
    Canvas(modifier = modifier) {
        if (ghostValues.size > 1) {
            drawDeckSparkCurve(ghostValues, domainMax, DeckSparkGhostStroke, Color.Transparent)
        }
        if (values.size > 1) {
            drawDeckSparkCurve(values, domainMax, DeckSparkStroke, DeckSparkAreaFill)
        }
    }
}

/**
 * Draws one sparkline series across the full canvas width. Every segment is a cubic whose two
 * control points keep the endpoints' heights, so the curve smooths out without ever overshooting
 * the sampled extremes. The optional fill closes the curve onto the canvas floor.
 */
private fun DrawScope.drawDeckSparkCurve(
    values: List<Float>,
    domainMax: Float,
    strokeColor: Color,
    fillColor: Color
) {
    val count = values.size
    if (count < 2 || size.width <= 0f || size.height <= 0f) return
    val top = domainMax.coerceAtLeast(1f)
    val padY = 1.5.dp.toPx()
    val usableH = (size.height - padY * 2).coerceAtLeast(1f)
    val stepX = size.width / (count - 1)

    fun yFor(value: Float) = size.height - padY - (value.coerceIn(0f, top) / top) * usableH

    val curve = Path()
    curve.moveTo(0f, yFor(values[0]))
    for (i in 1 until count) {
        val x0 = (i - 1) * stepX
        val x1 = i * stepX
        val midX = (x0 + x1) / 2f
        curve.cubicTo(midX, yFor(values[i - 1]), midX, yFor(values[i]), x1, yFor(values[i]))
    }

    if (fillColor.alpha > 0f) {
        val area = Path().apply {
            addPath(curve)
            lineTo(size.width, size.height)
            lineTo(0f, size.height)
            close()
        }
        drawPath(
            path = area,
            brush = Brush.verticalGradient(
                colors = listOf(fillColor, Color.Transparent),
                startY = 0f,
                endY = size.height
            )
        )
    }

    drawPath(
        path = curve,
        color = strokeColor,
        style = Stroke(width = 1.2.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round)
    )
}

/**
 * Flat Material You hardware sensor card: sensor name + 16.dp vector icon on top, one big
 * light value with a soft tonal progress cap below. `combo` swaps the cap for the 3-up
 * Workout-style thin rings (CPU / RAM / thermal).
 */
@Composable
fun StreamDeckTelemetryTile(
    label: String,
    metricType: String,
    liveCpu: Int,
    liveRam: Int,
    liveCpuTemp: Int,
    liveRamUsedGb: Double,
    liveRamTotalGb: Double,
    liveDiskPercent: Int = 0,
    liveDiskFreeGb: Double = 0.0,
    liveDiskTotalGb: Double = 0.0,
    liveGpu: Int = 0,
    liveGpuTemp: Int = 0,
    cpuHistory: List<Float> = emptyList(),
    ramHistory: List<Float> = emptyList(),
    colSpan: Int = 1,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("live_info", metricType, "")
    when (metricType) {
        "cpu_temp" -> StreamDeckCpuTempTile(cpuTemp = liveCpuTemp, colSpan = colSpan, palette = p, modifier = modifier)
        "disk" -> StreamDeckDiskTile(
            percent = liveDiskPercent,
            freeGb = liveDiskFreeGb,
            totalGb = liveDiskTotalGb,
            colSpan = colSpan,
            palette = p,
            modifier = modifier
        )
        "gpu" -> StreamDeckGpuTile(load = liveGpu, temp = liveGpuTemp, colSpan = colSpan, palette = p, modifier = modifier)
        "combo", "system", "activity_ring" -> DeckComboRings(
            cpu = liveCpu,
            ram = liveRam,
            cpuTemp = liveCpuTemp,
            cpuHistory = cpuHistory,
            ramHistory = ramHistory,
            palette = p,
            modifier = modifier
        )
        else -> {
            val isCpu = metricType.contains("cpu")
            if (colSpan >= 2) {
                DeckComboRings(
                    cpu = liveCpu,
                    ram = liveRam,
                    cpuTemp = liveCpuTemp,
                    cpuHistory = cpuHistory,
                    ramHistory = ramHistory,
                    palette = p,
                    modifier = modifier
                )
            } else if (isCpu) {
                DeckMetricCard(
                    title = "İşlemci",
                    icon = Icons.Default.DeveloperBoard,
                    value = "%" + liveCpu,
                    secondary = liveCpuTemp.toString() + "°C sıcaklık",
                    fraction = (liveCpu / 100f).coerceIn(0.02f, 1f),
                    history = cpuHistory,
                    palette = p,
                    modifier = modifier
                )
            } else {
                DeckMetricCard(
                    title = "RAM Bellek",
                    icon = Icons.Default.Memory,
                    value = "%" + liveRam,
                    secondary = String.format(java.util.Locale.US, "%.1f / %.1f GB", liveRamUsedGb, liveRamTotalGb),
                    fraction = (liveRam / 100f).coerceIn(0.02f, 1f),
                    history = ramHistory,
                    palette = p,
                    modifier = modifier
                )
            }
        }
    }
}

/** One sensor card: header row, big value + secondary line, history curve, tonal progress cap. */
@Composable
private fun DeckMetricCard(
    title: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    value: String,
    secondary: String,
    fraction: Float,
    palette: DeckPalette,
    history: List<Float> = emptyList(),
    modifier: Modifier = Modifier
) {
    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val valueSize = (maxHeight.value * 0.30f).coerceIn(18f, 40f).sp
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 12.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.SpaceBetween
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = title,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Icon(
                    imageVector = icon,
                    contentDescription = title,
                    tint = palette.accent,
                    modifier = Modifier.size(16.dp)
                )
            }
            Column {
                Text(
                    text = value,
                    fontSize = valueSize,
                    fontWeight = FontWeight.Light,
                    color = MinimalistPrimary,
                    style = TextStyle(fontFeatureSettings = "tnum"),
                    maxLines = 1
                )
                Text(
                    text = secondary,
                    fontSize = 10.5.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
            // History curve: takes whatever vertical slack the card has left (capped, so a 2x2
            // tile keeps its thin line instead of turning into a full height chart).
            DeckSparkline(
                values = history,
                modifier = Modifier
                    .weight(1f, fill = true)
                    .fillMaxWidth()
                    .heightIn(max = 20.dp)
            )
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(6.dp)
                    .clip(RoundedCornerShape(3.dp))
                    .background(MinimalistSurfaceElevated)
            ) {
                Box(
                    modifier = Modifier
                        .fillMaxHeight()
                        .fillMaxWidth(fraction.coerceIn(0f, 1f))
                        .clip(RoundedCornerShape(3.dp))
                        .background(palette.accent)
                )
            }
        }
    }
}

/** Three-up thin tonal rings (Workout style) for the `combo` telemetry card. */
@Composable
private fun DeckComboRings(
    cpu: Int,
    ram: Int,
    cpuTemp: Int,
    palette: DeckPalette,
    cpuHistory: List<Float> = emptyList(),
    ramHistory: List<Float> = emptyList(),
    modifier: Modifier = Modifier
) {
    val cpuColor = MinimalistPrimary
    val ramColor = MinimalistSecondary
    val tempColor = MinimalistSecondary.copy(alpha = 0.65f)
    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val ringSize = (minOf(maxWidth, maxHeight) * 0.62f).coerceIn(34.dp, 72.dp)
        Box(modifier = Modifier.fillMaxSize()) {
            // CPU/RAM history behind the rings, anchored to the card foot so the three readouts
            // stay on top and perfectly legible.
            DeckSparkline(
                values = cpuHistory,
                ghostValues = ramHistory,
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .fillMaxHeight(0.34f)
            )
            Row(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 12.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceEvenly
            ) {
                DeckMetricRing("İşlemci", "%" + cpu, (cpu / 100f).coerceIn(0f, 1f), cpuColor, ringSize)
                DeckMetricRing("Bellek", "%" + ram, (ram / 100f).coerceIn(0f, 1f), ramColor, ringSize)
                DeckMetricRing("Sıcaklık", cpuTemp.toString() + "°", (cpuTemp / 100f).coerceIn(0f, 1f), tempColor, ringSize)
            }
        }
    }
}

@Composable
private fun DeckMetricRing(
    label: String,
    valueText: String,
    fraction: Float,
    accent: Color,
    diameter: Dp
) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(modifier = Modifier.size(diameter), contentAlignment = Alignment.Center) {
            Canvas(modifier = Modifier.fillMaxSize()) {
                val strokePx = 4.dp.toPx()
                val diam = size.minDimension - strokePx
                val arcSize = androidx.compose.ui.geometry.Size(diam, diam)
                val topLeft = Offset((size.width - diam) / 2f, (size.height - diam) / 2f)
                drawArc(
                    color = MinimalistSurfaceElevated,
                    startAngle = 140f,
                    sweepAngle = 260f,
                    useCenter = false,
                    topLeft = topLeft,
                    size = arcSize,
                    style = Stroke(width = strokePx, cap = StrokeCap.Round)
                )
                if (fraction > 0f) {
                    drawArc(
                        color = accent,
                        startAngle = 140f,
                        sweepAngle = 260f * fraction.coerceIn(0f, 1f),
                        useCenter = false,
                        topLeft = topLeft,
                        size = arcSize,
                        style = Stroke(width = strokePx, cap = StrokeCap.Round)
                    )
                }
            }
            Text(
                text = valueText,
                fontSize = (diameter.value * 0.26f).coerceIn(9f, 15f).sp,
                fontWeight = FontWeight.Light,
                color = MinimalistPrimary,
                maxLines = 1
            )
        }
        Spacer(modifier = Modifier.height(6.dp))
        Text(
            text = label,
            fontSize = 9.5.sp,
            fontWeight = FontWeight.Medium,
            color = MinimalistSecondary,
            maxLines = 1
        )
    }
}

/**
 * Media transport card: 3 round tonal vector controls (previous / play-pause / next).
 */
@Composable
fun StreamDeckMediaTile(
    label: String,
    accentColor: Color,
    onAction: (String) -> Unit,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("media_widget", "", "")
    val accent = if (accentColor == Color.Unspecified) p.accent else accentColor
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            text = label.ifBlank { "Medya Masası" },
            fontSize = 11.sp,
            fontWeight = FontWeight.Medium,
            color = MinimalistSecondary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            DeckTransportButton(
                icon = Icons.Default.SkipPrevious,
                contentDescription = "Önceki Parça",
                palette = p
            ) { onAction("prevtrack") }
            DeckTransportButton(
                icon = Icons.Default.PlayArrow,
                contentDescription = "Oynat / Durdur",
                palette = p.copy(accent = accent),
                emphasised = true
            ) { onAction("playpause") }
            DeckTransportButton(
                icon = Icons.Default.SkipNext,
                contentDescription = "Sonraki Parça",
                palette = p
            ) { onAction("nexttrack") }
        }
    }
}

/**
 * Flat Material You card shell shared by every bento widget: tonal surface, hairline border,
 * 24.dp radius and no bevels, fake LEDs or gradient glows. Dispatches to the widget template that
 * matches the button type.
 */
@Composable
fun ElgatoKeycapTile(
    button: StreamDeckButtonInfo,
    live: StreamDeckLiveStats,
    masterVolume: Float,
    onVolumeChange: (Float) -> Unit,
    brightness: Int,
    onBrightnessChange: (Int) -> Unit,
    ipAddress: String,
    password: String,
    onPress: () -> Unit,
    onLongPress: () -> Unit,
    isEditMode: Boolean = false,
    isSelectedForSwap: Boolean = false,
    modifier: Modifier = Modifier
) {
    val palette = remember(button.type, button.value, button.color) {
        deckPaletteFor(button.type, button.value, button.color)
    }
    val shape = RoundedCornerShape(DeckCorner)
    val haptic = LocalHapticFeedback.current
    val isToggle = button.type == "toggle"
    val surface = when {
        button.bgStyle == "transparent" -> Color.Transparent
        isToggle && button.state -> palette.surfaceActive
        else -> palette.surface
    }
    val isVolume = button.type == "volume_slider"
    val isBrightness = button.type == "brightness_slider"
    val isNetSpeed = button.type == "net_speed" || (button.type == "live_info" && button.value == "net_speed")
    val isLive = button.type == "live_info" && !isNetSpeed
    val isMedia = button.type == "media_widget" || button.type == "media_control"

    Box(
        modifier = modifier
            .fillMaxSize()
            .clip(shape)
            .background(surface)
            .border(1.dp, if (isSelectedForSwap) palette.accent else palette.border, shape)
            .then(
                if (isEditMode) {
                    Modifier
                } else {
                    Modifier.pointerInput(button.id) {
                        detectTapGestures(
                            onTap = { onPress() },
                            onLongPress = {
                                haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                                onLongPress()
                            }
                        )
                    }
                }
            )
    ) {
        when {
            isVolume -> AppleCapsuleSlider(
                type = "volume",
                value = masterVolume,
                label = button.label.ifBlank { "Ana Ses" },
                accentColor = palette.accent,
                palette = palette,
                onValueChange = onVolumeChange,
                onLongClick = onLongPress
            )
            isBrightness -> AppleCapsuleSlider(
                type = "brightness",
                value = brightness.toFloat(),
                label = button.label.ifBlank { "Parlaklık" },
                accentColor = palette.accent,
                palette = palette,
                onValueChange = { onBrightnessChange(it.toInt()) },
                onLongClick = onLongPress
            )
            button.type == "weather" -> StreamDeckWeatherTile(
                label = button.label,
                city = button.value.ifBlank { live.weatherCity },
                tempC = live.weatherTemp,
                weatherCode = live.weatherCode,
                description = live.weatherDesc,
                windKph = live.weatherWind,
                palette = palette
            )
            button.type == "now_playing" -> StreamDeckNowPlayingTile(
                label = button.label,
                title = live.mediaTitle,
                artist = live.mediaArtist,
                isPlaying = live.mediaPlaying,
                palette = palette,
                onAction = { act ->
                    SocketManager.sendControl(JSONObject().apply {
                        put("type", "stream_deck_press")
                        put("button_id", button.id)
                        put("media_action", act)
                    })
                }
            )
            button.type == "clock_widget" -> StreamDeckClockTile(
                label = button.label,
                accentColor = palette.accent,
                colSpan = button.colSpan,
                clockStyle = button.clockStyle,
                fontFamilyStr = button.fontFamily,
                clockFontSize = button.clockFontSize,
                palette = palette
            )
            isLive -> StreamDeckTelemetryTile(
                label = button.label,
                metricType = button.value,
                liveCpu = live.cpu,
                liveRam = live.ram,
                liveCpuTemp = live.cpuTemp,
                liveRamUsedGb = live.ramUsedGb,
                liveRamTotalGb = live.ramTotalGb,
                liveDiskPercent = live.diskPercent,
                liveDiskFreeGb = live.diskFreeGb,
                liveDiskTotalGb = live.diskTotalGb,
                liveGpu = live.gpu,
                liveGpuTemp = live.gpuTemp,
                cpuHistory = live.cpuHistory,
                ramHistory = live.ramHistory,
                colSpan = button.colSpan,
                palette = palette
            )
            isMedia -> StreamDeckMediaTile(
                label = button.label,
                accentColor = palette.accent,
                palette = palette,
                onAction = { act ->
                    SocketManager.sendControl(JSONObject().apply {
                        put("type", "stream_deck_press")
                        put("button_id", button.id)
                        put("media_action", act)
                    })
                }
            )
            isNetSpeed -> StreamDeckNetSpeedTile(
                label = button.label,
                accentColor = palette.accent,
                downKbps = live.netDownKbps,
                upKbps = live.netUpKbps,
                downHistory = live.netDownHistory,
                upHistory = live.netUpHistory,
                palette = palette
            )
            button.type == "pomodoro" -> StreamDeckPomodoroTile(
                label = button.label,
                accentColor = palette.accent,
                palette = palette
            )
            else -> DeckActionCard(
                button = button,
                palette = palette,
                ipAddress = ipAddress,
                password = password
            )
        }
    }
}

/**
 * Template 1 — action / shortcut / toggle card: round tonal badge on the top left, left aligned
 * title + status caption on the bottom left. No bevels, no LED jewel, no icon stack.
 */
@Composable
private fun DeckActionCard(
    button: StreamDeckButtonInfo,
    palette: DeckPalette,
    ipAddress: String,
    password: String
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .padding(14.dp)
    ) {
        Box(
            modifier = Modifier
                .align(Alignment.TopStart)
                .size(36.dp)
                .clip(CircleShape)
                .background(MinimalistSurfaceElevated),
            contentAlignment = Alignment.Center
        ) {
            if (button.icon.startsWith("custom:") && button.icon.length > 7) {
                val iconPath = button.icon.substring(7)
                val imageUrl = "http://" + ipAddress + ":8085/" + iconPath + "?password=" + Uri.encode(password)
                AsyncImage(
                    model = imageUrl,
                    contentDescription = button.label,
                    modifier = Modifier
                        .size(18.dp)
                        .clip(CircleShape)
                )
            } else {
                Icon(
                    imageVector = getVectorIcon(button.icon),
                    contentDescription = button.label,
                    tint = palette.accent,
                    modifier = Modifier.size(18.dp)
                )
            }
        }
        Column(modifier = Modifier.align(Alignment.BottomStart)) {
            Text(
                text = button.label.ifBlank { "Buton" },
                fontSize = 13.5.sp,
                fontWeight = FontWeight.SemiBold,
                color = MinimalistPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
            Text(
                text = deckStatusLabel(button),
                fontSize = 11.sp,
                fontWeight = FontWeight.Medium,
                color = if (button.type == "toggle" && button.state) palette.accent else MinimalistSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/**
 * Live network throughput tile (download / upload) fed by the PC's `system_stats` broadcast.
 */
@Composable
fun StreamDeckNetSpeedTile(
    label: String,
    accentColor: Color,
    downKbps: Int,
    upKbps: Int,
    downHistory: List<Float> = emptyList(),
    upHistory: List<Float> = emptyList(),
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("live_info", "net_speed", "")
    val accent = if (accentColor == Color.Unspecified) p.accent else accentColor
    // Throughput has no natural 100% ceiling: the curve scales to the window peak instead.
    val peak = (downHistory + upHistory).maxOrNull() ?: 0f
    val throughputDomain = if (peak > 0f) peak * 1.1f else 1f
    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val valueSize = (maxHeight.value * 0.25f).coerceIn(14f, 28f).sp
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 12.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.SpaceBetween
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = label.ifBlank { "Ağ Hızı" },
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Icon(
                    imageVector = Icons.Default.Speed,
                    contentDescription = "Ağ Hızı",
                    tint = accent,
                    modifier = Modifier.size(16.dp)
                )
            }
            Column {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp)
                ) {
                    Icon(
                        imageVector = Icons.Default.Download,
                        contentDescription = "İndirme",
                        tint = accent,
                        modifier = Modifier.size(14.dp)
                    )
                    Text(
                        text = formatNetworkSpeed(downKbps),
                        fontSize = valueSize,
                        fontWeight = FontWeight.Light,
                        color = MinimalistPrimary,
                        maxLines = 1
                    )
                }
                Spacer(modifier = Modifier.height(2.dp))
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp)
                ) {
                    Icon(
                        imageVector = Icons.Default.Upload,
                        contentDescription = "Yükleme",
                        tint = MinimalistSecondary,
                        modifier = Modifier.size(14.dp)
                    )
                    Text(
                        text = formatNetworkSpeed(upKbps),
                        fontSize = 11.sp,
                        fontWeight = FontWeight.Medium,
                        color = MinimalistSecondary,
                        maxLines = 1
                    )
                }
            }
            // Download history with the upload curve as its fainter companion, pinned to the
            // bottom of the tile and capped so it never crowds the two readouts.
            DeckSparkline(
                values = downHistory,
                ghostValues = upHistory,
                domainMax = throughputDomain,
                modifier = Modifier
                    .weight(1f, fill = true)
                    .fillMaxWidth()
                    .heightIn(max = 20.dp)
            )
        }
    }
}

private fun formatNetworkSpeed(kbps: Int): String = when {
    kbps >= 1_000 -> String.format(java.util.Locale.US, "%.1f Mb/s", kbps / 1000.0)
    else -> "$kbps Kb/s"
}

private const val POMODORO_SECONDS = 25 * 60

/**
 * 25 minute Pomodoro tile: tapping starts or pauses the countdown, tapping after it finished
 * restarts a fresh session.
 */
@Composable
fun StreamDeckPomodoroTile(
    label: String,
    accentColor: Color,
    palette: DeckPalette? = null
) {
    val p = palette ?: deckPaletteFor("pomodoro", "", "")
    val accent = if (accentColor == Color.Unspecified) p.accent else accentColor
    var remainingSeconds by rememberSaveable { mutableStateOf(POMODORO_SECONDS) }
    var isRunning by rememberSaveable { mutableStateOf(false) }
    val haptic = LocalHapticFeedback.current

    LaunchedEffect(isRunning) {
        while (isRunning && remainingSeconds > 0) {
            kotlinx.coroutines.delay(1000)
            remainingSeconds -= 1
        }
        if (remainingSeconds <= 0) {
            isRunning = false
        }
    }

    val minutes = remainingSeconds / 60
    val seconds = remainingSeconds % 60
    val timeText = String.format(java.util.Locale.US, "%02d:%02d", minutes, seconds)
    val finished = remainingSeconds <= 0
    val stateText = when {
        finished -> "Süre doldu"
        isRunning -> "Odak"
        else -> "Duraklatıldı"
    }

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val timeSize = (maxHeight.value * 0.30f).coerceIn(18f, 38f).sp
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 12.dp, vertical = 12.dp)
                .pointerInput(Unit) {
                    detectTapGestures(
                        onTap = {
                            haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                            if (remainingSeconds <= 0) {
                                remainingSeconds = POMODORO_SECONDS
                                isRunning = true
                            } else {
                                isRunning = !isRunning
                            }
                        }
                    )
                },
            verticalArrangement = Arrangement.SpaceBetween
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = label.ifBlank { "Pomodoro" },
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Icon(
                    imageVector = if (isRunning) Icons.Default.Timer else Icons.Default.TimerOff,
                    contentDescription = "Pomodoro",
                    tint = if (finished) StatusRedText else accent,
                    modifier = Modifier.size(16.dp)
                )
            }
            Column(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = timeText,
                    fontSize = timeSize,
                    fontWeight = FontWeight.Light,
                    color = MinimalistPrimary,
                    style = TextStyle(fontFeatureSettings = "tnum"),
                    maxLines = 1
                )
                Text(
                    text = stateText,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = if (finished || isRunning) accent else MinimalistSecondary,
                    maxLines = 1
                )
            }
        }
    }
}

/** Flat 28.dp console action with a vector icon. */
@Composable
private fun DeckBarIcon(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    tint: Color,
    onClick: () -> Unit
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = Modifier
            .size(28.dp)
            .clip(RoundedCornerShape(12.dp))
            .clickable(onClickLabel = contentDescription) {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            },
        contentAlignment = Alignment.Center
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = tint,
            modifier = Modifier.size(16.dp)
        )
    }
}

/** Page pill used by the edit-mode floating bar: the active page inverts, the others stay muted. */
@Composable
private fun DeckPagePill(
    title: String,
    selected: Boolean,
    onClick: () -> Unit
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(999.dp))
            .background(if (selected) MinimalistPrimary else MinimalistSurfaceElevated)
            .border(1.dp, if (selected) MinimalistPrimary else MinimalistBorder, RoundedCornerShape(999.dp))
            .clickable {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            }
            .padding(horizontal = 11.dp, vertical = 5.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = title,
            fontSize = 11.sp,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
            color = if (selected) MinimalistBackground else MinimalistSecondary,
            maxLines = 1
        )
    }
}

/** Horizontally scrollable chip row used by the button editor for live widget values. */
@Composable
private fun EditorChoiceRow(
    title: String,
    options: List<Pair<String, String>>,
    selected: String,
    onSelect: (String) -> Unit
) {
    val haptic = LocalHapticFeedback.current
    Column(modifier = Modifier.fillMaxWidth()) {
        Text(
            text = title,
            fontSize = 12.sp,
            fontWeight = FontWeight.Bold,
            color = MinimalistSecondary
        )
        Spacer(modifier = Modifier.height(4.dp))
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(6.dp)
        ) {
            for ((key, label) in options) {
                val isSelected = key == selected
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(12.dp))
                        .background(if (isSelected) MinimalistPrimary else MinimalistSurfaceElevated)
                        .border(
                            1.dp,
                            if (isSelected) MinimalistPrimary else MinimalistBorder,
                            RoundedCornerShape(12.dp)
                        )
                        .clickable {
                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                            onSelect(key)
                        }
                        .padding(horizontal = 12.dp, vertical = 7.dp)
                ) {
                    Text(
                        text = label,
                        fontSize = 11.5.sp,
                        fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                        color = if (isSelected) MinimalistBackground else MinimalistSecondary
                    )
                }
            }
        }
    }
}

/**
 * Template 3 — weather card: city top left, pure vector condition glyph top right, light huge
 * temperature with the condition + wind detail underneath.
 */
@Composable
fun StreamDeckWeatherTile(
    label: String,
    city: String,
    tempC: Int,
    weatherCode: Int,
    description: String,
    windKph: Int,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("weather", "", "")
    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val compact = maxHeight < 88.dp
        val desc = description.ifBlank { label.ifBlank { "Hava Durumu" } }
        val detail = if (windKph > 0) desc + " · " + windKph + " km/s" else desc
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 14.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.SpaceBetween
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = city.ifBlank { "İstanbul" },
                    fontSize = 12.5.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Icon(
                    imageVector = weatherIcon(weatherCode),
                    contentDescription = desc,
                    tint = p.accent,
                    modifier = Modifier.size(22.dp)
                )
            }
            Column {
                Text(
                    text = tempC.toString() + "°",
                    fontSize = 32.sp,
                    fontWeight = FontWeight.Light,
                    color = MinimalistPrimary,
                    style = TextStyle(fontFeatureSettings = "tnum"),
                    maxLines = 1
                )
                if (!compact) {
                    Text(
                        text = detail,
                        fontSize = 11.sp,
                        fontWeight = FontWeight.Medium,
                        color = MinimalistSecondary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
        }
    }
}

/**
 * Template 5 — now playing card: tonal music badge + label, bold track title with the artist
 * underneath and 3 round vector transport buttons.
 */
@Composable
fun StreamDeckNowPlayingTile(
    label: String,
    title: String,
    artist: String,
    isPlaying: Boolean,
    onAction: (String) -> Unit,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("now_playing", "", "")
    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val compact = maxHeight < 96.dp
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 14.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.SpaceBetween
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(30.dp)
                        .clip(CircleShape)
                        .background(MinimalistSurfaceElevated),
                    contentAlignment = Alignment.Center
                ) {
                    Icon(
                        imageVector = Icons.Default.MusicNote,
                        contentDescription = null,
                        tint = p.accent,
                        modifier = Modifier.size(15.dp)
                    )
                }
                Text(
                    text = label.ifBlank { "Şimdi Çalıyor" },
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    color = MinimalistSecondary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
            Column(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = title.ifBlank { "Medya yok" },
                    fontSize = 13.5.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MinimalistPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
                if (!compact) {
                    Text(
                        text = artist.ifBlank { if (isPlaying) "PC'de çalıyor" else "Duraklatıldı" },
                        fontSize = 11.sp,
                        fontWeight = FontWeight.Medium,
                        color = MinimalistSecondary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                DeckTransportButton(
                    icon = Icons.Default.SkipPrevious,
                    contentDescription = "Önceki Parça",
                    palette = p
                ) { onAction("prevtrack") }
                DeckTransportButton(
                    icon = if (isPlaying) Icons.Default.Pause else Icons.Default.PlayArrow,
                    contentDescription = "Oynat / Durdur",
                    palette = p,
                    emphasised = true
                ) { onAction("playpause") }
                DeckTransportButton(
                    icon = Icons.Default.SkipNext,
                    contentDescription = "Sonraki Parça",
                    palette = p
                ) { onAction("nexttrack") }
            }
        }
    }
}

/** Round tonal transport control used by the now-playing card. */
@Composable
private fun DeckTransportButton(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    palette: DeckPalette,
    emphasised: Boolean = false,
    onClick: () -> Unit
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = Modifier
            .size(32.dp)
            .clip(CircleShape)
            .background(if (emphasised) MinimalistPrimary else MinimalistSurfaceElevated)
            .clickable {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            },
        contentAlignment = Alignment.Center
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = if (emphasised) MinimalistBackground else MinimalistPrimary,
            modifier = Modifier.size(16.dp)
        )
    }
}

/** Thermal sensor card: big °C value with a soft tonal progress cap. */
@Composable
fun StreamDeckCpuTempTile(
    cpuTemp: Int,
    colSpan: Int = 1,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("live_info", "cpu_temp", "")
    val state = when {
        cpuTemp >= 85 -> "Kritik"
        cpuTemp >= 70 -> "Sıcak"
        cpuTemp >= 55 -> "Ilık"
        else -> "Serin"
    }
    DeckMetricCard(
        title = "İşlemci Sıcaklığı",
        icon = Icons.Default.Thermostat,
        value = cpuTemp.toString() + "°C",
        secondary = state,
        fraction = (cpuTemp / 100f).coerceIn(0.02f, 1f),
        palette = p,
        modifier = modifier
    )
}

/** Disk occupancy card: free GB with the total capacity and a tonal fill cap. */
@Composable
fun StreamDeckDiskTile(
    percent: Int,
    freeGb: Double,
    totalGb: Double,
    colSpan: Int = 1,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("live_info", "disk", "")
    DeckMetricCard(
        title = "Disk C:",
        icon = Icons.Default.Storage,
        value = freeGb.toInt().toString() + " GB",
        secondary = if (totalGb > 0.0) totalGb.toInt().toString() + " GB toplam" else "Boş alan",
        fraction = (percent / 100f).coerceIn(0.02f, 1f),
        palette = p,
        modifier = modifier
    )
}

/** GPU card: load percentage with the GPU temperature and a tonal fill cap. */
@Composable
fun StreamDeckGpuTile(
    load: Int,
    temp: Int,
    colSpan: Int = 1,
    palette: DeckPalette? = null,
    modifier: Modifier = Modifier
) {
    val p = palette ?: deckPaletteFor("live_info", "gpu", "")
    DeckMetricCard(
        title = "Ekran Kartı",
        icon = Icons.Default.VideogameAsset,
        value = "%" + load,
        secondary = temp.toString() + "°C",
        fraction = (load / 100f).coerceIn(0.02f, 1f),
        palette = p,
        modifier = modifier
    )
}

/** Final 2D position of a button inside the bento matrix. */
private data class BentoPlacement(
    val button: StreamDeckButtonInfo,
    val row: Int,
    val col: Int,
    val rowSpan: Int,
    val colSpan: Int
)

/** Result of the 2-phase packer: final card positions + the leftover 1x1 cells. */
private data class DeckLayout(
    val placements: List<BentoPlacement>,
    val emptyCells: List<Pair<Int, Int>>
)

/**
 * 2-phase bento packer, identical to the PC `packBentoGrid`:
 * 1. cards with an explicit `gridRow`/`gridCol` claim exactly that cell (their span shrinks to fit
 *    the free neighbourhood first, and only falls back to auto placement if that cell is taken);
 * 2. every remaining card lands on the first free cell with its spans clamped to the matrix.
 * Every leftover cell becomes an [EmptySlotTile], so the matrix never scrolls and never leaks.
 */
private fun packBentoLayout(
    buttons: List<StreamDeckButtonInfo>,
    rowCount: Int,
    colCount: Int
): DeckLayout {
    val occupied = Array(rowCount) { BooleanArray(colCount) }
    val placed = mutableListOf<BentoPlacement>()

    fun fits(r: Int, c: Int, spanRows: Int, spanCols: Int): Boolean {
        if (r < 0 || c < 0 || r + spanRows > rowCount || c + spanCols > colCount) return false
        for (dr in 0 until spanRows) {
            for (dc in 0 until spanCols) {
                if (occupied[r + dr][c + dc]) return false
            }
        }
        return true
    }

    fun mark(r: Int, c: Int, spanRows: Int, spanCols: Int) {
        for (dr in 0 until spanRows) {
            for (dc in 0 until spanCols) {
                occupied[r + dr][c + dc] = true
            }
        }
    }

    fun spanAttempts(button: StreamDeckButtonInfo): List<Pair<Int, Int>> {
        val wantedCols = button.colSpan.coerceIn(1, colCount)
        val wantedRows = button.rowSpan.coerceIn(1, rowCount)
        return listOf(wantedRows to wantedCols, wantedRows to 1, 1 to wantedCols, 1 to 1)
    }

    // Phase 1 — explicitly placed cards keep their cell.
    for (button in buttons) {
        if (button.gridRow < 0 || button.gridCol < 0) continue
        for (attempt in spanAttempts(button)) {
            val spanRows = attempt.first
            val spanCols = attempt.second
            if (fits(button.gridRow, button.gridCol, spanRows, spanCols)) {
                mark(button.gridRow, button.gridCol, spanRows, spanCols)
                placed.add(BentoPlacement(button, button.gridRow, button.gridCol, spanRows, spanCols))
                break
            }
        }
    }

    // Phase 2 — everything else fills the first free cell.
    val pinnedIds = placed.map { it.button.id }.toSet()
    for (button in buttons) {
        if (button.id in pinnedIds) continue
        var chosen: BentoPlacement? = null
        for (attempt in spanAttempts(button)) {
            if (chosen != null) break
            val spanRows = attempt.first
            val spanCols = attempt.second
            scan@ for (r in 0..(rowCount - spanRows)) {
                for (c in 0..(colCount - spanCols)) {
                    if (fits(r, c, spanRows, spanCols)) {
                        mark(r, c, spanRows, spanCols)
                        chosen = BentoPlacement(button, r, c, spanRows, spanCols)
                        break@scan
                    }
                }
            }
        }
        chosen?.let { placed.add(it) }
    }

    val emptyCells = mutableListOf<Pair<Int, Int>>()
    for (r in 0 until rowCount) {
        for (c in 0 until colCount) {
            if (!occupied[r][c]) emptyCells.add(r to c)
        }
    }
    return DeckLayout(placed, emptyCells)
}

/**
 * Real 2D Bento grid: zero scrolling, zero holes, and every leftover cell rendered as an
 * [EmptySlotTile]. In edit mode each card gets its size chip plus delete and customise
 * affordances, and tapping two cards swaps them while tapping a free cell moves one.
 */
@Composable
fun BentoDeckGrid(
    buttons: List<StreamDeckButtonInfo>,
    rows: Int,
    cols: Int,
    gap: Dp,
    live: StreamDeckLiveStats,
    masterVolume: Float,
    onVolumeChange: (Float) -> Unit,
    brightness: Int,
    onBrightnessChange: (Int) -> Unit,
    ipAddress: String,
    password: String,
    onPress: (StreamDeckButtonInfo) -> Unit,
    onLongPress: (StreamDeckButtonInfo) -> Unit,
    onEmptySlotClick: (Int) -> Unit,
    onEmptySlotLongPress: () -> Unit = {},
    isEditMode: Boolean = false,
    selectedForSwapId: Int? = null,
    onSelectForSwap: (StreamDeckButtonInfo) -> Unit = {},
    onResizeButton: (StreamDeckButtonInfo) -> Unit = {},
    onDeleteButton: (StreamDeckButtonInfo) -> Unit = {},
    onCustomizeButton: (StreamDeckButtonInfo) -> Unit = {},
    onMoveToCell: (Int, Int) -> Unit = { _, _ -> },
    modifier: Modifier = Modifier
) {
    val rowCount = rows.coerceAtLeast(1)
    val colCount = cols.coerceAtLeast(1)

    val layout = remember(buttons, rowCount, colCount) {
        packBentoLayout(buttons, rowCount, colCount)
    }
    val placements = layout.placements
    val emptyCells = layout.emptyCells

    BoxWithConstraints(modifier = modifier) {
        val boundedWidth = if (maxWidth.value.isFinite()) maxWidth else gap * (colCount + 1) + 64.dp * colCount
        val boundedHeight = if (maxHeight.value.isFinite()) maxHeight else gap * (rowCount + 1) + 64.dp * rowCount
        val cellWidth = ((boundedWidth - gap * (colCount - 1)) / colCount).coerceAtLeast(1.dp)
        val cellHeight = ((boundedHeight - gap * (rowCount - 1)) / rowCount).coerceAtLeast(1.dp)

        Box(modifier = Modifier.fillMaxSize()) {
            emptyCells.forEachIndexed { index, cell ->
                val r = cell.first
                val c = cell.second
                EmptySlotTile(
                    isEditMode = isEditMode,
                    isDropTarget = isEditMode && selectedForSwapId != null,
                    onClick = {
                        if (isEditMode) {
                            onMoveToCell(r, c)
                        } else {
                            onEmptySlotClick(placements.size + index)
                        }
                    },
                    onLongPress = onEmptySlotLongPress,
                    modifier = Modifier
                        .offset(x = (cellWidth + gap) * c, y = (cellHeight + gap) * r)
                        .size(cellWidth, cellHeight)
                )
            }

            placements.forEach { placement ->
                val tileWidth = cellWidth * placement.colSpan + gap * (placement.colSpan - 1)
                val tileHeight = cellHeight * placement.rowSpan + gap * (placement.rowSpan - 1)
                val isSelected = selectedForSwapId == placement.button.id
                Box(
                    modifier = Modifier
                        .offset(x = (cellWidth + gap) * placement.col, y = (cellHeight + gap) * placement.row)
                        .size(tileWidth, tileHeight)
                ) {
                    ElgatoKeycapTile(
                        button = placement.button,
                        live = live,
                        masterVolume = masterVolume,
                        onVolumeChange = onVolumeChange,
                        brightness = brightness,
                        onBrightnessChange = onBrightnessChange,
                        ipAddress = ipAddress,
                        password = password,
                        onPress = { onPress(placement.button) },
                        onLongPress = { onLongPress(placement.button) },
                        isEditMode = isEditMode,
                        isSelectedForSwap = isSelected,
                        modifier = Modifier.fillMaxSize()
                    )

                    if (isEditMode) {
                        // Whole-card tap target: selects the card, then swaps or moves it.
                        Box(
                            modifier = Modifier
                                .matchParentSize()
                                .clip(RoundedCornerShape(DeckCorner))
                                .clickable { onSelectForSwap(placement.button) }
                        )
                        // `1×1` (size), `✎` (edit) and `×` (delete) on three corners so the three
                        // controls always stay tappable even on the narrowest 1×1 cell.
                        DeckEditChip(
                            label = placement.colSpan.toString() + "×" + placement.rowSpan.toString(),
                            onClick = { onResizeButton(placement.button) },
                            modifier = Modifier
                                .align(Alignment.TopStart)
                                .padding(5.dp)
                        )
                        DeckEditIconChip(
                            icon = Icons.Default.Close,
                            contentDescription = "Bileşeni Sil",
                            onClick = { onDeleteButton(placement.button) },
                            modifier = Modifier
                                .align(Alignment.TopEnd)
                                .padding(5.dp)
                        )
                        DeckEditIconChip(
                            icon = Icons.Default.Edit,
                            contentDescription = "Bileşeni Düzenle",
                            onClick = { onCustomizeButton(placement.button) },
                            modifier = Modifier
                                .align(Alignment.BottomEnd)
                                .padding(5.dp)
                        )
                    }
                }
            }
        }
    }
}

/** Size chip shown on every card while editing; tapping it cycles 1×1 → 2×1 → 1×2 → 2×2. */
@Composable
private fun DeckEditChip(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(12.dp))
            .background(MinimalistSurfaceElevated)
            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
            .clickable(onClickLabel = "Boyut") {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            }
            .padding(horizontal = 8.dp, vertical = 4.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = label,
            fontSize = 10.5.sp,
            fontWeight = FontWeight.SemiBold,
            color = MinimalistPrimary,
            maxLines = 1
        )
    }
}

/** Round edit affordance (edit / delete) drawn on top of a card. */
@Composable
private fun DeckEditIconChip(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = modifier
            .size(26.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(MinimalistSurfaceElevated)
            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
            .clickable(onClickLabel = contentDescription) {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            },
        contentAlignment = Alignment.Center
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = MinimalistPrimary,
            modifier = Modifier.size(14.dp)
        )
    }
}

/** Flat placeholder for a free matrix cell; in edit mode it becomes a move target. */
@Composable
fun EmptySlotTile(
    modifier: Modifier = Modifier,
    isEditMode: Boolean = false,
    isDropTarget: Boolean = false,
    onClick: () -> Unit,
    onLongPress: () -> Unit = {}
) {
    val haptic = LocalHapticFeedback.current
    val currentOnClick by rememberUpdatedState(onClick)
    val currentOnLongPress by rememberUpdatedState(onLongPress)
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(DeckCorner))
            .background(if (isDropTarget) MinimalistPrimary.copy(alpha = 0.08f) else MinimalistSurface.copy(alpha = 0.55f))
            .border(
                width = 1.dp,
                color = if (isDropTarget) MinimalistPrimary else MinimalistBorder,
                shape = RoundedCornerShape(DeckCorner)
            )
            .pointerInput(Unit) {
                detectTapGestures(
                    onTap = {
                        haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                        currentOnClick()
                    },
                    onLongPress = { currentOnLongPress() }
                )
            },
        contentAlignment = Alignment.Center
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(5.dp)
        ) {
            Icon(
                imageVector = Icons.Default.Add,
                contentDescription = null,
                tint = if (isDropTarget) MinimalistPrimary else MinimalistSecondary,
                modifier = Modifier.size(16.dp)
            )
            Text(
                text = if (isEditMode) "Buraya Taşı" else "Bileşen Ekle",
                fontSize = 10.5.sp,
                fontWeight = FontWeight.Medium,
                color = if (isDropTarget) MinimalistPrimary else MinimalistSecondary,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/**
 * StreamDeckGridSettingsDialog - Apple tactile Matrix configuration dialog.
 */
@Composable
fun StreamDeckGridSettingsDialog(
    currentRows: Int,
    currentCols: Int,
    onDismiss: () -> Unit,
    onApply: (rows: Int, cols: Int) -> Unit
) {
    var tempRows by remember { mutableIntStateOf(currentRows.coerceIn(1, 6)) }
    var tempCols by remember { mutableIntStateOf(currentCols.coerceIn(1, 6)) }
    val haptic = LocalHapticFeedback.current

    val presets = listOf(
        Triple("2×2", 2, 2),
        Triple("3×3", 3, 3),
        Triple("4×2", 2, 4),
        Triple("4×3", 3, 4),
        Triple("5×3", 3, 5),
        Triple("2×4", 4, 2)
    )

    androidx.compose.ui.window.Dialog(onDismissRequest = onDismiss) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(DeckCorner))
                .background(MinimalistSurface)
                .border(1.dp, MinimalistBorder, RoundedCornerShape(DeckCorner))
                .padding(20.dp)
        ) {
            Column(
                modifier = Modifier.fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(14.dp)
            ) {
                // Header
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Column {
                        Text(
                            text = "Matris & Izgara Düzeni",
                            fontSize = 16.sp,
                            fontWeight = FontWeight.ExtraBold,
                            color = MinimalistPrimary
                        )
                        Text(
                            text = "Telefon ve PC için yuva boyutu",
                            fontSize = 11.5.sp,
                            color = MinimalistSecondary
                        )
                    }
                    IconButton(
                        onClick = onDismiss,
                        modifier = Modifier.size(28.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.Close,
                            contentDescription = "Kapat",
                            tint = MinimalistSecondary,
                            modifier = Modifier.size(18.dp)
                        )
                    }
                }

                // Live Interactive Mini Preview
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(95.dp)
                        .clip(RoundedCornerShape(12.dp))
                        .background(MinimalistBackground)
                        .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                        .padding(8.dp),
                    contentAlignment = Alignment.Center
                ) {
                    Column(
                        verticalArrangement = Arrangement.spacedBy(3.dp),
                        horizontalAlignment = Alignment.CenterHorizontally
                    ) {
                        for (r in 0 until tempRows) {
                            Row(horizontalArrangement = Arrangement.spacedBy(3.dp)) {
                                for (c in 0 until tempCols) {
                                    Box(
                                        modifier = Modifier
                                            .size(if (tempCols > 4 || tempRows > 4) 11.dp else 14.dp)
                                            .clip(RoundedCornerShape(2.5.dp))
                                            .background(MinimalistPrimary.copy(alpha = 0.28f))
                                            .border(0.5.dp, MinimalistPrimary, RoundedCornerShape(2.5.dp))
                                    )
                                }
                            }
                        }
                        Spacer(modifier = Modifier.height(3.dp))
                        Text(
                            text = "$tempCols Sütun × $tempRows Satır (${tempCols * tempRows} Yuva)",
                            fontSize = 10.5.sp,
                            fontWeight = FontWeight.Bold,
                            fontFamily = FontFamily.Monospace,
                            color = MinimalistPrimary
                        )
                    }
                }

                // Quick Presets Title
                Text(
                    text = "HIZLI ŞABLONLAR",
                    fontSize = 9.5.sp,
                    fontWeight = FontWeight.Bold,
                    fontFamily = FontFamily.Monospace,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )

                // Presets Grid (2 rows of 3)
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    val row1 = presets.take(3)
                    val row2 = presets.drop(3)
                    for (rowList in listOf(row1, row2)) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(6.dp)
                        ) {
                            for ((label, r, c) in rowList) {
                                val isSelected = tempRows == r && tempCols == c
                                Box(
                                    modifier = Modifier
                                        .weight(1f)
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(if (isSelected) MinimalistPrimary else MinimalistSurfaceElevated)
                                        .border(
                                            1.dp,
                                            if (isSelected) MinimalistPrimary else MinimalistBorder,
                                            RoundedCornerShape(12.dp)
                                        )
                                        .clickable {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            tempRows = r
                                            tempCols = c
                                        }
                                        .padding(vertical = 7.dp),
                                    contentAlignment = Alignment.Center
                                ) {
                                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                        Text(
                                            text = label,
                                            fontSize = 12.5.sp,
                                            fontWeight = FontWeight.Bold,
                                            fontFamily = FontFamily.Monospace,
                                            color = if (isSelected) MinimalistBackground else MinimalistPrimary
                                        )
                                        Text(
                                            text = "${r * c} Yuva",
                                            fontSize = 8.5.sp,
                                            color = if (isSelected) MinimalistBackground.copy(alpha = 0.7f) else MinimalistSecondary
                                        )
                                    }
                                }
                            }
                        }
                    }
                }

                // Fine-Tuning Steppers
                Text(
                    text = "ÖZEL AYAR",
                    fontSize = 9.5.sp,
                    fontWeight = FontWeight.Bold,
                    fontFamily = FontFamily.Monospace,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )

                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(10.dp)
                ) {
                    // Cols Stepper
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(12.dp))
                            .background(MinimalistSurfaceElevated)
                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                            .padding(6.dp)
                    ) {
                        Column(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalAlignment = Alignment.CenterHorizontally
                        ) {
                            Text(
                                text = "Sütun Sayısı",
                                fontSize = 9.5.sp,
                                color = MinimalistSecondary
                            )
                            Spacer(modifier = Modifier.height(2.dp))
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.SpaceBetween,
                                modifier = Modifier.fillMaxWidth()
                            ) {
                                IconButton(
                                    onClick = {
                                        if (tempCols > 1) {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            tempCols--
                                        }
                                    },
                                    modifier = Modifier.size(26.dp)
                                ) {
                                    Text("-", fontSize = 16.sp, fontWeight = FontWeight.Bold, color = MinimalistPrimary)
                                }
                                Text(
                                    text = "$tempCols",
                                    fontSize = 15.sp,
                                    fontWeight = FontWeight.ExtraBold,
                                    fontFamily = FontFamily.Monospace,
                                    color = MinimalistPrimary
                                )
                                IconButton(
                                    onClick = {
                                        if (tempCols < 6) {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            tempCols++
                                        }
                                    },
                                    modifier = Modifier.size(26.dp)
                                ) {
                                    Text("+", fontSize = 16.sp, fontWeight = FontWeight.Bold, color = MinimalistPrimary)
                                }
                            }
                        }
                    }

                    // Rows Stepper
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(12.dp))
                            .background(MinimalistSurfaceElevated)
                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                            .padding(6.dp)
                    ) {
                        Column(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalAlignment = Alignment.CenterHorizontally
                        ) {
                            Text(
                                text = "Satır Sayısı",
                                fontSize = 9.5.sp,
                                color = MinimalistSecondary
                            )
                            Spacer(modifier = Modifier.height(2.dp))
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.SpaceBetween,
                                modifier = Modifier.fillMaxWidth()
                            ) {
                                IconButton(
                                    onClick = {
                                        if (tempRows > 1) {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            tempRows--
                                        }
                                    },
                                    modifier = Modifier.size(26.dp)
                                ) {
                                    Text("-", fontSize = 16.sp, fontWeight = FontWeight.Bold, color = MinimalistPrimary)
                                }
                                Text(
                                    text = "$tempRows",
                                    fontSize = 15.sp,
                                    fontWeight = FontWeight.ExtraBold,
                                    fontFamily = FontFamily.Monospace,
                                    color = MinimalistPrimary
                                )
                                IconButton(
                                    onClick = {
                                        if (tempRows < 6) {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            tempRows++
                                        }
                                    },
                                    modifier = Modifier.size(26.dp)
                                ) {
                                    Text("+", fontSize = 16.sp, fontWeight = FontWeight.Bold, color = MinimalistPrimary)
                                }
                            }
                        }
                    }
                }

                // Actions: Apply & Dismiss
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    TextButton(
                        onClick = onDismiss,
                        modifier = Modifier.weight(1f)
                    ) {
                        Text("İptal", color = MinimalistSecondary)
                    }
                    Button(
                        onClick = {
                            haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                            onApply(tempRows, tempCols)
                        },
                        modifier = Modifier.weight(1.5f),
                        colors = ButtonDefaults.buttonColors(
                            containerColor = MinimalistPrimary,
                            contentColor = MinimalistBackground
                        ),
                        shape = RoundedCornerShape(12.dp)
                    ) {
                        Text(
                            text = "Uygula",
                            fontWeight = FontWeight.Bold,
                            color = MinimalistBackground
                        )
                    }
                }
            }
        }
    }
}

private data class PresetItem(
    val label: String,
    val type: String,
    val value: String,
    val icon: String,
    val colSpan: Int,
    val rowSpan: Int,
    val color: String,
    val desc: String,
    val clockStyle: String = "apple_minimal"
)

/**
 * AddWidgetDialog - Visual picker to quickly populate an empty slot.
 */
@Composable
fun AddWidgetDialog(
    slotIndex: Int,
    onDismiss: () -> Unit,
    onSelectPreset: (label: String, type: String, value: String, icon: String, colSpan: Int, rowSpan: Int, color: String, clockStyle: String) -> Unit,
    onCustomCreate: () -> Unit
) {
    val haptic = LocalHapticFeedback.current

    val widgetPresets = listOf(
        PresetItem("Hava Durumu", "weather", "Istanbul", "weather", 1, 1, "#F4F4F5", "Canlı sıcaklık, şehir, durum ve rüzgar"),
        PresetItem("İşlemci Sıcaklığı", "live_info", "cpu_temp", "cpu", 1, 1, "#F4F4F5", "Termal sensör kartı ve tonal ilerleme"),
        PresetItem("Disk Doluluk", "live_info", "disk", "disk", 1, 1, "#F4F4F5", "C: doluluk yüzdesi ve boş alan"),
        PresetItem("Ekran Kartı", "live_info", "gpu", "gpu", 1, 1, "#F4F4F5", "GPU yükü ve sıcaklık"),
        PresetItem("Şimdi Çalıyor", "now_playing", "media_session", "now_playing", 2, 1, "#F4F4F5", "Çalan şarkı, sanatçı ve medya kontrolleri"),
        PresetItem("Pixel Saat", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Tarih, ince dev saat ve saat dilimi", clockStyle = "apple_minimal"),
        PresetItem("Saat & Takvim", "clock_widget", "", "clock", 1, 1, "#F4F4F5", "Tarih ve ince dev saat", clockStyle = "apple_minimal"),
        PresetItem("Flip Saat", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Split-flap koyu kartlı saat", clockStyle = "flip_clock"),
        PresetItem("Neon Saat", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Cyan neon monospace dijital saat", clockStyle = "neon_cyberpunk"),
        PresetItem("Kelime Saati", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Türkçe metin saati (ON BİR KIRK BEŞ)", clockStyle = "word_clock"),
        PresetItem("İsviçre Serif", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Serif tipografi ve ince saniye dairesi", clockStyle = "swiss_serif"),
        PresetItem("Terminal Saat", "clock_widget", "", "clock", 2, 1, "#F4F4F5", "Yeşil CRT [HH:MM:SS] terminal saati", clockStyle = "retro_terminal"),
        PresetItem("Ana Ses Barı", "volume_slider", "master_volume", "volume", 2, 1, "#F4F4F5", "Dokunmatik ses seviyesi"),
        PresetItem("Donanım Telemetrisi", "live_info", "combo", "cpu", 2, 1, "#F4F4F5", "İşlemci, bellek ve sıcaklık halkaları"),
        PresetItem("Medya Masası", "media_widget", "playpause", "play", 2, 1, "#F4F4F5", "Oynat/Durdur ve parça geçişi"),
        PresetItem("Canlı Ağ Hızı", "net_speed", "", "wifi", 1, 1, "#F4F4F5", "Anlık indirme ve yükleme hızı"),
        PresetItem("Pomodoro Sayacı", "pomodoro", "", "timer", 1, 1, "#F4F4F5", "Dokununca başlayan 25 dk odak sayacı"),
        PresetItem("Ekran Parlaklığı", "brightness_slider", "display_brightness", "bright", 2, 1, "#F4F4F5", "Monitör parlaklık ayarı"),
        PresetItem("Not Defteri", "command", "notepad.exe", "task", 1, 1, "#F4F4F5", "Tek tıkla PC'de başlat"),
        PresetItem("Google Chrome", "command", "cmd /c start chrome", "web", 1, 1, "#F4F4F5", "İnternet tarayıcısı"),
        PresetItem("Kısayol (Kopyala)", "hotkey", "ctrl+c", "copy", 1, 1, "#F4F4F5", "Ctrl+C panoya kopyala"),
        PresetItem("Kısayol (Yapıştır)", "hotkey", "ctrl+v", "copy", 1, 1, "#F4F4F5", "Ctrl+V panodan yapıştır"),
        PresetItem("Mikrofon Sustur", "toggle", "mic_mute", "mic", 1, 1, "#F4F4F5", "Dokununca aç/kapa"),
        PresetItem("PC'yi Kilitle", "command", "rundll32.exe user32.dll,LockWorkStation", "lock", 1, 1, "#F4F4F5", "Windows oturumunu kilitle")
    )

    androidx.compose.ui.window.Dialog(onDismissRequest = onDismiss) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .fillMaxHeight(0.85f)
                .clip(RoundedCornerShape(DeckCorner))
                .background(MinimalistSurface)
                .border(1.dp, MinimalistBorder, RoundedCornerShape(DeckCorner))
                .padding(18.dp)
        ) {
            Column(
                modifier = Modifier.fillMaxSize(),
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                // Header
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Column {
                        Text(
                            text = "Yeni Bileşen Ekle",
                            fontSize = 16.sp,
                            fontWeight = FontWeight.ExtraBold,
                            color = MinimalistPrimary
                        )
                        Text(
                            text = "Yuva #${slotIndex + 1} için bileşen seçin",
                            fontSize = 11.sp,
                            color = MinimalistSecondary
                        )
                    }
                    IconButton(onClick = onDismiss, modifier = Modifier.size(28.dp)) {
                        Icon(
                            imageVector = Icons.Default.Close,
                            contentDescription = "Kapat",
                            tint = MinimalistSecondary,
                            modifier = Modifier.size(18.dp)
                        )
                    }
                }

                // Custom Blank Button
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(12.dp))
                        .background(MinimalistSurfaceElevated)
                        .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                        .clickable {
                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                            onCustomCreate()
                        }
                        .padding(10.dp)
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(10.dp)
                    ) {
                        Box(
                            modifier = Modifier
                                .size(32.dp)
                                .clip(RoundedCornerShape(12.dp))
                                .background(MinimalistPrimary.copy(alpha = 0.20f)),
                            contentAlignment = Alignment.Center
                        ) {
                            Icon(Icons.Default.Add, contentDescription = null, tint = MinimalistPrimary, modifier = Modifier.size(18.dp))
                        }
                        Column {
                            Text(
                                text = "Özel Buton Tasarla",
                                fontSize = 12.5.sp,
                                fontWeight = FontWeight.Bold,
                                color = MinimalistPrimary
                            )
                            Text(
                                text = "Kendi tuş kombinasyonunu veya komutunu yaz",
                                fontSize = 10.sp,
                                color = MinimalistSecondary
                            )
                        }
                    }
                }

                Text(
                    text = "HAZIR BİLEŞENLER",
                    fontSize = 9.5.sp,
                    fontWeight = FontWeight.Bold,
                    fontFamily = FontFamily.Monospace,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )

                // Scrollable List of Preset Widgets
                Column(
                    modifier = Modifier
                        .weight(1f)
                        .verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(7.dp)
                ) {
                    for (preset in widgetPresets) {
                        val accentColor = MinimalistPrimary
                        Box(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(RoundedCornerShape(12.dp))
                                .background(MinimalistSurfaceElevated)
                                .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                                .clickable {
                                    haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                    onSelectPreset(
                                        preset.label,
                                        preset.type,
                                        preset.value,
                                        preset.icon,
                                        preset.colSpan,
                                        preset.rowSpan,
                                        preset.color,
                                        preset.clockStyle
                                    )
                                }
                                .padding(horizontal = 10.dp, vertical = 8.dp)
                        ) {
                            Row(
                                modifier = Modifier.fillMaxWidth(),
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.SpaceBetween
                            ) {
                                Row(
                                    verticalAlignment = Alignment.CenterVertically,
                                    horizontalArrangement = Arrangement.spacedBy(10.dp)
                                ) {
                                    Box(
                                        modifier = Modifier
                                            .size(30.dp)
                                            .clip(RoundedCornerShape(12.dp))
                                            .background(accentColor.copy(alpha = 0.2f)),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Icon(
                                            imageVector = getVectorIcon(preset.icon),
                                            contentDescription = null,
                                            tint = accentColor,
                                            modifier = Modifier.size(16.dp)
                                        )
                                    }
                                    Column {
                                        Text(
                                            text = preset.label,
                                            fontSize = 12.sp,
                                            fontWeight = FontWeight.Bold,
                                            color = MinimalistPrimary
                                        )
                                        Text(
                                            text = preset.desc,
                                            fontSize = 9.5.sp,
                                            color = MinimalistSecondary
                                        )
                                    }
                                }
                                // Size tag
                                Box(
                                    modifier = Modifier
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(MinimalistBackground)
                                        .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                                        .padding(horizontal = 7.dp, vertical = 3.dp)
                                ) {
                                    Text(
                                        text = "${preset.colSpan}×${preset.rowSpan}",
                                        fontSize = 8.5.sp,
                                        fontFamily = FontFamily.Monospace,
                                        fontWeight = FontWeight.Bold,
                                        color = MinimalistSecondary
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/**
 * StreamDeckTab — pure edge-to-edge Bento canvas. There is no header and no console bar: the
 * zero-scroll 2D matrix owns every pixel. Long-pressing a card or any free cell opens edit mode,
 * which floats a translucent pill (page selectors, `4×2` / `2×4` layout presets and a `✓ Bitti`
 * exit) on top of the deck; the cards then grow `1×1` (size), `✎` (edit) and `×` (delete)
 * controls. In normal mode a horizontal swipe switches page.
 */
@Composable
fun StreamDeckTab(
    isConnected: Boolean,
    buttons: List<StreamDeckButtonInfo>,
    rows: Int,
    cols: Int,
    pages: List<StreamDeckPageInfo>,
    activePage: Int,
    onPageChange: (Int) -> Unit,
    live: StreamDeckLiveStats,
    masterVolume: Float,
    onVolumeChange: (Float) -> Unit,
    brightness: Int,
    onBrightnessChange: (Int) -> Unit,
    ipAddress: String,
    password: String,
    onButtonLongClick: (StreamDeckButtonInfo) -> Unit,
    onApplyGridLayout: (rows: Int, cols: Int) -> Unit = { _, _ -> },
    onOpenGridSettings: () -> Unit = {},
    onAddWidgetClick: (slotIndex: Int) -> Unit = {},
    onAddPage: () -> Unit = {},
    onButtonsUpdated: (List<StreamDeckButtonInfo>) -> Unit = {}
) {
    val configuration = LocalConfiguration.current
    val effectiveLandscape = configuration.orientation == Configuration.ORIENTATION_LANDSCAPE
    val haptic = LocalHapticFeedback.current
    var totalDragX by remember { mutableFloatStateOf(0f) }
    var isEditMode by rememberSaveable { mutableStateOf(false) }
    var selectedForSwapId by remember { mutableStateOf<Int?>(null) }

    val colCount = cols.coerceAtLeast(1)
    val rowCount = rows.coerceAtLeast(1)
    val pageButtons = buttons.filter { it.page == activePage }

    // Long press anywhere on a card or a free cell flips the deck into edit mode.
    val enterEditMode: () -> Unit = {
        haptic.performHapticFeedback(HapticFeedbackType.LongPress)
        isEditMode = true
        selectedForSwapId = null
    }
    val exitEditMode: () -> Unit = {
        isEditMode = false
        selectedForSwapId = null
    }

    // Hardware/system back leaves edit mode first, and only then leaves the tab.
    BackHandler(enabled = isEditMode) { exitEditMode() }

    // Tap-to-move / swap: the first tap arms a card, the second swaps it or drops it on a cell.
    val handleCardTap: (StreamDeckButtonInfo) -> Unit = { btn ->
        val armed = selectedForSwapId
        when {
            armed == null -> {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                selectedForSwapId = btn.id
            }
            armed == btn.id -> selectedForSwapId = null
            else -> {
                val layout = packBentoLayout(pageButtons, rowCount, colCount)
                val from = layout.placements.firstOrNull { it.button.id == armed }
                val to = layout.placements.firstOrNull { it.button.id == btn.id }
                if (from != null && to != null) {
                    haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                    onButtonsUpdated(
                        buttons.map {
                            when (it.id) {
                                from.button.id -> it.copy(gridRow = to.row, gridCol = to.col)
                                to.button.id -> it.copy(gridRow = from.row, gridCol = from.col)
                                else -> it
                            }
                        }
                    )
                }
                selectedForSwapId = null
            }
        }
    }

    // A long press on a card never opens a dialog silently — it reveals edit mode, where the
    // `✎` chip is the only path into the card editor.
    val handleCardLongPress: (StreamDeckButtonInfo) -> Unit = { enterEditMode() }

    val handleMoveToCell: (Int, Int) -> Unit = { r, c ->
        val armed = selectedForSwapId
        if (armed != null) {
            haptic.performHapticFeedback(HapticFeedbackType.LongPress)
            onButtonsUpdated(
                buttons.map {
                    if (it.id == armed) it.copy(gridRow = r, gridCol = c) else it
                }
            )
            selectedForSwapId = null
        }
    }

    val handleResize: (StreamDeckButtonInfo) -> Unit = { btn ->
        haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
        val next = cycleDeckSpan(btn, rowCount, colCount)
        onButtonsUpdated(
            buttons.map { if (it.id == btn.id) next else it }
        )
    }

    val handleDelete: (StreamDeckButtonInfo) -> Unit = { btn ->
        haptic.performHapticFeedback(HapticFeedbackType.LongPress)
        onButtonsUpdated(buttons.filter { it.id != btn.id })
        if (selectedForSwapId == btn.id) selectedForSwapId = null
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MinimalistBackground)
            .pointerInput(activePage, pages.size, isEditMode) {
                detectHorizontalDragGestures(
                    onDragStart = { totalDragX = 0f },
                    onDragEnd = {
                        if (!isEditMode && totalDragX < -120f && pages.isNotEmpty()) {
                            val currentIndex = pages.indexOfFirst { it.id == activePage }
                            if (currentIndex != -1 && currentIndex < pages.size - 1) {
                                haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                                onPageChange(pages[currentIndex + 1].id)
                            }
                        } else if (!isEditMode && totalDragX > 120f && pages.isNotEmpty()) {
                            val currentIndex = pages.indexOfFirst { it.id == activePage }
                            if (currentIndex > 0) {
                                haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                                onPageChange(pages[currentIndex - 1].id)
                            }
                        }
                    },
                    onHorizontalDrag = { _, dragAmount ->
                        totalDragX += dragAmount
                    }
                )
            }
    ) {
        if (!isConnected) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = "Stream Deck kontrollerini kullanmak için PC bağlantısını kurun.",
                    color = MinimalistSecondary,
                    textAlign = TextAlign.Center
                )
            }
        } else {
            // Zero-scroll real 2D bento grid: the matrix owns every pixel, edge to edge.
            BentoDeckGrid(
                buttons = pageButtons,
                rows = rowCount,
                cols = colCount,
                gap = if (effectiveLandscape) 6.dp else 8.dp,
                live = live,
                masterVolume = masterVolume,
                onVolumeChange = onVolumeChange,
                brightness = brightness,
                onBrightnessChange = onBrightnessChange,
                ipAddress = ipAddress,
                password = password,
                onPress = { btn ->
                    SocketManager.sendControl(JSONObject().apply {
                        put("type", "stream_deck_press")
                        put("button_id", btn.id)
                    })
                },
                onLongPress = { btn -> handleCardLongPress(btn) },
                onEmptySlotClick = { onAddWidgetClick(it) },
                onEmptySlotLongPress = { if (!isEditMode) enterEditMode() },
                isEditMode = isEditMode,
                selectedForSwapId = selectedForSwapId,
                onSelectForSwap = { btn -> handleCardTap(btn) },
                onResizeButton = { btn -> handleResize(btn) },
                onDeleteButton = { btn -> handleDelete(btn) },
                onCustomizeButton = { btn -> onButtonLongClick(btn) },
                onMoveToCell = { r, c -> handleMoveToCell(r, c) },
                modifier = Modifier.fillMaxSize()
            )
        }

        // Edit mode is a transient overlay: the deck stays visible and scroll-free underneath.
        if (isEditMode) {
            DeckEditFloatingBar(
                pages = pages,
                activePage = activePage,
                onPageChange = onPageChange,
                onAddPage = onAddPage,
                rows = rowCount,
                cols = colCount,
                onApplyGridLayout = onApplyGridLayout,
                onOpenGridSettings = onOpenGridSettings,
                onDone = exitEditMode,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .padding(horizontal = 12.dp, vertical = 10.dp)
            )
        }
    }
}

/**
 * Translucent pill shown only while editing: page selectors (`Ana Sayfa`, `Medya & Ses`, `+`),
 * the two matrix presets (`4×2` / `2×4`), the full matrix dialog and a prominent `✓ Bitti` exit.
 */
@Composable
private fun DeckEditFloatingBar(
    pages: List<StreamDeckPageInfo>,
    activePage: Int,
    onPageChange: (Int) -> Unit,
    onAddPage: () -> Unit,
    rows: Int,
    cols: Int,
    onApplyGridLayout: (Int, Int) -> Unit,
    onOpenGridSettings: () -> Unit,
    onDone: () -> Unit,
    modifier: Modifier = Modifier
) {
    val shape = RoundedCornerShape(DeckCorner)
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(MinimalistSurfaceElevated.copy(alpha = 0.90f))
            .border(1.dp, MinimalistBorder, shape)
            .padding(horizontal = 8.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp)
    ) {
        Row(
            modifier = Modifier
                .weight(1f)
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            pages.forEachIndexed { index, page ->
                DeckPagePill(
                    title = page.title.ifBlank { if (index == 0) "Ana Sayfa" else "Sayfa " + (index + 1) },
                    selected = page.id == activePage,
                    onClick = { onPageChange(page.id) }
                )
            }
            DeckBarIcon(
                icon = Icons.Default.Add,
                contentDescription = "Sayfa Ekle",
                tint = MinimalistSecondary,
                onClick = onAddPage
            )
        }

        // `4×2` = 4 columns × 2 rows, `2×4` = 2 columns × 4 rows.
        DeckLayoutChip(
            label = "4×2",
            selected = cols == 4 && rows == 2,
            onClick = { onApplyGridLayout(2, 4) }
        )
        DeckLayoutChip(
            label = "2×4",
            selected = cols == 2 && rows == 4,
            onClick = { onApplyGridLayout(4, 2) }
        )
        DeckBarIcon(
            icon = Icons.Default.Tune,
            contentDescription = "Matris Ayarları",
            tint = MinimalistSecondary,
            onClick = onOpenGridSettings
        )
        DeckDoneButton(onClick = onDone)
    }
}

/** Monospace layout preset chip; inverts to the primary fill while it matches the live matrix. */
@Composable
private fun DeckLayoutChip(
    label: String,
    selected: Boolean,
    onClick: () -> Unit
) {
    val haptic = LocalHapticFeedback.current
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(12.dp))
            .background(if (selected) MinimalistPrimary else MinimalistSurface)
            .border(1.dp, if (selected) MinimalistPrimary else MinimalistBorder, RoundedCornerShape(12.dp))
            .clickable(onClickLabel = label) {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            }
            .padding(horizontal = 9.dp, vertical = 6.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = label,
            fontSize = 11.sp,
            fontWeight = FontWeight.SemiBold,
            fontFamily = FontFamily.Monospace,
            color = if (selected) MinimalistBackground else MinimalistPrimary,
            maxLines = 1
        )
    }
}

/** Prominent `✓ Bitti` exit that closes edit mode and restores the clean full-screen deck. */
@Composable
private fun DeckDoneButton(onClick: () -> Unit) {
    val haptic = LocalHapticFeedback.current
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(12.dp))
            .background(MinimalistPrimary)
            .clickable(onClickLabel = "Düzenlemeyi Bitir") {
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                onClick()
            }
            .padding(horizontal = 12.dp, vertical = 7.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Icon(
            imageVector = Icons.Default.Check,
            contentDescription = null,
            tint = MinimalistBackground,
            modifier = Modifier.size(15.dp)
        )
        Text(
            text = "Bitti",
            fontSize = 12.sp,
            fontWeight = FontWeight.Bold,
            color = MinimalistBackground
        )
    }
}

/** Cycles a card through 1x1, 2x1, 1x2 and 2x2, clamped to the current matrix. */
private fun cycleDeckSpan(
    button: StreamDeckButtonInfo,
    rowCount: Int,
    colCount: Int
): StreamDeckButtonInfo {
    val sequence = listOf(
        1 to 1,
        2 to 1,
        1 to 2,
        2 to 2
    ).filter { it.first <= colCount && it.second <= rowCount }
    if (sequence.isEmpty()) return button
    val current = sequence.indexOfFirst { it.first == button.colSpan && it.second == button.rowSpan }
    val nextIndex = if (current < 0 || current >= sequence.size - 1) 0 else current + 1
    val target = sequence[nextIndex]
    return button.copy(colSpan = target.first, rowSpan = target.second)
}

@Composable
fun TrackpadTab(isConnected: Boolean) {
    val context = LocalContext.current
    val haptic = LocalHapticFeedback.current
    val prefs = remember { context.getSharedPreferences("bigpocket_prefs", Context.MODE_PRIVATE) }
    val engine = remember { TrackpadEngine() }

    var precisionMode by rememberSaveable { mutableStateOf(prefs.getBoolean("trackpad_precision", false)) }
    var sensitivity by rememberSaveable { mutableStateOf(prefs.getFloat("trackpad_sensitivity", 1f)) }
    val precisionModeState = rememberUpdatedState(precisionMode)
    val sensitivityState = rememberUpdatedState(sensitivity)

    Column(
        modifier = Modifier.fillMaxSize()
    ) {
        TopHeader(
            title = "Sanal Mouse & Klavye",
            subtitle = "PC imlecini ve tuşlarını telefondan yönetin",
            isConnected = isConnected
        )

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .padding(horizontal = 20.dp, vertical = 4.dp),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            if (!isConnected) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text("Trackpad'i kullanmak için bilgisayara bağlanın.", textAlign = TextAlign.Center)
                }
                return
            }

            // Precision / sensitivity control bar (Editorial Monochrome tokens)
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(DeckCorner))
                    .background(MinimalistSurface)
                    .border(1.dp, MinimalistBorder, RoundedCornerShape(DeckCorner))
                    .padding(horizontal = 14.dp, vertical = 10.dp)
            ) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Row(
                        modifier = Modifier
                            .clip(RoundedCornerShape(12.dp))
                            .background(if (precisionMode) MinimalistPrimary else MinimalistSurfaceElevated)
                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                            .clickable {
                                precisionMode = !precisionMode
                                prefs.edit().putBoolean("trackpad_precision", precisionMode).apply()
                                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                            }
                            .padding(horizontal = 12.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Icon(
                            Icons.Default.Tune,
                            contentDescription = null,
                            tint = if (precisionMode) MinimalistBackground else MinimalistPrimary,
                            modifier = Modifier.size(16.dp)
                        )
                        Spacer(Modifier.width(6.dp))
                        Text(
                            "Hassas Mod",
                            fontSize = 12.sp,
                            fontWeight = FontWeight.Bold,
                            color = if (precisionMode) MinimalistBackground else MinimalistPrimary
                        )
                    }

                    Spacer(Modifier.weight(1f))

                    Text(
                        "Hassasiyet ${"%.1f".format(sensitivity)}x",
                        fontSize = 12.sp,
                        color = MinimalistSecondary,
                        fontWeight = FontWeight.Medium
                    )
                }

                Slider(
                    value = sensitivity,
                    onValueChange = {
                        sensitivity = it
                        prefs.edit().putFloat("trackpad_sensitivity", it).apply()
                    },
                    valueRange = 0.5f..1.6f,
                    modifier = Modifier.fillMaxWidth().height(32.dp),
                    colors = SliderDefaults.colors(
                        thumbColor = MinimalistPrimary,
                        activeTrackColor = MinimalistPrimary,
                        inactiveTrackColor = MinimalistBorder
                    )
                )
            }

            Spacer(modifier = Modifier.height(10.dp))

            // Trackpad area
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(DeckCorner))
                    .background(MinimalistSurface)
                    .border(1.dp, MinimalistBorder, RoundedCornerShape(DeckCorner))
                    .pointerInput(Unit) {
                        awaitEachGesture {
                            val down = awaitFirstDown(requireUnconsumed = false)
                            var isTwoFingerMode = false
                            var lastTwoFingerY = 0f
                            var lastMoveTime = down.uptimeMillis
                            var accumulatedMovement = 0f
                            val tapSlop = 14f

                            engine.precisionMode = precisionModeState.value
                            engine.sensitivity = sensitivityState.value
                            engine.resetGesture()

                            while (true) {
                                val event = awaitPointerEvent()
                                val changes = event.changes
                                val pointersCount = changes.filter { it.pressed }.size

                                if (pointersCount >= 2) {
                                    isTwoFingerMode = true
                                    val activeChanges = changes.filter { it.pressed }
                                    val currentY = activeChanges.map { it.position.y }.average().toFloat()
                                    if (lastTwoFingerY != 0f) {
                                        val dy = currentY - lastTwoFingerY
                                        accumulatedMovement += Math.abs(dy)
                                        // Low-pass smoothed wheel output.
                                        val wheel = engine.scroll(dy)
                                        if (wheel != null) {
                                            SocketManager.sendControl(JSONObject().apply {
                                                put("type", "mouse_scroll")
                                                put("dy", wheel)
                                            })
                                        }
                                    }
                                    lastTwoFingerY = currentY
                                    changes.forEach { it.consume() }
                                } else if (pointersCount == 1) {
                                    lastTwoFingerY = 0f
                                    val change = changes.first { it.pressed }
                                    if (!isTwoFingerMode) {
                                        val dx = change.position.x - change.previousPosition.x
                                        val dy = change.position.y - change.previousPosition.y
                                        if (dx != 0f || dy != 0f) {
                                            accumulatedMovement += hypot(dx.toDouble(), dy.toDouble()).toFloat()
                                            val dtMs = (change.uptimeMillis - lastMoveTime).coerceAtLeast(1L).toFloat()
                                            lastMoveTime = change.uptimeMillis
                                            // Power-curve acceleration + sub-pixel accumulation.
                                            val delta = engine.accelerate(dx, dy, dtMs)
                                            if (delta != null) {
                                                SocketManager.sendControl(JSONObject().apply {
                                                    put("type", "mouse_move")
                                                    put("dx", delta.first)
                                                    put("dy", delta.second)
                                                })
                                            }
                                        }
                                    }
                                    change.consume()
                                }

                                if (changes.all { !it.pressed }) {
                                    if (accumulatedMovement < tapSlop) {
                                        if (isTwoFingerMode) {
                                            haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                                            SocketManager.sendControl(JSONObject().apply {
                                                put("type", "mouse_click")
                                                put("button", "right")
                                            })
                                        } else {
                                            haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                            SocketManager.sendControl(JSONObject().apply {
                                                put("type", "mouse_click")
                                                put("button", "left")
                                            })
                                        }
                                    }
                                    break
                                }
                            }
                        }
                    },
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = "DOKUNMATİK ALAN\n\nTek Tık: Sol Click\nÇift Parmak Tık: Sağ Click\nSürükleme: İvmeli İmleç Hareketi\nÇift Parmak Sürükleme: Yumuşak Kaydırma\nÜst Bar: Hassas Mod & Hassasiyet",
                    textAlign = TextAlign.Center,
                    fontSize = 13.sp,
                    lineHeight = 20.sp,
                    color = MinimalistSecondary
                )
            }

            Spacer(modifier = Modifier.height(16.dp))

            var rawTypedText by remember { mutableStateOf("") }
            val speechLauncher = rememberLauncherForActivityResult(
                contract = ActivityResultContracts.StartActivityForResult()
            ) { result ->
                if (result.resultCode == Activity.RESULT_OK) {
                    val spoken = result.data?.getStringArrayListExtra(android.speech.RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
                    if (!spoken.isNullOrBlank()) {
                        SocketManager.sendControl(JSONObject().apply {
                            put("type", "keyboard_input")
                            put("text", spoken)
                        })
                        Toast.makeText(context, "Söylenen yazıldı: $spoken", Toast.LENGTH_SHORT).show()
                    }
                }
            }

            // Functional Toolbar Row (Esc, Tab, Space, Backspace, Voice)
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp)
            ) {
                // Voice Typing Button
                IconButton(
                    onClick = {
                        try {
                            val intent = Intent(android.speech.RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                                putExtra(android.speech.RecognizerIntent.EXTRA_LANGUAGE_MODEL, android.speech.RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                                putExtra(android.speech.RecognizerIntent.EXTRA_PROMPT, "PC'ye yazmak için konuşun...")
                            }
                            speechLauncher.launch(intent)
                        } catch (e: Exception) {
                            Toast.makeText(context, "Ses tanıma başlatılamadı", Toast.LENGTH_SHORT).show()
                        }
                    },
                    modifier = Modifier
                        .size(44.dp)
                        .background(MinimalistSurfaceElevated, RoundedCornerShape(12.dp))
                        .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                ) {
                    Icon(Icons.Default.PlayArrow, contentDescription = "Sesli Yaz", tint = MinimalistPrimary, modifier = Modifier.size(20.dp))
                }

                // Quick Hotkeys
                listOf(
                    "ESC" to "escape",
                    "TAB" to "tab",
                    "BOŞLUK" to "space",
                    "SİL" to "backspace"
                ).forEach { (label, key) ->
                    Button(
                        onClick = {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "keyboard_key")
                                put("key", key)
                            })
                        },
                        shape = RoundedCornerShape(12.dp),
                        colors = ButtonDefaults.buttonColors(containerColor = MinimalistSurfaceElevated, contentColor = MinimalistPrimary),
                        contentPadding = PaddingValues(horizontal = 10.dp, vertical = 0.dp),
                        modifier = Modifier.weight(1f).height(44.dp)
                    ) {
                        Text(label, fontSize = 11.sp, fontWeight = FontWeight.Bold)
                    }
                }
            }

            Spacer(modifier = Modifier.height(4.dp))

            // Direct Text Send Field
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically
            ) {
                OutlinedTextField(
                    value = rawTypedText,
                    onValueChange = { rawTypedText = it },
                    label = { Text("PC'ye Metin Gönder / Yaz") },
                    placeholder = { Text("Kelime veya cümle...") },
                    singleLine = true,
                    modifier = Modifier.weight(1f),
                    shape = RoundedCornerShape(12.dp),
                    keyboardOptions = KeyboardOptions(imeAction = androidx.compose.ui.text.input.ImeAction.Send),
                    keyboardActions = KeyboardActions(onSend = {
                        if (rawTypedText.isNotEmpty()) {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "keyboard_input")
                                put("text", rawTypedText)
                            })
                            rawTypedText = ""
                        }
                    }),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = MinimalistAccent,
                        unfocusedBorderColor = MinimalistBorder,
                        focusedContainerColor = MinimalistBackground,
                        unfocusedContainerColor = MinimalistBackground
                    )
                )

                Spacer(modifier = Modifier.width(8.dp))

                Button(
                    onClick = {
                        if (rawTypedText.isNotEmpty()) {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "keyboard_input")
                                put("text", rawTypedText)
                            })
                            rawTypedText = ""
                        } else {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "keyboard_key")
                                put("key", "enter")
                            })
                        }
                    },
                    shape = RoundedCornerShape(12.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = MinimalistPrimary, contentColor = MinimalistBackground),
                    modifier = Modifier.height(54.dp)
                ) {
                    Text(if (rawTypedText.isNotEmpty()) "Gönder" else "Enter", fontWeight = FontWeight.Bold, fontSize = 13.sp)
                }
            }
        }
    }
}

@Composable
fun MonitorTab(
    isConnected: Boolean,
    ipAddress: String,
    onExitTab: () -> Unit,
    isLandscapeMode: Boolean,
    onToggleLandscapeMode: () -> Unit,
    isMonitorFullscreen: Boolean,
    onToggleFullscreen: () -> Unit
) {
    val latestFrame = ScreenReceiver.latestFrame
    var isControlSuspended by remember { mutableStateOf(false) }
    var rightClickMode by remember { mutableStateOf(false) }
    var fitInsideMode by remember { mutableStateOf(true) }
    var showKeyboardDialog by remember { mutableStateOf(false) }
    
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MinimalistBackground)
    ) {
        if (!isConnected) {
            Column(modifier = Modifier.fillMaxSize()) {
                TopHeader(
                    title = "Sanal 2. Monitör",
                    subtitle = "Bilgisayar ekranını telefondan görüntüleyin ve kontrol edin",
                    isConnected = false
                )
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .weight(1f),
                    contentAlignment = Alignment.Center
                ) {
                    Text(
                        "İkinci ekranı görmek için bilgisayara bağlanın.",
                        textAlign = TextAlign.Center,
                        color = MinimalistSecondary
                    )
                }
            }
            return
        }

        DisposableEffect(Unit) {
            ScreenReceiver.start(ipAddress)
            onDispose {
                ScreenReceiver.stop()
            }
        }

        Box(
            modifier = Modifier
                .fillMaxSize()
                .pointerInput(isControlSuspended, rightClickMode) {
                    if (!isControlSuspended) {
                        detectTapGestures(
                            onTap = { offset ->
                                val rx = (offset.x / size.width).toDouble()
                                val ry = (offset.y / size.height).toDouble()
                                val action = if (rightClickMode) "right_click" else "click"
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "monitor_touch")
                                    put("action", action)
                                    put("x", rx)
                                    put("y", ry)
                                })
                            },
                            onDoubleTap = { offset ->
                                val rx = (offset.x / size.width).toDouble()
                                val ry = (offset.y / size.height).toDouble()
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "monitor_touch")
                                    put("action", "double_click")
                                    put("x", rx)
                                    put("y", ry)
                                })
                            },
                            onLongPress = { offset ->
                                val rx = (offset.x / size.width).toDouble()
                                val ry = (offset.y / size.height).toDouble()
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "monitor_touch")
                                    put("action", "right_click")
                                    put("x", rx)
                                    put("y", ry)
                                })
                            }
                        )
                    }
                }
                .pointerInput(isControlSuspended) {
                    if (!isControlSuspended) {
                        detectDragGestures(
                            onDragStart = { offset ->
                                val rx = (offset.x / size.width).toDouble()
                                val ry = (offset.y / size.height).toDouble()
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "monitor_touch")
                                    put("action", "down")
                                    put("x", rx)
                                    put("y", ry)
                                })
                            },
                            onDragEnd = {
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "monitor_touch")
                                    put("action", "up")
                                    put("x", 0.0)
                                    put("y", 0.0)
                                })
                            }
                        ) { change, _ ->
                            change.consume()
                            val rx = (change.position.x / size.width).toDouble()
                            val ry = (change.position.y / size.height).toDouble()
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "monitor_touch")
                                put("action", "move")
                                put("x", rx)
                                put("y", ry)
                            })
                        }
                    }
                },
            contentAlignment = Alignment.Center
        ) {
            if (latestFrame != null) {
                Image(
                    bitmap = latestFrame.asImageBitmap(),
                    contentDescription = "Second monitor view",
                    contentScale = if (fitInsideMode) ContentScale.Fit else ContentScale.FillBounds,
                    modifier = Modifier.fillMaxSize()
                )
            } else {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    CircularProgressIndicator(color = MinimalistPrimary)
                    Spacer(modifier = Modifier.height(12.dp))
                    Text(
                        text = "Ekran yayını bekleniyor...",
                        color = MinimalistSecondary
                    )
                }
            }
        }

        // Bottom Floating Action Toolbar for Easy Touch Control
        AnimatedVisibility(
            visible = !isControlSuspended,
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .padding(bottom = if (isMonitorFullscreen) 12.dp else 24.dp)
        ) {
            Surface(
                shape = RoundedCornerShape(DeckCorner),
                color = MinimalistSurface.copy(alpha = 0.95f),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                shadowElevation = 10.dp
            ) {
                Row(
                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 6.dp),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    // Mode toggle: Left vs Right click
                    FilterChip(
                        selected = rightClickMode,
                        onClick = { rightClickMode = !rightClickMode },
                        label = {
                            Text(
                                if (rightClickMode) "Sağ Tık" else "Sol Tık",
                                fontSize = 11.sp,
                                fontWeight = FontWeight.Bold
                            )
                        },
                        leadingIcon = {
                            Icon(
                                imageVector = Icons.Default.Mouse,
                                contentDescription = null,
                                modifier = Modifier.size(14.dp)
                            )
                        },
                        colors = FilterChipDefaults.filterChipColors(
                            selectedContainerColor = MinimalistPrimary,
                            selectedLabelColor = MinimalistPrimary,
                            containerColor = MinimalistBorder,
                            labelColor = MinimalistSecondary
                        )
                    )

                    // Keyboard input dialog trigger
                    IconButton(
                        onClick = { showKeyboardDialog = true },
                        modifier = Modifier.size(36.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.Keyboard,
                            contentDescription = "Klavye",
                            tint = MinimalistPrimary,
                            modifier = Modifier.size(18.dp)
                        )
                    }

                    // Scroll Up
                    IconButton(
                        onClick = {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "monitor_touch")
                                put("action", "scroll")
                                put("dy", 120.0)
                                put("x", 0.5)
                                put("y", 0.5)
                            })
                        },
                        modifier = Modifier.size(36.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.ArrowUpward,
                            contentDescription = "Yukarı Kaydır",
                            tint = MinimalistPrimary,
                            modifier = Modifier.size(18.dp)
                        )
                    }

                    // Scroll Down
                    IconButton(
                        onClick = {
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "monitor_touch")
                                put("action", "scroll")
                                put("dy", -120.0)
                                put("x", 0.5)
                                put("y", 0.5)
                            })
                        },
                        modifier = Modifier.size(36.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.ArrowDownward,
                            contentDescription = "Aşağı Kaydır",
                            tint = MinimalistPrimary,
                            modifier = Modifier.size(18.dp)
                        )
                    }

                    // Fit inside / Fill screen toggle
                    IconButton(
                        onClick = { fitInsideMode = !fitInsideMode },
                        modifier = Modifier.size(36.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.AspectRatio,
                            contentDescription = "Sığdır / Doldur",
                            tint = if (fitInsideMode) MinimalistPrimary else MinimalistSecondary,
                            modifier = Modifier.size(18.dp)
                        )
                    }
                }
            }
        }

        // Floating Control Bar/FAB overlay at top-right
        Row(
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(16.dp)
                .background(MinimalistSurface.copy(alpha = 0.95f), RoundedCornerShape(DeckCorner))
                .border(1.dp, MinimalistBorder, RoundedCornerShape(DeckCorner))
                .padding(horizontal = 6.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            // Toggle Control Mode button (Lock / Unlock)
            IconButton(
                onClick = { isControlSuspended = !isControlSuspended },
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = if (isControlSuspended) Icons.Default.Lock else Icons.Default.LockOpen,
                    contentDescription = "Kontrol Kilidi",
                    tint = if (isControlSuspended) MinimalistSecondary else MinimalistPrimary,
                    modifier = Modifier.size(20.dp)
                )
            }
            
            // Toggle Fullscreen button
            IconButton(
                onClick = onToggleFullscreen,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = if (isMonitorFullscreen) Icons.Default.FullscreenExit else Icons.Default.Fullscreen,
                    contentDescription = "Tam Ekran Yap",
                    tint = if (isMonitorFullscreen) MinimalistPrimary else MinimalistSecondary,
                    modifier = Modifier.size(20.dp)
                )
            }
            
            // Toggle orientation button
            IconButton(
                onClick = onToggleLandscapeMode,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = Icons.Default.ScreenRotation,
                    contentDescription = "Yön Değiştir",
                    tint = MinimalistPrimary,
                    modifier = Modifier.size(20.dp)
                )
            }
            
            // Exit Tab / Return to Dashboard button
            IconButton(
                onClick = onExitTab,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = Icons.Default.Home,
                    contentDescription = "Çıkış",
                    tint = MinimalistPrimary,
                    modifier = Modifier.size(20.dp)
                )
            }
        }

        // Virtual Screen Keyboard Input Dialog
        if (showKeyboardDialog) {
            var monitorTypedText by remember { mutableStateOf("") }
            AlertDialog(
                onDismissRequest = { showKeyboardDialog = false },
                shape = RoundedCornerShape(DeckCorner),
                containerColor = MinimalistSurface,
                title = { Text("PC'ye Klavye Girişi", fontSize = 16.sp, fontWeight = FontWeight.Bold) },
                text = {
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        OutlinedTextField(
                            value = monitorTypedText,
                            onValueChange = { monitorTypedText = it },
                            placeholder = { Text("Yazı yazın...", fontSize = 13.sp) },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(6.dp)
                        ) {
                            Button(
                                onClick = {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "keyboard_key")
                                        put("key", "enter")
                                    })
                                },
                                modifier = Modifier.weight(1f),
                                contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp)
                            ) {
                                Text("Enter", fontSize = 11.sp)
                            }
                            Button(
                                onClick = {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "keyboard_key")
                                        put("key", "backspace")
                                    })
                                },
                                modifier = Modifier.weight(1f),
                                contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp)
                            ) {
                                Text("Sil", fontSize = 11.sp)
                            }
                            Button(
                                onClick = {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "keyboard_key")
                                        put("key", "escape")
                                    })
                                },
                                modifier = Modifier.weight(1f),
                                contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp)
                            ) {
                                Text("Esc", fontSize = 11.sp)
                            }
                            Button(
                                onClick = {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "keyboard_key")
                                        put("key", "space")
                                    })
                                },
                                modifier = Modifier.weight(1f),
                                contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp)
                            ) {
                                Text("Boşluk", fontSize = 11.sp)
                            }
                        }
                    }
                },
                confirmButton = {
                    Button(
                        onClick = {
                            if (monitorTypedText.isNotEmpty()) {
                                SocketManager.sendControl(JSONObject().apply {
                                    put("type", "keyboard_input")
                                    put("text", monitorTypedText)
                                })
                                monitorTypedText = ""
                            }
                            showKeyboardDialog = false
                        }
                    ) {
                        Text("Gönder")
                    }
                },
                dismissButton = {
                    TextButton(onClick = { showKeyboardDialog = false }) {
                        Text("Kapat")
                    }
                }
            )
        }
    }
}

@Composable
fun BigPocketLogo(modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        
        // 1. Background rounded squircle
        drawRoundRect(
            color = MinimalistBackground,
            size = size,
            cornerRadius = androidx.compose.ui.geometry.CornerRadius(w * 0.22f, h * 0.22f)
        )
        drawRoundRect(
            color = MinimalistBorder,
            size = size,
            cornerRadius = androidx.compose.ui.geometry.CornerRadius(w * 0.22f, h * 0.22f),
            style = androidx.compose.ui.graphics.drawscope.Stroke(width = 2.dp.toPx())
        )
        
        // 2. Phone device sliding out of pocket
        val phoneLeft = w * 0.34f
        val phoneTop = h * 0.16f
        val phoneWidth = w * 0.32f
        val phoneHeight = h * 0.38f
        val phoneRadius = androidx.compose.ui.geometry.CornerRadius(6.dp.toPx(), 6.dp.toPx())
        
        drawRoundRect(
            color = MinimalistSurfaceElevated,
            topLeft = Offset(phoneLeft, phoneTop),
            size = androidx.compose.ui.geometry.Size(phoneWidth, phoneHeight),
            cornerRadius = phoneRadius
        )
        drawRoundRect(
            color = MinimalistPrimary,
            topLeft = Offset(phoneLeft, phoneTop),
            size = androidx.compose.ui.geometry.Size(phoneWidth, phoneHeight),
            cornerRadius = phoneRadius,
            style = androidx.compose.ui.graphics.drawscope.Stroke(width = 2.dp.toPx())
        )
        
        // Phone speaker notch
        drawRoundRect(
            color = MinimalistSecondary,
            topLeft = Offset(w * 0.44f, h * 0.20f),
            size = androidx.compose.ui.geometry.Size(w * 0.12f, h * 0.024f),
            cornerRadius = androidx.compose.ui.geometry.CornerRadius(1.5.dp.toPx(), 1.5.dp.toPx())
        )

        // 3. Pocket Body
        val pocketPath = androidx.compose.ui.graphics.Path().apply {
            moveTo(w * 0.26f, h * 0.38f)
            lineTo(w * 0.26f, h * 0.55f)
            cubicTo(
                w * 0.26f, h * 0.70f,
                w * 0.38f, h * 0.78f,
                w * 0.50f, h * 0.78f
            )
            cubicTo(
                w * 0.62f, h * 0.78f,
                w * 0.74f, h * 0.70f,
                w * 0.74f, h * 0.55f
            )
            lineTo(w * 0.74f, h * 0.38f)
            close()
        }
        // Fill pocket
        drawPath(path = pocketPath, color = MinimalistSurfaceElevated)
        // Pocket stroke
        drawPath(
            path = pocketPath,
            color = MinimalistPrimary,
            style = androidx.compose.ui.graphics.drawscope.Stroke(
                width = 3.5.dp.toPx(),
                join = androidx.compose.ui.graphics.StrokeJoin.Round
            )
        )

        // 4. Top Pocket Rim (White Bar)
        drawLine(
            color = MinimalistPrimary,
            start = Offset(w * 0.22f, h * 0.38f),
            end = Offset(w * 0.78f, h * 0.38f),
            strokeWidth = 4.dp.toPx(),
            cap = androidx.compose.ui.graphics.StrokeCap.Round
        )

        // 5. Pocket Accent Seam (Lavender)
        val seamPath = androidx.compose.ui.graphics.Path().apply {
            moveTo(w * 0.34f, h * 0.47f)
            cubicTo(
                w * 0.34f, h * 0.59f,
                w * 0.41f, h * 0.67f,
                w * 0.50f, h * 0.67f
            )
            cubicTo(
                w * 0.59f, h * 0.67f,
                w * 0.66f, h * 0.59f,
                w * 0.66f, h * 0.47f
            )
        }
        drawPath(
            path = seamPath,
            color = MinimalistSecondary,
            style = androidx.compose.ui.graphics.drawscope.Stroke(
                width = 2.5.dp.toPx(),
                cap = androidx.compose.ui.graphics.StrokeCap.Round
            )
        )

        // 6. Center Wireless Connection Dot
        drawCircle(
            color = MinimalistPrimary,
            radius = 3.dp.toPx(),
            center = Offset(w * 0.50f, h * 0.57f)
        )
    }
}

/** Rounded live camera preview shown in the corner while streaming video to the PC. */
@Composable
private fun CameraPreviewOverlay(previewView: PreviewView, modifier: Modifier = Modifier) {
    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            modifier = Modifier
                .size(width = 128.dp, height = 170.dp)
                .clip(RoundedCornerShape(12.dp))
                .background(Color.Black)
                .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
        ) {
            AndroidView(
                factory = {
                    // AndroidView refuses a view that still has a parent (e.g. after a tab switch).
                    (previewView.parent as? android.view.ViewGroup)?.removeView(previewView)
                    previewView
                },
                modifier = Modifier.fillMaxSize()
            )
        }
        Spacer(modifier = Modifier.height(6.dp))
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(5.dp)
        ) {
            Box(
                modifier = Modifier
                    .size(7.dp)
                    .clip(CircleShape)
                    .background(StatusRedText)
            )
            Text(
                text = "CANLI ÖNİZLEME",
                fontSize = 9.sp,
                fontWeight = FontWeight.Bold,
                color = StatusRedText
            )
        }
    }
}

@Composable
fun MediaTab(
    isConnected: Boolean,
    previewView: PreviewView? = null,
    isMicStreaming: Boolean,
    onMicToggle: (Boolean) -> Unit,
    isCamStreaming: Boolean,
    onCamToggle: (Boolean) -> Unit,
    useFrontCamera: Boolean,
    onCameraSelect: (Boolean) -> Unit,
    cameraFps: Int = 30,
    onFpsChange: (Int) -> Unit = {},
    cameraQuality: String = "720p HD",
    onQualityChange: (String) -> Unit = {}
) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("bigpocket_prefs", android.content.Context.MODE_PRIVATE) }
    var bgStreamingEnabled by remember { mutableStateOf(prefs.getBoolean("bg_camera_enabled", true) && prefs.getBoolean("bg_mic_enabled", true)) }
    Column(
        modifier = Modifier.fillMaxSize()
    ) {
        TopHeader(
            title = "Kamera & Mikrofon Yayını",
            subtitle = "Ses ve görüntüyü düşük gecikmeyle PC'ye iletin",
            isConnected = isConnected
        )

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Top
        ) {
            // Live camera picture-in-picture: what is currently being sent to the PC.
            if (isCamStreaming && previewView != null) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(bottom = 12.dp),
                    horizontalArrangement = Arrangement.End
                ) {
                    CameraPreviewOverlay(previewView = previewView)
                }
            }

            if (!isConnected) {
                Text("Ses ve görüntü aktarımı için bilgisayara bağlanın.", textAlign = TextAlign.Center, color = MinimalistSecondary)
                return
            }

            // Microphone Card
            Card(
                shape = RoundedCornerShape(DeckCorner),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                modifier = Modifier.fillMaxWidth()
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(20.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(
                            imageVector = Icons.Default.Mic,
                            contentDescription = "Mic",
                            tint = if (isMicStreaming) MinimalistPrimary else MinimalistSecondary,
                            modifier = Modifier.size(32.dp)
                        )
                        Spacer(modifier = Modifier.width(16.dp))
                        Column {
                            Text("Mikrofon Aktarımı", fontWeight = FontWeight.Bold, fontSize = 16.sp)
                            Text(
                                text = if (isMicStreaming) "Ses PC'ye iletiliyor..." else "Kapalı",
                                fontSize = 12.sp,
                                color = MinimalistSecondary
                            )
                        }
                    }
                    Switch(
                        checked = isMicStreaming,
                        onCheckedChange = onMicToggle,
                        colors = bigPocketSwitchColors()
                    )
                }
            }

            Spacer(modifier = Modifier.height(24.dp))

            // Camera Card
            Card(
                shape = RoundedCornerShape(DeckCorner),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.padding(20.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Icon(
                                imageVector = Icons.Default.Videocam,
                                contentDescription = "Webcam",
                                tint = if (isCamStreaming) MinimalistPrimary else MinimalistSecondary,
                                modifier = Modifier.size(32.dp)
                            )
                            Spacer(modifier = Modifier.width(16.dp))
                            Column {
                                Text("Kamera Yayını (Webcam)", fontWeight = FontWeight.Bold, fontSize = 16.sp)
                                Text(
                                    text = if (isCamStreaming) "Görüntü PC'ye aktarılıyor..." else "Kapalı",
                                    fontSize = 12.sp,
                                    color = MinimalistSecondary
                                )
                            }
                        }
                        Switch(
                            checked = isCamStreaming,
                            onCheckedChange = onCamToggle,
                            colors = bigPocketSwitchColors()
                        )
                    }
                
                    Spacer(modifier = Modifier.height(16.dp))
                    Divider(color = MinimalistBorder)
                    Spacer(modifier = Modifier.height(16.dp))
                
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Column {
                            Text("Kamera Seçimi", fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                            Text(
                                text = if (useFrontCamera) "Ön Kamera (Aynalanmış)" else "Arka Kamera (Standart)",
                                fontSize = 12.sp,
                                color = MinimalistSecondary
                            )
                        }
                        Button(
                            onClick = { onCameraSelect(!useFrontCamera) },
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistSurfaceElevated,
                                contentColor = MinimalistPrimary
                            ),
                            shape = RoundedCornerShape(12.dp)
                        ) {
                            Icon(
                                imageVector = Icons.Default.FlipCameraAndroid,
                                contentDescription = "Switch Camera",
                                modifier = Modifier.size(16.dp)
                            )
                            Spacer(modifier = Modifier.width(8.dp))
                            Text("Kamerayı Değiştir", fontSize = 12.sp)
                        }
                    }

                    Spacer(modifier = Modifier.height(16.dp))
                    Divider(color = MinimalistBorder)
                    Spacer(modifier = Modifier.height(16.dp))

                    // FPS Selection
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text("Kare Hızı (FPS)", fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                        Text("Düşük gecikme ve akıcılık seçimi", fontSize = 11.sp, color = MinimalistSecondary)
                        Spacer(modifier = Modifier.height(8.dp))
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            listOf(15, 30, 60).forEach { fpsVal ->
                                val isSelected = cameraFps == fpsVal
                                FilterChip(
                                    selected = isSelected,
                                    onClick = { onFpsChange(fpsVal) },
                                    label = {
                                        Text(
                                            "$fpsVal FPS",
                                            fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                                            fontSize = 12.sp
                                        )
                                    },
                                    modifier = Modifier.weight(1f),
                                    colors = FilterChipDefaults.filterChipColors(
                                        selectedContainerColor = MinimalistPrimary,
                                        selectedLabelColor = MinimalistBackground,
                                        containerColor = MinimalistSurfaceElevated,
                                        labelColor = MinimalistSecondary
                                    )
                                )
                            }
                        }
                    }

                    Spacer(modifier = Modifier.height(16.dp))
                    Divider(color = MinimalistBorder)
                    Spacer(modifier = Modifier.height(16.dp))

                    // Quality & Resolution Selection
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text("Çözünürlük & Kalite", fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                        Text("USB 1ms modunda 1080p önerilir", fontSize = 11.sp, color = MinimalistSecondary)
                        Spacer(modifier = Modifier.height(8.dp))
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            listOf("480p SD", "720p HD", "1080p FHD").forEach { qVal ->
                                val isSelected = cameraQuality == qVal
                                FilterChip(
                                    selected = isSelected,
                                    onClick = { onQualityChange(qVal) },
                                    label = {
                                        Text(
                                            qVal,
                                            fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                                            fontSize = 11.sp
                                        )
                                    },
                                    modifier = Modifier.weight(1f),
                                    colors = FilterChipDefaults.filterChipColors(
                                        selectedContainerColor = MinimalistPrimary,
                                        selectedLabelColor = MinimalistBackground,
                                        containerColor = MinimalistSurfaceElevated,
                                        labelColor = MinimalistSecondary
                                    )
                                )
                            }
                        }
                    }
                }
            }

            Spacer(modifier = Modifier.height(16.dp))

            // Quick Background Streaming Control Card
            Card(
                shape = RoundedCornerShape(16.dp),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurfaceElevated),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                modifier = Modifier.fillMaxWidth()
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(16.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text("Arka Planda Yayını Sürdür", fontWeight = FontWeight.SemiBold, fontSize = 13.5.sp, color = MinimalistPrimary)
                        Text(
                            text = if (bgStreamingEnabled) "Ekran kapandığında kamera ve mikrofon açık kalır" else "Uygulama arka plana geçince yayın durdurulur",
                            fontSize = 11.5.sp,
                            color = MinimalistSecondary
                        )
                    }
                    Switch(
                        checked = bgStreamingEnabled,
                        onCheckedChange = { checked ->
                            bgStreamingEnabled = checked
                            prefs.edit()
                                .putBoolean("bg_camera_enabled", checked)
                                .putBoolean("bg_mic_enabled", checked)
                                .putBoolean("bg_service_enabled", checked)
                                .apply()
                        },
                        colors = bigPocketSwitchColors()
                    )
                }
            }
        }
    }
}

@Composable
fun ToolsTab(
    isConnected: Boolean,
    clipboardText: String,
    onSendClipboard: () -> Unit,
    uploadStatus: String,
    onSelectFile: () -> Unit,
    ipAddress: String,
    password: String,
    onOpenSettings: () -> Unit = {}
) {
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()
    
    var activeFlowTab by remember { mutableStateOf("flow") }
    var flowMessagesList by remember { mutableStateOf<List<FlowMessageItem>>(emptyList()) }
    var flowInputText by remember { mutableStateOf("") }
    val flowListState = rememberLazyListState()

    var localTextToSend by remember { mutableStateOf("") }
    var sharedFilesList by remember { mutableStateOf<List<SharedFileInfo>>(emptyList()) }
    var isRefreshingFiles by remember { mutableStateOf(false) }

    val refreshFlow = {
        if (isConnected) {
            fetchFlowHistory(ipAddress, password, { list ->
                coroutineScope.launch(Dispatchers.Main) {
                    flowMessagesList = list
                }
            }, {})
        }
    }

    val refreshFiles = {
        if (isConnected) {
            isRefreshingFiles = true
            fetchSharedFiles(ipAddress, password, { files ->
                coroutineScope.launch(Dispatchers.Main) {
                    sharedFilesList = files.sortedByDescending { it.modified }
                    isRefreshingFiles = false
                }
            }, { err ->
                coroutineScope.launch(Dispatchers.Main) {
                    isRefreshingFiles = false
                    Toast.makeText(context, "Dosya listesi yüklenemedi.", Toast.LENGTH_SHORT).show()
                }
            })
        }
    }

    DisposableEffect(Unit) {
        SocketManager.flowMessageListener = { obj ->
            val newItem = FlowMessageItem(
                id = obj.optString("id", java.util.UUID.randomUUID().toString()),
                sender = obj.optString("sender", "pc"),
                type = obj.optString("type", "text"),
                text = obj.optString("text"),
                fileName = obj.optString("file_name"),
                fileSize = obj.optLong("file_size", 0L),
                fileUrl = obj.optString("file_url"),
                timestamp = obj.optLong("timestamp", System.currentTimeMillis())
            )
            coroutineScope.launch(Dispatchers.Main) {
                flowMessagesList = flowMessagesList + newItem
            }
        }
        SocketManager.flowClearedListener = {
            coroutineScope.launch(Dispatchers.Main) {
                flowMessagesList = emptyList()
            }
        }
        onDispose {
            SocketManager.flowMessageListener = null
            SocketManager.flowClearedListener = null
        }
    }

    // Load flow and files initially
    LaunchedEffect(isConnected, ipAddress) {
        refreshFlow()
        refreshFiles()
    }

    // Refresh files when upload status turns to success
    LaunchedEffect(uploadStatus) {
        if (uploadStatus.contains("başarıyla") || uploadStatus.contains("Uploaded")) {
            refreshFlow()
            refreshFiles()
        }
    }

    Column(
        modifier = Modifier.fillMaxSize()
    ) {
        TopHeader(
            title = "Pano & Dosya Transferi",
            subtitle = "Panoyu eşitleyin ve dosyaları çift yönlü aktarın",
            isConnected = isConnected
        )

        // Mode Switcher: Hızlı Akış (WhatsApp) vs Klasik Havuz
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Surface(
                shape = RoundedCornerShape(12.dp),
                color = if (activeFlowTab == "flow") MinimalistPrimary else MinimalistSurface,
                border = androidx.compose.foundation.BorderStroke(1.dp, if (activeFlowTab == "flow") Color.Transparent else MinimalistBorder),
                modifier = Modifier
                    .weight(1f)
                    .clickable { activeFlowTab = "flow" }
            ) {
                Row(
                    modifier = Modifier.padding(vertical = 9.dp),
                    horizontalArrangement = Arrangement.Center,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Icon(
                        imageVector = Icons.Default.Public,
                        contentDescription = null,
                        tint = if (activeFlowTab == "flow") MinimalistBackground else MinimalistPrimary,
                        modifier = Modifier.size(16.dp)
                    )
                    Spacer(modifier = Modifier.width(6.dp))
                    Text(
                        "Hızlı Akış (WhatsApp)",
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = if (activeFlowTab == "flow") MinimalistBackground else MinimalistPrimary
                    )
                }
            }

            Surface(
                shape = RoundedCornerShape(12.dp),
                color = if (activeFlowTab == "classic") MinimalistPrimary else MinimalistSurface,
                border = androidx.compose.foundation.BorderStroke(1.dp, if (activeFlowTab == "classic") Color.Transparent else MinimalistBorder),
                modifier = Modifier
                    .weight(1f)
                    .clickable { activeFlowTab = "classic" }
            ) {
                Row(
                    modifier = Modifier.padding(vertical = 9.dp),
                    horizontalArrangement = Arrangement.Center,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Icon(
                        imageVector = Icons.Default.Folder,
                        contentDescription = null,
                        tint = if (activeFlowTab == "classic") MinimalistBackground else MinimalistPrimary,
                        modifier = Modifier.size(16.dp)
                    )
                    Spacer(modifier = Modifier.width(6.dp))
                    Text(
                        "Klasik Havuz",
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = if (activeFlowTab == "classic") MinimalistBackground else MinimalistPrimary
                    )
                }
            }
        }

        if (activeFlowTab == "flow") {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
            ) {
                if (flowMessagesList.isEmpty()) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .weight(1f)
                            .padding(32.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Icon(Icons.Default.Public, contentDescription = null, tint = MinimalistSecondary, modifier = Modifier.size(36.dp))
                            Spacer(modifier = Modifier.height(10.dp))
                            Text("Akışınız Boş", fontSize = 15.sp, fontWeight = FontWeight.SemiBold, color = MinimalistPrimary)
                            Spacer(modifier = Modifier.height(4.dp))
                            Text("Bilgisayardan veya buradan mesaj, bağlantı ve dosya gönderin.", fontSize = 12.sp, color = MinimalistSecondary, textAlign = TextAlign.Center)
                        }
                    }
                } else {
                    LazyColumn(
                        state = flowListState,
                        modifier = Modifier
                            .fillMaxWidth()
                            .weight(1f)
                            .padding(horizontal = 16.dp, vertical = 6.dp),
                        verticalArrangement = Arrangement.spacedBy(10.dp)
                    ) {
                        items(flowMessagesList) { msg ->
                            val isPhone = msg.sender == "phone"
                            val timeStr = java.text.SimpleDateFormat("HH:mm", java.util.Locale.getDefault()).format(java.util.Date(msg.timestamp))
                            val align = if (isPhone) Alignment.End else Alignment.Start

                            Column(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalAlignment = align
                            ) {
                                Surface(
                                    shape = RoundedCornerShape(
                                        topStart = 16.dp,
                                        topEnd = 16.dp,
                                        bottomStart = if (isPhone) 16.dp else 4.dp,
                                        bottomEnd = if (isPhone) 4.dp else 16.dp
                                    ),
                                    color = if (isPhone) MinimalistSurface else MinimalistSurfaceElevated,
                                    border = androidx.compose.foundation.BorderStroke(1.dp, if (isPhone) MinimalistBorder else Color.White.copy(alpha = 0.08f)),
                                    modifier = Modifier.widthIn(min = 120.dp, max = 300.dp)
                                ) {
                                    Column(modifier = Modifier.padding(10.dp)) {
                                        Row(
                                            modifier = Modifier.fillMaxWidth(),
                                            horizontalArrangement = Arrangement.SpaceBetween,
                                            verticalAlignment = Alignment.CenterVertically
                                        ) {
                                            Text(
                                                if (isPhone) "📱 Telefon" else "💻 PC",
                                                fontSize = 10.5.sp,
                                                fontWeight = FontWeight.SemiBold,
                                                color = MinimalistSecondary
                                            )
                                            Text(
                                                timeStr,
                                                fontSize = 10.sp,
                                                color = MinimalistSecondary.copy(alpha = 0.8f)
                                            )
                                        }
                                        Spacer(modifier = Modifier.height(4.dp))

                                        if (msg.type == "image" && msg.fileUrl.isNotEmpty()) {
                                            val fullUrl = "http://$ipAddress:8085${msg.fileUrl}?password=${android.net.Uri.encode(password)}"
                                            AsyncImage(
                                                model = fullUrl,
                                                contentDescription = msg.fileName,
                                                modifier = Modifier
                                                    .fillMaxWidth()
                                                    .heightIn(max = 200.dp)
                                                    .clip(RoundedCornerShape(8.dp))
                                            )
                                            Spacer(modifier = Modifier.height(6.dp))
                                            Row(
                                                modifier = Modifier.fillMaxWidth(),
                                                horizontalArrangement = Arrangement.SpaceBetween,
                                                verticalAlignment = Alignment.CenterVertically
                                            ) {
                                                Text(msg.fileName, fontSize = 11.sp, color = MinimalistPrimary, maxLines = 1, modifier = Modifier.weight(1f))
                                                IconButton(
                                                    onClick = { downloadFileUsingManager(context, ipAddress, password, msg.fileName) },
                                                    modifier = Modifier.size(28.dp)
                                                ) {
                                                    Icon(Icons.Default.Download, contentDescription = "İndir", tint = MinimalistPrimary, modifier = Modifier.size(16.dp))
                                                }
                                            }
                                        } else if (msg.type == "file" && msg.fileUrl.isNotEmpty()) {
                                            Row(
                                                verticalAlignment = Alignment.CenterVertically,
                                                horizontalArrangement = Arrangement.spacedBy(8.dp)
                                            ) {
                                                Icon(Icons.Default.Description, contentDescription = null, tint = MinimalistPrimary, modifier = Modifier.size(24.dp))
                                                Column(modifier = Modifier.weight(1f)) {
                                                    Text(msg.fileName.ifEmpty { msg.text }, fontSize = 12.sp, fontWeight = FontWeight.SemiBold, color = MinimalistPrimary, maxLines = 1)
                                                    if (msg.fileSize > 0) {
                                                        Text(formatFileSize(msg.fileSize), fontSize = 10.5.sp, color = MinimalistSecondary)
                                                    }
                                                }
                                                IconButton(
                                                    onClick = { downloadFileUsingManager(context, ipAddress, password, msg.fileName) },
                                                    modifier = Modifier.size(30.dp)
                                                ) {
                                                    Icon(Icons.Default.Download, contentDescription = "İndir", tint = MinimalistPrimary, modifier = Modifier.size(18.dp))
                                                }
                                            }
                                        } else {
                                            Text(
                                                text = msg.text,
                                                 fontSize = 13.sp,
                                                 color = MinimalistPrimary,
                                                 lineHeight = 18.sp
                                             )
                                             Spacer(modifier = Modifier.height(4.dp))
                                             Surface(
                                                 shape = RoundedCornerShape(6.dp),
                                                 color = Color.White.copy(alpha = 0.06f),
                                                 modifier = Modifier
                                                     .clickable {
                                                         val clipboard = context.getSystemService(android.content.Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                                                         clipboard.setPrimaryClip(android.content.ClipData.newPlainText("BigPocket", msg.text))
                                                         Toast.makeText(context, "Kopyalandı", Toast.LENGTH_SHORT).show()
                                                     }
                                             ) {
                                                 Row(
                                                     modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                                                     verticalAlignment = Alignment.CenterVertically
                                                 ) {
                                                     Icon(Icons.Default.ContentCopy, contentDescription = null, tint = MinimalistSecondary, modifier = Modifier.size(11.dp))
                                                     Spacer(modifier = Modifier.width(4.dp))
                                                     Text("Kopyala", fontSize = 10.sp, color = MinimalistSecondary)
                                                 }
                                             }
                                         }
                                     }
                                 }
                             }
                         }
                     }
                 }

                 // Sticky Bottom Input Bar
                 Surface(
                     color = MinimalistSurface,
                     border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                     modifier = Modifier.fillMaxWidth()
                 ) {
                     Row(
                         modifier = Modifier
                             .fillMaxWidth()
                             .padding(horizontal = 12.dp, vertical = 8.dp),
                         verticalAlignment = Alignment.CenterVertically,
                         horizontalArrangement = Arrangement.spacedBy(8.dp)
                     ) {
                         IconButton(
                             onClick = { onSelectFile() },
                             modifier = Modifier.size(40.dp)
                         ) {
                             Icon(Icons.Default.Add, contentDescription = "Dosya Ekle", tint = MinimalistPrimary, modifier = Modifier.size(20.dp))
                         }

                         OutlinedTextField(
                             value = flowInputText,
                             onValueChange = { flowInputText = it },
                             placeholder = { Text("Mesaj yazın...", fontSize = 13.sp, color = MinimalistSecondary) },
                             colors = OutlinedTextFieldDefaults.colors(
                                 focusedBorderColor = Color.White.copy(alpha = 0.25f),
                                 unfocusedBorderColor = MinimalistBorder,
                                 focusedTextColor = MinimalistPrimary,
                                 unfocusedTextColor = MinimalistPrimary,
                                 focusedContainerColor = MinimalistBackground,
                                 unfocusedContainerColor = MinimalistBackground
                             ),
                             shape = RoundedCornerShape(12.dp),
                             modifier = Modifier.weight(1f),
                             maxLines = 3
                         )

                         IconButton(
                             onClick = {
                                 val t = flowInputText.trim()
                                 if (t.isNotEmpty()) {
                                     SocketManager.sendFlowMessage(t)
                                     flowInputText = ""
                                 }
                             },
                             modifier = Modifier
                                 .size(40.dp)
                                 .clip(RoundedCornerShape(12.dp))
                                 .background(MinimalistPrimary)
                         ) {
                             Icon(Icons.Default.Send, contentDescription = "Gönder", tint = MinimalistBackground, modifier = Modifier.size(18.dp))
                         }
                     }
                 }
             }
         } else {
             Column(
                 modifier = Modifier
                     .fillMaxWidth()
                     .weight(1f)
                     .verticalScroll(rememberScrollState())
                     .padding(horizontal = 20.dp, vertical = 8.dp),
                 horizontalAlignment = Alignment.CenterHorizontally,
                 verticalArrangement = Arrangement.spacedBy(16.dp)
             ) {
            // Settings Entry Card
            Card(
                shape = RoundedCornerShape(DeckCorner),
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { onOpenSettings() },
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(14.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Box(
                            modifier = Modifier
                                .size(36.dp)
                                .clip(RoundedCornerShape(12.dp))
                                .background(MinimalistAccent.copy(alpha = 0.15f)),
                            contentAlignment = Alignment.Center
                        ) {
                            Icon(
                                imageVector = Icons.Default.Settings,
                                contentDescription = "Ayarlar",
                                tint = MinimalistAccent,
                                modifier = Modifier.size(20.dp)
                            )
                        }
                        Spacer(modifier = Modifier.width(12.dp))
                        Column {
                            Text("Uygulama Ayarları & Güncelleme", fontWeight = FontWeight.SemiBold, fontSize = 13.5.sp, color = MinimalistPrimary)
                            Text("Sürüm kontrolü, ekran uyanıklığı ve tercihler", fontSize = 11.5.sp, color = MinimalistSecondary)
                        }
                    }
                    Icon(Icons.Default.ArrowForward, contentDescription = "Aç", tint = MinimalistSecondary, modifier = Modifier.size(16.dp))
                }
            }

            if (!isConnected) {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(32.dp),
                    contentAlignment = Alignment.Center
                ) {
                    Text("Dosya ve pano eşitlemesi için bilgisayara bağlanın.", textAlign = TextAlign.Center)
                }
                return@Column
            }

            // --- CLIPBOARD SECTION ---
            Card(
                shape = RoundedCornerShape(DeckCorner),
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
            ) {
                Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Pano Paylaşımı (Metin)", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MinimalistPrimary)
                
                    // Show current PC clipboard
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(12.dp))
                            .background(MinimalistSurfaceElevated)
                            .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                            .padding(12.dp)
                    ) {
                        Column {
                            Text("Son Senkronize Edilen Metin:", fontSize = 11.sp, color = MinimalistSecondary)
                            Spacer(modifier = Modifier.height(4.dp))
                            Text(
                                text = if (clipboardText.isNotEmpty()) clipboardText else "(Pano boş veya henüz senkronize edilmedi)",
                                fontSize = 13.sp,
                                color = MinimalistPrimary,
                                maxLines = 4
                            )
                            if (clipboardText.isNotEmpty()) {
                                Spacer(modifier = Modifier.height(8.dp))
                                Button(
                                    onClick = {
                                        try {
                                            val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                                            val clip = android.content.ClipData.newPlainText("BigPocket Sync", clipboardText)
                                            clipboard.setPrimaryClip(clip)
                                            Toast.makeText(context, "Telefona kopyalandı!", Toast.LENGTH_SHORT).show()
                                        } catch (e: Exception) {
                                            // ignore
                                        }
                                    },
                                    shape = RoundedCornerShape(12.dp),
                                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                                    colors = ButtonDefaults.buttonColors(
                                        containerColor = MinimalistSurfaceElevated,
                                        contentColor = MinimalistPrimary
                                    )
                                ) {
                                    Text("Telefona Kopyala", fontSize = 11.sp)
                                }
                            }
                        }
                    }

                    // Send text to PC
                    OutlinedTextField(
                        value = localTextToSend,
                        onValueChange = { localTextToSend = it },
                        label = { Text("Bilgisayara Gönderilecek Metin") },
                        placeholder = { Text("Metin yazın veya yapıştırın...") },
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(12.dp)
                    )

                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Button(
                            onClick = {
                                if (localTextToSend.isNotEmpty()) {
                                    SocketManager.sendControl(JSONObject().apply {
                                        put("type", "clipboard_sync")
                                        put("text", localTextToSend)
                                    })
                                    Toast.makeText(context, "Metin bilgisayara gönderildi!", Toast.LENGTH_SHORT).show()
                                    localTextToSend = ""
                                }
                            },
                            shape = RoundedCornerShape(12.dp),
                            modifier = Modifier.weight(1f),
                            enabled = localTextToSend.isNotEmpty()
                        ) {
                            Text("PC Pano Güncelle", fontSize = 12.sp)
                        }

                        Button(
                            onClick = onSendClipboard,
                            shape = RoundedCornerShape(12.dp),
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistSurfaceElevated,
                                contentColor = MinimalistPrimary
                            ),
                            modifier = Modifier.weight(1.2f)
                        ) {
                            Text("Panoyu PC'ye Yolla", fontSize = 12.sp)
                        }
                    }
                }
            }

            // --- FILE UPLOAD SECTION ---
            Card(
                shape = RoundedCornerShape(DeckCorner),
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
            ) {
                Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Dosya & Görsel Gönder", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MinimalistPrimary)
                    Text(
                        text = "Fotoğraf, video veya dokümanları bilgisayara doğrudan yükleyin.",
                        fontSize = 12.sp,
                        color = MinimalistSecondary
                    )
                
                    if (uploadStatus.isNotEmpty()) {
                        Text(
                            text = uploadStatus,
                            fontWeight = FontWeight.Bold,
                            color = MinimalistPrimary,
                            fontSize = 13.sp
                        )
                    }

                    Button(
                        onClick = onSelectFile,
                        shape = RoundedCornerShape(12.dp),
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text("Telefondan Dosya Seç ve Gönder")
                    }
                }
            }

            // --- FILES LIST SECTION ---
            Card(
                shape = RoundedCornerShape(DeckCorner),
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
                border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
            ) {
                Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text("Bilgisayardaki Dosyalar", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MinimalistPrimary)
                        IconButton(onClick = refreshFiles) {
                            if (isRefreshingFiles) {
                                CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                            } else {
                                Icon(Icons.Default.Refresh, contentDescription = "Yenile", modifier = Modifier.size(20.dp))
                            }
                        }
                    }

                    if (sharedFilesList.isEmpty()) {
                        Text(
                            text = if (isRefreshingFiles) "Yükleniyor..." else "Klasörde paylaşılan dosya bulunamadı.",
                            fontSize = 12.sp,
                            color = MinimalistSecondary,
                            modifier = Modifier.padding(vertical = 8.dp)
                        )
                    } else {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            for (file in sharedFilesList) {
                                val ext = file.name.substringAfterLast(".", "").lowercase()
                                val (badgeColor, typeIcon) = when (ext) {
                                    "mp4", "mkv", "avi", "mov" -> MinimalistPrimary to Icons.Default.Movie
                                    "mp3", "wav", "flac" -> MinimalistAccent to Icons.Default.MusicNote
                                    "jpg", "jpeg", "png", "gif", "webp" -> MinimalistPrimary to Icons.Default.Image
                                    else -> MinimalistSecondary to Icons.Default.Description
                                }

                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .clip(RoundedCornerShape(12.dp))
                                        .background(MinimalistSurfaceElevated)
                                        .border(1.dp, MinimalistBorder, RoundedCornerShape(12.dp))
                                        .padding(vertical = 10.dp, horizontal = 14.dp),
                                    horizontalArrangement = Arrangement.SpaceBetween,
                                    verticalAlignment = Alignment.CenterVertically
                                ) {
                                    Box(
                                        modifier = Modifier
                                            .size(36.dp)
                                            .clip(RoundedCornerShape(12.dp))
                                            .background(badgeColor.copy(alpha = 0.15f)),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Icon(
                                            imageVector = typeIcon,
                                            contentDescription = ext,
                                            tint = badgeColor,
                                            modifier = Modifier.size(18.dp)
                                        )
                                    }

                                    Spacer(modifier = Modifier.width(12.dp))

                                    Column(modifier = Modifier.weight(1f)) {
                                        Text(
                                            text = file.name,
                                            fontSize = 13.5.sp,
                                            fontWeight = FontWeight.SemiBold,
                                            color = MinimalistPrimary,
                                            maxLines = 1
                                        )
                                        Text(
                                            text = formatFileSize(file.size),
                                            fontSize = 11.5.sp,
                                            color = MinimalistSecondary
                                        )
                                    }
                                
                                    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                                        // Download Button (Real Download trigger)
                                        IconButton(
                                            onClick = {
                                                downloadFileUsingManager(context, ipAddress, password, file.name)
                                            },
                                            modifier = Modifier.size(36.dp)
                                        ) {
                                            Icon(
                                                imageVector = Icons.Default.Download,
                                                contentDescription = "İndir",
                                                tint = MinimalistPrimary
                                            )
                                        }

                                        // Delete Button
                                        IconButton(
                                            onClick = {
                                                deleteSharedFile(ipAddress, password, file.name, {
                                                    coroutineScope.launch(Dispatchers.Main) {
                                                        Toast.makeText(context, "Dosya silindi", Toast.LENGTH_SHORT).show()
                                                        refreshFiles()
                                                    }
                                                }, { err ->
                                                    coroutineScope.launch(Dispatchers.Main) {
                                                        Toast.makeText(context, "Silinemedi: ${err.message}", Toast.LENGTH_SHORT).show()
                                                    }
                                                })
                                            },
                                            modifier = Modifier.size(36.dp)
                                        ) {
                                            Icon(
                                                imageVector = Icons.Default.Delete,
                                                contentDescription = "Sil",
                                                tint = StatusRedText
                                            )
                                        }
                                    }
                                }
                        }
                    }
                }
            }
            }
        }
    }
}
}
// ----------------- Helpers -----------------

// CameraX capture logic
private val cameraExecutor = Executors.newSingleThreadExecutor()

private fun startCameraAnalysis(
    context: Context,
    lifecycleOwner: androidx.lifecycle.LifecycleOwner,
    useFrontCamera: Boolean,
    fps: Int = 30,
    quality: String = "720p HD",
    previewView: PreviewView? = null,
    onFrame: (ByteArray) -> Unit
) {
    val cameraProviderFuture = ProcessCameraProvider.getInstance(context)
    cameraProviderFuture.addListener({
        val cameraProvider = cameraProviderFuture.get()
        val targetResolution = when (quality) {
            "480p SD" -> android.util.Size(640, 480)
            "1080p FHD" -> android.util.Size(1920, 1080)
            else -> android.util.Size(1280, 720)
        }
        val imageAnalysis = ImageAnalysis.Builder()
            .setTargetResolution(targetResolution)
            .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
            .setOutputImageFormat(ImageAnalysis.OUTPUT_IMAGE_FORMAT_YUV_420_888)
            .build()

        val preview = previewView?.let { view ->
            Preview.Builder().build().apply { setSurfaceProvider(view.surfaceProvider) }
        }

        val minIntervalMs = 1000L / fps.coerceIn(5, 60)
        var lastFrameTime = 0L

        imageAnalysis.setAnalyzer(cameraExecutor) { imageProxy ->
            val now = System.currentTimeMillis()
            if (now - lastFrameTime < minIntervalMs) {
                imageProxy.close()
                return@setAnalyzer
            }
            lastFrameTime = now

            val jpegBytes = imageProxy.toJpegBytes(isFrontCamera = useFrontCamera, qualityPreset = quality)
            if (jpegBytes != null) {
                onFrame(jpegBytes)
            }
            imageProxy.close()
        }

        val cameraSelector = if (useFrontCamera) CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA
        try {
            cameraProvider.unbindAll()
            if (preview != null) {
                cameraProvider.bindToLifecycle(
                    lifecycleOwner,
                    cameraSelector,
                    preview,
                    imageAnalysis
                )
            } else {
                cameraProvider.bindToLifecycle(
                    lifecycleOwner,
                    cameraSelector,
                    imageAnalysis
                )
            }
        } catch (e: Exception) {
            Log.e("CameraStream", "Use case binding failed", e)
        }
    }, ContextCompat.getMainExecutor(context))
}

// ImageProxy to JPEG Conversion: the NV21 buffer is rotated/mirrored in a single pass and
// encoded exactly once (no intermediate Bitmap decode + re-encode round trip).
private fun ImageProxy.toJpegBytes(isFrontCamera: Boolean, qualityPreset: String = "720p HD"): ByteArray? {
    try {
        val rotation = imageInfo.rotationDegrees
        val nv21 = toRotatedNv21(rotation, mirror = isFrontCamera)

        val swapAxes = rotation == 90 || rotation == 270
        val outWidth = if (swapAxes) height else width
        val outHeight = if (swapAxes) width else height

        val compressQuality = when (qualityPreset) {
            "480p SD" -> 50
            "1080p FHD" -> 85
            else -> 70
        }

        val yuvImage = YuvImage(nv21, ImageFormat.NV21, outWidth, outHeight, null)
        val out = ByteArrayOutputStream()
        yuvImage.compressToJpeg(Rect(0, 0, outWidth, outHeight), compressQuality, out)
        return out.toByteArray()
    } catch (e: Exception) {
        Log.e("CameraStream", "Error converting frame to JPEG", e)
        return null
    }
}

/**
 * Packs the YUV_420_888 planes into a single NV21 buffer (Y plane + interleaved VU) while
 * applying [rotationDegrees] and an optional horizontal mirror, so the JPEG encoder receives an
 * already upright frame. Plane strides are honoured explicitly.
 */
private fun ImageProxy.toRotatedNv21(rotationDegrees: Int, mirror: Boolean): ByteArray {
    val sourceWidth = width
    val sourceHeight = height
    val swapAxes = rotationDegrees == 90 || rotationDegrees == 270
    val outWidth = if (swapAxes) sourceHeight else sourceWidth
    val outHeight = if (swapAxes) sourceWidth else sourceHeight

    val lumaSize = outWidth * outHeight
    val nv21 = ByteArray(lumaSize * 3 / 2)

    val yPlane = planes[0]
    val uPlane = planes[1]
    val vPlane = planes[2]
    val yBuffer = yPlane.buffer
    val uBuffer = uPlane.buffer
    val vBuffer = vPlane.buffer
    val yRowStride = yPlane.rowStride
    val yPixelStride = yPlane.pixelStride

    for (y in 0 until sourceHeight) {
        val sourceRow = y * yRowStride
        for (x in 0 until sourceWidth) {
            val luma = yBuffer.get(sourceRow + x * yPixelStride)
            var dstX: Int
            var dstY: Int
            when (rotationDegrees) {
                90 -> {
                    dstX = sourceHeight - 1 - y
                    dstY = x
                }
                180 -> {
                    dstX = sourceWidth - 1 - x
                    dstY = sourceHeight - 1 - y
                }
                270 -> {
                    dstX = y
                    dstY = sourceWidth - 1 - x
                }
                else -> {
                    dstX = x
                    dstY = y
                }
            }
            if (mirror) dstX = outWidth - 1 - dstX
            nv21[dstY * outWidth + dstX] = luma
        }
    }

    // Chroma is subsampled by two in both directions, so the same mapping is applied on the
    // half sized grid (camera frames always have even dimensions).
    val chromaWidth = sourceWidth / 2
    val chromaHeight = sourceHeight / 2
    val outChromaWidth = outWidth / 2
    val uRowStride = uPlane.rowStride
    val vRowStride = vPlane.rowStride
    val uPixelStride = uPlane.pixelStride
    val vPixelStride = vPlane.pixelStride

    for (chromaY in 0 until chromaHeight) {
        for (chromaX in 0 until chromaWidth) {
            var dstChromaX: Int
            var dstChromaY: Int
            when (rotationDegrees) {
                90 -> {
                    dstChromaX = chromaHeight - 1 - chromaY
                    dstChromaY = chromaX
                }
                180 -> {
                    dstChromaX = chromaWidth - 1 - chromaX
                    dstChromaY = chromaHeight - 1 - chromaY
                }
                270 -> {
                    dstChromaX = chromaY
                    dstChromaY = chromaWidth - 1 - chromaX
                }
                else -> {
                    dstChromaX = chromaX
                    dstChromaY = chromaY
                }
            }
            if (mirror) dstChromaX = outChromaWidth - 1 - dstChromaX
            val u = uBuffer.get(chromaY * uRowStride + chromaX * uPixelStride)
            val v = vBuffer.get(chromaY * vRowStride + chromaX * vPixelStride)
            val index = lumaSize + dstChromaY * outWidth + dstChromaX * 2
            nv21[index] = v
            nv21[index + 1] = u
        }
    }

    return nv21
}

@Composable
fun QrScannerDialog(
    onQrCodeScanned: (String) -> Unit,
    onDismiss: () -> Unit
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val cameraProviderFuture = remember { ProcessCameraProvider.getInstance(context) }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        shape = RoundedCornerShape(DeckCorner),
        containerColor = MinimalistSurface,
        title = { Text("QR Kodu Taratın") },
        text = {
            Box(
                modifier = Modifier
                    .size(280.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .background(Color.Black)
            ) {
                val previewView = remember { androidx.camera.view.PreviewView(context) }
                androidx.compose.ui.viewinterop.AndroidView(
                    factory = { previewView },
                    modifier = Modifier.fillMaxSize()
                )
                
                LaunchedEffect(Unit) {
                    val cameraProvider = cameraProviderFuture.get()
                    val preview = androidx.camera.core.Preview.Builder().build().apply {
                        setSurfaceProvider(previewView.surfaceProvider)
                    }
                    
                    val imageAnalysis = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()
                        
                    val scanner = com.google.mlkit.vision.barcode.BarcodeScanning.getClient()
                    val executor = Executors.newSingleThreadExecutor()
                    
                    imageAnalysis.setAnalyzer(executor) { imageProxy ->
                        @androidx.annotation.OptIn(androidx.camera.core.ExperimentalGetImage::class)
                        val mediaImage = imageProxy.image
                        if (mediaImage != null) {
                            val image = com.google.mlkit.vision.common.InputImage.fromMediaImage(
                                mediaImage,
                                imageProxy.imageInfo.rotationDegrees
                            )
                            scanner.process(image)
                                .addOnSuccessListener { barcodes ->
                                    for (barcode in barcodes) {
                                        val rawValue = barcode.rawValue
                                        if (rawValue != null) {
                                            onQrCodeScanned(rawValue)
                                            break
                                        }
                                    }
                                }
                                .addOnCompleteListener {
                                    imageProxy.close()
                                }
                        } else {
                            imageProxy.close()
                        }
                    }
                    
                    try {
                        cameraProvider.unbindAll()
                        cameraProvider.bindToLifecycle(
                            lifecycleOwner,
                            CameraSelector.DEFAULT_BACK_CAMERA,
                            preview,
                            imageAnalysis
                        )
                    } catch (e: Exception) {
                        Log.e("QrScanner", "Camera binding failed", e)
                    }
                }
            }
        },
        confirmButton = {
            Button(
                onClick = onDismiss,
                shape = RoundedCornerShape(12.dp)
            ) {
                Text("Kapat")
            }
        }
    )
}

// File sharing upload utilizing OkHttp (streamed: the file is never fully held in RAM)
private fun uploadFileToServer(context: Context, fileUri: Uri, ip: String, password: String): Boolean {
    val client = OkHttpClient()
    try {
        val contentResolver = context.contentResolver
        var displayName = "upload_file"
        var fileSize = -1L
        contentResolver.query(fileUri, null, null, null, null)?.use { cursor ->
            if (cursor.moveToFirst()) {
                val nameIndex = cursor.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
                if (nameIndex != -1) displayName = cursor.getString(nameIndex) ?: displayName
                val sizeIndex = cursor.getColumnIndex(android.provider.OpenableColumns.SIZE)
                if (sizeIndex != -1 && !cursor.isNull(sizeIndex)) fileSize = cursor.getLong(sizeIndex)
            }
        }

        val inputStream = contentResolver.openInputStream(fileUri) ?: return false
        val contentType = contentResolver.getType(fileUri) ?: "application/octet-stream"

        // 64 KB chunks are copied straight from the content stream into the request body, so
        // arbitrarily large files upload without any memory spike.
        val streamingBody = object : RequestBody() {
            override fun contentType() = contentType.toMediaTypeOrNull()

            override fun contentLength() = if (fileSize > 0) fileSize else -1L

            override fun writeTo(sink: BufferedSink) {
                inputStream.source().use { source ->
                    val buffer = Buffer()
                    while (true) {
                        val read = source.read(buffer, 64 * 1024L)
                        if (read == -1L) break
                        sink.write(buffer, read)
                    }
                }
            }
        }

        val requestBody = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("file", displayName, streamingBody)
            .build()

        val request = Request.Builder()
            .url("http://$ip:8085/upload")
            .post(requestBody)
            .addHeader("X-Password", password)
            .build()

        client.newCall(request).execute().use { response ->
            return response.isSuccessful
        }
    } catch (e: Exception) {
        Log.e("FileTransfer", "File upload failed", e)
        return false
    }
}

// Upload custom Stream Deck button icon
private fun uploadIconToServer(context: Context, fileUri: Uri, ip: String, password: String, buttonId: Int): Boolean {
    val client = OkHttpClient()
    try {
        val contentResolver = context.contentResolver
        val cursor = contentResolver.query(fileUri, null, null, null, null)
        var displayName = "icon.png"
        cursor?.use {
            if (it.moveToFirst()) {
                val index = it.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
                if (index != -1) displayName = it.getString(index)
            }
        }
        
        val inputStream: InputStream? = contentResolver.openInputStream(fileUri)
        val fileBytes = inputStream?.readBytes() ?: return false
        inputStream.close()

        val requestBody = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("button_id", buttonId.toString())
            .addFormDataPart(
                "file",
                displayName,
                fileBytes.toRequestBody("image/*".toMediaTypeOrNull(), 0, fileBytes.size)
            )
            .build()

        val request = Request.Builder()
            .url("http://$ip:8085/upload_icon")
            .post(requestBody)
            .addHeader("X-Password", password)
            .build()

        client.newCall(request).execute().use { response ->
            return response.isSuccessful
        }
    } catch (e: Exception) {
        Log.e("UploadIcon", "Icon upload failed", e)
        return false
    }
}

