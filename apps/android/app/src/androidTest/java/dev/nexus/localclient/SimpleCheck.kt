package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.os.Bundle
import android.util.Base64
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

private fun sampleInvite():String {
    val sample=JSONObject().put("v",2).put("host","friend.example").put("port",443)
        .put("invite", Base64.encodeToString(ByteArray(32), Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING))
    return "nexus://invite/v2#"+Base64.encodeToString(sample.toString().toByteArray(),Base64.NO_WRAP or Base64.URL_SAFE or Base64.NO_PADDING)
}

internal fun Instrumentation.simpleCheck(args:Bundle,result:Bundle) {
    val ctx=targetContext
    val mode=args.getString("mode")
    if (mode=="simple") {
        val good=sampleInvite()
        check(invitationMatrix(good)?.second==true)
        check(invitationMatrix("a".repeat(1800))?.second==false)
        check(invitationMatrix("a".repeat(4000))==null)
        result.putString("qr","standard branded, long unbranded and oversize share-only checked")
        // Clipboard subscription prompt: only clear subscription addresses qualify.
        check(listOf("https://panel.example/api/v1/client/subscribe?token=abc","https://a.example/link/AbC?sub=3",
            "https://b.example/sub/xyz","https://c.example/s?flag=clash&token=1","https://d.example/feed/a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6",
            "https://e.example/x/9f8e7d6c5b4a39281706f5e4d3c2b1a0").all { subscriptionLink(it)!=null })
        check(listOf("https://news.example/article/1","http://a.example/sub/x","https://a.example/sub x","nexus://invite/x",
            "https://a.example/subway/map","https://blog.example/how-to-set-up-a-home-network-2024").none { subscriptionLink(it)!=null })
        val links=Subscriptions(ctx).all().filter { it.url.isNotEmpty() }
        result.putString("clipboard-sources","recognised ${links.count { subscriptionLink(it.url)!=null }} / ${links.size} saved subscription links")
        if(args.getString("shape")=="yes") links.forEachIndexed { i,s -> val u=android.net.Uri.parse(s.url)
            fun mask(v:String)=if(v.matches(Regex("[A-Za-z]{1,12}"))) v else "<${v.length}>"
            result.putString("shape$i",u.scheme+"://<host>/"+u.pathSegments.joinToString("/",transform=::mask)+
                (u.queryParameterNames.takeIf { it.isNotEmpty() }?.joinToString("&","?") { mask(it)+"=<v>" } ?: "")) }
        // Reinstalled-server path: forgetting drops only that host's pin, key and readiness.
        val owners=ctx.getSharedPreferences("owner-access",0)
        val saved=owners.all.toMap()
        try {
            val pin="SHA256:"+"A".repeat(43)
            check(owners.edit().putString("key/192.0.2.7",JSONObject().put("pin",pin).toString()).putBoolean("ready/192.0.2.7",true)
                .putString("key/192.0.2.8",JSONObject().put("pin",pin).toString()).putString("current","192.0.2.7").commit())
            val access=OwnerAccess(ctx)
            access.forget("192.0.2.7")
            check(access.knownPin("192.0.2.7")==null && "192.0.2.7" !in access.hosts && access.current==null && access.knownPin("192.0.2.8")==pin)
        } finally {
            owners.edit().clear().apply { saved.forEach { (k,v) -> when(v) { is String -> putString(k,v); is Boolean -> putBoolean(k,v) } } }.commit()
        }
        result.putString("simple","clipboard recognition, invitation QR and owner identity removal checked")
        return
    }
    if(mode=="simple-overview" || mode=="simple-overview-actual") {
        val home=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        try {
            val store=Subscriptions(ctx)
            val now=System.currentTimeMillis()
            if(mode=="simple-overview") {
                fun demoNodes(count:Int)=JSONArray().apply {
                    repeat(count) { put(JSONObject().put("type","demo").put("name","演示线路 "+(it+1))) }
                }
                val sources=listOf(
                    Subscription("overview-demo-a","主力订阅","",demoNodes(3),
                        "upload=0; download=98784247808; total=107374182400; expire="+(now/1000+2*86400),now),
                    Subscription("overview-demo-b","备用订阅","",demoNodes(2),"",now-3600000))
                val rows=sourceRows(sources)
                val problems=mapOf(rows.last().favorite to "不支持")
                val probes=mapOf(rows[0].favorite to ProbeResult(50,"","",now),rows[1].favorite to ProbeResult(65,"","",now),
                    rows[3].favorite to ProbeResult(40,"","",now-25*3600000))
                runOnMainSync { home.setContent { NexusTheme { SimpleShell("连接",{}, {}) {
                    ConnectionHome(TunnelState(),false,true,"自动选最快节点",true,"",{},{},{},{},{}) {
                        SubscriptionOverview(sources,rows,problems,store,probes,now,onAdd={},onSource={})
                    }
                } } } }
                splitLabel("订阅总览",true)
                splitLabel("主力订阅",true)
            } else {
                splitLabel("订阅总览",true)
                splitLabel("Airport-Test",true)
            }
            uiAutomation.waitForIdle(250,3000)
            val shot=requireNotNull(uiAutomation.takeScreenshot())
            try { File(ctx.cacheDir,"simple-overview.png").outputStream().use { check(shot.compress(android.graphics.Bitmap.CompressFormat.PNG,100,it)) } }
            finally { shot.recycle() }
            if(mode=="simple-overview") {
                splitLabel("支持 3 / 3 · 真连通过 2",true)
                splitLabel("备用订阅",true)
                splitLabel("支持 1 / 2 · 未测试",true)
                splitLabel("合计 · 2 个来源 · 支持 4 / 5 · 真连通过 2",true)
                result.putString("overview","two sources; support 4/5; 24-hour passes 2; stale result excluded; total visible")
            } else {
                clickSplitLabel("添加订阅",true)
                splitLabel("粘贴并添加",false)
                clickSplitLabel("关闭")
                splitLabel("节点列表",false)
                uiAutomation.waitForIdle(250,3000)
                check(uiAutomation.performGlobalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_BACK))
                splitLabel("订阅总览",true)
                clickSplitLabel("Airport-Test")
                splitLabel("图标或简称",false)
                splitLabel("Airport-Test",true)
                result.putString("overview","Airport-Test visible on launch; add opened the importer; source opened its management dialog")
            }
        } finally {runOnMainSync {home.finish()}}
        return
    }
    if(mode=="simple-frames"){
        val activity=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        val ready=File(ctx.cacheDir,"signal-frames-ready")
        try {
            runOnMainSync { activity.window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);activity.setContent { NexusTheme { SimpleShell("连接",{}, {}) {
                ConnectionHome(TunnelState(vpn=true,connectedAt=1234L),false,true,"演示线路",true,"",{},{},{},{},{},region="SG",nodeInfo="演示订阅")
            } } } }
            splitLabel("网络受保护",false)
            Thread.sleep(1500);ready.writeText("ready")
            Thread.sleep(12000)
            if(args.getString("sleep")=="yes"){
                android.os.ParcelFileDescriptor.AutoCloseInputStream(uiAutomation.executeShellCommand("input keyevent 223")).use {it.readBytes()}
            } else runOnMainSync { activity.moveTaskToBack(true) }
            Thread.sleep(1000);File(ctx.cacheDir,"signal-frames-hidden").writeText("hidden");Thread.sleep(4000)
            check(!activity.lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED))
            result.putString("visibility","connected UI suspended; no VPN requested")
        } finally {ready.delete();File(ctx.cacheDir,"signal-frames-hidden").delete();runOnMainSync { activity.finish() }}
        return
    }
    if(mode=="simple-motion") {
        val activity=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        val state=androidx.compose.runtime.mutableStateOf(TunnelState())
        try {
            runOnMainSync { activity.setContent { NexusTheme { SimpleShell("连接",{}, {}) {
                ConnectionHome(state.value,false,true,"自动选最快节点",true,"",toggle={
                    state.value=if(state.value.active) TunnelState() else TunnelState(starting=true)
                },pick={},mode={},addSubscription={},addServer={})
            } } } }
            splitLabel("未连接",false)
            File(ctx.cacheDir,"simple-motion-ready").writeText("ready")
            Thread.sleep(1200)
            clickSplitLabel("连接")
            splitLabel("正在连接",false)
            Thread.sleep(700)
            runOnMainSync { state.value=TunnelState(vpn=true,connectedAt=android.os.SystemClock.elapsedRealtime()) }
            splitLabel("已连接",false)
            Thread.sleep(1500)
            clickSplitLabel("断开")
            splitLabel("未连接",false)
            Thread.sleep(600)
            result.putString("motion","idle, starting, connected, disconnected; simulated state, no VPN requested")
        } finally { runOnMainSync { activity.finish() } }
        return
    }
    if(mode=="simple-shots") {
        val home=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        runOnMainSync {home.window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)}
        fun shot(name:String,label:String) {
            splitLabel(label,false)
            Thread.sleep(if(name=="invite")2200 else 500)
            val image=requireNotNull(uiAutomation.takeScreenshot())
            try {
                File(ctx.cacheDir,"simple-$name.png").outputStream().use { check(image.compress(android.graphics.Bitmap.CompressFormat.PNG,100,it)) }
                if(args.getString("export")=="yes"){
                    val root=home.window.decorView
                    val top=(root.rootWindowInsets?.stableInsetTop ?: 0).coerceIn(0,image.height-1)
                    val bottom=(root.rootWindowInsets?.stableInsetBottom ?: 0).coerceIn(0,image.height-top-1)
                    val crop=android.graphics.Bitmap.createBitmap(image,0,top,image.width,image.height-top-bottom)
                    try{
                        val dir=File(requireNotNull(ctx.getExternalFilesDir(null)),"qa-shots").apply {mkdirs()}
                        File(dir,"$name.png").outputStream().use {check(crop.compress(android.graphics.Bitmap.CompressFormat.PNG,100,it))}
                    }finally{if(crop!==image)crop.recycle()}
                }
                if(name=="invite"){
                    val bounds=android.graphics.Rect()
                    splitLabel("邀请二维码",false).getBoundsInScreen(bounds)
                    val w=bounds.width();val h=bounds.height()
                    val pixels=IntArray(w*h)
                    image.getPixels(pixels,0,w,bounds.left,bounds.top,w,h)
                    val source=com.google.zxing.RGBLuminanceSource(w,h,pixels)
                    val input=com.google.zxing.BinaryBitmap(com.google.zxing.common.HybridBinarizer(source))
                    check(com.google.zxing.MultiFormatReader().decode(input,mapOf(com.google.zxing.DecodeHintType.TRY_HARDER to true)).text==sampleInvite())
                }
                }
            finally {image.recycle()}
        }
        try {
            splitLabel("设置",false)
            clickSplitLabel("设置");shot("settings","分流模式");clickSplitLabel("返回")
            clickSplitLabel("节点");splitLabel("添加节点",false)
            clickSplitLabel("添加节点");clickSplitLabel("导入设备邀请",true);splitLabel("粘贴邀请消息",false)
            clickSplitLabel("返回")
            clickSplitLabel("我的服务器")
            shot("server", if(OwnerAccess(ctx).current==null) "服务器 IP" else "已授权设备")
            // The receiving page is the real deep-link entry. Never redeem the synthetic invitation.
            val link=sampleInvite()
            val receiver=startActivitySync(Intent(Intent.ACTION_VIEW,android.net.Uri.parse(link),ctx,MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            try {
                runOnMainSync {receiver.window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)}
                runOnMainSync { receiver.setContent { NexusTheme { ShareInvite(link,listOf("7 天","一次领取")) {} } } }
                shot("invite","发送邀请")
                fun connectionSample(first:Boolean,state:TunnelState=TunnelState()) = runOnMainSync { receiver.setContent { NexusTheme {
                    SimpleShell("连接",{}, {}) {
                        ConnectionHome(state,first,true,"演示线路",true,state.error,{},{},{},{},{},receive={},nodeInfo="演示订阅",region="SG")
                    }
                } } }
                val demo=object:android.content.ContextWrapper(receiver){
                    override fun getSharedPreferences(name:String,mode:Int)=baseContext.getSharedPreferences("qa-shots-$name",mode)
                }
                val store=Subscriptions(demo)
                store.all().forEach {store.remove(it.id)}
                try{
                    store.import("演示订阅",listOf("香港 · 标准","新加坡 · 标准","东京 · 标准").joinToString("\n"){
                        "trojan://sample@demo.test:443?security=tls#"+android.net.Uri.encode(it)
                    })
                    runOnMainSync { receiver.setContent { NexusTheme { SimpleShell("节点",{},{}) {
                        androidx.compose.runtime.CompositionLocalProvider(androidx.compose.ui.platform.LocalContext provides demo){
                            SubscriptionScreen(true,true,onInvite={}){}
                        }
                    } } } }
                    shot("nodes","香港 · 标准")
                }finally{store.all().forEach {store.remove(it.id)}}
                connectionSample(true);shot("first","添加机场订阅")
                connectionSample(false,TunnelState(starting=true));shot("connecting","正在连接")
                connectionSample(false,TunnelState(vpn=true,connectedAt=android.os.SystemClock.elapsedRealtime()-65000));shot("connect","已连接")
                connectionSample(false,TunnelState(vpn=true,error="演示：节点没有响应，请换一个节点再试"));shot("failed","节点不可用")
            } finally { runOnMainSync { receiver.finish() } }
            result.putString("screens","6 screens, connecting and failed; synthetic connection state; no VPN requested")
        } finally { runOnMainSync { home.finish() } }
        return
    }
}
