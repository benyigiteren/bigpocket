package com.ygt.bigpocket.update

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * Checks GitHub Releases for a newer version and installs the attached APK.
 *
 * Release workflow: tag "vX.Y.Z", write changelog in release body (Markdown),
 * attach an asset ending with ".apk". Remember to bump versionName in build.gradle.kts.
 */
object UpdateManager {
    /** "owner/repo" of the public repository that publishes releases. */
    const val GITHUB_REPO = "benyigiteren/bigpocket"

    private const val PREFS = "bigpocket_prefs"
    private const val KEY_SKIP = "update_skip_version"

    data class UpdateInfo(
        val currentVersion: String,
        val latestVersion: String,
        val title: String,
        val notes: String,
        val apkUrl: String,
        val apkSize: Long,
        val htmlUrl: String,
    )

    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    fun currentVersion(context: Context): String = try {
        context.packageManager.getPackageInfo(context.packageName, 0).versionName ?: "0"
    } catch (e: Exception) {
        "0"
    }

    fun compareVersions(a: String, b: String): Int {
        val pa = a.trim().removePrefix("v").split(".")
        val pb = b.trim().removePrefix("v").split(".")
        for (i in 0 until maxOf(pa.size, pb.size)) {
            val x = pa.getOrNull(i)?.substringBefore("-")?.toIntOrNull() ?: 0
            val y = pb.getOrNull(i)?.substringBefore("-")?.toIntOrNull() ?: 0
            if (x != y) return x.compareTo(y)
        }
        return 0
    }

    /** Returns update info when a newer release with an APK exists, otherwise null. */
    suspend fun check(context: Context, ignoreSkipped: Boolean = false): UpdateInfo? = withContext(Dispatchers.IO) {
        try {
            val req = Request.Builder()
                .url("https://api.github.com/repos/$GITHUB_REPO/releases/latest")
                .header("Accept", "application/vnd.github+json")
                .header("User-Agent", "BigPocket-Android")
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext null
                val json = JSONObject(resp.body?.string() ?: return@withContext null)
                val latest = json.optString("tag_name").removePrefix("v")
                val current = currentVersion(context)
                if (compareVersions(latest, current) <= 0) return@withContext null

                if (!ignoreSkipped) {
                    val skipped = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_SKIP, null)
                    if (skipped == latest) return@withContext null
                }

                val assets = json.optJSONArray("assets") ?: return@withContext null
                var apkUrl = ""
                var apkSize = 0L
                for (i in 0 until assets.length()) {
                    val a = assets.getJSONObject(i)
                    if (a.optString("name").endsWith(".apk", ignoreCase = true)) {
                        apkUrl = a.optString("browser_download_url")
                        apkSize = a.optLong("size")
                        break
                    }
                }
                if (apkUrl.isEmpty()) return@withContext null

                UpdateInfo(
                    currentVersion = current,
                    latestVersion = latest,
                    title = json.optString("name").ifBlank { "BigPocket $latest" },
                    notes = json.optString("body"),
                    apkUrl = apkUrl,
                    apkSize = apkSize,
                    htmlUrl = json.optString("html_url"),
                )
            }
        } catch (e: Exception) {
            null
        }
    }

    fun skip(context: Context, version: String) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_SKIP, version).apply()
    }

    /** Downloads the APK into cache, reporting progress in 0..1. */
    suspend fun download(context: Context, info: UpdateInfo, onProgress: (Float) -> Unit): File = withContext(Dispatchers.IO) {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val file = File(dir, "bigpocket-${info.latestVersion}.apk")
        val req = Request.Builder().url(info.apkUrl).build()
        client.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) throw IllegalStateException("HTTP ${resp.code}")
            val body = resp.body ?: throw IllegalStateException("Empty body")
            val total = body.contentLength().takeIf { it > 0 } ?: info.apkSize
            body.byteStream().use { input ->
                file.outputStream().use { out ->
                    val buf = ByteArray(64 * 1024)
                    var done = 0L
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        done += n
                        if (total > 0) onProgress(done.toFloat() / total)
                    }
                }
            }
        }
        file
    }

    /** True if the user must first allow "install unknown apps" for BigPocket. */
    fun needsInstallPermission(context: Context): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !context.packageManager.canRequestPackageInstalls()

    fun openInstallPermissionSettings(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startActivity(
                Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            )
        }
    }

    fun install(context: Context, apk: File) {
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.updates", apk)
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        context.startActivity(intent)
    }
}
