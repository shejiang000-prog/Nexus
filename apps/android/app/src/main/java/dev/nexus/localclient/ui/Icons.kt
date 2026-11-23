package dev.nexus.localclient

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.size
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.Dp

/**
 * The app's line icons. All share one stroke weight (8.5% of the box) with round caps, drawn
 * in a square box, so they read as one family next to the hand-drawn tab icons.
 */
internal enum class Glyph { Chevron, Back, Plus, Check, Settings, Down, Up, Bolt, Refresh }

@Composable
internal fun GlyphIcon(glyph: Glyph, modifier: Modifier = Modifier, size: Dp = Sizes.icon, tint: Color = LocalContentColor.current) {
    Canvas(modifier.size(size)) { drawGlyph(glyph, tint) }
}

internal fun DrawScope.drawGlyph(glyph: Glyph, ink: Color) {
    val w = size.minDimension
    val pen = Stroke(w * .085f, cap = StrokeCap.Round, join = StrokeJoin.Round)
    fun path(vararg points: Pair<Float, Float>) = Path().apply {
        points.forEachIndexed { i, (x, y) -> if (i == 0) moveTo(w * x, w * y) else lineTo(w * x, w * y) }
    }
    fun line(x: Float, y: Float, a: Float, b: Float) = drawLine(ink, Offset(w * x, w * y), Offset(w * a, w * b), pen.width, StrokeCap.Round)
    when (glyph) {
        Glyph.Chevron -> drawPath(path(.38f to .2f, .66f to .5f, .38f to .8f), ink, style = pen)
        Glyph.Back -> { drawPath(path(.45f to .2f, .15f to .5f, .45f to .8f), ink, style = pen); line(.17f, .5f, .86f, .5f) }
        Glyph.Plus -> { line(.5f, .14f, .5f, .86f); line(.14f, .5f, .86f, .5f) }
        Glyph.Check -> drawPath(path(.16f to .52f, .41f to .76f, .86f to .26f), ink, style = pen)
        Glyph.Down -> { line(.5f, .14f, .5f, .84f); drawPath(path(.22f to .58f, .5f to .86f, .78f to .58f), ink, style = pen) }
        Glyph.Up -> { line(.5f, .86f, .5f, .16f); drawPath(path(.22f to .42f, .5f to .14f, .78f to .42f), ink, style = pen) }
        Glyph.Bolt -> drawPath(path(.56f to .08f, .2f to .56f, .48f to .56f, .42f to .92f, .8f to .42f, .52f to .42f).apply { close() }, ink, style = pen)
        Glyph.Refresh -> {
            drawArc(ink, -60f, 290f, false, Offset(w * .17f, w * .17f), androidx.compose.ui.geometry.Size(w * .66f, w * .66f), style = pen)
            drawPath(path(.62f to .1f, .78f to .24f, .6f to .36f), ink, style = pen)
        }
        Glyph.Settings -> for (i in 0..1) {
            val y = w * (if (i == 0) .3f else .7f); val x = w * (if (i == 0) .68f else .38f)
            drawLine(ink, Offset(.08f * w, y), Offset(x - .11f * w, y), pen.width * .92f, StrokeCap.Round)
            drawLine(ink, Offset(x + .11f * w, y), Offset(.92f * w, y), pen.width * .92f, StrokeCap.Round)
            drawCircle(ink, .11f * w, Offset(x, y), style = Stroke(pen.width * .92f))
        }
    }
}
