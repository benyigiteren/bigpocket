package com.ygt.bigpocket.update

import android.content.ActivityNotFoundException
import android.content.ClipData
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
 * Release workflow: tag "vX.Y.Z" — pre-release tags such as "v1.5.2-beta" are
 * picked up when the installed build is itself a pre-release or the user enabled
 * the beta channel — write the changelog in the release body (Markdown), attach
 * the APK as an asset named "*.apk". Remember to bump versionName in
 * build.gradle.kts.
 */
object UpdateManager {
    /** "owner/repo" of the public repository that publishes releases. */
    const val GITHUB_REPO = "benyigiteren/bigpocket"

    private const val PREFS = "bigpocket_prefs"
    private const val KEY_SKIP = "update_skip_version"
    /** Shared with the "Beta Güncellemelerini Al" switch in the settings screen. */
    private const val KEY_BETA = "beta_updates"

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

    /** `1.5.2-beta+build.7` → `[1, 5, 2]` plus the pre-release `beta`. */
    fun parseSemVer(v: String): ParsedVersion {
        val clean = v.trim().removePrefix("v").removePrefix("V")
        // Build metadata never takes part in precedence.
        val withoutBuild = clean.substringBefore("+")
        val parts = withoutBuild.split("-", limit = 2)
        val numParts = parts[0].split(".").map { part ->
            // An over-long component saturates instead of collapsing to 0, which
            // would make 1.5.99999999999999999999 look older than 1.5.2.
            val digits = part.trim().takeWhile { it.isDigit() }
            digits.toLongOrNull()?.coerceAtMost(Int.MAX_VALUE.toLong())?.toInt() ?: 0
        }
        val nums = (numParts + listOf(0, 0, 0)).take(3)
        val pre = if (parts.size > 1) parts[1].trim() else ""
        return ParsedVersion(nums, pre)
    }

    /**
     * SemVer precedence: 1 when [a] is newer, -1 when it is older, 0 when equal.
     *
     * Build metadata is ignored and a release outranks its own pre-releases, so
     * `1.5.2 > 1.5.2-beta` and `1.5.2-beta > 1.5.0-beta`.
     */
    fun compareVersions(a: String, b: String): Int {
        val pa = parseSemVer(a)
        val pb = parseSemVer(b)
        for (i in 0 until 3) {
            if (pa.nums[i] != pb.nums[i]) return if (pa.nums[i] > pb.nums[i]) 1 else -1
        }
        return comparePrerelease(pa.pre, pb.pre)
    }

    /**
     * Pre-release precedence (semver.org spec item 11): an empty pre-release is
     * the higher one, identifiers are compared numerically when both are numeric
     * (`beta.2 < beta.10`) and in ASCII order otherwise, a numeric identifier
     * ranks below an alphanumeric one, and a shorter list that prefixes a longer
     * one ranks lower (`alpha < alpha.1`).
     */
    fun comparePrerelease(a: String, b: String): Int {
        if (a.isEmpty() && b.isEmpty()) return 0
        if (a.isEmpty()) return 1
        if (b.isEmpty()) return -1
        val left = a.split('.')
        val right = b.split('.')
        for (i in 0 until minOf(left.size, right.size)) {
            val x = left[i]
            val y = right[i]
            val nx = x.toLongOrNull()
            val ny = y.toLongOrNull()
            val cmp = when {
                nx != null && ny != null -> nx.compareTo(ny)
                nx != null -> -1
                ny != null -> 1
                else -> x.compareTo(y)
            }
            if (cmp != 0) return if (cmp > 0) 1 else -1
        }
        return when {
            left.size == right.size -> 0
            left.size < right.size -> -1
            else -> 1
        }
    }

    /** The installable APK of a release, or null when it ships none. */
    private fun findApkAsset(assets: org.json.JSONArray?): Pair<String, Long>? {
        if (assets == null) return null
        for (i in 0 until assets.length()) {
            val asset = assets.getJSONObject(i)
            if (!asset.optString("name").endsWith(".apk", ignoreCase = true)) continue
            // A blank URL would be offered as an update and then fail to download.
            val url = asset.optString("browser_download_url")
            if (url.isBlank()) continue
            return url to asset.optLong("size")
        }
        return null
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /**
     * Whether pre-releases may satisfy a check: the user opted into the beta
     * channel, or the installed build is itself a pre-release.
     */
    fun isBetaChannel(context: Context, current: String): Boolean =
        prefs(context).getBoolean(KEY_BETA, false) ||
            current.contains("beta", ignoreCase = true)

    /** One release that can actually be installed on Android. */
    private data class ApkRelease(
        val json: JSONObject,
        val version: String,
        val apkUrl: String,
        val apkSize: Long,
    )

    /** GitHub answered with an error object (rate limit, unknown repo, …). */
    private class GitHubError(message: String) : Exception(message)

    /**
     * The repository's recent releases, filtered to the channel in use. Beta
     * builds scan further back because pre-release and stable tags interleave in
     * the feed, so one page can hide the newest build.
     */
    private fun fetchReleases(isBeta: Boolean): List<JSONObject> {
        val perPage = if (isBeta) 30 else 10
        val request = Request.Builder()
            .url("https://api.github.com/repos/$GITHUB_REPO/releases?per_page=$perPage")
            .header("Accept", "application/vnd.github+json")
            .header("User-Agent", "BigPocket-Android")
            .build()

        val body = client.newCall(request).execute().use { resp ->
            if (!resp.isSuccessful) throw GitHubError("GitHub API HTTP ${resp.code}")
            resp.body?.string() ?: throw GitHubError("Boş yanıt")
        }

        val releases = mutableListOf<JSONObject>()
        if (body.trim().startsWith("[")) {
            val array = org.json.JSONArray(body)
            for (i in 0 until array.length()) {
                val release = array.getJSONObject(i)
                if (release.optBoolean("draft", false)) continue
                if (!isBeta && release.optBoolean("prerelease", false)) continue
                releases.add(release)
            }
        } else {
            // A single release object: `/releases/latest` answers that way, and
            // so does every API error.
            val release = JSONObject(body)
            if (release.has("message") && !release.has("tag_name")) {
                throw GitHubError(release.optString("message"))
            }
            if (isBeta || !release.optBoolean("prerelease", false)) releases.add(release)
        }
        return releases
    }

    /**
     * Highest-versioned release that ships an APK. The asset is resolved *before*
     * the maximum is taken, so a Windows- or macOS-only release can no longer
     * hide the Android update underneath it.
     */
    private fun bestApkRelease(releases: List<JSONObject>): ApkRelease? {
        var best: ApkRelease? = null
        for (release in releases) {
            val tag = release.optString("tag_name").removePrefix("v").removePrefix("V")
            if (tag.isBlank()) continue
            val asset = findApkAsset(release.optJSONArray("assets")) ?: continue
            val current = best
            if (current == null || compareVersions(tag, current.version) > 0) {
                best = ApkRelease(release, tag, asset.first, asset.second)
            }
        }
        return best
    }

    private fun updateInfoFor(current: String, release: ApkRelease): UpdateInfo = UpdateInfo(
        currentVersion = current,
        latestVersion = release.version,
        title = release.json.optString("name").ifBlank { "BigPocket ${release.version}" },
        notes = release.json.optString("body"),
        apkUrl = release.apkUrl,
        apkSize = release.apkSize,
        htmlUrl = release.json.optString("html_url"),
    )

    sealed class CheckResult {
        data class UpdateAvailable(val info: UpdateInfo) : CheckResult()
        data class UpToDate(val currentVersion: String) : CheckResult()
        data class Error(val message: String) : CheckResult()
    }

    /** Detailed check for manual button in Settings */
    suspend fun checkDetailed(context: Context): CheckResult = withContext(Dispatchers.IO) {
        try {
            val current = currentVersion(context)
            val best = bestApkRelease(fetchReleases(isBetaChannel(context, current)))
                ?: return@withContext CheckResult.UpToDate(current)
            if (compareVersions(best.version, current) > 0) {
                CheckResult.UpdateAvailable(updateInfoFor(current, best))
            } else {
                CheckResult.UpToDate(current)
            }
        } catch (e: Exception) {
            CheckResult.Error(e.message ?: "Bağlantı kurulamadı")
        }
    }

    /** Returns update info when a newer release with an APK exists, otherwise null. */
    suspend fun check(context: Context, ignoreSkipped: Boolean = false): UpdateInfo? = withContext(Dispatchers.IO) {
        try {
            val current = currentVersion(context)
            val best = bestApkRelease(fetchReleases(isBetaChannel(context, current)))
                ?: return@withContext null
            if (compareVersions(best.version, current) <= 0) return@withContext null

            if (!ignoreSkipped) {
                val skipped = prefs(context).getString(KEY_SKIP, null)
                // Compared instead of string-matched, so a stored "1.5.2" also
                // skips the same release when it is tagged "v1.5.2".
                if (skipped != null && compareVersions(skipped, best.version) == 0) {
                    return@withContext null
                }
            }

            updateInfoFor(current, best)
        } catch (e: Exception) {
            null
        }
    }

    fun skip(context: Context, version: String) {
        prefs(context).edit().putString(KEY_SKIP, version).apply()
    }

    /** Downloads the APK into cache, reporting progress in 0..1. */
    suspend fun download(context: Context, info: UpdateInfo, onProgress: (Float) -> Unit): File = withContext(Dispatchers.IO) {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        // The version comes from a release tag: keep it a plain file name so it
        // can neither escape the cache folder nor break the FileProvider root.
        val safeVersion = info.latestVersion.replace(Regex("[^A-Za-z0-9._-]"), "_")
        val file = File(dir, "bigpocket-$safeVersion.apk")
        val request = Request.Builder().url(info.apkUrl).build()
        client.newCall(request).execute().use { resp ->
            if (!resp.isSuccessful) throw IllegalStateException("HTTP ${resp.code}")
            val body = resp.body ?: throw IllegalStateException("Empty body")
            // GitHub sends the asset length; fall back to the API-reported size.
            val total = body.contentLength().takeIf { it > 0 } ?: info.apkSize
            try {
                body.byteStream().use { input ->
                    file.outputStream().use { out ->
                        val buffer = ByteArray(64 * 1024)
                        var done = 0L
                        while (true) {
                            val read = input.read(buffer)
                            if (read < 0) break
                            out.write(buffer, 0, read)
                            done += read
                            // Clamped: a progress value above 1 would break the UI bar.
                            if (total > 0) onProgress((done.toFloat() / total).coerceIn(0f, 1f))
                        }
                    }
                }
                // A truncated transfer must not be handed to the installer.
                if (total > 0 && file.length() < total) {
                    throw IllegalStateException("İndirme eksik kaldı (${file.length()}/$total bayt)")
                }
            } catch (e: Exception) {
                file.delete()
                throw e
            }
        }
        // Reach 100% even when the server did not report a length.
        onProgress(1f)
        file
    }

    /** True if the user must first allow "install unknown apps" for BigPocket. */
    fun needsInstallPermission(context: Context): Boolean =
        try {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !context.packageManager.canRequestPackageInstalls()
        } catch (e: Exception) {
            false
        }

    /**
     * Opens the "install unknown apps" screen for this app, falling back to the
     * app's details (and finally the security settings) on ROMs that do not
     * publish the dedicated action.
     */
    fun openInstallPermissionSettings(context: Context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val appUri = Uri.parse("package:${context.packageName}")
        val candidates = listOf(
            Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, appUri),
            Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, appUri),
            Intent(Settings.ACTION_SECURITY_SETTINGS),
        )
        for (intent in candidates) {
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            try {
                context.startActivity(intent)
                return
            } catch (e: ActivityNotFoundException) {
                // Try the next screen.
            }
        }
    }

    /**
     * Starts the package installer for a downloaded APK.
     *
     * @throws IllegalStateException when the file is missing or no installer can
     *   be started, with a message that can be shown to the user: the callers in
     *   the UI report it instead of crashing.
     */
    fun install(context: Context, apk: File) {
        if (!apk.isFile || apk.length() == 0L) {
            throw IllegalStateException("Kurulum dosyası bulunamadı, yeniden indirin.")
        }
        val uri = try {
            FileProvider.getUriForFile(context, "${context.packageName}.updates", apk)
        } catch (e: IllegalArgumentException) {
            throw IllegalStateException("Kurulum dosyası paylaşılamadı: ${e.message}")
        }
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            // The read grant only reaches the installer through the clip data on
            // several OEM builds, where setDataAndType alone is not enough.
            clipData = ClipData.newRawUri(null, uri)
        }
        try {
            context.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            throw IllegalStateException("Kurulum başlatılamadı: cihazda paket yükleyici yok.")
        }
    }
}
