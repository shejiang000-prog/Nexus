package dev.nexus.localclient

import androidx.compose.animation.core.*
import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.*
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.*
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.*
import kotlinx.coroutines.delay

internal val signalCard=RoundedCornerShape(20.dp)
private val buttonShape=RoundedCornerShape(17.dp)
internal val enterCurve=CubicBezierEasing(.05f,.7f,.1f,1f)
internal val exitCurve=CubicBezierEasing(.3f,0f,.8f,.15f)
internal val slideCurve=CubicBezierEasing(.2f,0f,0f,1f)

// Wall time also preserves the short accessibility fade when Android's duration scale is zero.
internal suspend fun frameProgress(ms:Int, update:(Float)->Unit) {
    val start=withFrameNanos { it }
    do {
        val f=withFrameNanos { ((it-start)/(ms*1_000_000f)).coerceIn(0f,1f) }
        update(f)
    } while(f<1f)
}

@Composable
internal fun PrimaryAction(onClick:()->Unit,modifier:Modifier=Modifier,enabled:Boolean=true,content:@Composable RowScope.()->Unit) {
    val c=MaterialTheme.colorScheme
    val glow=signal.copy(alpha=if(signalDark) .20f else .22f)
    Button(onClick,modifier.heightIn(min=56.dp).then(if(enabled) Modifier.shadow(6.dp,buttonShape,ambientColor=glow,spotColor=glow) else Modifier),
        enabled=enabled,shape=buttonShape,colors=ButtonDefaults.buttonColors(containerColor=signal,contentColor=Color(0xFF03140C),
            disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant),content=content)
}

@Composable
internal fun SecondaryAction(onClick:()->Unit,modifier:Modifier=Modifier,enabled:Boolean=true,content:@Composable RowScope.()->Unit) {
    val c=MaterialTheme.colorScheme
    OutlinedButton(onClick,modifier.heightIn(min=48.dp),enabled=enabled,shape=buttonShape,border=BorderStroke(1.5.dp,c.outlineVariant),
        colors=ButtonDefaults.outlinedButtonColors(contentColor=c.onSurface,disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant),content=content)
}

@Composable
internal fun Mark(modifier:Modifier,ink:Color=LocalContentColor.current) {
    Canvas(modifier) {
        val w=size.minDimension
        drawPath(Path().apply { moveTo(0f,w*.85f);lineTo(w*.30f,w*.13f);lineTo(w*.53f,w*.13f)
            lineTo(w*.71f,w*.51f);lineTo(w*.87f,w*.13f);lineTo(w,w*.13f);lineTo(w*.70f,w*.87f)
            lineTo(w*.51f,w*.87f);lineTo(w*.32f,w*.47f);lineTo(w*.17f,w*.85f);close() },ink)
    }
}

@Composable
internal fun Segment(labels:List<String>,selected:Int,select:(Int)->Unit) {
    val c=MaterialTheme.colorScheme
    val motion=LocalMotion.current
    BoxWithConstraints(Modifier.fillMaxWidth().background(c.surfaceVariant,RoundedCornerShape(16.dp)).padding(4.dp)) {
        val width=maxWidth/labels.size
        val position by animateDpAsState(width*selected,tween(if(motion) 300 else 0,easing=slideCurve),label="选中位置")
        Box(Modifier.offset(x=position).width(width).height(44.dp).background(c.onSurface,RoundedCornerShape(12.dp)))
        Row { labels.forEachIndexed { i,s -> Box(Modifier.weight(1f).height(44.dp).selectable(i==selected,role=Role.Tab){select(i)},
            contentAlignment=Alignment.Center){Text(s,color=if(i==selected)c.background else c.onSurfaceVariant,fontWeight=FontWeight.SemiBold)} } }
    }
}

@Composable
internal fun Notice(message:String,error:Boolean=false) {
    val c=MaterialTheme.colorScheme
    Surface(shape=RoundedCornerShape(14.dp),color=c.surfaceVariant) {
        Text(message,Modifier.fillMaxWidth().padding(14.dp),color=if(error)c.error else c.onSurfaceVariant)
    }
}

@Composable
internal fun ActionRow(title:String,detail:String,index:Int,click:(()->Unit)?) {
    val c=MaterialTheme.colorScheme
    Surface(shape=signalCard,color=c.surface,border=BorderStroke(1.dp,c.outlineVariant)) {
        Row(Modifier.fillMaxWidth().then(if(click==null) Modifier else Modifier.clickable(role=Role.Button,onClick=click))
            .padding(16.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)) {
            Box(Modifier.size(44.dp).background(c.surfaceVariant,RoundedCornerShape(12.dp)),contentAlignment=Alignment.Center){SectionIcon(index)}
            Column(Modifier.weight(1f)) { Text(title,fontWeight=FontWeight.SemiBold);if(detail.isNotEmpty())Text(detail,color=c.onSurfaceVariant) }
            if(click!=null)Text("›",color=c.onSurfaceVariant,fontSize=22.sp)
        }
    }
}

private fun regionBadge(region:String):String {
    val codes=region.codePoints().toArray()
    if(codes.size==2 && codes.all {it in 0x1F1E6..0x1F1FF})return codes.map { ('A'.code+it-0x1F1E6).toChar() }.joinToString("")
    return if(region.isBlank() || region=="未提供") "·" else String(codes,0,minOf(2,codes.size)).uppercase(java.util.Locale.ROOT)
}

@Composable
internal fun SignalNode(region:String,name:String,detail:String,latency:Long?,selected:Boolean,info:String,
                        onDetails:()->Unit,onSelect:(()->Unit)?) {
    val c=MaterialTheme.colorScheme
    val bars=when { latency==null->0;latency<150->4;latency<350->3;latency<800->2;else->1 }
    Row(Modifier.fillMaxWidth().background(c.surface).clickable(onClick=onDetails).padding(start=12.dp,end=4.dp).heightIn(min=64.dp),
        verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)) {
        Box(Modifier.size(38.dp).background(c.surfaceVariant,RoundedCornerShape(12.dp)),contentAlignment=Alignment.Center){
            Text(regionBadge(region),style=numberStyle,fontWeight=FontWeight.Bold) }
        Column(Modifier.weight(1f).padding(vertical=8.dp)) { Text(name,fontWeight=FontWeight.SemiBold);Text(detail,color=c.onSurfaceVariant)
            if(info.isNotBlank()) Text(info,color=c.onSurfaceVariant) }
        Canvas(Modifier.size(18.dp,16.dp)){ repeat(4){ i ->
            val h=size.height*(i+1)/4
            drawLine(if(i<bars)c.onSurfaceVariant else c.outlineVariant,Offset(size.width*(i+.5f)/4,size.height),Offset(size.width*(i+.5f)/4,size.height-h),2.dp.toPx())
        } }
        if(latency!=null) Text("${latency} ms",style=numberStyle)
        if(onSelect!=null || selected) Box(Modifier.size(48.dp).then(if(onSelect!=null)Modifier.clickable(role=Role.Button,onClick=onSelect)
            .semantics {contentDescription="使用此节点"} else Modifier),contentAlignment=Alignment.Center){
            if(selected)Box(Modifier.size(24.dp).background(signal,androidx.compose.foundation.shape.CircleShape),contentAlignment=Alignment.Center){Text("✓",color=Color(0xFF03140C))}
            else Text("›",color=c.onSurfaceVariant,fontSize=22.sp)
        }
    }
}

@Composable
internal fun ListEntrance(index:Int,content:@Composable ()->Unit) {
    val motion=LocalMotion.current
    var fraction by remember { mutableFloatStateOf(if(index<8) 0f else 1f) }
    LaunchedEffect(Unit){ if(index<8){if(motion)delay(index*30L);frameProgress(if(motion)450 else 150){fraction=enterCurve.transform(it)}} }
    Box(Modifier.graphicsLayer { alpha=fraction;translationY=if(motion)(1-fraction)*10.dp.toPx() else 0f }) {content()}
}

@Composable
internal fun AutoSelection(checked:Boolean,enabled:Boolean,onChange:(Boolean)->Unit){
    val c=MaterialTheme.colorScheme
    Surface(shape=signalCard,color=c.surface,border=BorderStroke(1.dp,c.outlineVariant)){
        Row(Modifier.fillMaxWidth().padding(horizontal=14.dp,vertical=8.dp),verticalAlignment=Alignment.CenterVertically){
            Column(Modifier.weight(1f)){Text("自动选最快",fontWeight=FontWeight.SemiBold)
                Text(if(checked)"按当前网络的实测结果选择节点" else "正在使用手动选择的节点",color=c.onSurfaceVariant)}
            Switch(checked,onChange,enabled=enabled,colors=SwitchDefaults.colors(checkedTrackColor=signal,checkedThumbColor=Color(0xFF03140C),
                uncheckedTrackColor=c.surfaceVariant,uncheckedThumbColor=c.onSurface))
        }
    }
}
