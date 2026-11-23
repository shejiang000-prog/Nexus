package dev.nexus.localclient

import androidx.compose.animation.core.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/*
 * Design tokens. Screens read colours, sizes, shapes and motion from here instead of writing
 * numbers inline, so a later page (devices, invitations, expiry) picks up the same look by
 * using the same names.
 */

/** Brand and state colours the Material scheme has no slot for. Material roles are mapped from these in [NexusTheme]. */
@Immutable
internal class NexusPalette(
    val dark: Boolean,
    /** Page background, card face, raised face (sheets, pressed cards), recessed well, hairline. */
    val canvas: Color, val surface: Color, val raised: Color, val well: Color, val line: Color,
    /** Primary text, secondary text, tertiary text and idle strokes. */
    val ink: Color, val inkSoft: Color, val inkMuted: Color,
    /** Connected / primary fill, its gradient partner, the contrast-safe text variant, and content on the fill. */
    val signal: Color, val signalDeep: Color, val signalText: Color, val onSignal: Color, val signalSoft: Color,
    /** Connecting, waiting and stale results. */
    val warning: Color, val warningSoft: Color,
    /** Failed connection, quota exhausted, destructive actions. */
    val danger: Color, val dangerSoft: Color,
)

/**
 * The app is white with a blue brand colour in every system theme: no black surfaces.
 * Text uses a deep slate rather than pure black.
 */
internal val LightPalette = NexusPalette(
    dark = false,
    canvas = Color(0xFFF4F7FC), surface = Color(0xFFFFFFFF), raised = Color(0xFFFFFFFF), well = Color(0xFFEDF2FA), line = Color(0xFFDDE5F0),
    ink = Color(0xFF0F1B2D), inkSoft = Color(0xFF4A5A70), inkMuted = Color(0xFF6B7A90),
    signal = Color(0xFF2F6BFF), signalDeep = Color(0xFF1AA7EC), signalText = Color(0xFF1F56E0), onSignal = Color(0xFFFFFFFF),
    signalSoft = Color(0xFF2F6BFF).copy(alpha = .10f),
    warning = Color(0xFFB86E00), warningSoft = Color(0xFFFFB547).copy(alpha = .20f),
    danger = Color(0xFFD13434), dangerSoft = Color(0xFFD13434).copy(alpha = .09f),
)

internal val LocalPalette = staticCompositionLocalOf { LightPalette }
internal val palette: NexusPalette @Composable @ReadOnlyComposable get() = LocalPalette.current

// Older call sites read these names; they now come from the palette.
internal val signalDark @Composable @ReadOnlyComposable get() = palette.dark
internal val signal @Composable @ReadOnlyComposable get() = palette.signal
internal val signalSoft @Composable @ReadOnlyComposable get() = palette.signalSoft
internal val signalAmber @Composable @ReadOnlyComposable get() = palette.warning

/** Spacing scale (4 dp grid). */
internal object Space {
    val xxs = 2.dp
    val xs = 4.dp
    val sm = 8.dp
    val md = 12.dp
    val lg = 16.dp
    val xl = 20.dp
    val xxl = 28.dp
    /** Horizontal page margin. */
    val page = 16.dp
}

/** Corner radii. */
internal object Radius {
    val xs = 8.dp
    val sm = 12.dp
    val md = 16.dp
    val lg = 18.dp
    val xl = 24.dp
}

/** Shapes built from [Radius]. */
internal object Shapes {
    val chip = RoundedCornerShape(Radius.sm)
    val control = RoundedCornerShape(Radius.md)
    val card = RoundedCornerShape(Radius.lg)
    val sheet = RoundedCornerShape(topStart = Radius.xl, topEnd = Radius.xl)
    val pill = CircleShape
}

/** Fixed component sizes. */
internal object Sizes {
    /** Minimum touch target. */
    val touch = 48.dp
    val buttonHeight = 50.dp
    val compactHeight = 40.dp
    /** The connection dial and its centre button. */
    val dial = 188.dp
    val dialCore = 100.dp
    /** Height of segmented controls. */
    val segment = 40.dp
    val icon = 22.dp
    val smallIcon = 16.dp
    val badge = 36.dp
    val hairline = 1.dp
}

/**
 * Durations and curves. When the system animator scale is zero, [LocalMotion] is false and
 * transitions fall back to a short fade with no movement ([Motion.reduced]).
 */
internal object Motion {
    const val quick = 150
    const val exit = 200
    const val standard = 300
    const val emphasized = 450
    const val reduced = 150
    val enter = CubicBezierEasing(.05f, .7f, .1f, 1f)
    val leave = CubicBezierEasing(.3f, 0f, .8f, .15f)
    val slide = CubicBezierEasing(.2f, 0f, 0f, 1f)
}

// Names used before the token file existed.
internal val enterCurve get() = Motion.enter
internal val exitCurve get() = Motion.leave
internal val slideCurve get() = Motion.slide

/** A tween that honours the reduced-motion setting. */
@Composable
internal fun <T> motionTween(ms: Int = Motion.standard, easing: Easing = Motion.enter): TweenSpec<T> =
    tween(if (LocalMotion.current) ms else Motion.reduced, easing = easing)

/** The same tween for places that are not composable, such as AnimatedContent transition specs. */
internal fun <T> tweenFor(motion: Boolean, ms: Int = Motion.standard, easing: Easing = Motion.enter): TweenSpec<T> =
    tween(if (motion) ms else Motion.reduced, easing = easing)

/** Fade-through used when one piece of content replaces another. */
internal fun fadeThrough(motion: Boolean, enterMs: Int = Motion.standard): androidx.compose.animation.ContentTransform =
    androidx.compose.animation.ContentTransform(
        androidx.compose.animation.fadeIn(tweenFor(motion, enterMs)),
        androidx.compose.animation.fadeOut(tweenFor(motion, Motion.quick, Motion.leave)),
        sizeTransform = androidx.compose.animation.SizeTransform(clip = false) { _, _ -> tweenFor(motion, Motion.standard) })

/** A spring for direct-manipulation feedback; snaps when motion is reduced. */
@Composable
internal fun <T> motionSpring(stiffness: Float = Spring.StiffnessMediumLow, damping: Float = Spring.DampingRatioLowBouncy): FiniteAnimationSpec<T> =
    if (LocalMotion.current) spring(damping, stiffness) else snap()
