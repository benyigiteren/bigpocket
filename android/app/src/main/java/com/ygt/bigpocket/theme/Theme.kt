package com.ygt.bigpocket.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable

private val MinimalistColorScheme = darkColorScheme(
    primary = MinimalistPrimary,
    onPrimary = MinimalistBackground,
    secondary = MinimalistSecondary,
    onSecondary = MinimalistPrimary,
    background = MinimalistBackground,
    onBackground = MinimalistPrimary,
    surface = MinimalistSurface,
    onSurface = MinimalistPrimary,
    surfaceVariant = MinimalistSurfaceElevated,
    onSurfaceVariant = MinimalistSecondary,
    outline = MinimalistBorder,
    outlineVariant = MinimalistBorder,
    error = StatusRedText,
    onError = MinimalistBackground
)

@Composable
fun BigPocketTheme(
    isGamerTheme: Boolean = false,
    content: @Composable () -> Unit
) {
    // Both configurations now resolve to the premium dark monochrome color scheme
    MaterialTheme(
        colorScheme = MinimalistColorScheme,
        typography = Typography,
        content = content
    )
}

@Composable
fun bigPocketSwitchColors() = androidx.compose.material3.SwitchDefaults.colors(
    checkedThumbColor = androidx.compose.ui.graphics.Color.White,
    checkedTrackColor = SwitchCheckedTrack,
    checkedBorderColor = androidx.compose.ui.graphics.Color.Transparent,
    uncheckedThumbColor = MinimalistSecondary,
    uncheckedTrackColor = MinimalistSurfaceElevated,
    uncheckedBorderColor = MinimalistBorder
)
