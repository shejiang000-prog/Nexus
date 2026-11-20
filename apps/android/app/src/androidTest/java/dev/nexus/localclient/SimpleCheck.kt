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
import java.net.InetAddress
import java.security.KeyStore

private fun sampleInvite():String {
    val sample=JSONObject().put("v",1).put("name","家庭网络").put("url","https://invite.test/claim")
        .put("pin","0".repeat(64)).put("token","X".repeat(43))
    return "nexus://invite/"+Base64.encodeToString(sample.toString().toByteArray(),Base64.NO_WRAP or Base64.URL_SAFE or Base64.NO_PADDING)
}

internal fun Instrumentation.simpleCheck(args:Bundle,result:Bundle) {
    val ctx=targetContext
    val mode=args.getString("mode")
    if (mode=="simple") {
        val good=sampleInvite()
        check(sharedInvitation(good)!=null && sharedInvitation(INVITE_MESSAGE+"\n"+good)!=null)
        check(listOf("nexus://invite/?test",good+"?x",good.replaceFirst("nexus:","https:"),"https://example.test/",good+"\n"+good).none { sharedInvitation(it)!=null })
        check(invitationMatrix(good)?.second==true)
        check(invitationMatrix("a".repeat(1800))?.second==false)
        check(invitationMatrix("a".repeat(4000))==null)
        result.putString("qr","standard branded, long unbranded and oversize share-only checked")
        val limits=invitationLimits()
        check(limits.getInt("valid_seconds")==604800 && limits.getInt("device_seconds")==604800 && limits.getInt("max_devices")==1 && limits.isNull("quota"))
        check(usageAmount(0)=="0.00 MB" && usageAmount(1500000)=="1.50 MB" && usageAmount(1000000000)=="1.00 GB")
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
        result.putString("simple","clipboard accepts 2 forms, rejects 5; 7 days / 1 device / unlimited; decimal MB and GB")
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
            clickSplitLabel("节点");shot("nodes","添加节点")
            clickSplitLabel("添加节点");clickSplitLabel("导入设备邀请",true);splitLabel("粘贴邀请消息",false)
            clickSplitLabel("返回")
            clickSplitLabel("我的服务器")
            splitLabel(if(OwnerAccess(ctx).current==null)"服务器 IP" else "已用本机密钥绑定，管理时不用再输密码",true)
            val sampleDevices=JSONArray().put(JSONObject().put("name","我的手机").put("used",375000000L).put("quota",1000000000L).put("revoked",0))
            @androidx.compose.runtime.Composable fun serverSample(){
                SimpleShell("我的服务器",{},{}){
                    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp),verticalArrangement=Arrangement.spacedBy(12.dp)){
                        ServerSummary("家庭网络",sampleDevices){}
                        PrimaryAction({},Modifier.fillMaxWidth()){Text("邀请朋友或我的设备")}
                        Text("已授权设备");DeviceCard(sampleDevices.getJSONObject(0),false){}
                        SecondaryAction({}){Text("高级")}
                    }
                }
            }
            runOnMainSync {home.setContent { NexusTheme {serverSample()} }}
            shot("server","已用本机密钥绑定，管理时不用再输密码")
            // The receiving page is the real deep-link entry. Never redeem the synthetic invitation.
            val link=sampleInvite()
            val receiver=startActivitySync(Intent(Intent.ACTION_VIEW,android.net.Uri.parse(link),ctx,MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            try {
                runOnMainSync {receiver.window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)}
                shot("receive","收到一个邀请")
                runOnMainSync { receiver.setContent { NexusTheme { Surface(Modifier.fillMaxSize()) { serverSample();ShareInvite(link,listOf("7 天","1 台","不限")) {} } } } }
                shot("invite","发送邀请")
                fun connectionSample(first:Boolean,state:TunnelState=TunnelState()) = runOnMainSync { receiver.setContent { NexusTheme {
                    SimpleShell("连接",{}, {}) {
                        ConnectionHome(state,first,true,"演示线路",true,"",{},{},{},{},{},receive={},nodeInfo="演示订阅",region="SG")
                    }
                } } }
                connectionSample(true);shot("first","添加机场订阅")
                connectionSample(false,TunnelState(starting=true));shot("connecting","正在连接")
                connectionSample(false,TunnelState(vpn=true,connectedAt=android.os.SystemClock.elapsedRealtime()-65000));shot("connect","已连接")
            } finally { runOnMainSync { receiver.finish() } }
            result.putString("screens","6 screens and connecting; synthetic connection state; no VPN requested")
        } finally { runOnMainSync { home.finish() } }
        return
    }
    val host=requireNotNull(args.getString("host"))
    require(android.util.Patterns.IP_ADDRESS.matcher(host).matches() && InetAddress.getByName(host).isSiteLocalAddress)
    val access=OwnerAccess(ctx)
    val inbox=File(ctx.noBackupFilesDir,"simple-lab-invite")
    if(mode=="simple-host") {
        check(runCatching { access.manager(host).owner("devices") }.exceptionOrNull() is ChangedServer)
        result.putString("pin",friendlyError(ChangedServer()));return
    }
    if(mode=="simple-guest") {
        check(!TunnelService.state.value.active && android.net.VpnService.prepare(ctx)==null)
        val keys=KeyStore.getInstance("AndroidKeyStore").apply {load(null)}
        val guest=Identity.rotate();val alias=requireNotNull(Identity.owner(guest))
        check(alias.startsWith("nexus-device-v1-"))
        try {
            val home=startActivitySync(Intent(Intent.ACTION_VIEW,android.net.Uri.parse(inbox.readText()),ctx,MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            try {
                clickSplitLabel("接受并连接",true)
                runBlocking { withTimeout(45000) { TunnelService.state.first { it.connectedAt>0L } } }
                val m=access.manager(host)
                val devices=JSONArray(m.owner("devices"))
                val row=devices.getJSONObject(0)
                check(row.getString("name")==android.os.Build.MODEL)
                m.owner("revoke",JSONObject().put("id",row.getString("id")))
                runBlocking { withTimeout(95000) { TunnelService.state.first { it.connectedAt==0L } } }
                result.putString("guest","second identity claimed, saved, connected, then lost authenticated connection after revoke")
            } finally { ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
                runBlocking {withTimeout(10000){TunnelService.state.first { !it.active }}}
                runOnMainSync { home.finish() } }
        } finally { keys.deleteEntry(alias) }
        return
    }
    val secret=File(ctx.noBackupFilesDir,"simple-lab-secret")
    val pass=try { secret.readText() } finally { secret.delete() }
    val create=mode=="simple-owner"
    val pin=access.checkHost(host)
    val p=access.enroll(host,pin,pass,create,requireNotNull(args.getString("dns")),8443,8444)
    if(create) requireNotNull(p).save(ctx)
    val reopened=OwnerAccess(ctx)
    val m=reopened.manager(host)
    check(JSONArray(m.owner("devices")).length()>0)
    if(create) inbox.writeText(JSONObject(m.owner("invite",invitationLimits())).getString("link"))
    result.putString("owner",if(create) "deployed, invited, reopened with local key" else "bound existing installation and listed devices using local key")
}
