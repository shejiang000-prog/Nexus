package dev.nexus.localclient

import android.os.Build
import android.view.HapticFeedbackConstants
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.*
import androidx.compose.foundation.*
import androidx.compose.foundation.interaction.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.draw.*
import androidx.compose.ui.geometry.*
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.*
import kotlin.math.*

/** What the connection control shows. Derived from [TunnelState] only; it never changes the tunnel. */
internal enum class Link { Idle, Busy, On, Failed }

internal fun TunnelState.link():Link {
    val on=connectedAt>0 && !waiting && !starting
    return when {
        on -> Link.On
        starting || waiting || vpn && error.isBlank() -> Link.Busy
        error.isNotBlank() -> Link.Failed
        else -> Link.Idle
    }
}

/** The accent for each [Link] state, shared by the dial and the status pill. */
@Composable
internal fun Link.tone():Color = when(this) {
    Link.On -> palette.signal
    Link.Busy -> palette.warning
    Link.Failed -> palette.danger
    Link.Idle -> MaterialTheme.colorScheme.outline
}

@Composable
internal fun Orbit(state:TunnelState,enabled:Boolean,toggle:()->Unit) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val link=state.link()
    val on=link==Link.On
    val busy=link==Link.Busy
    val motion=LocalMotion.current
    val active=LocalForeground.current
    val view=LocalView.current
    var previouslyOn by remember { mutableStateOf(on) }
    LaunchedEffect(on) {
        if(previouslyOn!=on && active) view.performHapticFeedback(if(Build.VERSION.SDK_INT<30) HapticFeedbackConstants.CONTEXT_CLICK
            else if(on) HapticFeedbackConstants.CONFIRM else HapticFeedbackConstants.REJECT)
        previouslyOn=on
    }
    val interaction=remember{MutableInteractionSource()}
    val pressed by interaction.collectIsPressedAsState()
    // Answer the finger at once; the tunnel may take seconds to report back.
    LaunchedEffect(pressed) { if(pressed && enabled) view.performHapticFeedback(HapticFeedbackConstants.VIRTUAL_KEY) }
    val scale by animateFloatAsState(if(pressed && motion && enabled).94f else 1f,motionSpring(Spring.StiffnessMedium,.6f),label="连接按压")
    val colorSpec=motionTween<Color>(Motion.emphasized)
    val accent by animateColorAsState(link.tone(),colorSpec,label="轨道颜色")
    val mark by animateColorAsState(when(link){Link.On->p.onSignal;Link.Busy->p.warning;Link.Failed->p.danger;Link.Idle->c.onSurface},colorSpec,label="标志颜色")
    val face by animateColorAsState(if(link==Link.Failed) lerp(c.surface,p.danger,.10f) else c.surface,colorSpec,label="按钮底色")
    val lit by animateFloatAsState(if(on) 1f else 0f,motionTween(Motion.emphasized),label="点亮")
    val glow by animateFloatAsState(when(link){Link.On->.34f;Link.Busy,Link.Failed->.18f;Link.Idle->0f},motionTween(Motion.emphasized),label="光晕")
    val lift by animateDpAsState(if(on) 14.dp else if(p.dark) 0.dp else 4.dp,motionTween(Motion.emphasized),label="按钮高度")
    // A short shake when a connection attempt fails, so the change is felt as well as seen.
    val shake=remember { Animatable(0f) }
    LaunchedEffect(link) {
        if(link==Link.Failed && motion && active) shake.animateTo(0f,keyframes { durationMillis=420
            -9f at 60; 8f at 130; -6f at 200; 4f at 270; -2f at 340 })
    }
    val ms=remember { mutableLongStateOf(0L) }
    LaunchedEffect(on,busy,motion,active) {
        ms.longValue=0
        if(motion && active && (on||busy)) {
            val start=withFrameNanos { it }
            while(true) withFrameNanos { ms.longValue=(it-start)/1_000_000 }
        }
    }
    val periods=remember(busy){if(busy) intArrayOf(1100,1600,2300) else intArrayOf(14000,20000,28000)}
    val track=p.line
    val verb=if(state.active)"断开" else "连接"
    val signalFill=remember(p) { Brush.linearGradient(listOf(p.signal,p.signalDeep)) }
    Box(Modifier.size(Sizes.dial).graphicsLayer{scaleX=scale;scaleY=scale;translationX=shake.value.dp.toPx()},contentAlignment=Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val unit=size.minDimension/260f
            val t=ms.longValue
            if(glow>0f) drawCircle(Brush.radialGradient(listOf(accent.copy(alpha=glow),accent.copy(alpha=glow*.35f),Color.Transparent),
                center,size.minDimension/2),size.minDimension/2)
            for(i in 0..2) {
                val r=(126-18*i)*unit
                drawCircle(track,r,style=Stroke(1.5f*unit))
                val direction=if(i==1)-1 else 1
                val start=when(i){0->-90f;1->35f;else->150f} + direction*(t%periods[i])*360f/periods[i]
                val arc=when(i){0->96;1->54;else->150}
                val sweep=(arc*unit/r*180/PI).toFloat()
                val box=Size(r*2,r*2); val corner=Offset(center.x-r,center.y-r)
                // A faint tail behind each moving head gives the arcs a direction.
                if(on || busy) {
                    val tail=sweep*1.2f
                    drawArc(accent.copy(alpha=.22f),if(direction>0) start-tail else start+sweep,tail,false,corner,box,style=Stroke(3*unit,cap=StrokeCap.Round))
                }
                drawArc(accent,start,sweep,false,corner,box,style=Stroke(3*unit,cap=StrokeCap.Round))
            }
            if(on && motion && active) repeat(2){i->
                val f=((t+i*1400)%2800)/2800f
                drawCircle(accent.copy(alpha=.30f*(1-f)),(Sizes.dialCore.toPx()/2*(.55f+.95f*f)),style=Stroke(1.dp.toPx()))
            }
        }
        Box(Modifier.size(Sizes.dialCore).graphicsLayer {
            val breath=if(on && active && motion) 1f+.02f*(1-cos(2*PI*(ms.longValue%3200)/3200)).toFloat() else 1f
            scaleX=breath;scaleY=breath
        }.shadow(lift,CircleShape,ambientColor=accent.copy(alpha=.5f),spotColor=accent)
            .clip(CircleShape)
            .drawBehind {
                drawRect(face)
                if(lit>0f) drawRect(signalFill,alpha=lit)
            }
            .border(1.dp,lerp(track,accent,if(link==Link.Idle) 0f else .55f*(1f-lit)),CircleShape)
            .clickable(interactionSource=interaction,indication=null,enabled=enabled,role=Role.Button,onClick=toggle)
            .semantics {contentDescription=verb},contentAlignment=Alignment.Center) {
            Mark(Modifier.size(34.dp),mark)
        }
    }
}
