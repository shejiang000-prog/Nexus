package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import org.json.JSONObject

private fun exchange(vpn: Network,ip: String, port: Int, bytes: ByteArray,tcp: Boolean,dns: Boolean=false): ByteArray {
    val address=InetSocketAddress(InetAddress.getByName(ip),port)
    if (tcp) return vpn.socketFactory.createSocket().use { s ->
        s.soTimeout=6500;s.connect(address,6500)
        DataOutputStream(s.getOutputStream()).apply { if(dns) writeShort(bytes.size);write(bytes);flush() }
        val input=DataInputStream(s.getInputStream())
        if (dns) ByteArray(input.readUnsignedShort()).also(input::readFully) else input.bufferedReader().readLine().toByteArray()
    }
    return DatagramSocket().use { s ->
        vpn.bindSocket(s);s.soTimeout=6500;s.connect(address);s.send(DatagramPacket(bytes,bytes.size))
        val reply=DatagramPacket(ByteArray(2048),2048);s.receive(reply);reply.data.copyOf(reply.length)
    }
}

private fun lab(vpn:Network, fixture:JSONObject, direct:Boolean, mode:String,result:Bundle){
    val host=fixture.getString("host")
    val proxy=fixture.getString("proxy")
    for(tcp in listOf(false,true)) {
        val proto=if(tcp)"TCP" else "UDP"
        for(local in listOf(true,false)){
            val domain=if(local)"local.split.test" else "remote.split.test"
            val q=byteArrayOf(0x21,0x34,1,0,0,1,0,0,0,0,0,0)+
                domain.split('.').flatMap { listOf(it.length.toByte())+it.toByteArray().toList() }.toByteArray()+byteArrayOf(0,0,1,0,1)
            val reply=exchange(vpn,proxy,53,q,tcp,true)
            val expected=if(local && direct)host else proxy
            check(reply.size>=q.size+16 && reply.takeLast(4)==InetAddress.getByName(expected).address.toList()) { "$mode $proto DNS $local" }
            val target=if(local)host else proxy
            val request=(if(tcp)"receipt\n" else "receipt").toByteArray()
            val path=exchange(vpn,target,fixture.getInt("port"),request,tcp).decodeToString()
            check(path==if(local && direct)"direct" else "node") { "$mode $proto path $local $path" }
        }
        result.putString("$mode-$proto","both DNS queries and both source receipts matched")
    }
}

private fun labRate(vpn:Network,fixture:JSONObject,up:Boolean):Double {
    val address=InetSocketAddress(fixture.getString("proxy"),fixture.getInt("port"))
    val chunk=ByteArray(32768)
    val rates=(0..4).map {
        val start=SystemClock.elapsedRealtimeNanos()
        vpn.socketFactory.createSocket().use { socket ->
            socket.soTimeout=12000
            socket.connect(address,6000)
            val out=socket.getOutputStream()
            val head=if(up)"POST /up HTTP/1.1\r\nContent-Length: 1048576" else "GET /down HTTP/1.1"
            // An address as Host: Xray connects by the sniffed host (routeOnly=false), and the lab
            // peer cannot resolve made-up names.
            out.write((head+"\r\nHost: "+fixture.getString("proxy")+"\r\nConnection: close\r\n\r\n").toByteArray())
            val input=DataInputStream(socket.getInputStream().buffered())
            if(up) repeat(32){out.write(chunk)}
            out.flush()
            val response="HTTP/1.1 200 OK\r\nContent-Length: "+(if(up)2 else 1048576)+"\r\nConnection: close\r\n\r\n"
            val header=ByteArray(response.length)
            input.readFully(header)
            check(header.decodeToString()==response)
            if(up) check(input.readUnsignedShort()==0x6f6b) else repeat(32){input.readFully(chunk)}

        }
        1048576.0*8000/(SystemClock.elapsedRealtimeNanos()-start)
    }
    return rates.sorted()[2]
}

private val sites=listOf("https://myip.ipip.net/","https://www.cloudflare.com/cdn-cgi/trace")
private fun exits(net:Network)=sites.map { site -> runCatching {
    val conn=net.openConnection(URL(site)) as HttpsURLConnection
    try {
        conn.connectTimeout=6500;conn.readTimeout=9000;conn.useCaches=false
        conn.instanceFollowRedirects=false;conn.setRequestProperty("Connection","close")
        check(conn.responseCode==200)
        val body=conn.inputStream.bounded(65536).decodeToString()
        val ip=if(site==sites[0]) Regex("""(?:\d{1,3}\.){3}\d{1,3}""").find(body)?.value
            else body.lineSequence().firstOrNull { it.startsWith("ip=") }?.substringAfter('=')
        require(!ip.isNullOrEmpty() && ip.matches(Regex("[0-9a-fA-F:.]+")))
        InetAddress.getByName(ip).hostAddress
    } finally {conn.disconnect()}
}.getOrNull() }

internal fun Instrumentation.splitCheck(args: Bundle, result: Bundle) {
    val ctx = targetContext
    val cm = ctx.getSystemService(ConnectivityManager::class.java)
    val prefs = ctx.getSharedPreferences("local",0)
    val store = Subscriptions(ctx)
    val engine = requireNotNull(args.getString("engine")).also { require(it in setOf("v2","xray","shoes")) }
    val fixture = if (args.getString("mode")=="split-lab") JSONObject(File(ctx.noBackupFilesDir,"split-nodes.json").readText()) else null
    val override = File(ctx.filesDir,"split-routing-test.txt")
    check(!override.exists() && !TunnelService.state.value.active && VpnService.prepare(ctx)==null)
    var home: android.app.Activity? = null
    val saved = prefs.all[SplitRouting.KEY] as? Boolean
    val last = prefs.getString(TunnelService.LAST,null)
    val resume = prefs.getString(TunnelService.RESUME,null)
    var temporary: String? = null
    var v2Key: String? = null
    fun awaitVpn(previous: Long): Network {
        val deadline = SystemClock.elapsedRealtime()+30000
        while (SystemClock.elapsedRealtime()<deadline) {
            val state = TunnelService.state.value
            val vpn = cm.activeNetwork
            if (state.connectedAt>previous && vpn!=null && cm.getNetworkCapabilities(vpn)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true) return vpn
            Thread.sleep(120)
        }
        error("VPN readiness timeout")
    }
    try {
        if (fixture!=null) override.writeText("resolvers:"+fixture.getString("host")+":"+fixture.getInt("domesticPort")+"\ndomain:local.split.test\nip:2001:db8::/64\n")
        val intent = if (engine=="v2") {
            requireNotNull(fixture)
            val node = V2Devices.enroll(ctx,V2Invite.parse(fixture.getString("invitation")),
                fixture.getString("endpoint"),fixture.getString("ca").toByteArray())
            v2Key = node.key
            Intent(ctx,TunnelService::class.java).setAction("v2").putExtra("v2-key",node.key)
                .putExtra("dns",fixture.getString("proxy"))
        } else {
            val label = if (fixture!=null) "Nexus-QA-split" else "Airport-Test"
            if (fixture!=null) {
                check(store.all().none { it.label==label })
                store.import(label,fixture.toString())
            }
            val source=if(fixture==null && args.getString("any-source")=="yes")store.all().first {sub->
                (0 until sub.nodes.length()).any { index->
                    val node=sub.nodes.getJSONObject(index)
                    node.optString("type") in CompatEngine.types-ShoesEngine.types && CompatEngine.problem(node)==null
                }
            } else store.all().single { it.label==label }
            if (fixture!=null) temporary=source.id
            val types=if (engine=="xray") CompatEngine.types-ShoesEngine.types else ShoesEngine.types
            val candidates=(0 until source.nodes.length()).map { NodeRow(source,it,source.nodes.getJSONObject(it)) }
                .filter { it.type in types && CompatEngine.problem(it.node)==null }
            val row=candidates.getOrNull(args.getString("index")?.toInt() ?: 0) ?: error("no matching node")
            subscriptionRequest(ctx,row,listOf(row),fixture?.getString("proxy") ?: "1.1.1.1",false)
        }
        check(prefs.edit().putBoolean(SplitRouting.KEY,false).commit())
        home = startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        ctx.startForegroundService(intent)
        var vpn=awaitVpn(0)
        val physical=requireNotNull(TunnelService.state.value.physical)
        val physicalExits=if(fixture==null) exits(physical) else emptyList()
        val global=if(fixture==null) exits(vpn) else emptyList()
        fun speed(mode:String){
            if(fixture!=null && engine=="xray") result.putString("lab-1MiB-$mode",
                java.lang.String.format(java.util.Locale.ROOT,"down %.2f / up %.2f Mbps (median of 5)",labRate(vpn,fixture,false),labRate(vpn,fixture,true)))
            if(fixture==null && engine=="xray") result.putString("speed-$mode",runCatching {
                NodeQuality(ctx,physical).measure(vpn,"split-test"){TunnelService.state.value.connectedAt>0}
            }.getOrElse { it.javaClass.simpleName })
        }
        if(fixture!=null) {
            val before=requireNotNull(trafficBytes(ctx))
            lab(vpn,fixture,false,"global",result)
            Thread.sleep(1100)
            val after=requireNotNull(trafficBytes(ctx))
            check(after.first>before.first && after.second>before.second)
            result.putString("traffic","TUN bytes increased: up "+(after.first-before.first)+", down "+(after.second-before.second))
        }
        speed("global")
        val prev=TunnelService.state.value.connectedAt
        val visual = args.getString("visual")=="1"
        if (visual) clickSplitLabel("绕过大陆",scroll=true) else {
            check(prefs.edit().putBoolean(SplitRouting.KEY,true).commit())
            ctx.startService(Intent(ctx,TunnelService::class.java).setAction("split-mode"))
        }

        vpn=awaitVpn(prev)
        if(fixture!=null) lab(vpn,fixture,engine!="shoes","bypass",result) else {
            val split=exits(vpn)
            fun same(a:String?,b:String?)=if(a==null || b==null)"未测得" else if(a==b)"一致" else "不一致"
            result.putString("global-CN-vs-CF",same(global[0],global[1]))
            result.putString("bypass-CN-vs-physical",same(split[0],physicalExits[0]))
            result.putString("bypass-CN-vs-global",same(split[0],global[0]))
            result.putString("bypass-CF-vs-global",same(split[1],global[1]))
            result.putString("physical-CN-vs-global",same(physicalExits[0],global[0]))
        }
        speed("bypass")
        if(engine=="shoes") check(TunnelService.state.value.shoes)
        if(visual) {
            check(fixture!=null && engine=="shoes")
            splitScreen("card",SplitRouting.SHOES)
            clickSplitLabel("设置")
            splitLabel("返回",false)
            splitScreen("settings","全局代理")
            clickSplitLabel("返回")
            clickSplitLabel("节点")
            splitScreen("row",SplitRouting.SHOES,rows=true)
            result.putString("visual","home toggle reconnected; card, settings and node warning captured")
        }
    } finally {
        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
        val deadline=SystemClock.elapsedRealtime()+6000
        while(TunnelService.state.value.active && SystemClock.elapsedRealtime()<deadline) Thread.sleep(80)
        override.delete()
        temporary?.let(store::remove)
        v2Key?.let { V2Devices.remove(ctx,it) }
        check(prefs.edit().putString(TunnelService.LAST,last).putString(TunnelService.RESUME,resume).apply {
            if(saved==null) remove(SplitRouting.KEY) else putBoolean(SplitRouting.KEY,saved)
        }.commit())
        home?.let { runOnMainSync(it::finish) }
    }
}

internal fun Instrumentation.splitLabel(text:String, scroll:Boolean, rows:Boolean=false): android.view.accessibility.AccessibilityNodeInfo {
    val pkg=targetContext.packageName
    val until=SystemClock.elapsedRealtime()+25000
    var previousViewport:List<String>?=null
    var forward=true
    while(SystemClock.elapsedRealtime()<until){
        val root=uiAutomation.rootInActiveWindow
        if(root!=null) {
            fun nodes(n:android.view.accessibility.AccessibilityNodeInfo):Sequence<android.view.accessibility.AccessibilityNodeInfo> = sequence {
                yield(n)
                for(i in 0 until n.childCount) n.getChild(i)?.let { yieldAll(nodes(it)) }
            }
            val visible=nodes(root).filter { it.packageName?.toString()==pkg && it.isVisibleToUser }.toList()
            val same=visible.firstOrNull { it.contentDescription?.toString()==text } ?: visible.firstOrNull { it.text?.toString()==text } ?: visible.firstOrNull { it.text?.toString()?.contains(text)==true }
            val card=visible.any { it.text?.toString()=="我的网络" }
            if(same!=null && (!rows || !card && visible.any { it.text?.toString()?.startsWith("TUIC · ")==true })) return same
            if(scroll){
                // The home screen has two independent scroll panes in landscape.
                val pane=visible.lastOrNull { it.isScrollable }
                if(pane!=null){
                    val viewport=nodes(pane).filter {it.isVisibleToUser}.map { it.text?.toString().orEmpty() }.toList()
                    if(viewport==previousViewport)forward=!forward
                    previousViewport=viewport
                    val bounds=android.graphics.Rect().also(pane::getBoundsInScreen)
                    val x=bounds.centerX()
                    val top=bounds.top+bounds.height()/4
                    val bottom=bounds.bottom-bounds.height()/4
                    val from=if(forward)bottom else top
                    val to=if(forward)top else bottom
                    android.os.ParcelFileDescriptor.AutoCloseInputStream(uiAutomation.executeShellCommand("input swipe $x $from $x $to 350")).use { it.readBytes() }
                }
            }
        }
        Thread.sleep(350)
    }
    val image=uiAutomation.takeScreenshot()
    if(image!=null) try { File(targetContext.cacheDir,"split-ui-timeout.png").outputStream().use {
        image.compress(android.graphics.Bitmap.CompressFormat.PNG,100,it)
    } } finally { image.recycle() }
    error("split UI control missing")
}
internal fun Instrumentation.clickSplitLabel(text:String,scroll:Boolean=false){
    repeat(5) {
        var item:android.view.accessibility.AccessibilityNodeInfo?=splitLabel(text,scroll)
        while(item!=null && !item.isClickable) item=item.parent
        if(item?.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK)==true) return
        Thread.sleep(200)
    }
    error("control could not be clicked: "+text)
}
private fun Instrumentation.splitScreen(name:String,text:String,rows:Boolean=false){
    sendStatus(1,android.os.Bundle().apply { putString("screen",name) })
    splitLabel(text,rows || name=="card",rows)
    uiAutomation.waitForIdle(250,3000)
    val image=requireNotNull(uiAutomation.takeScreenshot())
    try { File(targetContext.cacheDir,"split-ui-$name.png").outputStream().use {
        check(image.compress(android.graphics.Bitmap.CompressFormat.PNG,100,it))
    } } finally {image.recycle()}
}
