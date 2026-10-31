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

    data class ParsedVersion(val nums: List<Int>, val pre: String)

    fun parseSemVer(v: String): ParsedVersion {
        val clean = v.trim().removePrefix("v")
        val parts = clean.split("-", limit = 2)
        val numParts = parts[0].split(".").map { it.toIntOrNull() ?: 0 }
        val nums = (numParts + listOf(0, 0, 0)).take(3)
        val pre = if (parts.size > 1) parts[1] else ""
        return ParsedVersion(nums, pre)
    }

    fun compareVersions(a: String, b: String): Int {
        val pa = parseSemVer(a)
        val pb = parseSemVer(b)
        for (i in 0 until 3) {
            if (pa.nums[i] > pb.nums[i]) return 1
            if (pa.nums[i] < pb.nums[i]) return -1
        }
        if (pa.pre.isEmpty() && pb.pre.isNotEmpty()) return 1
        if (pa.pre.isNotEmpty() && pb.pre.isEmpty()) return -1
        if (pa.pre.isEmpty() && pb.pre.isEmpty()) return 0
        return pa.pre.compareTo(pb.pre)
    }

    private fun findApkAsset(assets: org.json.JSONArray?): Pair<String, Long>? {
        if (assets == null) return null
        for (i in 0 until assets.length()) {
            val a = assets.getJSONObject(i)
            if (a.optString("name").endsWith(".apk", ignoreCase = true)) {
                return a.optString("browser_download_url") to a.optLong("size")
            }
        }
        return null
    }

    sealed class CheckResult {
        data class UpdateAvailable(val info: UpdateInfo) : CheckResult()
        data class UpToDate(val currentVersion: String) : CheckResult()
        data class Error(val message: String) : CheckResult()
    }

    /** Detailed check for manual button in Settings */
    suspend fun checkDetailed(context: Context): CheckResult = withContext(Dispatchers.IO) {
        try {
            val current = currentVersion(context)
            val isBeta = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean("beta_updates", false) || current.contains("beta", ignoreCase = true)
            val targetUrl = "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=10"

            val req = Request.Builder()
                .url(targetUrl)
                .header("Accept", "application/vnd.github+json")
                .header("User-Agent", "BigPocket-Android")
                .build()

            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext CheckResult.Error("GitHub API HTTP ${resp.code}")
                val bodyStr = resp.body?.string() ?: return@withContext CheckResult.Error("Boş yanıt")

                val releases = mutableListOf<JSONObject>()
                if (bodyStr.trim().startsWith("[")) {
                    val arr = org.json.JSONArray(bodyStr)
                    for (i in 0 until arr.length()) {
                        val r = arr.getJSONObject(i)
                        if (isBeta || !r.optBoolean("prerelease", false)) {
                            releases.add(r)
                        }
                    }
                } else {
                    releases.add(JSONObject(bodyStr))
                }

                var bestRelease: JSONObject? = null
                for (rel in releases) {
                    val tag = rel.optString("tag_name").removePrefix("v")
                    if (bestRelease == null || compareVersions(tag, bestRelease.optString("tag_name").removePrefix("v")) > 0) {
                        bestRelease = rel
                    }
                }

                if (bestRelease == null) return@withContext CheckResult.UpToDate(current)

                val latestTag = bestRelease.optString("tag_name").removePrefix("v")
                val assetInfo = findApkAsset(bestRelease.optJSONArray("assets"))

                if (compareVersions(latestTag, current) > 0 && assetInfo != null) {
                    val info = UpdateInfo(
                        currentVersion = current,
                        latestVersion = latestTag,
                        title = bestRelease.optString("name").ifBlank { "BigPocket $latestTag" },
                        notes = bestRelease.optString("body"),
                        apkUrl = assetInfo.first,
                        apkSize = assetInfo.second,
                        htmlUrl = bestRelease.optString("html_url")
                    )
                    CheckResult.UpdateAvailable(info)
                } else {
                    CheckResult.UpToDate(current)
                }
            }
        } catch (e: Exception) {
            CheckResult.Error(e.message ?: "Bağlantı kurulamadı")
        }
    }

    /** Returns update info when a newer release with an APK exists, otherwise null. */
    suspend fun check(context: Context, ignoreSkipped: Boolean = false): UpdateInfo? = withContext(Dispatchers.IO) {
        try {
            val current = currentVersion(context)
            val isBeta = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean("beta_updates", false) || current.contains("beta", ignoreCase = true)
            val targetUrl = "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=10"

            val req = Request.Builder()
                .url(targetUrl)
                .header("Accept", "application/vnd.github+json")
                .header("User-Agent", "BigPocket-Android")
                .build()

            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext null
                val bodyStr = resp.body?.string() ?: return@withContext null

                val releases = mutableListOf<JSONObject>()
                if (bodyStr.trim().startsWith("[")) {
                    val arr = org.json.JSONArray(bodyStr)
                    for (i in 0 until arr.length()) {
                        val r = arr.getJSONObject(i)
                        if (isBeta || !r.optBoolean("prerelease", false)) {
                            releases.add(r)
                        }
                    }
                } else {
                    releases.add(JSONObject(bodyStr))
                }
                var bestRelease: JSONObject? = null
                for (rel in releases) {
                    val tag = rel.optString("tag_name").removePrefix("v")
                    if (bestRelease == null || compareVersions(tag, bestRelease.optString("tag_name").removePrefix("v")) > 0) {
                        bestRelease = rel
                    }
                }

                if (bestRelease == null) return@withContext null

                val latestTag = bestRelease.optString("tag_name").removePrefix("v")
                if (compareVersions(latestTag, current) <= 0) return@withContext null

                if (!ignoreSkipped) {
                    val skipped = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_SKIP, null)
                    if (skipped == latestTag) return@withContext null
                }

                val assetInfo = findApkAsset(bestRelease.optJSONArray("assets")) ?: return@withContext null

                UpdateInfo(
                    currentVersion = current,
                    latestVersion = latestTag,
                    title = bestRelease.optString("name").ifBlank { "BigPocket $latestTag" },
                    notes = bestRelease.optString("body"),
                    apkUrl = assetInfo.first,
                    apkSize = assetInfo.second,
                    htmlUrl = bestRelease.optString("html_url")
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
        try {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !context.packageManager.canRequestPackageInstalls()
        } catch (e: Exception) {
            false
        }

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
