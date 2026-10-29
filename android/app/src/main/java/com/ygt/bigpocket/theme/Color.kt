package com.ygt.bigpocket.theme

import androidx.compose.ui.graphics.Color

// Editorial Monochrome Zinc — one design system shared 1:1 with the PC frontend (style.css).
val MinimalistBackground = Color(0xFF101012)        // --bg-main: whole app + Stream Deck canvas
val MinimalistSurface = Color(0xFF18181B)           // --bg-surface: every card, tab panel and widget
val MinimalistSurfaceElevated = Color(0xFF222226)   // --bg-elevated: buttons, inputs, icon badges, active pills
val MinimalistBorder = Color(0xFF27272A)            // --border-color: 1.dp hairline on every surface

val MinimalistPrimary = Color(0xFFF4F4F5)           // --text-main: titles, values, icons
val MinimalistSecondary = Color(0xFFA1A1AA)         // --text-muted: subtitles, captions, passive states
val MinimalistAccent = Color(0xFFF4F4F5)            // --accent-solid: primary buttons, active pills, progress fill

// Status indicators — used only inside connection / recording badges and active switch toggles.
val StatusGreenText = Color(0xFF4ADE80)
val StatusGreenBg = Color(0x1F4ADE80)
val SwitchCheckedTrack = Color(0xFF22C55E) // Apple / iOS standard active switch green
val StatusRedText = Color(0xFFF87171)
val StatusRedBg = Color(0x1FF87171)

val StatusBlueText = Color(0xFF60A5FA)
val StatusBlueBg = Color(0x1F60A5FA)
