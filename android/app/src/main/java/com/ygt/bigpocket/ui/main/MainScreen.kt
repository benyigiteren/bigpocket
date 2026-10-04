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
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
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
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
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
import java.util.concurrent.Executors
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items

data class StreamDeckButtonInfo(
    val id: Int,
    val label: String,
    val type: String,
    val value: String,
    val icon: String,
    val page: Int = 0,
    val state: Boolean = false,
    val color: String = ""
)

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
                            color = item.optString("color", "")
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
    var ipAddress by remember { mutableStateOf(prefs.getString("ip_address", "192.168.1.100") ?: "192.168.1.100") }
    var password by remember { mutableStateOf(prefs.getString("password", "") ?: "") }
    var isGamerTheme by remember { mutableStateOf(false) }
    var keepScreenOn by remember { mutableStateOf(prefs.getBoolean("keep_screen_on", true)) }

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
    var selectedTab by remember { mutableIntStateOf(0) }
    var showQrScanner by remember { mutableStateOf(false) }
    var streamDeckButtons by remember { mutableStateOf<List<StreamDeckButtonInfo>>(emptyList()) }
    var streamDeckRows by remember { mutableIntStateOf(2) }
    var streamDeckCols by remember { mutableIntStateOf(4) }
    var streamDeckPages by remember { mutableStateOf<List<StreamDeckPageInfo>>(listOf(
        StreamDeckPageInfo(0, "Ana Sayfa"),
        StreamDeckPageInfo(1, "Medya & Ses"),
        StreamDeckPageInfo(2, "Sistem & PC")
    )) }
    var activeDeckPage by remember { mutableIntStateOf(0) }
    var liveCpu by remember { mutableIntStateOf(0) }
    var liveRam by remember { mutableIntStateOf(0) }
    var currentMasterVolume by remember { mutableFloatStateOf(50f) }

    // Camera FPS & Quality preferences
    var cameraFps by remember { mutableIntStateOf(prefs.getInt("camera_fps", 30)) }
    var cameraQuality by remember { mutableStateOf(prefs.getString("camera_quality", "720p") ?: "720p") }
    var isDeckLandscape by remember { mutableStateOf(prefs.getBoolean("deck_landscape_v2", false)) }
    var isDeckFullscreen by remember { mutableStateOf(prefs.getBoolean("deck_fullscreen", false)) }
    var isMonitorLandscape by remember { mutableStateOf(prefs.getBoolean("monitor_landscape", false)) }
    var isMonitorFullscreen by remember { mutableStateOf(prefs.getBoolean("monitor_fullscreen", false)) }
    var installedApps by remember { mutableStateOf<List<InstalledAppInfo>>(emptyList()) }
    var showEditDialog by remember { mutableStateOf(false) }
    var editingButton by remember { mutableStateOf<StreamDeckButtonInfo?>(null) }
    var showSettingsDialog by remember { mutableStateOf(false) }
    var manualUpdateInfo by remember { mutableStateOf<UpdateManager.UpdateInfo?>(null) }

    // Media streaming states
    var isMicStreaming by remember { mutableStateOf(false) }
    var isCamStreaming by remember { mutableStateOf(false) }
    var useFrontCamera by remember { mutableStateOf(prefs.getBoolean("use_front_camera", false)) }

    // Clipboard State
    var clipboardText by remember { mutableStateOf("") }

    // File Upload State
    var uploadStatus by remember { mutableStateOf("") }

    // Screen rotation and fullscreen controller based on tab selection
    LaunchedEffect(selectedTab, isDeckLandscape, isMonitorLandscape, isDeckFullscreen, isMonitorFullscreen) {
        val activity = context as? Activity
        val shouldBeLandscape = (selectedTab == 1 && isDeckLandscape) || (selectedTab == 3 && isMonitorLandscape)
        val shouldBeFullscreen = (selectedTab == 1 && isDeckFullscreen) || (selectedTab == 3 && isMonitorFullscreen)

        if (selectedTab == 1 || selectedTab == 3) {
            activity?.requestedOrientation = if (shouldBeLandscape) {
                ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
            } else {
                ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            }

            activity?.window?.let { window ->
                if (shouldBeFullscreen) {
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
        } else {
            activity?.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
            activity?.window?.let { window ->
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

        SocketManager.systemStatsListener = { cpu, ram ->
            coroutineScope.launch(Dispatchers.Main) {
                liveCpu = cpu
                liveRam = ram
            }
        }
        
        onDispose {
            SocketManager.connectionStateListener = null
            SocketManager.clipboardListener = null
            SocketManager.configUpdateListener = null
            SocketManager.buttonStateListener = null
            SocketManager.pageSwitchedListener = null
            SocketManager.volumeChangedListener = null
            SocketManager.systemStatsListener = null
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
            startCameraAnalysis(context, lifecycleOwner, useFrontCamera, cameraFps, cameraQuality) { jpegBytes ->
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
                Manifest.permission.RECORD_AUDIO
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
        com.ygt.bigpocket.services.PcDiscovery.discover(context)
            .catch { e -> Log.w("MainScreen", "Discovery unavailable", e) }
            .collect { pc ->
            if (SocketManager.isConnected) return@collect
            val now = System.currentTimeMillis()
            if (now - (lastAttempt[pc.ip] ?: 0L) < 8000) return@collect
            lastAttempt[pc.ip] = now

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
                                SocketManager.connect(ip, pw)
                            }
                        } else {
                            ipAddress = rawVal
                            prefs.edit().putString("ip_address", rawVal).apply()
                            SocketManager.connect(rawVal, password)
                        }
                    } catch (e: Exception) {
                        Log.e("MainScreen", "QR Scan Parse Error", e)
                        ipAddress = rawVal
                        prefs.edit().putString("ip_address", rawVal).apply()
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
            var appSearchQuery by remember(currentBtn) { mutableStateOf("") }

            LaunchedEffect(streamDeckButtons) {
                val updatedBtn = streamDeckButtons.find { it.id == currentBtn.id }
                if (updatedBtn != null) {
                    editIcon = updatedBtn.icon
                }
            }

            AlertDialog(
                onDismissRequest = { showEditDialog = false },
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

                        Text("Eylem Türü", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            Button(
                                onClick = { editType = "hotkey" },
                                colors = ButtonDefaults.buttonColors(
                                    containerColor = if (editType == "hotkey") MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceVariant,
                                    contentColor = if (editType == "hotkey") MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant
                                ),
                                modifier = Modifier.weight(1f),
                                shape = RoundedCornerShape(8.dp)
                            ) {
                                Text("Kısayol")
                            }
                            Button(
                                onClick = { editType = "command" },
                                colors = ButtonDefaults.buttonColors(
                                    containerColor = if (editType == "command") MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceVariant,
                                    contentColor = if (editType == "command") MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant
                                ),
                                modifier = Modifier.weight(1f),
                                shape = RoundedCornerShape(8.dp)
                            ) {
                                Text("Uygulama/Komut")
                            }
                        }

                        OutlinedTextField(
                            value = editValue,
                            onValueChange = { editValue = it },
                            label = { Text(if (editType == "hotkey") "Tuş Kombinasyonu (Örn. ctrl+c)" else "Uygulama / Komut Yolu") },
                            placeholder = { Text(if (editType == "hotkey") "ctrl+shift+esc veya volumeup" else "notepad.exe veya cmd.exe") },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )

                        if (editType == "command") {
                            Text("Hazır PC Uygulamaları", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MaterialTheme.colorScheme.primary)
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
                                            .clip(RoundedCornerShape(8.dp))
                                            .background(MaterialTheme.colorScheme.surfaceVariant)
                                            .border(1.dp, MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f), RoundedCornerShape(8.dp))
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
                            Text("Bilgisayardaki Uygulamalar", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MaterialTheme.colorScheme.primary)
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
                                    Text("Uygulama bulunamadı.", fontSize = 11.sp, color = Color.Gray, modifier = Modifier.padding(vertical = 4.dp))
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
                                        Divider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.05f))
                                    }
                                }
                            }
                        } else {
                            Text("Hazır Kısayollar", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MaterialTheme.colorScheme.primary)
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
                                            .clip(RoundedCornerShape(8.dp))
                                            .background(MaterialTheme.colorScheme.surfaceVariant)
                                            .border(1.dp, MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f), RoundedCornerShape(8.dp))
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
                                        .clip(RoundedCornerShape(8.dp))
                                        .background(if (editIcon == iconName) MaterialTheme.colorScheme.primary.copy(alpha = 0.2f) else MaterialTheme.colorScheme.surfaceVariant)
                                        .border(
                                            1.dp,
                                            if (editIcon == iconName) MaterialTheme.colorScheme.primary else Color.Transparent,
                                            RoundedCornerShape(8.dp)
                                        )
                                        .clickable { editIcon = iconName }
                                        .padding(8.dp),
                                    contentAlignment = Alignment.Center
                                ) {
                                    Icon(
                                        imageVector = getVectorIcon(iconName),
                                        contentDescription = iconName,
                                        tint = if (editIcon == iconName) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
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
                                containerColor = MaterialTheme.colorScheme.surfaceVariant,
                                contentColor = MaterialTheme.colorScheme.onSurfaceVariant
                            ),
                            modifier = Modifier.fillMaxWidth(),
                            shape = RoundedCornerShape(8.dp)
                        ) {
                            Icon(Icons.Default.Add, contentDescription = "Upload", modifier = Modifier.size(16.dp))
                            Spacer(modifier = Modifier.width(8.dp))
                            Text("Galeriden Özel Görsel Yükle", fontSize = 12.sp)
                        }

                        Divider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f))
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Text("Aktif Simge Önizleme:", fontSize = 13.sp, modifier = Modifier.weight(1f))
                            Box(
                                modifier = Modifier
                                    .size(44.dp)
                                    .clip(RoundedCornerShape(8.dp))
                                    .background(MaterialTheme.colorScheme.surfaceVariant)
                                    .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.15f), RoundedCornerShape(8.dp)),
                                contentAlignment = Alignment.Center
                            ) {
                                if (editIcon.startsWith("custom:") && editIcon.length > 7) {
                                    val iconPath = editIcon.substring(7)
                                    val imageUrl = "http://$ipAddress:8085/$iconPath?password=${Uri.encode(password)}"
                                    AsyncImage(
                                        model = imageUrl,
                                        contentDescription = "Custom Icon",
                                        modifier = Modifier.size(32.dp).clip(RoundedCornerShape(4.dp))
                                    )
                                } else {
                                    Icon(
                                        imageVector = getVectorIcon(editIcon),
                                        contentDescription = "Icon Preview",
                                        tint = MaterialTheme.colorScheme.primary,
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
                                    StreamDeckButtonInfo(currentBtn.id, editLabel, editType, editValue, editIcon)
                                } else {
                                    it
                                }
                            }
                            saveStreamDeckConfig(ipAddress, password, updatedButtons, streamDeckRows, streamDeckCols) {
                                coroutineScope.launch(Dispatchers.Main) {
                                    Toast.makeText(context, "Buton başarıyla güncellendi!", Toast.LENGTH_SHORT).show()
                                    streamDeckButtons = updatedButtons
                                }
                            }
                            showEditDialog = false
                        },
                        shape = RoundedCornerShape(8.dp)
                    ) {
                        Text("Kaydet")
                    }
                },
                dismissButton = {
                    TextButton(onClick = { showEditDialog = false }) {
                        Text("İptal")
                    }
                }
            )
        }
        val configuration = androidx.compose.ui.platform.LocalConfiguration.current
        val isLandscape = configuration.orientation == android.content.res.Configuration.ORIENTATION_LANDSCAPE
        val hideBars = (selectedTab == 1 && isDeckFullscreen) || (selectedTab == 3 && isMonitorFullscreen)

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
                            shape = RoundedCornerShape(24.dp),
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
                                    icon = { Icon(Icons.Default.PlayArrow, contentDescription = "Stream Deck") },
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
                                    icon = { Icon(Icons.Default.Build, contentDescription = "Trackpad") },
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
                                    icon = { Icon(Icons.Default.Share, contentDescription = "Media") },
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
                                    icon = { Icon(Icons.Default.Menu, contentDescription = "Tools") },
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
                    .background(MaterialTheme.colorScheme.background)
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
                        isLandscape = isDeckLandscape,
                        isDeckFullscreen = isDeckFullscreen,
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
                        liveCpu = liveCpu,
                        liveRam = liveRam,
                        masterVolume = currentMasterVolume,
                        onVolumeChange = { lvl ->
                            currentMasterVolume = lvl
                            SocketManager.sendControl(JSONObject().apply {
                                put("type", "set_volume")
                                put("action", "set")
                                put("level", lvl.toDouble())
                            })
                        },
                        ipAddress = ipAddress,
                        password = password,
                        onButtonLongClick = { btn ->
                            editingButton = btn
                            showEditDialog = true
                        },
                        onToggleLandscape = {
                            val newVal = !isDeckLandscape
                            isDeckLandscape = newVal
                            prefs.edit().putBoolean("deck_landscape_v2", newVal).apply()
                        },
                        onToggleFullscreen = {
                            val newVal = !isDeckFullscreen
                            isDeckFullscreen = newVal
                            prefs.edit().putBoolean("deck_fullscreen", newVal).apply()
                        },
                        onExitClick = { selectedTab = 0 }
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
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 8.dp),
            horizontalArrangement = Arrangement.End
        ) {
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

        BigPocketLogo(
            modifier = Modifier.size(72.dp)
        )
        
        Spacer(modifier = Modifier.height(16.dp))
        
        Text(
            text = "BigPocket Mobile",
            fontSize = 24.sp,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground
        )

        Spacer(modifier = Modifier.height(8.dp))

        // Connection Card (Glassy & Ultra-Soft)
        Card(
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(24.dp),
            colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
            border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(20.dp),
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                // Connection Status Chip
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(999.dp))
                        .background(if (isConnected) com.ygt.bigpocket.theme.StatusGreenBg else com.ygt.bigpocket.theme.StatusRedBg)
                        .border(1.dp, if (isConnected) com.ygt.bigpocket.theme.StatusGreenText.copy(alpha = 0.3f) else com.ygt.bigpocket.theme.StatusRedText.copy(alpha = 0.3f), RoundedCornerShape(999.dp))
                        .padding(vertical = 6.dp, horizontal = 16.dp)
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Box(
                            modifier = Modifier
                                .size(8.dp)
                                .clip(RoundedCornerShape(999.dp))
                                .background(if (isConnected) com.ygt.bigpocket.theme.StatusGreenText else com.ygt.bigpocket.theme.StatusRedText)
                        )
                        Text(
                            text = if (isConnected) "PC'YE BAĞLI" else "BAĞLANTI BEKLENİYOR",
                            color = if (isConnected) com.ygt.bigpocket.theme.StatusGreenText else com.ygt.bigpocket.theme.StatusRedText,
                            fontSize = 12.sp,
                            fontWeight = FontWeight.SemiBold
                        )
                    }
                }

                Spacer(modifier = Modifier.height(20.dp))

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
                        shape = RoundedCornerShape(16.dp),
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
                            .background(MinimalistBackground, RoundedCornerShape(16.dp))
                            .border(1.dp, MinimalistBorder, RoundedCornerShape(16.dp))
                    ) {
                        Icon(
                            imageVector = Icons.Default.Share,
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
                    shape = RoundedCornerShape(16.dp),
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
                        contentColor = if (isConnected) StatusRedText else MinimalistBackground
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(52.dp),
                    shape = RoundedCornerShape(16.dp)
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
                        shape = RoundedCornerShape(16.dp),
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
                            containerColor = Color(0xFF0F172A),
                            contentColor = Color(0xFF38BDF8)
                        ),
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(52.dp),
                        shape = RoundedCornerShape(16.dp),
                        border = androidx.compose.foundation.BorderStroke(1.dp, Color(0xFF0284C7).copy(alpha = 0.5f))
                    ) {
                        Icon(Icons.Default.Bolt, contentDescription = "USB 1ms", tint = Color(0xFF38BDF8), modifier = Modifier.size(18.dp))
                        Spacer(modifier = Modifier.width(10.dp))
                        Text("⚡ USB Sıfır Gecikme (127.0.0.1 - 1ms)", fontWeight = FontWeight.Bold, fontSize = 13.sp)
                    }
                }

                Spacer(modifier = Modifier.height(16.dp))

                // Keep Screen Awake Option
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(14.dp))
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
                        colors = SwitchDefaults.colors(
                            checkedThumbColor = MinimalistPrimary,
                            checkedTrackColor = MinimalistSurfaceElevated
                        )
                    )
                }
            }
        }

        Spacer(modifier = Modifier.height(24.dp))

        // USB Tethering Guide Card (Replaces theme switcher)
        Card(
            shape = RoundedCornerShape(8.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.3f)),
            modifier = Modifier
                .fillMaxWidth()
                .border(1.dp, MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f), RoundedCornerShape(8.dp))
        ) {
            Column(modifier = Modifier.padding(16.dp)) {
                Text(
                    text = "Sıfır Gecikmeli Kablolu Bağlantı",
                    fontWeight = FontWeight.Bold,
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.primary
                )
                Spacer(modifier = Modifier.height(6.dp))
                Text(
                    text = "1. Telefonu USB kablosu ile bilgisayara bağlayın.\n" +
                           "2. Telefon Ayarlarından USB Tethering özelliğini aktif edin.\n" +
                           "3. Yukarıdaki IP kutusuna tethering IP'sini girin veya QR okutun.",
                    fontSize = 11.sp,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f),
                    lineHeight = 16.sp
                )
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
        "add" -> Icons.Default.Add
        "delete" -> Icons.Default.Delete
        "edit" -> Icons.Default.Edit
        "close" -> Icons.Default.Close
        else -> Icons.Default.Star
    }
}

@Composable
fun StreamDeckTab(
    isConnected: Boolean,
    isLandscape: Boolean,
    isDeckFullscreen: Boolean,
    buttons: List<StreamDeckButtonInfo>,
    rows: Int,
    cols: Int,
    pages: List<StreamDeckPageInfo>,
    activePage: Int,
    onPageChange: (Int) -> Unit,
    liveCpu: Int,
    liveRam: Int,
    masterVolume: Float,
    onVolumeChange: (Float) -> Unit,
    ipAddress: String,
    password: String,
    onButtonLongClick: (StreamDeckButtonInfo) -> Unit,
    onToggleLandscape: () -> Unit,
    onToggleFullscreen: () -> Unit,
    onExitClick: () -> Unit
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        if (!isConnected) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("Eylemleri tetiklemek için bilgisayara bağlanın.", textAlign = TextAlign.Center)
            }
            return
        }

        // Filter buttons for the currently active page
        val pageButtons = buttons.filter { it.page == activePage }
        val displayButtons = if (pageButtons.isNotEmpty()) {
            pageButtons
        } else {
            // Fallback: take slice corresponding to page
            val startIdx = activePage * (rows * cols)
            val sub = buttons.drop(startIdx).take(rows * cols)
            if (sub.isNotEmpty()) sub else {
                List(rows * cols) { idx ->
                    StreamDeckButtonInfo(idx, "Slot ${idx + 1}", "hotkey", "", "")
                }
            }
        }

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(if (isLandscape) 8.dp else 16.dp)
                .then(if (!isLandscape) Modifier.verticalScroll(rememberScrollState()) else Modifier),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            // Header: Page Selector Tabs & Live Stats
            if (!isLandscape) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(bottom = 6.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = "Stream Deck",
                        fontSize = 20.sp,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground
                    )

                    // Live CPU/RAM badge
                    Box(
                        modifier = Modifier
                            .clip(RoundedCornerShape(12.dp))
                            .background(Color(0xFF1E293B))
                            .border(1.dp, Color(0xFF334155), RoundedCornerShape(12.dp))
                            .padding(horizontal = 10.dp, vertical = 4.dp)
                    ) {
                        Text(
                            text = "⚡ CPU: %$liveCpu | RAM: %$liveRam",
                            fontSize = 11.sp,
                            fontWeight = FontWeight.SemiBold,
                            color = Color(0xFF38BDF8)
                        )
                    }
                }
            }

            // Horizontal Page Pill Tabs
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .padding(bottom = 6.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                pages.forEach { page ->
                    val isSelected = page.id == activePage
                    Box(
                        modifier = Modifier
                            .clip(RoundedCornerShape(20.dp))
                            .background(if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surface)
                            .border(
                                1.dp,
                                if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f),
                                RoundedCornerShape(20.dp)
                            )
                            .clickable { onPageChange(page.id) }
                            .padding(horizontal = 14.dp, vertical = 6.dp)
                    ) {
                        Text(
                            text = page.title,
                            fontSize = 12.sp,
                            fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Normal,
                            color = if (isSelected) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface
                        )
                    }
                }
            }

            // Button Grid
            for (r in 0 until rows) {
                Row(
                    modifier = Modifier
                        .then(if (isLandscape) Modifier.weight(1f) else Modifier.height(108.dp))
                        .fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    for (c in 0 until cols) {
                        val index = r * cols + c
                        val btn = displayButtons.getOrNull(index)
                        if (btn != null) {
                            val isToggle = btn.type == "toggle"
                            val isVolume = btn.type == "volume_slider"
                            val isLive = btn.type == "live_info"

                            Box(
                                modifier = Modifier
                                    .weight(1f)
                                    .fillMaxHeight()
                                    .clip(RoundedCornerShape(14.dp))
                                    .background(
                                        when {
                                            isToggle && btn.state -> Color(0xFF064E3B).copy(alpha = 0.5f)
                                            isVolume -> Color(0xFF1E1B4B).copy(alpha = 0.5f)
                                            isLive -> Color(0xFF1E293B).copy(alpha = 0.6f)
                                            else -> MaterialTheme.colorScheme.surface
                                        }
                                    )
                                    .border(
                                        1.dp,
                                        when {
                                            isToggle && btn.state -> Color(0xFF10B981)
                                            isToggle -> Color(0xFFEF4444).copy(alpha = 0.4f)
                                            isVolume -> Color(0xFF6366F1).copy(alpha = 0.5f)
                                            isLive -> Color(0xFF38BDF8).copy(alpha = 0.4f)
                                            else -> MaterialTheme.colorScheme.primary.copy(alpha = 0.15f)
                                        },
                                        RoundedCornerShape(14.dp)
                                    )
                                    .pointerInput(btn.id, btn.type) {
                                        detectTapGestures(
                                            onTap = {
                                                if (isVolume) {
                                                    // Toggle or click volume
                                                    SocketManager.sendControl(JSONObject().apply {
                                                        put("type", "stream_deck_press")
                                                        put("button_id", btn.id)
                                                    })
                                                } else {
                                                    SocketManager.sendControl(JSONObject().apply {
                                                        put("type", "stream_deck_press")
                                                        put("button_id", btn.id)
                                                    })
                                                }
                                            },
                                            onLongPress = {
                                                onButtonLongClick(btn)
                                            }
                                        )
                                    },
                                contentAlignment = Alignment.Center
                            ) {
                                // LED indicator for Toggle buttons
                                if (isToggle) {
                                    Box(
                                        modifier = Modifier
                                            .align(Alignment.TopEnd)
                                            .padding(6.dp)
                                            .size(8.dp)
                                            .clip(RoundedCornerShape(4.dp))
                                            .background(if (btn.state) Color(0xFF10B981) else Color(0xFFEF4444))
                                    )
                                }

                                Column(
                                    horizontalAlignment = Alignment.CenterHorizontally,
                                    verticalArrangement = Arrangement.Center,
                                    modifier = Modifier.padding(horizontal = 4.dp, vertical = 6.dp)
                                ) {
                                    val iconSize = if (isLandscape) {
                                        when (rows) {
                                            1 -> 38.dp
                                            2 -> 28.dp
                                            3 -> 22.dp
                                            else -> 18.dp
                                        }
                                    } else {
                                        28.dp
                                    }

                                    // Content based on button type
                                    if (isVolume) {
                                        // Volume Slider / Step Widget
                                        Icon(
                                            imageVector = Icons.Default.VolumeUp,
                                            contentDescription = "Volume",
                                            tint = Color(0xFF818CF8),
                                            modifier = Modifier.size(iconSize)
                                        )
                                        Spacer(modifier = Modifier.height(2.dp))
                                        Text(
                                            text = btn.label,
                                            fontSize = 11.sp,
                                            fontWeight = FontWeight.Bold,
                                            color = MaterialTheme.colorScheme.onSurface,
                                            maxLines = 1
                                        )
                                        Spacer(modifier = Modifier.height(2.dp))
                                        Row(
                                            verticalAlignment = Alignment.CenterVertically,
                                            horizontalArrangement = Arrangement.spacedBy(4.dp)
                                        ) {
                                            Box(
                                                modifier = Modifier
                                                    .size(22.dp)
                                                    .clip(RoundedCornerShape(6.dp))
                                                    .background(Color(0xFF312E81))
                                                    .clickable {
                                                        SocketManager.sendControl(JSONObject().apply {
                                                            put("type", "set_volume")
                                                            put("action", "down")
                                                        })
                                                    },
                                                contentAlignment = Alignment.Center
                                            ) {
                                                Text("-", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                                            }
                                            Text(
                                                text = "${masterVolume.toInt()}%",
                                                fontSize = 11.sp,
                                                fontWeight = FontWeight.Bold,
                                                color = Color(0xFFA5B4FC)
                                            )
                                            Box(
                                                modifier = Modifier
                                                    .size(22.dp)
                                                    .clip(RoundedCornerShape(6.dp))
                                                    .background(Color(0xFF312E81))
                                                    .clickable {
                                                        SocketManager.sendControl(JSONObject().apply {
                                                            put("type", "set_volume")
                                                            put("action", "up")
                                                        })
                                                    },
                                                contentAlignment = Alignment.Center
                                            ) {
                                                Text("+", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                                            }
                                        }
                                    } else if (isLive) {
                                        // Live Performance Stats Widget
                                        val isCpuMetric = btn.value.contains("cpu")
                                        val metricVal = if (isCpuMetric) liveCpu else liveRam
                                        val metricColor = if (metricVal > 80) Color(0xFFEF4444) else if (metricVal > 50) Color(0xFFF59E0B) else Color(0xFF10B981)

                                        Icon(
                                            imageVector = if (isCpuMetric) Icons.Default.Memory else Icons.Default.Storage,
                                            contentDescription = btn.label,
                                            tint = metricColor,
                                            modifier = Modifier.size(iconSize)
                                        )
                                        Spacer(modifier = Modifier.height(2.dp))
                                        Text(
                                            text = btn.label,
                                            fontSize = 11.sp,
                                            fontWeight = FontWeight.SemiBold,
                                            color = MaterialTheme.colorScheme.onSurface,
                                            maxLines = 1
                                        )
                                        Text(
                                            text = "%$metricVal",
                                            fontSize = 13.sp,
                                            fontWeight = FontWeight.ExtraBold,
                                            color = metricColor
                                        )
                                    } else {
                                        // Standard Hotkey, Command, or Toggle Button
                                        if (btn.icon.startsWith("custom:") && btn.icon.length > 7) {
                                            val iconPath = btn.icon.substring(7)
                                            val imageUrl = "http://$ipAddress:8085/$iconPath?password=${Uri.encode(password)}"
                                            AsyncImage(
                                                model = imageUrl,
                                                contentDescription = btn.label,
                                                modifier = Modifier
                                                    .size(iconSize)
                                                    .clip(RoundedCornerShape(4.dp))
                                            )
                                        } else {
                                            Icon(
                                                imageVector = getVectorIcon(btn.icon),
                                                contentDescription = btn.label,
                                                tint = when {
                                                    isToggle && btn.state -> Color(0xFF10B981)
                                                    isToggle -> Color(0xFFEF4444)
                                                    else -> MaterialTheme.colorScheme.primary
                                                },
                                                modifier = Modifier.size(iconSize)
                                            )
                                        }
                                        Spacer(modifier = Modifier.height(4.dp))
                                        Text(
                                            text = btn.label,
                                            fontSize = if (isLandscape) {
                                                when (rows) {
                                                    1 -> 13.sp
                                                    2 -> 11.sp
                                                    else -> 10.sp
                                                }
                                            } else {
                                                12.sp
                                            },
                                            fontWeight = FontWeight.Bold,
                                            color = MaterialTheme.colorScheme.onSurface,
                                            textAlign = TextAlign.Center,
                                            maxLines = 1
                                        )
                                        if (isToggle) {
                                            Text(
                                                text = if (btn.state) "AÇIK" else "KAPALI",
                                                fontSize = 9.sp,
                                                fontWeight = FontWeight.Bold,
                                                color = if (btn.state) Color(0xFF10B981) else Color(0xFF94A3B8)
                                            )
                                        }
                                    }
                                }
                            }
                        } else {
                            Box(modifier = Modifier.weight(1f).fillMaxHeight())
                        }
                    }
                }
            }
        }

        // Floating menu overlay for Deck Tab controls
        Row(
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(if (isLandscape) 8.dp else 16.dp)
                .background(Color(0xCC131316), RoundedCornerShape(24.dp))
                .border(1.dp, Color(0x22FFFFFF), RoundedCornerShape(24.dp))
                .padding(horizontal = 6.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            // Fullscreen toggle button
            IconButton(
                onClick = onToggleFullscreen,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = if (isDeckFullscreen) Icons.Default.FullscreenExit else Icons.Default.Fullscreen,
                    contentDescription = "Tam Ekran",
                    tint = if (isDeckFullscreen) Color(0xFF94D82D) else Color.White,
                    modifier = Modifier.size(20.dp)
                )
            }

            // Landscape/Orientation toggle button
            IconButton(
                onClick = onToggleLandscape,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = Icons.Default.ScreenRotation,
                    contentDescription = "Yön Değiştir",
                    tint = if (isLandscape) Color(0xFF74C0FC) else Color.White,
                    modifier = Modifier.size(20.dp)
                )
            }
            
            // Exit Tab button
            IconButton(
                onClick = onExitClick,
                modifier = Modifier.size(36.dp)
            ) {
                Icon(
                    imageVector = Icons.Default.Home,
                    contentDescription = "Çıkış",
                    tint = Color.White,
                    modifier = Modifier.size(20.dp)
                )
            }
        }
    }
}

@Composable
fun TrackpadTab(isConnected: Boolean) {
    val context = LocalContext.current
    var textInput by remember { mutableStateOf("  ") }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Text(
            text = "Sanal Mouse & Klavye",
            fontSize = 20.sp,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.padding(bottom = 8.dp)
        )

        if (!isConnected) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("Trackpad'i kullanmak için bilgisayara bağlanın.", textAlign = TextAlign.Center)
            }
            return
        }

        // Trackpad area
        Box(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f))
                .border(1.dp, MaterialTheme.colorScheme.onSurface.copy(alpha = 0.1f), RoundedCornerShape(8.dp))
                .pointerInput(Unit) {
                    awaitEachGesture {
                        val down = awaitFirstDown(requireUnconsumed = false)
                        var isTwoFingerMode = false
                        var lastTwoFingerY = 0f
                        var hasMoved = false

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
                                    if (Math.abs(dy) > 2f) {
                                        val scrollAmount = (dy * 0.8f).toInt()
                                        if (scrollAmount != 0) {
                                            SocketManager.sendControl(JSONObject().apply {
                                                put("type", "mouse_scroll")
                                                put("dy", scrollAmount)
                                            })
                                        }
                                    }
                                }
                                lastTwoFingerY = currentY
                                changes.forEach { it.consume() }
                            } else if (pointersCount == 1) {
                                lastTwoFingerY = 0f
                                val change = changes.first { it.pressed }
                                if (!isTwoFingerMode) {
                                    val dragAmount = change.position - change.previousPosition
                                    if (dragAmount.x != 0f || dragAmount.y != 0f) {
                                        hasMoved = true
                                        SocketManager.sendControl(JSONObject().apply {
                                            put("type", "mouse_move")
                                            put("dx", dragAmount.x.toInt())
                                            put("dy", dragAmount.y.toInt())
                                        })
                                    }
                                }
                                change.consume()
                            }

                            if (changes.all { !it.pressed }) {
                                if (!hasMoved) {
                                    if (isTwoFingerMode) {
                                        SocketManager.sendControl(JSONObject().apply {
                                            put("type", "mouse_click")
                                            put("button", "right")
                                        })
                                    } else {
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
                text = "DOKUNMATİK ALAN\n\nTek Tık: Sol Click\nÇift Parmak Tık: Sağ Click\nSürükleme: İmleç Hareketi\nÇift Parmak Sürükleme: Dikey Kaydırma",
                textAlign = TextAlign.Center,
                fontSize = 14.sp,
                color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f)
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
                Icon(Icons.Default.PlayArrow, contentDescription = "Sesli Yaz", tint = StatusGreenText, modifier = Modifier.size(20.dp))
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
                shape = RoundedCornerShape(14.dp),
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
                shape = RoundedCornerShape(14.dp),
                colors = ButtonDefaults.buttonColors(containerColor = MinimalistPrimary, contentColor = MinimalistBackground),
                modifier = Modifier.height(54.dp)
            ) {
                Text(if (rawTypedText.isNotEmpty()) "Gönder" else "Enter", fontWeight = FontWeight.Bold, fontSize = 13.sp)
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
            .background(Color.Black)
    ) {
        if (!isConnected) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("İkinci ekranı görmek için bilgisayara bağlanın.", textAlign = TextAlign.Center, color = Color.White)
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
                    CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
                    Spacer(modifier = Modifier.height(12.dp))
                    Text(
                        text = "Ekran yayını bekleniyor...",
                        color = Color.White.copy(alpha = 0.5f)
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
                shape = RoundedCornerShape(20.dp),
                color = Color(0xDD121217),
                border = androidx.compose.foundation.BorderStroke(1.dp, Color(0x33FFFFFF)),
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
                            selectedContainerColor = Color(0xFF3B82F6),
                            selectedLabelColor = Color.White,
                            containerColor = Color(0x22FFFFFF),
                            labelColor = Color(0xFFCCCCCC)
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
                            tint = Color.White,
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
                            tint = Color.White,
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
                            tint = Color.White,
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
                            tint = if (fitInsideMode) Color(0xFF10B981) else Color(0xFFF59E0B),
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
                .background(Color(0xCC131316), RoundedCornerShape(24.dp))
                .border(1.dp, Color(0x22FFFFFF), RoundedCornerShape(24.dp))
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
                    tint = if (isControlSuspended) Color(0xFFFF8787) else Color(0xFF94D82D),
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
                    tint = if (isMonitorFullscreen) Color(0xFF94D82D) else Color.White,
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
                    tint = if (isLandscapeMode) Color(0xFF74C0FC) else Color.White,
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
                    tint = Color.White,
                    modifier = Modifier.size(20.dp)
                )
            }
        }

        // Virtual Screen Keyboard Input Dialog
        if (showKeyboardDialog) {
            var monitorTypedText by remember { mutableStateOf("") }
            AlertDialog(
                onDismissRequest = { showKeyboardDialog = false },
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
            color = Color(0xFF0E0E12),
            size = size,
            cornerRadius = androidx.compose.ui.geometry.CornerRadius(w * 0.22f, h * 0.22f)
        )
        drawRoundRect(
            color = Color(0xFF2A2A34),
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
            color = Color(0xFF818CF8), // Electric Indigo
            topLeft = Offset(phoneLeft, phoneTop),
            size = androidx.compose.ui.geometry.Size(phoneWidth, phoneHeight),
            cornerRadius = phoneRadius
        )
        drawRoundRect(
            color = Color.White,
            topLeft = Offset(phoneLeft, phoneTop),
            size = androidx.compose.ui.geometry.Size(phoneWidth, phoneHeight),
            cornerRadius = phoneRadius,
            style = androidx.compose.ui.graphics.drawscope.Stroke(width = 2.dp.toPx())
        )
        
        // Phone speaker notch
        drawRoundRect(
            color = Color.White.copy(alpha = 0.9f),
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
        drawPath(path = pocketPath, color = Color(0xFF181820))
        // Pocket stroke
        drawPath(
            path = pocketPath,
            color = Color.White,
            style = androidx.compose.ui.graphics.drawscope.Stroke(
                width = 3.5.dp.toPx(),
                join = androidx.compose.ui.graphics.StrokeJoin.Round
            )
        )

        // 4. Top Pocket Rim (White Bar)
        drawLine(
            color = Color.White,
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
            color = Color(0xFFC084FC),
            style = androidx.compose.ui.graphics.drawscope.Stroke(
                width = 2.5.dp.toPx(),
                cap = androidx.compose.ui.graphics.StrokeCap.Round
            )
        )

        // 6. Center Wireless Connection Dot
        drawCircle(
            color = Color.White,
            radius = 3.dp.toPx(),
            center = Offset(w * 0.50f, h * 0.57f)
        )
    }
}

@Composable
fun MediaTab(
    isConnected: Boolean,
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
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(
            text = "Kamera & Mikrofon Yayını",
            fontSize = 20.sp,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
            modifier = Modifier.padding(bottom = 32.dp)
        )

        if (!isConnected) {
            Text("Ses ve görüntü aktarımı için bilgisayara bağlanın.", textAlign = TextAlign.Center)
            return
        }

        // Microphone Card
        Card(
            shape = RoundedCornerShape(8.dp),
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
                        imageVector = Icons.Default.Info,
                        contentDescription = "Mic",
                        tint = if (isMicStreaming) MaterialTheme.colorScheme.primary else Color.Gray,
                        modifier = Modifier.size(32.dp)
                    )
                    Spacer(modifier = Modifier.width(16.dp))
                    Column {
                        Text("Mikrofon Aktarımı", fontWeight = FontWeight.Bold, fontSize = 16.sp)
                        Text(
                            text = if (isMicStreaming) "Ses PC'ye iletiliyor..." else "Kapalı",
                            fontSize = 12.sp,
                            color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                        )
                    }
                }
                Switch(
                    checked = isMicStreaming,
                    onCheckedChange = onMicToggle
                )
            }
        }

        Spacer(modifier = Modifier.height(24.dp))

        // Camera Card
        Card(
            shape = RoundedCornerShape(8.dp),
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
                            imageVector = Icons.Default.Share,
                            contentDescription = "Webcam",
                            tint = if (isCamStreaming) MaterialTheme.colorScheme.primary else Color.Gray,
                            modifier = Modifier.size(32.dp)
                        )
                        Spacer(modifier = Modifier.width(16.dp))
                        Column {
                            Text("Kamera Yayını (Webcam)", fontWeight = FontWeight.Bold, fontSize = 16.sp)
                            Text(
                                text = if (isCamStreaming) "Görüntü PC'ye aktarılıyor..." else "Kapalı",
                                fontSize = 12.sp,
                                color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                            )
                        }
                    }
                    Switch(
                        checked = isCamStreaming,
                        onCheckedChange = onCamToggle
                    )
                }
                
                Spacer(modifier = Modifier.height(16.dp))
                Divider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f))
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
                            color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                        )
                    }
                    Button(
                        onClick = { onCameraSelect(!useFrontCamera) },
                        colors = ButtonDefaults.buttonColors(
                            containerColor = MaterialTheme.colorScheme.surfaceVariant,
                            contentColor = MaterialTheme.colorScheme.onSurfaceVariant
                        ),
                        shape = RoundedCornerShape(8.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.Refresh,
                            contentDescription = "Switch Camera",
                            modifier = Modifier.size(16.dp)
                        )
                        Spacer(modifier = Modifier.width(8.dp))
                        Text("Kamerayı Değiştir", fontSize = 12.sp)
                    }
                }

                Spacer(modifier = Modifier.height(16.dp))
                Divider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f))
                Spacer(modifier = Modifier.height(16.dp))

                // FPS Selection
                Column(modifier = Modifier.fillMaxWidth()) {
                    Text("Kare Hızı (FPS)", fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                    Text("Düşük gecikme ve akıcılık seçimi", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
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
                                    selectedContainerColor = MaterialTheme.colorScheme.primary,
                                    selectedLabelColor = MaterialTheme.colorScheme.onPrimary
                                )
                            )
                        }
                    }
                }

                Spacer(modifier = Modifier.height(16.dp))
                Divider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f))
                Spacer(modifier = Modifier.height(16.dp))

                // Quality & Resolution Selection
                Column(modifier = Modifier.fillMaxWidth()) {
                    Text("Çözünürlük & Kalite", fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                    Text("USB 1ms modunda 1080p önerilir", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
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
                                    selectedContainerColor = MaterialTheme.colorScheme.primary,
                                    selectedLabelColor = MaterialTheme.colorScheme.onPrimary
                                )
                            )
                        }
                    }
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
    
    var localTextToSend by remember { mutableStateOf("") }
    var sharedFilesList by remember { mutableStateOf<List<SharedFileInfo>>(emptyList()) }
    var isRefreshingFiles by remember { mutableStateOf(false) }

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

    // Load files initially
    LaunchedEffect(isConnected, ipAddress) {
        refreshFiles()
    }

    // Refresh files when upload status turns to success
    LaunchedEffect(uploadStatus) {
        if (uploadStatus.contains("başarıyla") || uploadStatus.contains("Uploaded")) {
            refreshFiles()
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(rememberScrollState()),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Text(
            text = "Pano & Dosya Transferi",
            fontSize = 20.sp,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
            modifier = Modifier.padding(vertical = 4.dp)
        )

        // Settings Entry Card
        Card(
            shape = RoundedCornerShape(16.dp),
            modifier = Modifier
                .fillMaxWidth()
                .clickable { onOpenSettings() },
            colors = CardDefaults.cardColors(containerColor = MinimalistSurfaceElevated),
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
                            .clip(RoundedCornerShape(10.dp))
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
            shape = RoundedCornerShape(12.dp),
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
        ) {
            Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Pano Paylaşımı (Metin)", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MaterialTheme.colorScheme.primary)
                
                // Show current PC clipboard
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(8.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f))
                        .border(1.dp, MaterialTheme.colorScheme.onSurface.copy(alpha = 0.05f), RoundedCornerShape(8.dp))
                        .padding(12.dp)
                ) {
                    Column {
                        Text("Son Senkronize Edilen Metin:", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
                        Spacer(modifier = Modifier.height(4.dp))
                        Text(
                            text = if (clipboardText.isNotEmpty()) clipboardText else "(Pano boş veya henüz senkronize edilmedi)",
                            fontSize = 13.sp,
                            color = MaterialTheme.colorScheme.onSurface,
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
                                shape = RoundedCornerShape(6.dp),
                                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                                colors = ButtonDefaults.buttonColors(
                                    containerColor = MaterialTheme.colorScheme.surfaceVariant,
                                    contentColor = MaterialTheme.colorScheme.onSurfaceVariant
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
                    shape = RoundedCornerShape(8.dp)
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
                        shape = RoundedCornerShape(8.dp),
                        modifier = Modifier.weight(1f),
                        enabled = localTextToSend.isNotEmpty()
                    ) {
                        Text("PC Pano Güncelle", fontSize = 12.sp)
                    }

                    Button(
                        onClick = onSendClipboard,
                        shape = RoundedCornerShape(8.dp),
                        colors = ButtonDefaults.buttonColors(
                            containerColor = MaterialTheme.colorScheme.surfaceVariant,
                            contentColor = MaterialTheme.colorScheme.onSurfaceVariant
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
            shape = RoundedCornerShape(12.dp),
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
        ) {
            Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Dosya & Görsel Gönder", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MaterialTheme.colorScheme.primary)
                Text(
                    text = "Fotoğraf, video veya dokümanları bilgisayara doğrudan yükleyin.",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                )
                
                if (uploadStatus.isNotEmpty()) {
                    Text(
                        text = uploadStatus,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.primary,
                        fontSize = 13.sp
                    )
                }

                Button(
                    onClick = onSelectFile,
                    shape = RoundedCornerShape(8.dp),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Telefondan Dosya Seç ve Gönder")
                }
            }
        }

        // --- FILES LIST SECTION ---
        Card(
            shape = RoundedCornerShape(12.dp),
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
        ) {
            Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text("Bilgisayardaki Dosyalar", fontWeight = FontWeight.Bold, fontSize = 15.sp, color = MaterialTheme.colorScheme.primary)
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
                        color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f),
                        modifier = Modifier.padding(vertical = 8.dp)
                    )
                } else {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        for (file in sharedFilesList) {
                            val ext = file.name.substringAfterLast(".", "").lowercase()
                            val (badgeColor, typeIcon) = when (ext) {
                                "mp4", "mkv", "avi", "mov" -> StatusBlueText to Icons.Default.Share
                                "mp3", "wav", "flac" -> MinimalistAccent to Icons.Default.Info
                                "jpg", "jpeg", "png", "gif", "webp" -> StatusGreenText to Icons.Default.Refresh
                                else -> MinimalistSecondary to Icons.Default.Menu
                            }

                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .clip(RoundedCornerShape(14.dp))
                                    .background(MinimalistSurfaceElevated)
                                    .border(1.dp, MinimalistBorder, RoundedCornerShape(14.dp))
                                    .padding(vertical = 10.dp, horizontal = 14.dp),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Box(
                                    modifier = Modifier
                                        .size(36.dp)
                                        .clip(RoundedCornerShape(10.dp))
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
                                            imageVector = Icons.Default.Share,
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

// ----------------- Helpers -----------------

// CameraX capture logic
private val cameraExecutor = Executors.newSingleThreadExecutor()

private fun startCameraAnalysis(
    context: Context,
    lifecycleOwner: androidx.lifecycle.LifecycleOwner,
    useFrontCamera: Boolean,
    fps: Int = 30,
    quality: String = "720p HD",
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
            cameraProvider.bindToLifecycle(
                lifecycleOwner,
                cameraSelector,
                imageAnalysis
            )
        } catch (e: Exception) {
            Log.e("CameraStream", "Use case binding failed", e)
        }
    }, ContextCompat.getMainExecutor(context))
}

// ImageProxy to JPEG Conversion NV21 (with robust stride handling and rotation/mirroring)
private fun ImageProxy.toJpegBytes(isFrontCamera: Boolean, qualityPreset: String = "720p HD"): ByteArray? {
    try {
        val yPlane = planes[0]
        val uPlane = planes[1]
        val vPlane = planes[2]

        val yBuffer = yPlane.buffer
        val uBuffer = uPlane.buffer
        val vBuffer = vPlane.buffer

        yBuffer.rewind()
        uBuffer.rewind()
        vBuffer.rewind()

        val ySize = yBuffer.remaining()
        val nv21 = ByteArray(width * height * 3 / 2)

        // Copy Y data
        var pos = 0
        val yRowStride = yPlane.rowStride
        val yPixelStride = yPlane.pixelStride
        if (yPixelStride == 1 && yRowStride == width) {
            yBuffer.get(nv21, 0, ySize)
            pos = ySize
        } else {
            val rowBuffer = ByteArray(yRowStride)
            for (row in 0 until height) {
                yBuffer.position(row * yRowStride)
                yBuffer.get(rowBuffer, 0, yRowStride)
                for (col in 0 until width) {
                    nv21[pos++] = rowBuffer[col * yPixelStride]
                }
            }
        }

        // Copy VU data (interleaved)
        val uRowStride = uPlane.rowStride
        val uPixelStride = uPlane.pixelStride
        val vRowStride = vPlane.rowStride
        val vPixelStride = vPlane.pixelStride

        val uvWidth = width / 2
        val uvHeight = height / 2

        for (row in 0 until uvHeight) {
            for (col in 0 until uvWidth) {
                vBuffer.position(row * vRowStride + col * vPixelStride)
                nv21[pos++] = vBuffer.get()
                uBuffer.position(row * uRowStride + col * uPixelStride)
                nv21[pos++] = uBuffer.get()
            }
        }

        val compressQuality = when (qualityPreset) {
            "480p SD" -> 50
            "1080p FHD" -> 85
            else -> 70
        }

        val yuvImage = YuvImage(nv21, ImageFormat.NV21, this.width, this.height, null)
        val out = ByteArrayOutputStream()
        yuvImage.compressToJpeg(Rect(0, 0, this.width, this.height), compressQuality, out)
        val rawBytes = out.toByteArray()

        val rotation = this.imageInfo.rotationDegrees
        if (rotation != 0 || isFrontCamera) {
            val bitmap = android.graphics.BitmapFactory.decodeByteArray(rawBytes, 0, rawBytes.size)
            val matrix = android.graphics.Matrix()
            
            if (rotation != 0) {
                matrix.postRotate(rotation.toFloat())
            }
            if (isFrontCamera) {
                // Front camera needs horizontal flip to mirror correctly
                matrix.postScale(-1f, 1f)
            }

            val rotatedBitmap = android.graphics.Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, matrix, true)
            val rotatedOut = ByteArrayOutputStream()
            rotatedBitmap.compress(android.graphics.Bitmap.CompressFormat.JPEG, compressQuality, rotatedOut)
            bitmap.recycle()
            rotatedBitmap.recycle()
            return rotatedOut.toByteArray()
        }
        return rawBytes
    } catch (e: Exception) {
        Log.e("CameraStream", "Error converting frame to JPEG", e)
        return null
    }
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
        shape = RoundedCornerShape(8.dp),
        title = { Text("QR Kodu Taratın") },
        text = {
            Box(
                modifier = Modifier
                    .size(280.dp)
                    .clip(RoundedCornerShape(8.dp))
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
                shape = RoundedCornerShape(8.dp)
            ) {
                Text("Kapat")
            }
        }
    )
}

// File sharing upload utilizing OkHttp
private fun uploadFileToServer(context: Context, fileUri: Uri, ip: String, password: String): Boolean {
    val client = OkHttpClient()
    try {
        val contentResolver = context.contentResolver
        val cursor = contentResolver.query(fileUri, null, null, null, null)
        var displayName = "upload_file"
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
            .addFormDataPart(
                "file",
                displayName,
                fileBytes.toRequestBody("application/octet-stream".toMediaTypeOrNull(), 0, fileBytes.size)
            )
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

