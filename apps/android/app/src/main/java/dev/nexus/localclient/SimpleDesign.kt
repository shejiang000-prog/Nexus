package dev.nexus.localclient

import androidx.compose.animation.*
import androidx.compose.animation.core.*
import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.*

internal val shellTabs=listOf("连接","节点","我的服务器")

/**
 * How deep a page sits below the tabs. Moving deeper slides the new page in from the right,
 * going back slides it in from the left, and switching tabs fades it up in place.
 * New pages (devices, invitations, expiry) only need an entry here.
 */
internal fun pageDepth(page:String)=when(page) {
    in shellTabs -> 0
    "设置","服务器高级","领取" -> 1
    else -> 2
}

@Composable
internal fun SimpleShell(page:String,navigate:(String)->Unit,back:()->Unit,content:@Composable (String)->Unit) {
    val tabs=shellTabs
    val c=MaterialTheme.colorScheme
    val motion=LocalMotion.current
    var shown by remember { mutableStateOf(page) }
    var progress by remember { mutableFloatStateOf(0f) }
    var direction by remember { mutableIntStateOf(0) }
    LaunchedEffect(page) {
        if(shown!=page) {
            direction=Integer.signum(pageDepth(page)-pageDepth(shown))
            val start=progress
            frameProgress(if(motion)Motion.exit else Motion.reduced){progress=start*(1-exitCurve.transform(it))}
            shown=page
        }
        frameProgress(if(motion)Motion.emphasized else Motion.reduced){progress=enterCurve.transform(it)}
    }
    Scaffold(containerColor=c.background,topBar={
        Row(Modifier.fillMaxWidth().statusBarsPadding().heightIn(min=56.dp).padding(horizontal=Space.page),
            verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.SpaceBetween) {
            if(shown=="连接") Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.sm)){
                Mark(Modifier.size(26.dp));Text("Nexus",fontSize=21.sp,fontFamily=numberFont,fontWeight=FontWeight.Bold)
            } else if(shown in tabs) Text(shown,style=MaterialTheme.typography.headlineMedium)
            else TextButton(back,contentPadding=PaddingValues(start=Space.xs,end=Space.md)){
                GlyphIcon(Glyph.Back,size=20.dp,tint=c.onSurface);Spacer(Modifier.width(6.dp))
                Text("返回",color=c.onSurface,style=MaterialTheme.typography.labelLarge)
            }
            if(shown in tabs) IconButton({navigate("设置")},modifier=Modifier.semantics{contentDescription="设置"}) {
                GlyphIcon(Glyph.Settings,tint=c.onSurface)
            }
        }
    },bottomBar={if(shown in tabs) Column(Modifier.background(c.surface).navigationBarsPadding()){
        HorizontalDivider(color=c.outlineVariant)
        Row {tabs.forEachIndexed{i,label->val selected=shown==label
            val pill by animateDpAsState(if(selected) 60.dp else 28.dp,motionSpring(Spring.StiffnessMediumLow,.7f),label="标签胶囊宽度")
            val fill by animateColorAsState(if(selected) signalSoft else signalSoft.copy(alpha=0f),motionTween(Motion.standard),label="标签胶囊颜色")
            val ink by animateColorAsState(if(selected) c.primary else c.onSurfaceVariant,motionTween(Motion.standard),label="标签颜色")
            Column(Modifier.weight(1f).selectable(selected,role=Role.Tab){navigate(label)}.padding(vertical=Space.sm),
                horizontalAlignment=Alignment.CenterHorizontally) {
                Box(Modifier.height(32.dp).width(60.dp),contentAlignment=Alignment.Center){
                    Box(Modifier.size(pill,32.dp).background(fill,Shapes.pill))
                    CompositionLocalProvider(LocalContentColor provides ink){SectionIcon(i)} }
                Text(label,color=ink,style=MaterialTheme.typography.labelMedium.copy(fontSize=13.sp,lineHeight=18.sp,
                    fontWeight=if(selected) FontWeight.SemiBold else FontWeight.Medium))
            }
        }}
    }}){insets->Box(Modifier.fillMaxSize().padding(insets),contentAlignment=Alignment.TopCenter){
        Box(Modifier.widthIn(max=760.dp).fillMaxSize().graphicsLayer{
            alpha=progress
            val travel=(1-progress)*(if(motion) 1f else 0f)
            if(direction==0) translationY=travel*12.dp.toPx() else translationX=direction*travel*28.dp.toPx()
        }){
            key(shown){content(shown)}
        }
    }}
}

/** A 0..1 wave for "working" indicators; flat when motion is reduced or the app is hidden. */
@Composable
internal fun rememberPulse(running:Boolean,periodMs:Int=1200):State<Float> {
    val motion=LocalMotion.current
    val active=LocalForeground.current
    val value=remember { mutableFloatStateOf(0f) }
    LaunchedEffect(running,motion,active) {
        value.floatValue=0f
        if(running && motion && active) { val start=withFrameNanos { it }
            while(true) withFrameNanos { value.floatValue=(.5f-.5f*kotlin.math.cos(2*Math.PI*((it-start)/1e6%periodMs)/periodMs)).toFloat() } }
    }
    return value
}

/** The status capsule under the dial: colour follows the connection state, text slides between states. */
@Composable
internal fun StatusPill(link:Link,status:String) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val spec=motionTween<Color>(Motion.emphasized)
    val container by animateColorAsState(when(link){Link.On->p.signalSoft;Link.Busy->p.warningSoft;Link.Failed->p.dangerSoft;Link.Idle->c.surfaceVariant},spec,label="状态底色")
    val ink by animateColorAsState(when(link){Link.On->c.primary;Link.Busy->p.warning;Link.Failed->p.danger;Link.Idle->c.onSurfaceVariant},spec,label="状态文字")
    val dot by animateColorAsState(link.tone(),spec,label="状态点")
    val pulse by rememberPulse(link==Link.Busy)
    val motion=LocalMotion.current
    Surface(shape=Shapes.pill,color=container) {
        Row(Modifier.padding(horizontal=14.dp,vertical=6.dp).animateContentSize(motionTween(Motion.standard)),
            verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.sm)){
            Box(Modifier.size(8.dp).graphicsLayer{val s=1f+.5f*pulse;scaleX=s;scaleY=s;alpha=1f-.45f*pulse}.background(dot,CircleShape))
            AnimatedContent(status,transitionSpec={
                (fadeIn(tweenFor(motion))+slideInVertically(tweenFor(motion)){ if(motion) it/2 else 0 }) togetherWith
                    (fadeOut(tweenFor(motion,Motion.quick,Motion.leave))+slideOutVertically(tweenFor(motion,Motion.quick,Motion.leave)){ if(motion) -it/2 else 0 }) using
                    SizeTransform(clip=false){ _,_ -> tweenFor(motion) }
            },label="连接状态") { text -> Text(text,color=ink,style=MaterialTheme.typography.labelLarge.copy(fontSize=15.sp)) }
        }
    }
}

/** The connected-time readout; reading the clock here keeps the per-second tick out of the rest of the page. */
@Composable
private fun ClockText(time:()->String) { Text(time(),style=MaterialTheme.typography.displaySmall) }

/** Shown while the connection page reads saved sources off the main thread. */
@Composable
internal fun HomeSkeleton() {
    Column(Modifier.fillMaxSize().padding(horizontal=Space.page,vertical=Space.sm),horizontalAlignment=Alignment.CenterHorizontally,
        verticalArrangement=Arrangement.spacedBy(Space.md)) {
        Box(Modifier.size(248.dp),contentAlignment=Alignment.Center){
            val line=palette.line
            Canvas(Modifier.fillMaxSize()){ for(i in 0..2) drawCircle(line,size.minDimension/2*(126-18*i)/130f,style=Stroke(1.5.dp.toPx())) }
            Box(Modifier.size(132.dp).shimmer(CircleShape))
        }
        SkeletonBlock(120.dp,30.dp,shape=Shapes.pill)
        SkeletonBlock(160.dp,20.dp)
        Spacer(Modifier.height(Space.sm))
        SkeletonBlock(null,72.dp,shape=Shapes.card)
        SkeletonBlock(null,52.dp,shape=Shapes.control)
    }
}

@Composable
internal fun ConnectionHome(state:TunnelState,first:Boolean,enabled:Boolean,node:String,bypass:Boolean,notice:String,
    toggle:()->Unit,pick:()->Unit,mode:(Boolean)->Unit,addSubscription:()->Unit,addServer:()->Unit,
    receive:(()->Unit)?=null,nodeInfo:String="",region:String="",latency:Long?=null,overview:(@Composable ()->Unit)?=null) {
    val c=MaterialTheme.colorScheme
    val p=palette
    if(first){
        val hero:@Composable ()->Unit={
            Column(horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(10.dp)){
                Box(Modifier.size(176.dp),contentAlignment=Alignment.Center){
                    Canvas(Modifier.fillMaxSize()){
                        drawCircle(Brush.radialGradient(listOf(p.signal.copy(alpha=.22f),Color.Transparent)),size.width/2)
                        for(i in 0..2)drawCircle(p.line,size.width*(.48f-.075f*i),style=Stroke(1.dp.toPx()))
                    }
                    Box(Modifier.size(76.dp).background(c.onSurface,CircleShape),contentAlignment=Alignment.Center){Mark(Modifier.size(38.dp),c.background)}
                }
                Text("欢迎使用 Nexus",style=MaterialTheme.typography.headlineMedium)
                Text("先添加一个节点来源。之后每次打开，点一下就能连接。",textAlign=TextAlign.Center,color=c.onSurfaceVariant)
            }
        }
        val actions:@Composable ()->Unit={
            Column(horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(14.dp)){
                ListEntrance(1){ActionRow("添加机场订阅","粘贴订阅链接，或扫二维码",1,addSubscription)}
                ListEntrance(2){ActionRow("接入我的服务器","使用服务器地址和密码绑定",2,addServer)}
                if(receive!=null) TextButton(receive){Text("收到了邀请？扫码或粘贴",color=c.onSurfaceVariant)}
                Text("节点和密钥只保存在这台手机上",color=c.onSurfaceVariant,style=MaterialTheme.typography.detail)
                if(notice.isNotBlank())Notice(notice)
            }
        }
        BoxWithConstraints(Modifier.fillMaxSize()){
            val height=maxHeight
            if(maxWidth>=600.dp && maxWidth>height)Row(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).heightIn(min=height).padding(Space.page),
                verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.xxl)){
                Box(Modifier.weight(1f),contentAlignment=Alignment.Center){ListEntrance(0){hero()}};Box(Modifier.weight(1.2f)){actions()}
            }else Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).heightIn(min=height).padding(Space.page),
                horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.SpaceEvenly){ListEntrance(0){hero()};Spacer(Modifier.height(Space.xl));actions()}
        }
        return
    }
    val link=state.link()
    val on=link==Link.On
    val status=when {state.waiting->"等待网络";state.starting->"正在连接…";state.error.isNotBlank() && state.vpn->"节点不可用"
        !state.vpn && state.port>0->"本地转发已开启";on->"已连接 · 网络受保护";state.vpn->"正在连接…";else->"未连接"}
    val traffic=rememberTraffic(state)
    val motion=LocalMotion.current
    val dial:@Composable ()->Unit={
        Column(Modifier.fillMaxWidth(),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(6.dp)){
            Orbit(state,enabled,toggle)
            StatusPill(link,status)
            AnimatedContent(on,transitionSpec={
                (fadeIn(tweenFor(motion,Motion.emphasized))+scaleIn(tweenFor(motion,Motion.emphasized),initialScale=if(motion) .92f else 1f)) togetherWith
                    fadeOut(tweenFor(motion,Motion.quick,Motion.leave)) using SizeTransform(clip=false){ _,_ -> tweenFor(motion) }
            },contentAlignment=Alignment.Center,label="计时与提示") { connected ->
                if(connected) ClockText { traffic.value.time }
                else Text(if(enabled)"点按圆环即可"+(if(state.active)"断开" else "连接") else "请先选择可用节点",Modifier.padding(vertical=Space.xs),
                    color=c.onSurfaceVariant,style=MaterialTheme.typography.bodyMedium)
            }
        }
    }
    val details:@Composable ()->Unit={
        CurrentNode(region,displayName(node).ifBlank{"选择节点"},nodeInfo,latency,region=="NX",pick)
        ModeSwitch(bypass,mode)
        AnimatedContent(if(bypass && state.shoes)SplitRouting.SHOES else if(bypass)"国内网站直接访问，其余走加密通道" else "所有网站都走当前节点",
            transitionSpec={ fadeThrough(motion) },label="分流说明") { text ->
            Text(text,Modifier.fillMaxWidth(),color=c.onSurfaceVariant,style=MaterialTheme.typography.detail,textAlign=TextAlign.Center)
        }
        AnimatedVisibility(on,enter=fadeIn(motionTween(Motion.emphasized))+expandVertically(motionTween(Motion.emphasized)),
            exit=fadeOut(motionTween(Motion.exit,Motion.leave))+shrinkVertically(motionTween(Motion.exit,Motion.leave))) { RateTiles { traffic.value } }
        if(state.port>0 && !state.vpn)Text("本地端口 "+state.port,style=numberStyle)
        // Keep the last message while the notice collapses, so it does not blank out mid-animation.
        val failed=state.error.isNotEmpty()
        var lastNotice by remember { mutableStateOf(notice to failed) }
        if(notice.isNotBlank()) SideEffect { lastNotice=notice to failed }
        AnimatedVisibility(notice.isNotBlank(),enter=fadeIn(motionTween(Motion.standard))+expandVertically(motionTween(Motion.standard)),
            exit=fadeOut(motionTween(Motion.quick,Motion.leave))+shrinkVertically(motionTween(Motion.standard,Motion.slide))) {
            val (text,error)=if(notice.isNotBlank()) notice to failed else lastNotice
            Notice(text,error)
        }
        if(overview!=null){Spacer(Modifier.height(10.dp));overview()}
    }
    // A faint wash of the state colour behind the dial; it fades between states with the dial.
    val wash by animateColorAsState(link.tone().copy(alpha=when(link){Link.On->.10f;Link.Busy,Link.Failed->.07f;Link.Idle->0f}),
        motionTween(Motion.emphasized),label="页面底色")
    BoxWithConstraints(Modifier.fillMaxSize().drawBehind {
        if(wash.alpha>0f) {
            val wide=maxOf(size.width,size.height)>=600.dp.toPx() && size.width>size.height
            val centre=androidx.compose.ui.geometry.Offset(if(wide) size.width*.25f else size.width/2,132.dp.toPx())
            drawCircle(Brush.radialGradient(listOf(wash,wash.copy(alpha=wash.alpha*.4f),Color.Transparent),centre,size.width*.8f),size.width*.8f,centre)
        }
    }){
        if(maxWidth>=600.dp && maxWidth>maxHeight)Row(Modifier.fillMaxSize().padding(horizontal=Space.page),horizontalArrangement=Arrangement.spacedBy(Space.xl)){
            Column(Modifier.weight(.85f).verticalScroll(rememberScrollState())){dial()}
            Column(Modifier.weight(1.25f).verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(10.dp)){details()}
        } else Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal=Space.page,vertical=Space.sm),
            verticalArrangement=Arrangement.spacedBy(10.dp)){dial();details()}
    }
}
