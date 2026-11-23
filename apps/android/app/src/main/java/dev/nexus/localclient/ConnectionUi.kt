package dev.nexus.localclient

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.TrafficStats
import android.os.Build
import android.os.SystemClock
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.background
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

internal fun trafficBytes(ctx:Context): Pair<Long,Long>? {
    val cm=ctx.getSystemService(ConnectivityManager::class.java)
    val name=cm.allNetworks.asSequence().filter { cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true }
        .mapNotNull { cm.getLinkProperties(it) }.firstOrNull { p -> p.linkAddresses.any { it.address.hostAddress=="192.0.2.2" } }?.interfaceName
        ?: TunnelService.interfaceName ?: return null
    if(!name.matches(Regex("[a-zA-Z0-9_.-]{1,16}"))) return null
    fun read(direction:String):Long? = runCatching { File("/sys/class/net/$name/statistics/${direction}_bytes").readText().trim().toLong() }.getOrNull()
    val tx=read("tx") ?: if(Build.VERSION.SDK_INT>=31) TrafficStats.getTxBytes(name) else -1
    val rx=read("rx") ?: if(Build.VERSION.SDK_INT>=31) TrafficStats.getRxBytes(name) else -1
    return if(tx>=0 && rx>=0) tx to rx else null
}

internal data class RateSample(val at:Long,val up:Double?,val down:Double?)
internal data class Traffic(val time:String="00:00:00",val samples:List<RateSample> = emptyList())

@Composable
/** Ticks once a second while connected. Returned as State so only the readers recompose, not the whole page. */
internal fun rememberTraffic(state:TunnelState):State<Traffic> {
    val ctx=LocalContext.current
    val active=LocalForeground.current
    val holder=remember(state.connectedAt) { mutableStateOf(Traffic()) }
    var traffic by holder
    LaunchedEffect(state.connectedAt,active,state.vpn) {
        if(state.connectedAt==0L || !active)return@LaunchedEffect
        var previous:Pair<Long,Long>?=null
        var at=0L
        while(true){
            val counts=if(state.vpn)withContext(Dispatchers.IO){trafficBytes(ctx)} else null
            val now=SystemClock.elapsedRealtime()
            fun rate(n:Long?,old:Long?):Double? = if(n==null || old==null || old>n || now<=at)null else (n-old)*1000.0/(now-at)
            val sample=RateSample(now,rate(counts?.first,previous?.first),rate(counts?.second,previous?.second))
            val seconds=(now-state.connectedAt).coerceAtLeast(0)/1000
            traffic=Traffic(java.lang.String.format(java.util.Locale.ROOT,"%02d:%02d:%02d",seconds/3600,seconds/60%60,seconds%60),
                traffic.samples.filter {now-it.at<24000}+sample)
            previous=counts;at=now;delay(1000)
        }
    }
    return holder
}

@Composable
internal fun RateTiles(compact:Boolean=false,read:()->Traffic) {
    val traffic=read()
    val c=MaterialTheme.colorScheme
    val p=palette
    Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(Space.sm)){
        listOf("下载","上传").forEachIndexed { index,label ->
            val samples=traffic.samples
            fun rate(s:RateSample)=if(index==0)s.down else s.up
            val latest=samples.lastOrNull()?.let(::rate)
            val peak=samples.maxOfOrNull{rate(it)?:0.0}?.coerceAtLeast(1.0)?:1.0
            val tone=if(index==0) p.signal else p.signalDeep
            NexusCard(Modifier.weight(1f)){
                Column(Modifier.padding(start=Space.md,end=Space.md,top=if(compact) 4.dp else 10.dp)){
                    Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(6.dp)){
                        androidx.compose.foundation.layout.Box(Modifier.size(22.dp).background(tone.copy(alpha=.16f),Shapes.pill),
                            contentAlignment=androidx.compose.ui.Alignment.Center){
                            GlyphIcon(if(index==0) Glyph.Down else Glyph.Up,size=13.dp,tint=if(index==0) c.primary else lerp(tone,c.onSurface,.25f)) }
                        Text(label,color=c.onSurfaceVariant,style=MaterialTheme.typography.labelMedium)
                    }
                    Text(if(latest==null)"—" else java.lang.String.format(java.util.Locale.ROOT,if(latest>=1e6)"%.2f MB/s" else "%.1f KB/s",latest/if(latest>=1e6)1e6 else 1e3),
                        Modifier.padding(top=Space.xxs),style=numberStyle.copy(fontSize=18.sp,lineHeight=24.sp),fontWeight=FontWeight.SemiBold)
                }
                Canvas(Modifier.fillMaxWidth().height(if(compact) 10.dp else 20.dp)){
                    val end=samples.lastOrNull()?.at?:return@Canvas
                    val points=samples.mapNotNull { s -> rate(s)?.let { Offset(size.width*(1f-(end-s.at)/24000f),size.height-2f-(it/peak*(size.height-6f)).toFloat()) } }
                    if(points.size<2) return@Canvas
                    val line=Path().apply { points.forEachIndexed { i,o -> if(i==0) moveTo(o.x,o.y) else lineTo(o.x,o.y) } }
                    val area=Path().apply { addPath(line);lineTo(points.last().x,size.height);lineTo(points.first().x,size.height);close() }
                    drawPath(area,Brush.verticalGradient(listOf(tone.copy(alpha=.28f),tone.copy(alpha=0f))))
                    drawPath(line,tone,style=Stroke(1.8.dp.toPx(),cap=StrokeCap.Round,join=StrokeJoin.Round))
                }
            }
        }
    }
}

@Composable
internal fun ModeSwitch(bypass:Boolean, select:(Boolean)->Unit) {
    Segment(listOf(SplitRouting.label(true),SplitRouting.label(false)),if(bypass)0 else 1){select(it==0)}
}

@Composable
internal fun SectionIcon(index:Int, iconSize:androidx.compose.ui.unit.Dp=22.dp) {
    val ink=LocalContentColor.current
    Canvas(Modifier.size(iconSize)) {
        val w=size.width
        val pen=w*.085f
        fun line(x:Float,y:Float,a:Float,b:Float)=drawLine(ink,Offset(w*x,w*y),Offset(w*a,w*b),pen)
        when(index) {
            0 -> { drawArc(ink,-52f,284f,false,Offset(w*.13f,w*.15f),androidx.compose.ui.geometry.Size(w*.74f,w*.74f),style=Stroke(pen));line(.5f,.02f,.5f,.51f) }
            1 -> for(y in listOf(.2f,.5f,.8f)){drawCircle(ink,w*.065f,Offset(w*.13f,w*y));line(.34f,y,.93f,y)}
            3 -> { drawRoundRect(ink,Offset(w*.25f,w*.05f),androidx.compose.ui.geometry.Size(w*.5f,w*.9f),androidx.compose.ui.geometry.CornerRadius(w*.09f),style=Stroke(pen));line(.45f,.82f,.55f,.82f) }
            else -> for(y in listOf(.12f,.58f)){drawRect(ink,Offset(w*.1f,w*y),androidx.compose.ui.geometry.Size(w*.8f,w*.30f),style=Stroke(pen));drawCircle(ink,w*.04f,Offset(w*.28f,w*(y+.15f)))}
        }
    }
}
