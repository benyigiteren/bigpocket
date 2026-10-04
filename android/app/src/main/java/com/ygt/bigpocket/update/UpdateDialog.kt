package com.ygt.bigpocket.update

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.ygt.bigpocket.theme.*
import kotlinx.coroutines.launch
import java.io.File

/** Checks for an update once on launch and shows a dialog with release notes. */
@Composable
fun UpdatePrompt() {
    val context = LocalContext.current
    var info by remember { mutableStateOf<UpdateManager.UpdateInfo?>(null) }
    LaunchedEffect(Unit) { info = UpdateManager.check(context) }
    info?.let { UpdateDialog(it, onDismiss = { info = null }) }
}

@Composable
fun UpdateDialog(info: UpdateManager.UpdateInfo, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var progress by remember { mutableStateOf<Float?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var downloaded by remember { mutableStateOf<File?>(null) }

    fun installNow(file: File) {
        if (UpdateManager.needsInstallPermission(context)) {
            error = "Kurulum için \"Bilinmeyen uygulamaları yükle\" iznini verin, sonra tekrar Kur'a basın."
            UpdateManager.openInstallPermissionSettings(context)
        } else {
            UpdateManager.install(context, file)
        }
    }

    Dialog(onDismissRequest = { if (progress == null) onDismiss() }, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Column(
            modifier = Modifier
                .fillMaxWidth(0.9f)
                .heightIn(max = 560.dp)
                .background(MinimalistSurface, RoundedCornerShape(24.dp))
                .padding(22.dp)
        ) {
            Text(
                "Yeni güncelleme",
                color = MinimalistAccent,
                fontSize = 12.sp,
                modifier = Modifier
                    .background(MinimalistAccent.copy(alpha = 0.12f), RoundedCornerShape(50))
                    .padding(horizontal = 10.dp, vertical = 4.dp)
            )
            Spacer(Modifier.height(12.dp))
            Text(info.title, color = MinimalistPrimary, fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
            Text(
                "v${info.currentVersion} → v${info.latestVersion}",
                color = MinimalistSecondary, fontSize = 13.sp, fontFamily = FontFamily.Monospace
            )
            Spacer(Modifier.height(14.dp))
            Column(
                modifier = Modifier
                    .weight(1f, fill = false)
                    .fillMaxWidth()
                    .background(MinimalistSurfaceElevated, RoundedCornerShape(16.dp))
                    .verticalScroll(rememberScrollState())
                    .padding(14.dp)
            ) {
                ReleaseNotes(info.notes)
            }

            progress?.let {
                Spacer(Modifier.height(14.dp))
                LinearProgressIndicator(
                    progress = { it },
                    modifier = Modifier.fillMaxWidth().height(6.dp),
                    color = MinimalistAccent,
                    trackColor = MinimalistBorder,
                )
                Text("İndiriliyor… %${(it * 100).toInt()}", color = MinimalistSecondary, fontSize = 12.sp, modifier = Modifier.padding(top = 6.dp))
            }
            error?.let {
                Text(it, color = StatusRedText, fontSize = 12.sp, modifier = Modifier.padding(top = 10.dp))
            }

            Spacer(Modifier.height(16.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                TextButton(enabled = progress == null, onClick = { UpdateManager.skip(context, info.latestVersion); onDismiss() }) {
                    Text("Atla", color = MinimalistSecondary)
                }
                TextButton(enabled = progress == null, onClick = onDismiss) {
                    Text("Sonra", color = MinimalistSecondary)
                }
                Spacer(Modifier.width(6.dp))
                Button(
                    enabled = progress == null,
                    shape = RoundedCornerShape(14.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = MinimalistAccent, contentColor = MinimalistBackground),
                    onClick = {
                        downloaded?.let { installNow(it); return@Button }
                        error = null
                        progress = 0f
                        scope.launch {
                            try {
                                val file = UpdateManager.download(context, info) { p -> progress = p }
                                downloaded = file
                                progress = null
                                installNow(file)
                            } catch (e: Exception) {
                                progress = null
                                error = "İndirme başarısız: ${e.message}"
                            }
                        }
                    }
                ) { Text(if (downloaded != null) "Kur" else "Güncelle", fontWeight = FontWeight.SemiBold) }
            }
        }
    }
}

/** Minimal Markdown rendering: headings, bullet lists, **bold** and `code`. */
@Composable
private fun ReleaseNotes(md: String) {
    if (md.isBlank()) {
        Text("Sürüm notu yok.", color = MinimalistSecondary, fontSize = 14.sp)
        return
    }
    md.lines().forEach { raw ->
        val line = raw.trim()
        when {
            line.isEmpty() -> Spacer(Modifier.height(6.dp))
            line.startsWith("#") -> Text(
                line.trimStart('#').trim(), color = MinimalistPrimary, fontWeight = FontWeight.SemiBold,
                fontSize = 15.sp, modifier = Modifier.padding(top = 6.dp, bottom = 2.dp)
            )
            line.startsWith("- ") || line.startsWith("* ") -> Row(Modifier.padding(vertical = 2.dp)) {
                Text("•  ", color = MinimalistAccent, fontSize = 14.sp)
                Text(inlineMd(line.substring(2)), color = MinimalistPrimary, fontSize = 14.sp)
            }
            else -> Text(inlineMd(line), color = MinimalistPrimary, fontSize = 14.sp, modifier = Modifier.padding(vertical = 2.dp))
        }
    }
}

private fun inlineMd(text: String) = buildAnnotatedString {
    val regex = Regex("""\*\*(.+?)\*\*|`([^`]+)`""")
    var last = 0
    regex.findAll(text).forEach { m ->
        append(text.substring(last, m.range.first))
        if (m.groupValues[1].isNotEmpty()) withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(m.groupValues[1]) }
        else withStyle(SpanStyle(fontFamily = FontFamily.Monospace)) { append(m.groupValues[2]) }
        last = m.range.last + 1
    }
    append(text.substring(last))
}
