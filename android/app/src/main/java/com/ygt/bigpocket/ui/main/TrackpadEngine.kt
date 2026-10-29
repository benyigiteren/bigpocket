package com.ygt.bigpocket.ui.main

import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.pow

/**
 * Pure-Kotlin input processing for the virtual touchpad.
 *
 * Deliberately free of Android/Compose dependencies so the numerics stay
 * trivially testable. It hosts two independent pipelines:
 *
 *  - [accelerate]: power-curve pointer acceleration with sub-pixel accumulation.
 *  - [scroll]: low-pass (EMA) smoothed two-finger wheel output.
 */
class TrackpadEngine {

    /** "Hassas Mod" – tighter precision and a lower top speed. */
    var precisionMode: Boolean = false

    /** User sensitivity multiplier coming from the trackpad top bar (0.5x – 1.6x). */
    var sensitivity: Float = 1f

    // --- pointer acceleration state ---
    private var smoothedSpeed = 0f
    private var accumX = 0f
    private var accumY = 0f

    // --- scroll smoothing state ---
    private var smoothedScroll = 0f
    private var scrollAccum = 0f

    /** Call whenever a new touch sequence begins (first finger down). */
    fun resetGesture() {
        smoothedSpeed = 0f
        accumX = 0f
        accumY = 0f
        smoothedScroll = 0f
        scrollAccum = 0f
    }

    /**
     * Converts a raw per-frame finger delta into an integer mouse delta.
     *
     * Finger speed (px/ms) is passed through a power curve: slow drags stay at
     * [PRECISION_FACTOR] for pixel-level control, fast flicks ramp up to
     * [MAX_FACTOR] so the cursor covers distance quickly. Sub-pixel remainders
     * are carried over, so integer truncation never loses travelled distance.
     *
     * @return integer (dx, dy) to send, or null when only a sub-pixel move happened.
     */
    fun accelerate(rawDx: Float, rawDy: Float, dtMs: Float): Pair<Int, Int>? {
        val distance = hypot(rawDx.toDouble(), rawDy.toDouble()).toFloat()
        val instantSpeed = if (dtMs > 0f) distance / dtMs else 0f
        // EMA keeps the curve stable across noisy frames and decays to the
        // precision end as soon as the finger slows down or pauses.
        smoothedSpeed = smoothedSpeed * SPEED_SMOOTHING + instantSpeed * (1f - SPEED_SMOOTHING)

        val normalised = (smoothedSpeed / FAST_SPEED).coerceIn(0f, 1f)
        val minFactor = if (precisionMode) PRECISION_MODE_FACTOR else PRECISION_FACTOR
        val maxFactor = if (precisionMode) PRECISION_MODE_MAX else MAX_FACTOR
        val factor = (minFactor + (maxFactor - minFactor) * normalised.pow(ACCEL_EXPONENT)) * sensitivity

        accumX += rawDx * factor
        accumY += rawDy * factor

        val outX = accumX.toInt()
        val outY = accumY.toInt()
        if (outX == 0 && outY == 0) return null
        accumX -= outX
        accumY -= outY
        return outX to outY
    }

    /**
     * Low-pass filters a raw two-finger vertical delta and turns it into wheel
     * steps, so the scroll wheel glides instead of jumping on each frame.
     *
     * @return integer wheel delta to send, or null when no full step is ready.
     */
    fun scroll(rawDy: Float): Int? {
        // Drop sub-pixel finger jitter, but keep draining a residual tail.
        if (abs(rawDy) < SCROLL_DEADZONE && abs(smoothedScroll) < SCROLL_DEADZONE) return null

        // Unity-gain EMA: steady-state distance is preserved, only the
        // per-frame distribution is smoothed.
        smoothedScroll += (rawDy - smoothedScroll) * SCROLL_SMOOTHING
        scrollAccum += smoothedScroll * SCROLL_SCALE

        val step = scrollAccum.toInt()
        if (step == 0) return null
        scrollAccum -= step
        return step
    }

    private companion object {
        // --- Pointer acceleration curve ---
        const val PRECISION_FACTOR = 0.7f      // slow-drag fine-control gain
        const val MAX_FACTOR = 2.5f            // fast-flick gain (top of curve)
        const val PRECISION_MODE_FACTOR = 0.5f // "Hassas Mod" slow gain
        const val PRECISION_MODE_MAX = 1.3f    // "Hassas Mod" top gain
        const val FAST_SPEED = 4.0f            // px/ms that saturates the curve (~fast flick)
        const val ACCEL_EXPONENT = 1.5f        // power-curve steepness
        const val SPEED_SMOOTHING = 0.65f      // EMA weight of the previous speed

        // --- Two-finger scroll smoothing ---
        const val SCROLL_SMOOTHING = 0.35f     // EMA weight of the newest sample
        const val SCROLL_SCALE = 0.8f          // px -> wheel delta scale
        const val SCROLL_DEADZONE = 0.6f       // ignore sub-pixel finger jitter
    }
}
