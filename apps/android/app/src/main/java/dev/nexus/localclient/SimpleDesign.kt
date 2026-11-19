package dev.nexus.localclient

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.*

@Composable
internal fun SimpleShell(page:String,navigate:(String)->Unit,back:()->Unit,content:@Composable (String)->Unit) {
    val tabs=listOf("连接","节点","我的服务器")
    val c=MaterialTheme.colorScheme
    val motion=LocalMotion.current
    var shown by remember { mutableStateOf(page) }
    var progress by remember { mutableFloatStateOf(0f) }
    LaunchedEffect(page) {
        if(shown!=page) {
            val start=progress
            frameProgress(if(motion)200 else 150){progress=start*(1-exitCurve.transform(it))}
            shown=page
        }
        frameProgress(if(motion)450 else 150){progress=enterCurve.transform(it)}
    }
    Scaffold(containerColor=c.background,topBar={
        Row(Modifier.fillMaxWidth().statusBarsPadding().heightIn(min=56.dp).padding(horizontal=20.dp),
            verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.SpaceBetween) {
            if(shown=="连接") Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)){
                Mark(Modifier.size(26.dp));Text("Nexus",fontSize=21.sp,fontFamily=numberFont,fontWeight=FontWeight.Bold)
            } else if(shown in tabs) Text(shown,style=MaterialTheme.typography.headlineMedium)
            else TextButton(back){Text("‹  返回",color=c.onSurfaceVariant)}
            if(shown in tabs) IconButton({navigate("设置")},modifier=Modifier.semantics{contentDescription="设置"}) {
                Canvas(Modifier.size(22.dp)){val w=size.width;for(i in 0..1){
                    val y=w*(if(i==0).3f else .7f);val x=w*(if(i==0).68f else .38f)
                    drawLine(c.onSurface,androidx.compose.ui.geometry.Offset(.08f*w,y),androidx.compose.ui.geometry.Offset(.92f*w,y),2.dp.toPx())
                    drawCircle(c.background,.11f*w,androidx.compose.ui.geometry.Offset(x,y));drawCircle(c.onSurface,.11f*w,androidx.compose.ui.geometry.Offset(x,y),style=Stroke(1.7.dp.toPx()))
                }}
            }
        }
    },bottomBar={if(shown in tabs) Column(Modifier.background(c.surface).navigationBarsPadding()){
        HorizontalDivider(color=c.outlineVariant)
        Row {tabs.forEachIndexed{i,label->val selected=shown==label
            Column(Modifier.weight(1f).selectable(selected,role=Role.Tab){navigate(label)}.padding(vertical=8.dp),
                horizontalAlignment=Alignment.CenterHorizontally) {
                Box(Modifier.size(60.dp,32.dp).background(if(selected)signalSoft else c.surface,CircleShape),contentAlignment=Alignment.Center){
                    CompositionLocalProvider(LocalContentColor provides if(selected)c.primary else c.onSurfaceVariant){SectionIcon(i)} }
                Text(label,fontSize=13.sp,lineHeight=18.sp,color=if(selected)c.primary else c.onSurfaceVariant)
            }
        }}
    }}){insets->Box(Modifier.fillMaxSize().padding(insets),contentAlignment=Alignment.TopCenter){
        Box(Modifier.widthIn(max=760.dp).fillMaxSize().graphicsLayer{alpha=progress;translationY=if(motion)(1-progress)*12.dp.toPx() else 0f}){
            key(shown){content(shown)}
        }
    }}
}

@Composable
internal fun ConnectionHome(state:TunnelState,first:Boolean,enabled:Boolean,node:String,bypass:Boolean,notice:String,
    toggle:()->Unit,pick:()->Unit,mode:(Boolean)->Unit,addSubscription:()->Unit,addServer:()->Unit,
    receive:(()->Unit)?=null,nodeInfo:String="",region:String="",latency:Long?=null,overview:(@Composable ()->Unit)?=null) {
    val c=MaterialTheme.colorScheme
    if(first){
        val hero:@Composable ()->Unit={
            Column(horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(10.dp)){
                Box(Modifier.size(176.dp),contentAlignment=Alignment.Center){
                    Canvas(Modifier.fillMaxSize()){for(i in 0..2)drawCircle(c.outlineVariant,size.width*(.48f-.075f*i),style=Stroke(1.dp.toPx()))}
                    Box(Modifier.size(76.dp).background(c.onSurface,CircleShape),contentAlignment=Alignment.Center){Mark(Modifier.size(38.dp),c.background)}
                }
                Text("欢迎使用 Nexus",style=MaterialTheme.typography.headlineMedium)
                Text("先添加一个节点来源。之后每次打开，点一下就能连接。",textAlign=TextAlign.Center,color=c.onSurfaceVariant)
            }
        }
        val actions:@Composable ()->Unit={
            Column(horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(14.dp)){
                ActionRow("添加机场订阅","粘贴订阅链接，或扫二维码",1,addSubscription)
                ActionRow("接入我的服务器","使用服务器地址和密码绑定",2,addServer)
                if(receive!=null) TextButton(receive){Text("收到了邀请？扫码或粘贴",color=c.onSurfaceVariant)}
                Text("节点和密钥只保存在这台手机上",color=c.onSurfaceVariant)
                if(notice.isNotBlank())Notice(notice)
            }
        }
        BoxWithConstraints(Modifier.fillMaxSize()){
            val height=maxHeight
            if(maxWidth>=600.dp && maxWidth>height)Row(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).heightIn(min=height).padding(20.dp),
                verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(28.dp)){
                Box(Modifier.weight(1f),contentAlignment=Alignment.Center){hero()};Box(Modifier.weight(1.2f)){actions()}
            }else Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).heightIn(min=height).padding(20.dp),
                horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.SpaceEvenly){hero();Spacer(Modifier.height(20.dp));actions()}
        }
        return
    }
    val on=state.connectedAt>0 && !state.starting && !state.waiting
    val status=when {state.waiting->"等待网络";state.starting->"正在连接…";state.error.isNotBlank() && state.vpn->"节点不可用"
        !state.vpn && state.port>0->"本地转发已开启";on->"已连接 · 网络受保护";state.vpn->"正在连接…";else->"未连接"}
    val traffic=rememberTraffic(state)
    val dial:@Composable ()->Unit={
        Column(Modifier.fillMaxWidth(),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(4.dp)){
            Orbit(state,enabled,toggle)
            Surface(shape=CircleShape,color=if(on)signalSoft else c.surfaceVariant){
                Row(Modifier.padding(horizontal=12.dp,vertical=4.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(6.dp)){
                    Box(Modifier.size(8.dp).background(if(on)signal else if(state.starting)signalAmber else c.outline,CircleShape))
                    Text(status,color=if(on)c.primary else c.onSurfaceVariant)
                }
            }
            if(on)Text(traffic.time,style=numberStyle.copy(fontSize=46.sp,lineHeight=54.sp,fontWeight=FontWeight.Bold))
            else Text(if(enabled)"点按圆环即可"+(if(state.active)"断开" else "连接") else "请先选择可用节点",color=c.onSurfaceVariant)
        }
    }
    val details:@Composable ()->Unit={
        Surface(shape=signalCard,border=BorderStroke(1.dp,c.outlineVariant),color=c.surface){
            Column{Text("当前节点",Modifier.padding(start=14.dp,top=8.dp),color=c.onSurfaceVariant)
                SignalNode(region,node.ifBlank{"选择节点"},nodeInfo,latency,false,"",pick,pick)}
        }
        ModeSwitch(bypass,mode)
        Text(if(bypass && state.shoes)SplitRouting.SHOES else if(bypass)"国内网站直接访问，其余走加密通道" else "所有网站都走当前节点",color=c.onSurfaceVariant)
        RateTiles(traffic)
        if(state.port>0 && !state.vpn)Text("本地端口 "+state.port)
        if(notice.isNotBlank())Notice(notice,state.error.isNotEmpty())
        overview?.invoke()
    }
    BoxWithConstraints(Modifier.fillMaxSize()){
        if(maxWidth>=600.dp && maxWidth>maxHeight)Row(Modifier.fillMaxSize().padding(horizontal=20.dp),horizontalArrangement=Arrangement.spacedBy(20.dp)){
            Column(Modifier.weight(.85f).verticalScroll(rememberScrollState())){dial()}
            Column(Modifier.weight(1.25f).verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(10.dp)){details()}
        } else Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal=20.dp,vertical=8.dp),
            verticalArrangement=Arrangement.spacedBy(10.dp)){dial();details()}
    }
}
