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

@Composable
internal fun Orbit(state:TunnelState,enabled:Boolean,toggle:()->Unit) {
    val c=MaterialTheme.colorScheme
    val on=state.connectedAt>0 && !state.waiting && !state.starting
    val busy=!on && (state.starting || state.waiting || state.vpn && state.error.isBlank())
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
    val scale=animateFloatAsState(if(pressed && motion).96f else 1f,if(motion)spring() else snap(),label="连接按压")
    val accent=animateColorAsState(if(on)signal else if(busy)signalAmber else c.outline,tween(if(motion)450 else 150),label="轨道颜色")
    val core=animateColorAsState(if(on)signal else c.surface,tween(if(motion)450 else 150),label="连接按钮颜色")
    val ms=remember { mutableLongStateOf(0L) }
    LaunchedEffect(on,busy,motion,active) {
        ms.longValue=0
        if(motion && active && (on||busy)) {
            val start=withFrameNanos { it }
            while(true) withFrameNanos { ms.longValue=(it-start)/1_000_000 }
        }
    }
    val periods=remember(busy){if(busy) intArrayOf(1100,1600,2300) else intArrayOf(14000,20000,28000)}
    val track=c.outlineVariant
    val verb=if(state.active)"断开" else "连接"
    Box(Modifier.size(248.dp).graphicsLayer{scaleX=scale.value;scaleY=scale.value},contentAlignment=Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val unit=size.minDimension/260f
            val t=ms.longValue
            for(i in 0..2) {
                val r=(126-18*i)*unit
                drawCircle(track,r,style=Stroke(1.5f*unit))
                val start=when(i){0->-90f;1->35f;else->150f} + (if(i==1)-1 else 1)*(t%periods[i])*360f/periods[i]
                val arc=when(i){0->96;1->54;else->150}
                drawArc(accent.value,start,(arc*unit/r*180/PI).toFloat(),false,Offset(center.x-r,center.y-r),Size(r*2,r*2),style=Stroke(3*unit,cap=StrokeCap.Round))
            }
            if(on && motion && active) repeat(2){i->
                val f=((t+i*1400)%2800)/2800f
                drawCircle(accent.value.copy(alpha=.30f*(1-f)),(66.dp.toPx()*(.55f+.95f*f)),style=Stroke(1.dp.toPx()))
            }
        }
        Box(Modifier.size(132.dp).graphicsLayer {
            val breath=if(on && active && motion) 1f+.02f*(1-cos(2*PI*(ms.longValue%3200)/3200)).toFloat() else 1f
            scaleX=breath;scaleY=breath
        }.clip(CircleShape).background(core.value).border(1.dp,track,CircleShape)
            .clickable(interactionSource=interaction,indication=null,enabled=enabled,role=Role.Button,onClick=toggle)
            .semantics {contentDescription=verb},contentAlignment=Alignment.Center) {
            Mark(Modifier.size(46.dp),if(on)Color(0xFF03140C) else if(busy)signalAmber else c.onSurface)
        }
    }
}
