package dev.nexus.localclient

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.os.Bundle
import android.util.Base64
import java.util.UUID
import java.io.File
import org.json.JSONObject
import org.json.JSONArray
import com.jcraft.jsch.JSch
import com.jcraft.jsch.KeyPair
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout

class CatalogCheck : Instrumentation() {
    private lateinit var args: Bundle
    override fun onCreate(arguments: Bundle) { super.onCreate(arguments); args = arguments; start() }
    override fun onStart() {
        if (args.containsKey("mode")) { nodeCheck(); return }
        val result = Bundle()
        val store = Subscriptions(targetContext)
        val prefix = "Nexus-QA-" + UUID.randomUUID().toString().take(8)
        val link = "trojan://test-only@node.invalid:443?security=tls#First"
        var code = Activity.RESULT_CANCELED
        try {
            for ((index, label) in listOf("配置","证书","撤销","资源","目标","中断","已经送达").withIndex()) {
                val code = index + 1
                check(V2Connection.error(code).contains(label))
                check(V2Connection.retry(code) == (code == 4 || code == 6))
            }
            result.putString("v2-errors", "7 error messages and retry policies passed")
            fun exit(status: Int, body: String? = null) = diagnosticResponse("ipinfo.io", status, body, 12)
            check(exit(200, """{"ip":"192.0.2.1"}""").ip == "192.0.2.1")
            check(exit(200, """{"ip":"2001:db8::1"}""").ip == "2001:db8::1")
            check(exit(403).message == "ipinfo.io 拒绝了这个出口的查询（403）。这是服务方的限制，不代表节点故障。")
            check(exit(429).message == "ipinfo.io 暂时限制了这个出口的查询（429）。稍后再试或换个节点，不代表节点故障。")
            check(exit(500).message == "ipinfo.io 返回 HTTP 500，请稍后再试。")
            for (body in listOf("invalid", "{}", """{"ip":"999.0.2.1"}""", """{"ip":"2001:::1"}""")) {
                val response = exit(200, body)
                check(response.ip == null && response.message == "ipinfo.io 返回了无法识别的内容。")
            }
            for (status in listOf(403, 429, 500)) {
                check(diagnosticResponse("example.com", status, null, 12).message == "example.com 返回 HTTP $status，请稍后再试。")
            }
            check(diagnosticResponse("example.com", 200, "Example Domain", 12).message == "HTTPS 网页验证成功 · 12 ms")
            check(diagnosticResponse("example.com", 200, "unexpected", 12).message == "example.com 返回了无法识别的内容。")
            result.putString("diagnostics", "HTTP status and response content checks passed")
            store.import("$prefix-A", link)
            store.import("$prefix-B", "proxies:\n  - name: Second\n    type: ss\n    method: aes-128-gcm\n    password: test-only\n    server: node.invalid\n    port: 3434")
            store.import("$prefix-C",Base64.encodeToString(link.replace("First","Third").toByteArray(),Base64.NO_WRAP))
            val three = store.all().filter { it.label.startsWith(prefix) }
            check(three.size==3 && three.all { it.nodes.length()==1 })
            for (s in three) CompatEngine.outbound(s.nodes.getJSONObject(0))
            val first = three.first()
            store.import(first.label,link.replace("First","Updated"),first)
            val next = store.all().filter { it.label.startsWith(prefix) }
            check(next.map { it.id } == three.map { it.id })
            check(next.first().nodes.getJSONObject(0).getString("name")=="Updated")
            check(next.drop(1).map { it.json().toString() } == three.drop(1).map { it.json().toString() })
            val disk = targetContext.getSharedPreferences("subscriptions",0).all
            check(three.none { disk[it.id].toString().contains("test-only") })
            result.putString("catalog","3 sources, isolated update, encrypted persistence, native outbound validation")
            code = Activity.RESULT_OK
        } catch (e: Throwable) { result.putString("error",e.javaClass.simpleName + " at " + e.stackTrace.firstOrNull()) }
        finally {
            store.all().filter { it.label.startsWith(prefix) }.forEach { store.remove(it.id) }
            finish(code,result)
        }
    }
    private fun checkRefresh(result: Bundle) {
        val ctx = targetContext
        var fail = false
        var downloads = 0
        val store = Subscriptions(ctx) {
            downloads++
            if (fail) throw java.io.IOException()
            FormatSamples.clash to "download=20;total=100"
        }
        val prefs = ctx.getSharedPreferences("local",0)
        val last = prefs.getString(TunnelService.LAST,null)
        val resume = prefs.getString(TunnelService.RESUME,null)
        val prefix = "Nexus-QA-" + UUID.randomUUID().toString().take(8)
        val home = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        var started = false
        fun waitVpn(active: Boolean) = runBlocking {
            withTimeout(12000) { TunnelService.state.first { it.vpn == active } }
        }
        try {
            check(!TunnelService.state.value.active && android.net.VpnService.prepare(ctx) == null)
            for (suffix in listOf("A","B")) store.import("$prefix-$suffix","https://fixture.test/sub")
            fun fixtures() = store.all().filter { it.label.startsWith(prefix) }
            val sources = fixtures()
            val first = sources.first()
            val due = sources.maxOf { it.updated } + 12*3600000L
            val key = nodeKey(first.id,first.nodes.getJSONObject(0))
            SubscriptionRefresh.updateDue(store,sources,due-3600000)
            check(downloads == 2)
            fail = true
            SubscriptionRefresh.updateDue(store,listOf(first),due)
            check(store.get(first.id).json().toString() == first.json().toString())
            check(store.appearance.getString("error/"+first.id,null) == "网络请求失败")
            SubscriptionRefresh.updateDue(store,listOf(first),due+60000)
            check(downloads == 3)
            fail = false
            val retry = due+15*60000
            SubscriptionRefresh.updateDue(store,listOf(first),retry)
            val updated = store.get(first.id)
            check(updated.updated > first.updated && nodeKey(updated.id,updated.nodes.getJSONObject(0)) == key)
            val backup = sources.last()
            val refs = JSONArray().put(JSONObject().put("source",backup.id).put("index",0).put("updated",backup.updated))
            val request = subscriptionRequest(ctx,NodeRow(updated,0,updated.nodes.getJSONObject(0)),emptyList(),"1.1.1.1",false)
                .putExtra("alternates",refs.toString())
            ctx.startForegroundService(request)
            started = true
            waitVpn(true)
            if (args.getString("mode") == "probe-vpn") checkProbeDisabled(result)
            SubscriptionRefresh.updateDue(store,fixtures(),retry+86400000)
            check(downloads == 4 && store.get(backup.id).updated == backup.updated)
            ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
            waitVpn(false)
            started = false
            SubscriptionRefresh.updateDue(store,fixtures(),retry+86400000)
            check(downloads == 6)
            result.putString("refresh","failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed")
        } finally {
            if (started) {
                ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
                waitVpn(false)
            }
            check(prefs.edit().putString(TunnelService.LAST,last).putString(TunnelService.RESUME,resume).commit())
            store.all().filter { it.label.startsWith(prefix) }.forEach { store.remove(it.id) }
            runOnMainSync { home.finish() }
        }
    }

    private fun checkFailover(result: Bundle) {
        val ctx = targetContext
        val good = requireNotNull(Profile.load(ctx))
        require(good.dns != null && android.net.VpnService.prepare(ctx) == null)
        val port = requireNotNull(args.getString("offlinePort")).toInt()
        require(port in 1..65535 && port != good.server.substringAfterLast(':').toInt())
        val offline = good.copy(server = good.server.substringBeforeLast(':') + ":" + port)
        val prefs = ctx.getSharedPreferences("local", 0)
        val prior = prefs.all
        val keys = listOf("profile", "servers", "pinned", "failover")
        val home = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        fun action(name: String) = ctx.startService(Intent(ctx, TunnelService::class.java).setAction(name))
        fun until(predicate: (TunnelState) -> Boolean) = runBlocking {
            withTimeout(60000) { TunnelService.state.first(predicate) }
        }
        try {
            check(!TunnelService.state.value.active)
            val setup = prefs.edit()
            keys.forEach { setup.remove(it) }
            check(setup.putBoolean("failover", true).commit())
            good.save(ctx)
            offline.save(ctx)
            check(prefs.edit().putString("pinned", offline.server + "/" + offline.fingerprint).commit())
            action("vpn")
            repeat(3) {
                until { it.starting || it.connectedAt != 0L }.also { check(it.connectedAt == 0L) }
                until { it.error.isNotEmpty() || it.connectedAt != 0L }.also { check(it.vpn && it.connectedAt == 0L) }
            }
            result.putString("pinned", "three failed attempts kept the VPN on its unavailable pinned node")
            action("stop")
            until { !it.active }
            check(prefs.edit().remove("pinned").commit())
            action("vpn")
            val ready = until { it.connectedAt != 0L }
            check(ready.vpn && ready.activeProfile?.server == good.server)
            result.putString("failover", "authenticated with the alternate private node")
        } finally {
            try {
                action("stop")
                until { !it.active }
            } finally {
                val restore = prefs.edit()
                for (key in keys) when (val value = prior[key]) {
                    is String -> restore.putString(key, value)
                    is Boolean -> restore.putBoolean(key, value)
                    else -> restore.remove(key)
                }
                check(restore.commit())
                runOnMainSync { home.finish() }
            }
        }
    }
    /** Clash/mihomo YAML, sing-box JSON and Base64 links through the real parser and libXray. */
    private fun checkFormats(result: Bundle) {
        val store = Subscriptions(targetContext)
        val prefix = "Nexus-QA-" + UUID.randomUUID().toString().take(8)
        try {
            store.import("$prefix-clash", FormatSamples.clash)
            store.import("$prefix-singbox", FormatSamples.singBox)
            store.import("$prefix-links", Base64.encodeToString(FormatSamples.links.toByteArray(), Base64.NO_WRAP))
            val nodes = store.all().filter { it.label.startsWith(prefix) }
                .flatMap { s -> (0 until s.nodes.length()).map { s.nodes.getJSONObject(it) } }
                .associateBy { it.getString("name") }
            check(nodes.keys.none { it in setOf("proxy", "direct") }) { "sing-box selector or direct imported" }
            val failures = mutableListOf<String>()
            for ((name, expect) in FormatSamples.cases) {
                val node = nodes[name]
                if (node == null) { failures += "$name: not imported"; continue }
                val problem = CompatEngine.problem(node)
                if (expect.startsWith("!")) {
                    if (problem?.contains(expect.drop(1)) != true) failures += "$name: expected refusal ${expect.drop(1)}, got $problem"
                } else if (problem != null) failures += "$name: $problem"
                else {
                    val text = CompatEngine.outbound(node).toString().replace("\\/", "/")
                    expect.split('|').filterNot { it in text }.takeIf { it.isNotEmpty() }?.let { missing ->
                        failures += "$name lacks $missing in ${text.take(700)}"
                    }
                }
            }
            check(nodes.size == FormatSamples.cases.size) { "imported ${nodes.keys.sorted()}" }
            if (failures.isNotEmpty()) {
                result.putString("failures", failures.joinToString("\n"))
                error("${failures.size} format cases failed")
            }
            result.putString("formats", "${FormatSamples.cases.size} Clash, sing-box and link cases converted or refused as expected")
        } finally {
            // keep=yes leaves the samples for a screenshot; remove-formats deletes them afterwards.
            if (args.getString("keep") != "yes") store.all().filter { it.label.startsWith(prefix) }.forEach { store.remove(it.id) }
        }
    }
    /**
     * Each node of one imported source, through the app's own conversion and engine, fetches
     * https://example.com with normal certificate checks. Reports time per node; prints no credentials.
     */
    private fun checkProbeDisabled(result: Bundle) {
        clickSplitLabel("节点")
        clickSplitLabel("筛选与测速",true)
        fun nodes(n: android.view.accessibility.AccessibilityNodeInfo): Sequence<android.view.accessibility.AccessibilityNodeInfo> =
            sequence {
                yield(n)
                for (i in 0 until n.childCount) n.getChild(i)?.let { yieldAll(nodes(it)) }
            }
        val seen = mutableSetOf<String>()
        repeat(28) {
            val tree = uiAutomation.rootInActiveWindow?.let(::nodes)?.toList().orEmpty()
            val label = tree.firstOrNull { it.text?.toString() == "测试真实连通" }
            if (label != null && tree.any { it.text?.toString() == "请先断开 VPN" }) {
                check(generateSequence(label) { it.parent }.any { !it.isEnabled })
                check(TunnelService.state.value.vpn)
                result.putString("vpn","active VPN: real-test button disabled, disconnect hint visible")
                return
            }
            tree.firstOrNull()?.packageName?.let { seen.add(it.toString()) }
            tree.mapNotNull { it.text?.toString() }.filter { it in setOf("我的网络","订阅总览","添加节点","仅收藏","请先断开 VPN","测试真实连通","连接","收藏") }.forEach(seen::add)
            result.putString("seen",seen.joinToString())
            val metrics = targetContext.resources.displayMetrics
            val x = metrics.widthPixels / 2
            val y = metrics.heightPixels
            uiAutomation.executeShellCommand("input swipe $x ${y*8/10} $x ${y*4/10} 500").use {
                android.os.ParcelFileDescriptor.AutoCloseInputStream(it).readBytes()
            }
            Thread.sleep(250)
        }
        error("probe button not found")
    }

    private fun batchProbe(result: Bundle, lab: Boolean) = runBlocking {
        val ctx = targetContext
        val fixture = if (lab) JSONObject(File(ctx.noBackupFilesDir,"qa-interop").readText()) else null
        val source = if (fixture != null) Subscription("qa-lab","QA","",fixture.getJSONArray("nodes"),"",0)
            else Subscriptions(ctx).all().single { it.label == args.getString("source") }
        val rows = (0 until source.nodes.length()).map { NodeRow(source,it,source.nodes.getJSONObject(it)) }
        val outcomes = java.util.concurrent.ConcurrentHashMap<Int,ProbeResult>()
        val start = android.os.SystemClock.elapsedRealtime()
        val report = { row: NodeRow, value: ProbeResult -> outcomes[row.index] = value }
        val peak = if (fixture == null) NodeProbe.run(ctx,rows,onResult=report) else {
            val trust = java.security.KeyStore.getInstance(java.security.KeyStore.getDefaultType()).apply {
                load(null)
                setCertificateEntry("lab",java.security.cert.CertificateFactory.getInstance("X.509")
                    .generateCertificate(fixture.getString("ca").byteInputStream()))
            }
            val managers = javax.net.ssl.TrustManagerFactory.getInstance(javax.net.ssl.TrustManagerFactory.getDefaultAlgorithm())
            managers.init(trust)
            val factory = javax.net.ssl.SSLContext.getInstance("TLS").apply { init(null,managers.trustManagers,null) }.socketFactory
            fun stopped(ports: List<Int>) {
                check(!com.shoesproxy.ShoesNative.isRunning())
                check(ports.size == rows.size && ports.all { port ->
                    runCatching { java.net.Socket("127.0.0.1",port).use {} }.exceptionOrNull() is java.net.ConnectException
                })
            }
            val opened = kotlinx.coroutines.CompletableDeferred<List<Int>>()
            val cancellation = launch {
                NodeProbe.run(ctx,rows,listOf(ProbeSite("nexus.test","/slow",200)),factory,{ opened.complete(it) },report)
            }
            val firstPorts = withTimeout(15000) { opened.await() }
            kotlinx.coroutines.delay(200)
            cancellation.cancelAndJoin()
            stopped(firstPorts)
            check(outcomes.isEmpty())
            var ports = emptyList<Int>()
            val n = NodeProbe.run(ctx,rows,listOf(ProbeSite("nexus.test","/missing",204),ProbeSite("nexus.test","/check",200)),
                factory,{ ports=it },report)
            stopped(ports)
            result.putString("lifecycle","normal and cancelled batches released all listeners; shoes stopped")
            n
        }
        val failed = rows.filter { outcomes[it.index]?.ok == false }
        result.putString("batch","${outcomes.values.count { it.ok }} / ${outcomes.size} passed, peak $peak, ${android.os.SystemClock.elapsedRealtime()-start} ms")
        result.putString("failed",failed.joinToString { it.node.getString("name")+" "+outcomes[it.index]?.reason })
        if (lab) {
            check(peak in 1..8 && outcomes.size == rows.size && failed.isEmpty())
            check(outcomes.values.all { "HTTP 404" in it.detail && "HTTP 200" in it.detail })
        }
    }

    private fun probeSource(result: Bundle) {
        val label = requireNotNull(args.getString("source"))
        require(!TunnelService.state.value.active) { "disconnect the VPN first" }
        val source = Subscriptions(targetContext).all().single { it.label == label }
        val network = requireNotNull(targetContext.getSystemService(android.net.ConnectivityManager::class.java).activeNetwork)
        var passed = 0
        for (i in 0 until source.nodes.length()) {
            val node = source.nodes.getJSONObject(i)
            val name = "%02d %s %s".format(i, node.getString("type"), node.getString("name"))
            result.putString(name, try {
                "OK ${httpsVia(CompatEngine.resolved(CompatEngine.outbound(node), network))} ms".also { passed++ }
            } catch (e: Exception) { "FAIL ${e.javaClass.simpleName}: ${e.message?.take(120)}" })
        }
        result.putString("summary", "$passed / ${source.nodes.length()} nodes fetched https://example.com")
    }

    private fun httpsVia(out: JSONObject): Long {
        val port = java.net.ServerSocket(0, 1, java.net.InetAddress.getLoopbackAddress()).use { it.localPort }
        fun call(method: String, payload: JSONObject) =
            JSONObject(libXray.LibXray.invoke(JSONObject().put("apiVersion", 3).put("method", method).put("payload", payload).toString()))
        val pair = if (out.has("shoesClient")) android.os.ParcelFileDescriptor.createSocketPair() else null
        var handle = 0L
        try {
            if (pair != null) {
                val client = JSONObject(out.getJSONObject("shoesClient").toString())
                val rule = JSONObject().put("masks", "0.0.0.0/0").put("action", "allow").put("client_chain", client)
                val servers = JSONArray().put(JSONObject().put("device_fd", pair[0].fd))
                    .put(JSONObject().put("address", "127.0.0.1:$port").put("protocol", JSONObject().put("type", "mixed"))
                        .put("rules", JSONArray().put(rule)))
                check(com.shoesproxy.ShoesNative.init("off") == 0)
                handle = com.shoesproxy.ShoesNative.start(servers.toString()) { true }
                check(handle > 0)
                Thread.sleep(300)
            } else {
                val cfg = JSONObject().put("inbounds", JSONArray().put(JSONObject().put("listen", "127.0.0.1").put("port", port)
                    .put("protocol", "socks").put("settings", JSONObject().put("auth", "noauth"))))
                    .put("outbounds", JSONArray().put(out)).put("log", JSONObject().put("loglevel", "none"))
                check(call("runXray", JSONObject().put("xrayJson", cfg.toString())).getBoolean("success"))
            }
            val start = android.os.SystemClock.elapsedRealtime()
            val proxy = java.net.Proxy(java.net.Proxy.Type.SOCKS, java.net.InetSocketAddress("127.0.0.1", port))
            java.net.Socket(proxy).use { raw ->
                raw.soTimeout = 12000
                raw.connect(java.net.InetSocketAddress.createUnresolved("example.com", 443), 12000)
                val factory = javax.net.ssl.SSLSocketFactory.getDefault() as javax.net.ssl.SSLSocketFactory
                (factory.createSocket(raw, "example.com", 443, true) as javax.net.ssl.SSLSocket).use { tls ->
                    tls.sslParameters = tls.sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS" }
                    tls.startHandshake()
                    tls.outputStream.write("GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n".toByteArray())
                    val status = tls.inputStream.bufferedReader().readLine().orEmpty()
                    check(" 200 " in status) { status.take(40) }
                }
            }
            return android.os.SystemClock.elapsedRealtime() - start
        } finally {
            if (handle > 0) com.shoesproxy.ShoesNative.stop(handle)
            pair?.forEach { it.close() }
            if (pair == null) call("stopXray", JSONObject())
        }
    }
    private fun nodeCheck() {
        val result = Bundle()
        val root = targetContext.noBackupFilesDir
        val privateKey = File(root, "qa-temporary-ssh")
        val pub = File(root,"qa-temporary-ssh.pub")
        var status = Activity.RESULT_CANCELED
        try {
            when (args.getString("mode")) {
                "security" -> {
                    val store=Subscriptions(targetContext)
                    val expired=java.util.concurrent.CountDownLatch(1)
                    val interruptedRead=java.util.concurrent.atomic.AtomicBoolean()
                    val handler=object:java.net.URLStreamHandler(){
                        override fun openConnection(url:java.net.URL)=object:javax.net.ssl.HttpsURLConnection(url){
                            override fun connect() {}
                            override fun disconnect() { expired.countDown() }
                            override fun usingProxy()=false
                            override fun getCipherSuite()="fixture"
                            override fun getLocalCertificates():Array<java.security.cert.Certificate>?=null
                            override fun getServerCertificates()=emptyArray<java.security.cert.Certificate>()
                            override fun getResponseCode():Int {
                                return if(url.path=="/redirect") 302 else 200
                            }
                            override fun getHeaderField(name:String)=if(name=="Location") "http://demo.test/" else null
                            override fun getInputStream():java.io.InputStream {
                                interruptedRead.set(expired.await(3,java.util.concurrent.TimeUnit.SECONDS))
                                return "fixture".byteInputStream()
                            }
                        }
                    }
                    fun endpoint(path:String)=java.net.URL(null,"https://demo.test/"+path,handler)
                    check(runCatching {store.fetch(endpoint("budget"),100)}.isFailure && interruptedRead.get())
                    check(runCatching {store.fetch(endpoint("redirect"),5000)}.isFailure)
                    fun refuses(input:String){
                        val added=runCatching {store.import("Nexus-QA-security",input)}.getOrNull()
                        if(added!=null)store.remove(added.id)
                        check(added==null)
                    }
                    val link="trojan://sample@demo.test:443"
                    refuses(link+"?path="+"a".repeat(17000))
                    refuses(List(2049){link}.joinToString("\n"))
                    refuses(Base64.encodeToString(Base64.encode(link.toByteArray(),Base64.NO_WRAP),Base64.NO_WRAP))
                    refuses("""{"format":"nexus-nodes/1","nodes":[],"nodes":[]}""")
                    refuses("proxies: &items []\noutbounds: *items")
                    refuses("{\"nested\":"+"[".repeat(25)+"0"+"]".repeat(25)+"}")
                    refuses("proxies:\n - type: trojan\n   type: trojan")
                    for(parameter in listOf("path=%0A","extra=sample")){
                        val n=JSONObject().put("type","trojan").put("name","演示").put("config","$link?$parameter")
                        check(CompatEngine.problem(n)!=null)
                    }
                    val label="test\"{}"
                    val source=store.import("Nexus-QA-security",link+"#"+android.net.Uri.encode(label))
                    try{check(source.nodes.getJSONObject(0).getString("name")==label)}finally{store.remove(source.id)}
                    for(ip in listOf("127.0.0.2","::1","0.0.0.0","fe80::1","224.0.0.1","240.0.0.1","::ffff:127.0.0.1"))
                        check(!allowedNodeAddress(java.net.InetAddress.getByName(ip)))
                    for(ip in listOf("192.168.0.1","10.0.2.2","100.64.0.1","198.18.0.1","fd00::1","203.0.113.7","2001:db8::1"))
                        check(allowedNodeAddress(java.net.InetAddress.getByName(ip)))
                    var toggles=0
                    var pending:(()->Unit)?=null
                    afterTileUnlock(true,{pending=it}){toggles++}
                    check(toggles==0)
                    requireNotNull(pending).invoke()
                    afterTileUnlock(false,{error("unexpected prompt")}){toggles++}
                    check(toggles==2)
                    result.putString("security","input bounds, ambiguous encoding, control characters, literals and local endpoints checked")
                }
                "release-icons","release-import","release-update","release-key","release-private","release-boot","release-state","release-stop" -> releaseCheck(args,result)
                "simple", "simple-owner", "simple-bind", "simple-host", "simple-guest", "simple-shots", "simple-frames", "simple-motion", "simple-overview", "simple-overview-actual" -> simpleCheck(args,result)
                "m0-key", "m0-udp", "m0-handshakes" -> Class.forName("dev.nexus.localclient.M0Check")
                    .getMethod("run", Instrumentation::class.java, Bundle::class.java, Bundle::class.java)
                    .invoke(null, this, args, result)
                "m3" -> m3Check(result, args.getString("phase") ?: "all", args.getString("probePackage") ?: "dev.nexus.vpnprobe")
                "m3-ui" -> m3UiCheck(result)
                "split-lab", "split-public" -> splitCheck(args,result)
                // QA transfer of a saved source between the owner's devices. The link stays in app-private
                // storage on each side and is never printed.
                "source-export" -> {
                    val source = Subscriptions(targetContext).all().single { it.label == args.getString("source") }
                    File(targetContext.noBackupFilesDir, "source-transfer").writeText(source.url)
                    result.putString("export", "written")
                }
                // Replays what Android does for an always-on VPN after the process was killed: a start
                // with the VpnService interface action must resume the user's last connection.
                "resume" -> {
                    val saved = targetContext.getSharedPreferences("local", 0).getString("resume-request", null)
                    check(saved != null) { "no saved request" }
                    targetContext.startForegroundService(Intent(android.net.VpnService.SERVICE_INTERFACE).setClass(targetContext, TunnelService::class.java))
                    val deadline = android.os.SystemClock.elapsedRealtime() + 45000
                    while (android.os.SystemClock.elapsedRealtime() < deadline && TunnelService.state.value.connectedAt == 0L) Thread.sleep(250)
                    val s = TunnelService.state.value
                    check(s.connectedAt > 0 && s.error.isEmpty()) { "not resumed: " + s.error }
                    result.putString("resume", "resumed " + (if (s.node != null) "subscription node" else "private node") +
                        " after " + Intent.parseUri(saved, 0).action + " request")
                    targetContext.startService(Intent(targetContext, TunnelService::class.java).setAction("stop"))
                    Thread.sleep(2000)
                }
                "source-import" -> {
                    val file = File(targetContext.noBackupFilesDir, "source-transfer")
                    try {
                        val source = Subscriptions(targetContext).import(requireNotNull(args.getString("source")), file.readText().trim())
                        result.putString("import", "${source.nodes.length()} nodes")
                    } finally { file.delete() }
                }
                "migration-crypto" -> {
                    val data = JSONObject().put("format","nexus-migration/1").put("probe",UUID.randomUUID().toString())
                    val secret = UUID.randomUUID().toString()
                    val output = java.io.ByteArrayOutputStream()
                    writeMigration(secret.toCharArray(),output) { data.toString().toByteArray() }
                    val sealed = output.toByteArray()
                    check(!sealed.toString(Charsets.ISO_8859_1).contains(data.getString("probe")))
                    check(readMigration(sealed.inputStream(),secret.toCharArray()).getString("probe") == data.getString("probe"))
                    check(runCatching { readMigration(sealed.inputStream(),UUID.randomUUID().toString().toCharArray()) }.isFailure)
                    sealed[sealed.lastIndex] = (sealed.last().toInt() xor 1).toByte()
                    check(runCatching { readMigration(sealed.inputStream(),secret.toCharArray()) }.isFailure)
                    result.putString("migration","round trip, incorrect password and modified ciphertext checked")
                }
                "remove-qa-source" -> {
                    val label = requireNotNull(args.getString("source"))
                    require(label.matches(Regex("Nexus-QA-[0-9a-f]{8}[.](json|txt)")))
                    val store = Subscriptions(targetContext)
                    store.all().singleOrNull { it.label == label }?.let { store.remove(it.id) }
                }
                "subscription-vpn" -> {
                    val label = requireNotNull(args.getString("source"))
                    require(label.startsWith("Nexus-QA-"))
                    val source = Subscriptions(targetContext).all().single { it.label == label }
                    val rows = (0 until source.nodes.length()).map { NodeRow(source,it,source.nodes.getJSONObject(it)) }
                    val row = rows.single { it.type == args.getString("protocol") }
                    val ctx = targetContext
                    val settings = Subscriptions(ctx).appearance
                    val key = "auto/"+source.id
                    val previous = settings.all[key] as? Boolean
                    // Receipt targets use reserved TEST-NET addresses, which bypass mode sends direct.
                    val local = ctx.getSharedPreferences("local",0)
                    val split = local.all[SplitRouting.KEY] as? Boolean
                    check(local.edit().putBoolean(SplitRouting.KEY,false).commit())
                    val signal = File(ctx.cacheDir,"subscription-qa-stop")
                    signal.delete()
                    require(android.net.VpnService.prepare(ctx) == null)
                    val home = startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    try {
                        check(settings.edit().putBoolean(key,true).commit())
                        ctx.startForegroundService(subscriptionRequest(ctx,row,rows,"192.0.2.53",true))
                        val deadline = android.os.SystemClock.elapsedRealtime() + 540000
                        val stateFile = File(ctx.cacheDir,"subscription-qa-state")
                        var previousState = ""
                        while (!signal.exists() && android.os.SystemClock.elapsedRealtime() < deadline) {
                            val state = TunnelService.state.value
                            val statusText = if (state.connectedAt != 0L) state.subscription.substringAfterLast(" · ")
                                else if (state.waiting) "waiting" else if (state.error.isNotEmpty()) "retry" else "starting"
                            if (statusText != previousState) {
                                stateFile.writeText(statusText)
                                previousState = statusText
                            }
                            Thread.sleep(150)
                        }
                        check(signal.exists()) { "matrix host timed out" }
                    } finally {
                        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
                        runBlocking { withTimeout(12000) { TunnelService.state.first { !it.active } } }
                        val restore = settings.edit()
                        if (previous == null) restore.remove(key) else restore.putBoolean(key,previous)
                        restore.commit()
                        local.edit().apply { if (split == null) remove(SplitRouting.KEY) else putBoolean(SplitRouting.KEY,split) }.commit()
                        File(ctx.cacheDir,"subscription-qa-state").delete()
                        signal.delete()
                        runOnMainSync { home.finish() }
                    }
                }
                "failover" -> checkFailover(result)
                "formats" -> {
                    checkFormats(result)
                    fun names(vararg names: String) = JSONArray().apply { names.forEach { put(JSONObject().put("name",it)) } }
                    val meta = names("剩余流量：123.45 GB","套餐到期：2026-12-31","距离下次重置剩余：12 天")
                    check((0 until meta.length()).all { CompatEngine.problem(meta.getJSONObject(it)) == "信息" })
                    val parsed = Usage.parse("",meta)
                    check(parsed.remaining == (123.45*1073741824).toLong() && parsed.total == null && parsed.used == null)
                    check(parsed.expires == java.time.LocalDate.of(2026,12,31).atStartOfDay(java.time.ZoneId.systemDefault()).toEpochSecond())
                    check(Usage.parse("total=100;download=25",meta).remaining == 75L)
                    // A header without expire still takes the expiry date from an info node.
                    check(Usage.parse("total=100;download=25",meta).expires == parsed.expires)
                    check(Usage.parse("total=100;download=25;expire=1893456000",meta).expires == 1893456000L)
                    check(listOf("节点 剩余流量：1 GB","套餐到期：2026-02-31","距离下次重置剩余：12 天 US").none(Usage::isInfo))
                    result.putString("usage","3 metadata formats, strict negatives and header precedence passed")
                }
                "refresh", "probe-vpn" -> checkRefresh(result)
                "refresh-source" -> {
                    val store = Subscriptions(targetContext)
                    val source = store.all().single { it.label == args.getString("source") }
                    check(SubscriptionRefresh.refresh(store,source,force=true))
                    val updated = store.get(source.id)
                    check(updated.updated > source.updated && updated.nodes.length() > 0)
                    result.putString("refresh", "${updated.nodes.length()} nodes; ${updated.allowance.traffic()}")
                }
                "probe-lab", "probe-batch" -> batchProbe(result, args.getString("mode") == "probe-lab")
                "probe-source" -> probeSource(result)
                "remove-formats" -> {
                    val store = Subscriptions(targetContext)
                    store.all().filter { it.label.matches(Regex("Nexus-QA-[0-9a-f]{8}-(clash|singbox|links)")) }
                        .forEach { store.remove(it.id) }
                }
                "key" -> {
                    require(!privateKey.exists())
                    val kp = KeyPair.genKeyPair(JSch(),KeyPair.ECDSA,256)
                    try {
                        privateKey.outputStream().use { kp.writePrivateKey(it) }
                        pub.outputStream().use { kp.writePublicKey(it,"emulator-qa") }
                    } finally { kp.dispose() }
                }
                "migration-ssh" -> {
                    val host = requireNotNull(args.getString("host"))
                    fun peer(key: String) = NodeManager(targetContext,host,args.getString(key+"Port")!!.toInt(),
                        "root","",args.getString(key+"Pin")!!,privateKey.readBytes())
                    val src = peer("src")
                    val dst = peer("dst")
                    val dns = requireNotNull(args.getString("dns"))
                    val before = src.deploy("Nexus migration QA",dns,28741,28742)
                    val link = JSONObject(src.owner("invite",JSONObject().put("name","QA migration guest").put("device_seconds",3600)
                        .put("valid_seconds",180).put("quota",123456))).getString("link")
                    Invitation.parse(link).claim("QA migration guest")
                    val devices = JSONArray(src.owner("devices"))
                    val guest = (0 until devices.length()).map(devices::getJSONObject).single { it.getString("name")=="QA migration guest" }
                    src.owner("revoke",JSONObject().put("id",guest.getString("id")))
                    val secret = UUID.randomUUID().toString()
                    val buffer = java.io.ByteArrayOutputStream()
                    src.exportMigration(secret.toCharArray(),buffer)
                    val restored = dst.deploy("Nexus migration QA",dns,28751,28752,readMigration(buffer.toByteArray().inputStream(),secret.toCharArray()))
                    check(before.fingerprint==restored.fingerprint && before.server!=restored.server)
                    val after = JSONArray(dst.owner("devices"))
                    val transferred=(0 until after.length()).map(after::getJSONObject).single { it.getString("id")==guest.getString("id") }
                    check(transferred.getInt("revoked")==1 && transferred.getLong("quota")==123456L)
                    val statusText=dst.service(false)
                    check("nexus-server 0.1.0" in statusText && "CPU 使用率" in statusText && "状态  active" in statusText)
                    result.putString("migration","pinned SSH deployment, encrypted migration, CA, revoked guest, quota and active services")
                    val ctx=targetContext
                    require(android.net.VpnService.prepare(ctx)==null && !TunnelService.state.value.active)
                    val prefs=ctx.getSharedPreferences("local",0)
                    val key=restored.server+"/"+restored.fingerprint
                    val saved=listOf("profile","servers","pinned").associateWith { prefs.getString(it,null) }
                    val home=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    try {
                        check(prefs.edit().remove("pinned").putBoolean("monitor/$key",true).putBoolean("hold/$key",true)
                            .putString("exit/$key","192.0.2.253").commit())
                        restored.save(ctx)
                        ctx.startForegroundService(Intent(ctx,TunnelService::class.java).setAction("vpn"))
                        runBlocking { withTimeout(65000) { TunnelService.state.first { it.vpn && it.error.contains("出口 IP 已变化") } } }
                        check(prefs.getString("exit/$key","192.0.2.253")!="192.0.2.253")
                        result.putString("exit","real HTTPS exit lookup held the VPN after a seeded baseline change")
                    } finally {
                        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
                        runBlocking { withTimeout(10000) { TunnelService.state.first { !it.active } } }
                        val edit=prefs.edit().remove("monitor/$key").remove("hold/$key").remove("exit/$key")
                        saved.forEach { (k,v) -> edit.putString(k,v) }
                        check(edit.commit())
                        runOnMainSync { home.finish() }
                    }
                }
                "deploy" -> {
                    val host = args.getString("host")!!
                    val manager = NodeManager(targetContext,host,args.getString("ssh")!!.toInt(),"root","",args.getString("pin")!!,privateKey.readBytes())
                    val p = manager.deploy("Nexus QA",args.getString("dns")!!,args.getString("nodePort")?.toInt() ?: 28443,args.getString("claimPort")?.toInt() ?: 28444)
                    p.save(targetContext)
                    val rows = JSONArray(manager.owner("devices"))
                    check(rows.length()==1 && rows.getJSONObject(0).getInt("revoked")==0)
                    val invitation = JSONObject(manager.owner("invite",JSONObject().put("name","QA Guest").put("device_seconds",86400)
                        .put("valid_seconds",600).put("quota",1048576))).getString("link")
                    File(root,"qa-invitation").writeText(invitation)
                    result.putString("node","SSH pin, SFTP binaries, systemd, owner profile + device registry, invite")
                }
                "claim" -> {
                    val path = File(root,"qa-invitation")
                    val invite = Invitation.parse(path.readText())
                    val p = invite.claim("QA independent device")
                    check(p.device.contentEquals(invite.claim("QA independent device").device))
                    p.save(targetContext)
                    path.delete()
                    result.putString("claim","independent Android Keystore P-256, pinned TLS, idempotent enrollment")
                }
                "cleanup" -> { privateKey.delete(); pub.delete(); File(root,"qa-invitation").delete() }
                "rotate" -> {
                    val profile=requireNotNull(Profile.load(targetContext))
                    val old=Identity.publicKey()
                    val new=Identity.rotate()
                    check(!old.contentEquals(new) && Identity.publicKey().contentEquals(new))
                    val data=byteArrayOf(7,3,5)
                    val cert=java.security.cert.CertificateFactory.getInstance("X.509")
                        .generateCertificate(profile.device.inputStream())
                    val verifier=java.security.Signature.getInstance("SHA256withECDSA")
                    verifier.initVerify(cert.publicKey);verifier.update(data)
                    check(verifier.verify(Identity.sign(data,profile.signingAlias)))
                    check(Profile.load(targetContext)?.signingAlias==profile.signingAlias)
                    result.putString("rotation","new public key; existing profile still signs with its original non-exportable key")
                    if(args.getString("restore")=="yes") {
                        val alias=requireNotNull(Identity.owner(new));check(alias.startsWith("nexus-device-v1-"))
                        java.security.KeyStore.getInstance("AndroidKeyStore").apply {load(null);deleteEntry(alias)}
                    }
                }
                "packets" -> {
                    val p = requireNotNull(Profile.load(targetContext))
                    val dns = requireNotNull(p.dns)
                    val pair = android.os.ParcelFileDescriptor.createSocketPair()
                    val cancel = android.os.ParcelFileDescriptor.createSocketPair()
                    android.system.Os.fcntlInt(pair[0].fileDescriptor,android.system.OsConstants.F_SETFL,android.system.OsConstants.O_NONBLOCK)
                    val ready = java.util.concurrent.CompletableFuture<Int>()
                    val end = java.util.concurrent.CompletableFuture<Int>()
                    Thread {
                        try {
                            val relay = Tunnel(p.signingAlias,{ true }) { ready.complete(it) }
                            end.complete(relay.run(cancel[0].detachFd(),pair[0].detachFd(),0L,"",dns,p.server,p.tlsName,p.target,p.ca,p.device))
                        } catch (e:Throwable) { end.completeExceptionally(e) }
                    }.start()
                    try {
                        check(ready.get(20,java.util.concurrent.TimeUnit.SECONDS)==0)
                        val query=byteArrayOf(0x6e,0x78,1,0,0,1,0,0,0,0,0,0,7,101,120,97,109,112,108,101,3,99,111,109,0,0,1,0,1)
                        val ip=java.nio.ByteBuffer.allocate(28+query.size)
                        ip.put(0x45).put(0).putShort(ip.capacity().toShort()).putInt(0).put(64).put(17).putShort(0)
                            .put(byteArrayOf(192.toByte(),0,2,2)).put(java.net.InetAddress.getByName(dns).address)
                        var sum=(0 until 20 step 2).sumOf { ip.getShort(it).toInt() and 65535 }
                        while (sum>65535) sum=(sum and 65535)+(sum ushr 16)
                        ip.putShort(10,sum.inv().toShort())
                        ip.putShort(40533.toShort()).putShort(53).putShort((8+query.size).toShort()).putShort(0).put(query)
                        android.system.Os.write(pair[1].fileDescriptor,ip.array(),0,ip.capacity())
                        val poll=android.system.StructPollfd().apply {fd=pair[1].fileDescriptor;events=android.system.OsConstants.POLLIN.toShort()}
                        check(android.system.Os.poll(arrayOf(poll),15000)>0)
                        val data=ByteArray(1500)
                        val size=android.system.Os.read(pair[1].fileDescriptor,data,0,data.size)
                        val msg=java.nio.ByteBuffer.wrap(data,0,size)
                        val udp=(data[0].toInt() and 15)*4
                        check(size>=udp+20 && msg.getShort(udp).toInt()==53 && (msg.getShort(udp+2).toInt() and 65535)==40533)
                        check(msg.getShort(udp+8).toInt()==0x6e78 && data[udp+10].toInt() and 128!=0 &&
                            data[udp+11].toInt() and 15==0 && msg.getShort(udp+14)>0)
                        result.putString("packets","Android Rust packet path: DNS response from deployed resolver, transaction and answers verified")
                    } finally { cancel[1].close();pair[1].close();cancel[0].close();pair[0].close() }
                    end.get(10,java.util.concurrent.TimeUnit.SECONDS)
                }
                "private" -> {
                    val p = requireNotNull(Profile.load(targetContext))
                    val host = "example.com"
                    val ip = java.net.InetAddress.getAllByName(host).first { it is java.net.Inet4Address }.hostAddress
                    val pipe = android.os.ParcelFileDescriptor.createSocketPair()
                    val ready = java.util.concurrent.CompletableFuture<Int>()
                    val outcome = java.util.concurrent.CompletableFuture<Int>()
                    val native = Tunnel(p.signingAlias,{ true }) { ready.complete(it) }
                    Thread {
                        try { outcome.complete(native.run(pipe[0].detachFd(),-1,0L,"","",p.server,p.tlsName,"$ip:443",p.ca,p.device)) }
                        catch (e: Throwable) { outcome.completeExceptionally(e) }
                    }.start()
                    val seconds = java.util.concurrent.TimeUnit.SECONDS
                    try {
                        if (args.getString("deny")=="yes") {
                            check(outcome.get(25,seconds)!=0 && !ready.isDone)
                            result.putString("private","revoked Android device denied before port opened")
                        } else {
                            val local = ready.get(25,seconds)
                            val factory = javax.net.ssl.SSLSocketFactory.getDefault() as javax.net.ssl.SSLSocketFactory
                            java.net.Socket("127.0.0.1",local).use { socket ->
                                (factory.createSocket(socket,host,443,false) as javax.net.ssl.SSLSocket).use { tls ->
                                    tls.sslParameters = tls.sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS" }
                                    tls.soTimeout = 10000
                                    tls.startHandshake()
                                    tls.outputStream.write("GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n".toByteArray())
                                    check(tls.inputStream.bufferedReader().readLine().contains(" 200 "))
                                }
                            }
                            result.putString("private","device authenticated on deployed Debian and received verified HTTPS 200")
                        }
                    } finally { pipe[1].close(); pipe[0].close() }
                    outcome.get(15,seconds)
                    if (ready.isDone) {
                        val local=ready.get()
                        val declined=runCatching { java.net.Socket("127.0.0.1",local).close() }.exceptionOrNull()
                        check(declined is java.net.ConnectException)
                        result.putString("disconnect","old loopback port refused a fresh connection")
                    }
                }
                "interop" -> {
                    val file = File(root,"qa-interop")
                    val fixture = JSONObject(file.readText())
                    val list = fixture.getJSONArray("nodes")
                    val failed = mutableListOf<String>()
                    for (i in 0 until list.length()) {
                        val node = list.getJSONObject(i)
                        val type = node.getString("type")
                        val label = node.optString("name",type)
                        result.putString(label,"started")
                        try {
                        val out = CompatEngine.outbound(node)
                        val port = java.net.ServerSocket(0,1,java.net.InetAddress.getLoopbackAddress()).use { it.localPort }
                        val cfg = JSONObject().put("inbounds",JSONArray().put(JSONObject().put("listen","127.0.0.1").put("port",port)
                            .put("protocol","socks").put("settings",JSONObject().put("auth","noauth"))))
                            .put("outbounds",JSONArray().put(out)).put("log",JSONObject().put("loglevel","none"))
                        fun call(method: String, payload: JSONObject) =
                            JSONObject(libXray.LibXray.invoke(JSONObject().put("apiVersion",3).put("method",method).put("payload",payload).toString()))
                        var handle = 0L
                        val pair = if (out.has("shoesClient")) android.os.ParcelFileDescriptor.createSocketPair() else null
                        try {
                            if (pair != null) {
                                val client = out.getJSONObject("shoesClient")
                                val tls = client.optJSONObject("quic_settings") ?: client.getJSONObject("protocol")
                                val pin = fixture.getString("pin")
                                require(pin.matches(Regex("[0-9a-f]{64}")))
                                // The private fixture uses its exact leaf fingerprint as its trust anchor.
                                tls.put("verify",false).put("server_fingerprints",JSONArray().put(pin))
                                val rule = JSONObject().put("masks","0.0.0.0/0").put("action","allow").put("client_chain",client)
                                val servers = JSONArray().put(JSONObject().put("device_fd",pair[0].fd))
                                    .put(JSONObject().put("address","127.0.0.1:$port").put("protocol",JSONObject().put("type","mixed"))
                                        .put("rules",JSONArray().put(rule)))
                                check(com.shoesproxy.ShoesNative.init("off")==0)
                                handle = com.shoesproxy.ShoesNative.start(servers.toString()) { true }
                                check(handle>0)
                                Thread.sleep(500)
                                check(com.shoesproxy.ShoesNative.isRunning())
                            } else check(call("runXray",JSONObject().put("xrayJson",cfg.toString())).getBoolean("success"))
                            val proxy = java.net.Proxy(java.net.Proxy.Type.SOCKS,java.net.InetSocketAddress("127.0.0.1",port))
                            java.net.Socket(proxy).use { socket ->
                                socket.soTimeout=10000
                                socket.connect(java.net.InetSocketAddress("192.0.2.5",80),10000)
                                socket.getOutputStream().write("GET /check HTTP/1.0\r\nHost: node.test\r\n\r\n".toByteArray())
                                val reader = java.io.DataInputStream(socket.getInputStream())
                                check(reader.readLine()?.contains(" 200 ") == true)
                                var length = 0
                                while (true) {
                                    val line = checkNotNull(reader.readLine())
                                    if (line.isEmpty()) break
                                    if (line.startsWith("Content-Length:",true)) length = line.substringAfter(':').trim().toInt()
                                }
                                require(length in 1..512)
                                val body = ByteArray(length)
                                reader.readFully(body)
                                check(body.toString(Charsets.UTF_8).startsWith("Nexus interop "))
                            }
                            result.putString(label,"node-side HTTP receipt")
                        } finally {
                            if (handle>0) com.shoesproxy.ShoesNative.stop(handle)
                            pair?.forEach { it.close() }
                            if (pair==null) call("stopXray",JSONObject())
                        }
                        } catch (e: Exception) {
                            failed += label
                            result.putString(label,"FAIL ${e.javaClass.simpleName}: ${e.message}")
                        }
                    }
                    file.delete()
                    check(failed.isEmpty()) { "failed: $failed" }
                }
                else -> error("unknown QA mode")
            }
            status = Activity.RESULT_OK
        } catch (e: Throwable) {
            result.putString("error",e.javaClass.simpleName + " at " + e.stackTrace.take(3).joinToString())
        } finally { finish(status,result) }
    }
}
