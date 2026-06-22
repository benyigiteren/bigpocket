package com.ygt.bigpocket.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val MinimalistColorScheme = darkColorScheme(
    primary = MinimalistPrimary,
    secondary = MinimalistSecondary,
    background = MinimalistBackground,
    surface = MinimalistSurface,
    onPrimary = MinimalistBackground, // Text on primary button (inverse)
    onSecondary = Color.White,
    onBackground = MinimalistPrimary,
    onSurface = MinimalistPrimary,
    surfaceVariant = Color(0xFF1B1B1F)
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
