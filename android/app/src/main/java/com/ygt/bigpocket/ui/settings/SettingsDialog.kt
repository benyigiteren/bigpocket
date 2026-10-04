package com.ygt.bigpocket.ui.settings

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.ygt.bigpocket.theme.*
import com.ygt.bigpocket.update.UpdateManager
import kotlinx.coroutines.launch

@Composable
fun SettingsDialog(
    onDismiss: () -> Unit,
    keepScreenOn: Boolean,
    onKeepScreenOnChange: (Boolean) -> Unit,
    onUpdateAvailable: (UpdateManager.UpdateInfo) -> Unit
) {
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()
    val currentVersion = UpdateManager.currentVersion(context)

    var isCheckingUpdate by remember { mutableStateOf(false) }
    var updateCheckStatus by remember { mutableStateOf<String?>(null) }
    var updateCheckIsSuccess by remember { mutableStateOf<Boolean?>(null) }

    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false)
    ) {
        Card(
            modifier = Modifier
                .fillMaxWidth(0.92f)
                .heightIn(max = 620.dp),
            shape = RoundedCornerShape(24.dp),
            colors = CardDefaults.cardColors(containerColor = MinimalistSurface),
            border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder)
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(20.dp)
                    .verticalScroll(rememberScrollState())
            ) {
                // Header
                Row(
                    modifier = Modifier.fillMaxWidth(),
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
                                contentDescription = "Settings",
                                tint = MinimalistAccent,
                                modifier = Modifier.size(20.dp)
                            )
                        }
                        Spacer(modifier = Modifier.width(10.dp))
                        Text(
                            text = "Uygulama Ayarları",
                            fontSize = 18.sp,
                            fontWeight = FontWeight.Bold,
                            color = MinimalistPrimary
                        )
                    }

                    IconButton(
                        onClick = onDismiss,
                        modifier = Modifier.size(32.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Default.Close,
                            contentDescription = "Kapat",
                            tint = MinimalistSecondary,
                            modifier = Modifier.size(20.dp)
                        )
                    }
                }

                Spacer(modifier = Modifier.height(18.dp))

                // Card 1: Sürüm & Güncelleme
                Text(
                    text = "SÜRÜM & GÜNCELLEME",
                    fontSize = 11.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )
                Spacer(modifier = Modifier.height(6.dp))

                Card(
                    shape = RoundedCornerShape(16.dp),
                    colors = CardDefaults.cardColors(containerColor = MinimalistSurfaceElevated),
                    border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Column(modifier = Modifier.padding(14.dp)) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween,
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Column {
                                Text(
                                    text = "BigPocket Mobile",
                                    fontSize = 14.sp,
                                    fontWeight = FontWeight.SemiBold,
                                    color = MinimalistPrimary
                                )
                                Text(
                                    text = "Mevcut sürüm: v$currentVersion",
                                    fontSize = 12.sp,
                                    fontFamily = FontFamily.Monospace,
                                    color = MinimalistSecondary
                                )
                            }

                            Box(
                                modifier = Modifier
                                    .clip(RoundedCornerShape(50))
                                    .background(StatusGreenText.copy(alpha = 0.15f))
                                    .padding(horizontal = 8.dp, vertical = 4.dp)
                            ) {
                                Text(
                                    text = "Kurulu",
                                    fontSize = 11.sp,
                                    fontWeight = FontWeight.Bold,
                                    color = StatusGreenText
                                )
                            }
                        }

                        Spacer(modifier = Modifier.height(12.dp))

                        // Güncellemeleri Denetle Butonu
                        Button(
                            onClick = {
                                if (isCheckingUpdate) return@Button
                                isCheckingUpdate = true
                                updateCheckStatus = "GitHub kontrol ediliyor..."
                                updateCheckIsSuccess = null

                                coroutineScope.launch {
                                    val result = UpdateManager.checkDetailed(context)
                                    isCheckingUpdate = false
                                    when (result) {
                                        is UpdateManager.CheckResult.UpdateAvailable -> {
                                            updateCheckStatus = "Yeni v${result.info.latestVersion} sürümü bulundu!"
                                            updateCheckIsSuccess = true
                                            onDismiss()
                                            onUpdateAvailable(result.info)
                                        }
                                        is UpdateManager.CheckResult.UpToDate -> {
                                            updateCheckStatus = "En güncel sürümü kullanıyorsunuz (v${result.currentVersion}) 🎉"
                                            updateCheckIsSuccess = true
                                        }
                                        is UpdateManager.CheckResult.Error -> {
                                            updateCheckStatus = "Kontrol başarısız: ${result.message}"
                                            updateCheckIsSuccess = false
                                        }
                                    }
                                }
                            },
                            enabled = !isCheckingUpdate,
                            shape = RoundedCornerShape(12.dp),
                            colors = ButtonDefaults.buttonColors(
                                containerColor = MinimalistAccent,
                                contentColor = MinimalistBackground
                            ),
                            modifier = Modifier
                                .fillMaxWidth()
                                .height(44.dp)
                        ) {
                            if (isCheckingUpdate) {
                                CircularProgressIndicator(
                                    modifier = Modifier.size(18.dp),
                                    strokeWidth = 2.dp,
                                    color = MinimalistBackground
                                )
                                Spacer(modifier = Modifier.width(8.dp))
                                Text("Kontrol Ediliyor...", fontSize = 13.sp)
                            } else {
                                Icon(
                                    imageVector = Icons.Default.Refresh,
                                    contentDescription = "Güncelle",
                                    modifier = Modifier.size(16.dp)
                                )
                                Spacer(modifier = Modifier.width(8.dp))
                                Text("Güncellemeleri Denetle", fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
                            }
                        }

                        if (updateCheckStatus != null) {
                            Spacer(modifier = Modifier.height(8.dp))
                            val textColor = when (updateCheckIsSuccess) {
                                true -> StatusGreenText
                                false -> StatusRedText
                                else -> MinimalistSecondary
                            }
                            Text(
                                text = updateCheckStatus ?: "",
                                fontSize = 12.sp,
                                color = textColor
                            )
                        }
                    }
                }

                Spacer(modifier = Modifier.height(18.dp))

                // Card 2: Ekran & Davranış
                Text(
                    text = "TERCİHLER",
                    fontSize = 11.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )
                Spacer(modifier = Modifier.height(6.dp))

                Card(
                    shape = RoundedCornerShape(16.dp),
                    colors = CardDefaults.cardColors(containerColor = MinimalistSurfaceElevated),
                    border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Column(modifier = Modifier.padding(14.dp)) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween,
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Column(modifier = Modifier.weight(1f)) {
                                Text("Ekran Asla Kararmasın", fontSize = 13.5.sp, fontWeight = FontWeight.SemiBold, color = MinimalistPrimary)
                                Text("Kumanda veya deck açıkken ekran kapanmaz", fontSize = 11.5.sp, color = MinimalistSecondary)
                            }
                            Switch(
                                checked = keepScreenOn,
                                onCheckedChange = onKeepScreenOnChange,
                                colors = SwitchDefaults.colors(
                                    checkedThumbColor = MinimalistPrimary,
                                    checkedTrackColor = MinimalistAccent
                                )
                            )
                        }
                    }
                }

                Spacer(modifier = Modifier.height(18.dp))

                // Card 3: Ağ & Proje
                Text(
                    text = "AĞ & AÇIK KAYNAK",
                    fontSize = 11.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MinimalistSecondary,
                    letterSpacing = 1.sp
                )
                Spacer(modifier = Modifier.height(6.dp))

                Card(
                    shape = RoundedCornerShape(16.dp),
                    colors = CardDefaults.cardColors(containerColor = MinimalistSurfaceElevated),
                    border = androidx.compose.foundation.BorderStroke(1.dp, MinimalistBorder),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Column(modifier = Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Text("Otomatik Keşif:", fontSize = 12.5.sp, color = MinimalistSecondary)
                            Text("UDP Port 47800", fontSize = 12.5.sp, color = MinimalistPrimary, fontFamily = FontFamily.Monospace)
                        }
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Text("Kontrol Servisi:", fontSize = 12.5.sp, color = MinimalistSecondary)
                            Text("WS Port 8085", fontSize = 12.5.sp, color = MinimalistPrimary, fontFamily = FontFamily.Monospace)
                        }

                        HorizontalDivider(
                            modifier = Modifier.padding(vertical = 4.dp),
                            color = MinimalistBorder
                        )

                        // GitHub Link Button
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(RoundedCornerShape(10.dp))
                                .background(MinimalistBackground)
                                .clickable {
                                    try {
                                        val intent = Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/benyigiteren/bigpocket"))
                                        context.startActivity(intent)
                                    } catch (e: Exception) {
                                        // ignore
                                    }
                                }
                                .padding(horizontal = 12.dp, vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Icon(
                                    imageVector = Icons.Default.Info,
                                    contentDescription = "GitHub",
                                    tint = MinimalistPrimary,
                                    modifier = Modifier.size(18.dp)
                                )
                                Spacer(modifier = Modifier.width(10.dp))
                                Text("GitHub Deposu & Kaynak Kod", fontSize = 12.5.sp, color = MinimalistPrimary)
                            }
                            Icon(
                                imageVector = Icons.Default.ArrowForward,
                                contentDescription = "Aç",
                                tint = MinimalistSecondary,
                                modifier = Modifier.size(14.dp)
                            )
                        }
                    }
                }

                Spacer(modifier = Modifier.height(18.dp))

                // Close Button
                Button(
                    onClick = onDismiss,
                    shape = RoundedCornerShape(14.dp),
                    colors = ButtonDefaults.buttonColors(
                        containerColor = MinimalistSurfaceElevated,
                        contentColor = MinimalistPrimary
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(44.dp)
                ) {
                    Text("Kapat")
                }
            }
        }
    }
}
