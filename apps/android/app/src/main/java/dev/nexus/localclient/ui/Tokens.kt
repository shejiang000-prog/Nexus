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

private const val SIGNAL_INK = 0xFF03140C

internal val LightPalette = NexusPalette(
    dark = false,
    canvas = Color(0xFFF3F5F8), surface = Color(0xFFFFFFFF), raised = Color(0xFFFFFFFF), well = Color(0xFFE9EDF1), line = Color(0xFFDCE2E8),
    ink = Color(0xFF0A0D11), inkSoft = Color(0xFF47525E), inkMuted = Color(0xFF5F6B78),
    signal = Color(0xFF2BD394), signalDeep = Color(0xFF14B8A6), signalText = Color(0xFF0A7F55), onSignal = Color(SIGNAL_INK),
    signalSoft = Color(0xFF2BD394).copy(alpha = .16f),
    warning = Color(0xFF9A5B00), warningSoft = Color(0xFFFFB547).copy(alpha = .20f),
    danger = Color(0xFFC42B2B), dangerSoft = Color(0xFFC42B2B).copy(alpha = .10f),
)

internal val DarkPalette = NexusPalette(
    dark = true,
    canvas = Color(0xFF0A0D11), surface = Color(0xFF12171D), raised = Color(0xFF181F26), well = Color(0xFF1A2129), line = Color(0xFF263039),
    ink = Color(0xFFEEF2F6), inkSoft = Color(0xFFA9B4BF), inkMuted = Color(0xFF8592A0),
    signal = Color(0xFF3EE6A3), signalDeep = Color(0xFF22C7C0), signalText = Color(0xFF3EE6A3), onSignal = Color(SIGNAL_INK),
    signalSoft = Color(0xFF3EE6A3).copy(alpha = .14f),
    warning = Color(0xFFFFB547), warningSoft = Color(0xFFFFB547).copy(alpha = .14f),
    danger = Color(0xFFFF7A7A), dangerSoft = Color(0xFFFF7A7A).copy(alpha = .14f),
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
    val page = 20.dp
}

/** Corner radii. */
internal object Radius {
    val xs = 8.dp
    val sm = 12.dp
    val md = 16.dp
    val lg = 20.dp
    val xl = 28.dp
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
    val buttonHeight = 56.dp
    val compactHeight = 44.dp
    val icon = 22.dp
    val smallIcon = 16.dp
    val badge = 40.dp
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
