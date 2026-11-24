package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import java.io.BufferedInputStream
import java.io.Closeable
import java.io.File
import java.net.InetSocketAddress
import java.net.Inet4Address
import java.net.URL
import java.security.MessageDigest
import java.util.Locale
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory
import org.json.JSONObject

private fun now() = SystemClock.elapsedRealtime()

private fun until(ms: Long, pollMs: Long = 50, condition: () -> Boolean) {
    val end = now() + ms
    do {
        if (condition()) return
        SystemClock.sleep(pollMs)
    } while (now() < end)
    error("timeout")
}

private fun https(raw: String): URL = URL(raw).also {
    require(it.protocol == "https" && it.host.isNotBlank() && it.userInfo == null && it.ref == null)
}

// Each instance owns one VPN-bound TCP socket; repeated HEADs cannot reconnect silently.
private class HeldHttps(network: Network, private val url: URL, timeout: Int) : Closeable {
    private val socket: SSLSocket
    private val input: BufferedInputStream
    init {
        val port = if (url.port < 0) 443 else url.port
        val plain = network.socketFactory.createSocket()
        try {
            val addresses = network.getAllByName(url.host)
            val address = addresses.firstOrNull { it is Inet4Address } ?: addresses.first()
            plain.connect(InetSocketAddress(address, port), timeout)
            plain.soTimeout = timeout
            socket = (SSLSocketFactory.getDefault() as SSLSocketFactory)
                .createSocket(plain, url.host, port, true) as SSLSocket
            socket.sslParameters = socket.sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS" }
            socket.startHandshake()
            input = BufferedInputStream(socket.inputStream)
        } catch (e: Throwable) { plain.close(); throw e }
    }
    fun ping() {
        val request = "HEAD ${url.file.ifEmpty { "/" }} HTTP/1.1\r\nHost: ${url.authority}\r\nConnection: keep-alive\r\nAccept-Encoding: identity\r\n\r\n"
        socket.outputStream.write(request.toByteArray(Charsets.US_ASCII))
        socket.outputStream.flush()
        fun line(): String {
            val out = StringBuilder()
            while (out.length <= 8192) {
                val ch = input.read()
                check(ch >= 0)
                if (ch == 10) return out.toString().trimEnd('\r')
                out.append(ch.toChar())
            }
            error("long response header")
        }
        check(line().split(' ').getOrNull(1) == "200")
        repeat(128) { if (line().isEmpty()) return }
        error("too many headers")
    }
    override fun close() = socket.close()
}

internal fun Instrumentation.m4Probe(args: Bundle, result: Bundle) {
    val ctx = targetContext
    val phase = requireNotNull(args.getString("phase"))
    fun file(name: String) = File(ctx.noBackupFilesDir, "m4-" + name)
    val cfgFile = File(ctx.noBackupFilesDir, "m4.json")
    val cfg = if (cfgFile.isFile) JSONObject(cfgFile.readText()) else JSONObject()
    fun ownerHost(): String = args.getString("host") ?: cfg.optString("ownerHost").ifBlank { cfg.optString("host") }.ifBlank {
        requireNotNull(OwnerAccess(ctx).current)
    }
    fun v2Host() = cfg.optString("v2Host").ifBlank { ownerHost() }.removeSurrounding("[","]").lowercase(Locale.ROOT)
    val owners = ctx.getSharedPreferences("m4-owner-keys",0)
    fun manager() = OwnerAccess(ctx).manager(ownerHost())
    var temporary: V2Node? = null
    fun v2(): V2Node {
        temporary?.let { return requireNotNull(V2Devices.get(ctx,it.key)) }
        val host = v2Host()
        owners.getString(host,null)?.let { V2Devices.get(ctx,it) }
            ?.takeIf { it.host.equals(host,true) }?.let { return it }
        val peerKey = file("device-key").takeIf { it.isFile }?.readText()
        return V2Devices.list(ctx).single { it.host.equals(host,true) && it.key != peerKey }
            .also { check(owners.edit().putString(host,it.key).commit()) }
    }
    fun deviceRow(id: String): JSONObject {
        val rows = manager().v2Devices()
        return (0 until rows.length()).map(rows::getJSONObject).single { it.getString("id") == id }
    }
    when (phase) {
        "enroll" -> {
            val path = file("enroll.link")
            val invite = try { V2Invite.parse(path.readText()) } finally { check(path.delete()) }
            val host = cfg.getString("v2Host").removeSurrounding("[","]").lowercase(Locale.ROOT)
            check(host.isNotBlank() && invite.host.removeSurrounding("[","]").equals(host,true))
            val node = V2Devices.enroll(ctx,invite)
            check(node.host.equals(host,true) && owners.edit().putString(host,node.key).commit())
            result.putString("enroll","verified")
            return
        }
        "invite" -> { file("v2.link").writeText(manager().v2Invite()); result.putString("invite","created"); return }
        "links" -> {
            val row = deviceRow(v2().deviceId)
            val protocol = args.getString("protocol") ?: "reality"
            require(protocol in setOf("reality","hysteria2"))
            val link = manager().v2CompatLink(row.getString("user_id"),protocol)
            check(android.net.Uri.parse(link).host?.removeSurrounding("[","]").equals(v2Host(),true))
            file(protocol+".link").writeText(link)
            result.putString("export",protocol)
            return
        }
    }
    require(phase in setOf("bench","block","v2","manage","restart","reference"))
    check(VpnService.prepare(ctx) == null)
    check(!TunnelService.state.value.active)
    val prefs = ctx.getSharedPreferences("local",0)
    val oldSplit = prefs.all[SplitRouting.KEY] as? Boolean
    val cm = ctx.getSystemService(ConnectivityManager::class.java)
    val state = { TunnelService.state.value }
    fun vpn(): Network = requireNotNull(cm.activeNetwork).also {
        check(cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true)
    }
    fun bearer() = state().bearer.lowercase(Locale.ROOT)
    fun endpoint() = https(cfg.optString("probeUrl","https://example.com/"))
    fun fresh() = HeldHttps(vpn(),endpoint(),2000).use { it.ping() }
    fun available(kind: String): Boolean {
        val s = state()
        if (kind == "v2") return s.connectedAt > 0 && s.vpn && bearer() in setOf("h2","h3")
        return s.connectedAt > 0 && s.vpn && (if (kind == "h2" || kind == "h3") bearer() == kind
            else s.subscription.isNotBlank() && !s.shoes)
    }
    fun usable(kind: String, ms: Long) {
        until(ms) {
            val before = available(kind)
            before && runCatching { fresh() }.isSuccess && available(kind)
        }
    }
    fun stop() {
        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
        until(12000) { !state().active && cm.getNetworkCapabilities(cm.activeNetwork)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) != true }
    }
    val store = Subscriptions(ctx)
    var source: String? = null
    fun start(kind: String) {
        val request = if (kind == "v2") v2Request(ctx,v2().key) else {
            val link = file(kind+".link").readText().trim()
            val uri = android.net.Uri.parse(link)
            require(uri.scheme == "vless" && uri.getQueryParameter("security") == "reality")
            check(uri.host?.removeSurrounding("[","]").equals(v2Host(),true))
            val sub = store.import("M4-"+kind,link)
            source = sub.id
            val row = sourceRows(listOf(sub)).single()
            check(row.type in CompatEngine.types-ShoesEngine.types)
            subscriptionRequest(ctx,row,listOf(row),cfg.optString("dns","1.1.1.1"),false)
        }
        ctx.startForegroundService(request)
    }
    fun clearSource() { source?.let(store::remove); source = null }
    val stage = file("stage")
    val signal = file("signal")
    fun marker(value: String) { stage.writeText(value); result.putString("stage",value) }
    fun awaitSignal(value: String) { until(180000) { signal.takeIf { it.exists() }?.readText()?.trim() == value } }
    val benchBearer = if (args.getString("protocol") == "h2") "h2" else "h3"
    fun download(kind: String, index: Int): Double {
        val url = https(cfg.getString("url"))
        val expectedSize = cfg.getLong("bytes").also { require(it > 0) }
        val expectedHash = cfg.getString("sha256").lowercase(Locale.ROOT).also { require(it.matches(Regex("[a-f0-9]{64}"))) }
        val conn = vpn().openConnection(url) as HttpsURLConnection
        val hash = MessageDigest.getInstance("SHA-256")
        try {
            conn.useCaches=false; conn.instanceFollowRedirects=false; conn.connectTimeout=12000; conn.readTimeout=30000
            conn.setRequestProperty("Accept-Encoding","identity"); conn.setRequestProperty("Connection","close")
            val began=SystemClock.elapsedRealtimeNanos()
            check(conn.responseCode == 200)
            val buffer=ByteArray(65536)
            var bytes=0L
            conn.inputStream.use { input ->
                while (true) {
                    val read = input.read(buffer)
                    if (read == -1) break
                    bytes += read
                    check(bytes <= expectedSize)
                    hash.update(buffer,0,read)
                }
            }
            val elapsed = SystemClock.elapsedRealtimeNanos()-began
            check(bytes==expectedSize && hash.digest().joinToString("") { "%02x".format(it) }==expectedHash)
            check(available(if(kind=="v2") benchBearer else "xray"))
            val rate = 8000.0*bytes/elapsed
            val sample = "$kind-$index"
            result.putDouble(sample,rate)
            sendStatus(1,Bundle().apply { putDouble(sample,rate) })
            return rate
        } finally { conn.disconnect() }
    }
    val drain = runCatching { mobile.Mobile::class.java.getMethod("drainTransportTrace") }.getOrNull()
    fun trace(label: String) {
        val lines = drain?.invoke(null)?.toString()?.lineSequence()?.filter {
            it.matches(Regex("[0-9]+\\t[a-z_0-9]+\\t[a-z0-9_-]*([\\t][0-9]*)?"))
        }?.joinToString("|")
        result.putString("trace-$label", lines ?: "unavailable")
    }
    fun observeSelection(kind: String, stamp: java.util.concurrent.atomic.AtomicLong) =
        CoroutineScope(Dispatchers.Unconfined).launch(start = CoroutineStart.UNDISPATCHED) {
            TunnelService.state.collect { s ->
                if (s.vpn && s.bearer.equals(kind,true)) stamp.compareAndSet(-1,now())
            }
        }
    val home = startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    try {
        check(prefs.edit().putBoolean(SplitRouting.KEY,false).commit())
        when (phase) {
            "bench" -> {
                val samples = requireNotNull(args.getString("pairs")).toInt().also { require(it in 1..60) }
                result.putString("v2-benchmark-bearer",benchBearer)
                val rates = mapOf("v2" to mutableListOf<Double>(),"reality" to mutableListOf<Double>())
                repeat(samples) { n ->
                    for(kind in if(n%2==0) rates.keys.toList() else rates.keys.reversed()) {
                        marker("bench-$kind-${n+1}")
                        start(kind); usable(if(kind=="v2") benchBearer else "xray",30000)
                        rates.getValue(kind).add(download(kind,n+1))
                        stop(); clearSource()
                    }
                }
                for ((kind,values) in rates) {
                    values.sort()
                    result.putDouble("$kind-median-Mbps",(values[(samples-1)/2]+values[samples/2])/2)
                }
            }
            "v2" -> {
                start("v2"); usable("v2",30000)
                val loaded = java.util.concurrent.CountDownLatch(1)
                lateinit var page: android.webkit.WebView
                runOnMainSync {
                    page = android.webkit.WebView(home)
                    home.setContentView(page)
                    page.webViewClient = object : android.webkit.WebViewClient() {
                        override fun onPageFinished(view: android.webkit.WebView, url: String) {
                            if (view.title == "Example Domain" && url == "https://example.com/") loaded.countDown()
                        }
                    }
                    page.loadUrl("https://example.com/")
                }
                try {
                    check(loaded.await(25,java.util.concurrent.TimeUnit.SECONDS))
                    check(available("v2"))
                    result.putString("https","Example Domain rendered over " + bearer())
                } finally { runOnMainSync { home.setContentView(android.widget.FrameLayout(home)); page.destroy() } }
            }
            "manage" -> {
                val owner = v2()
                val admin = manager()
                val invitation = admin.v2Invite()
                val freshDevice = V2Devices.enroll(ctx,V2Invite.parse(invitation))
                check(owner.deviceId != freshDevice.deviceId && owner.host == freshDevice.host)
                temporary = freshDevice
                file("device-key").writeText(freshDevice.key)
                val initial = deviceRow(freshDevice.deviceId)
                check(initial.isNull("expires_at") && initial.isNull("revoked_at"))
                val account = initial.getString("user_id")
                marker("permanent")
                start("v2"); usable("h3",30000)
                check(v2().expiryKnown && v2().expiresAt == null); stop()
                admin.v2Renew(account,V2ShareValidity(1))
                val expiry = deviceRow(freshDevice.deviceId).getLong("expires_at")
                marker("one-day")
                start("v2"); usable("h3",30000)
                until(6000) { v2().expiryKnown && v2().expiresAt == expiry }
                splitLabel(v2().expiryText(),false)
                result.putLong("one-day-ssh-and-device-expiry",expiry)
                stop()
                admin.v2Renew(account,V2ShareValidity(null))
                check(deviceRow(freshDevice.deviceId).isNull("expires_at"))
                marker("permanent-again")
                start("v2"); usable("h3",30000)
                until(6000) { v2().expiryKnown && v2().expiresAt == null }
                splitLabel(v2().expiryText(),false)
                result.putBoolean("home-displayed-both-expiries",true)
                result.putString("permanent-ssh-and-device","verified")
                stop()
                marker("revoke-peer")
                admin.v2Revoke(freshDevice.deviceId)
                check(!deviceRow(freshDevice.deviceId).isNull("revoked_at"))
                start("v2")
                until(30000) { !state().vpn && state().error == V2Connection.error(3) }
                check(!prefs.contains(TunnelService.RESUME))
                result.putString("peer-auth","rejected after SSH revoke")
                check(V2Devices.get(ctx,owner.key)?.deviceId == owner.deviceId)
                temporary = null
            }
            "restart" -> {
                start("v2"); usable("v2",30000)
                val initialNetwork = vpn()
                val originalPid = android.os.Process.myPid()
                val originalLost = java.util.concurrent.atomic.AtomicBoolean()
                val cb = object : ConnectivityManager.NetworkCallback() {
                    override fun onLost(network: Network) {
                        if (network == initialNetwork) originalLost.set(true)
                    }
                }
                cm.registerNetworkCallback(android.net.NetworkRequest.Builder()
                    .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    .addTransportType(NetworkCapabilities.TRANSPORT_VPN).build(),cb)
                try {
                    marker("ready-restart"); awaitSignal("arm-restart")
                    val began = now()
                    var firstFailure = -1L
                    var latestFailure = -1L
                    var resumed = -1L
                    marker("armed-restart")
                    until(420000) {
                        val newBootAlreadyVerified = signal.takeIf { it.isFile }?.readText()?.trim()=="node-up"
                        val ok = runCatching { fresh() }.isSuccess
                        if (!ok) {
                            if (firstFailure < 0) firstFailure = now()-began
                            latestFailure = now()-began
                            resumed = -1
                        } else if (firstFailure >= 0 && resumed < 0) resumed = now()-began
                        val done = newBootAlreadyVerified && ok && resumed >= 0 && available("v2")
                        if (!done) SystemClock.sleep(150)
                        done
                    }
                    check(originalPid == android.os.Process.myPid())
                    result.putLong("restart-first-failure-ms-from-arm",firstFailure)
                    result.putLong("restart-last-failure-ms-from-arm",latestFailure)
                    result.putLong("restart-first-recovered-https-ms-from-arm",resumed)
                    result.putBoolean("same-android-vpn-network",!originalLost.get() && vpn()==initialNetwork)
                    result.putLong("restart-post-verified-boot-https-ms-from-arm",now()-began)
                    result.putBoolean("same-app-process",true)
                    result.putString("recovered-bearer",bearer())
                } finally { cm.unregisterNetworkCallback(cb) }
            }
            "reference" -> M4Traffic(this,home,cfg,::vpn,::available).use { traffic ->
                trace("prior")
                start("v2")
                until(30000) { available("h3") && runCatching { vpn() }.isSuccess }
                traffic.echoSocket().use(traffic::ping)
                check(available("h3"))
                val offH3 = java.util.concurrent.atomic.AtomicLong(-1)
                val observer = observeSelection("h2",offH3)
                try {
                    val origin = now()
                    val report = traffic.measure("reference","h3",origin,origin+30000)
                    if (offH3.get() >= 0) report.put("state","ineligible-bearer-changed")
                    result.putString("reference",report.toString())
                    trace("reference")
                } finally { observer.cancel() }
            }
            "block" -> {
                fun origin(expected: String, pollMs: Long = 50): Long {
                    var stamp: Long? = null
                    until(180000,pollMs) {
                        val value = signal.takeIf { it.isFile }?.readText()?.trim().orEmpty()
                        if (value.startsWith("$expected ")) stamp = value.substringAfter(' ').toLongOrNull()
                        stamp != null
                    }
                    return requireNotNull(stamp)
                }
                M4Traffic(this,home,cfg,::vpn,::available).use { traffic ->
                    trace("prior")
                    start("v2")
                    until(30000) { available("h3") && runCatching { vpn() }.isSuccess }
                    traffic.echoSocket().use { old ->
                        traffic.ping(old)
                        val firstH2 = java.util.concurrent.atomic.AtomicLong(-1)
                        val observer = observeSelection("h2",firstH2)
                        marker("ready-drop"); awaitSignal("arm-drop")
                        val running = java.util.concurrent.atomic.AtomicBoolean(true)
                        val last = java.util.concurrent.atomic.AtomicLong(-1)
                        val failure = java.util.concurrent.atomic.AtomicLong(-1)
                        val worker = kotlin.concurrent.thread {
                            while (running.get()) {
                                try {
                                    traffic.ping(old)
                                    last.set(now())
                                } catch (_: Exception) {
                                    if (running.get()) failure.set(now())
                                    break
                                }
                                SystemClock.sleep(100)
                            }
                            // If the held flow dies before a fallback is available, maintain ordinary
                            // demand independently. These attempts are never used as business receipts.
                            while (running.get() && !available("h2")) {
                                runCatching { traffic.echoSocket().use(traffic::ping) }
                                SystemClock.sleep(200)
                            }
                        }
                        marker("armed-drop")
                        try {
                            val began = origin("blocked")
                            val hot = traffic.measure("hot","h2",began,began+60000)
                            hot.put("firstH2ObservedBootMs",firstH2.get())
                            if (firstH2.get() in 0 until began) hot.put("state","ineligible-prior-H2")
                            result.putString("hot",hot.toString())
                        } finally { running.set(false); observer.cancel(); worker.join() }
                        result.putLong("old-H3-last-reply-boot-ms",last.get())
                        result.putLong("old-H3-first-error-boot-ms",failure.get())
                    }
                    trace("hot")
                    stop()
                    val coldRequest = v2Request(ctx,v2().key)
                    marker("ready-cold-drop"); awaitSignal("arm-cold-drop")
                    marker("armed-cold-drop")
                    val cold = origin("cold-blocked",5)
                    val launch = now()
                    ctx.startForegroundService(coldRequest)
                    val measured = traffic.measure("cold","h2",cold,cold+60000)
                    measured.put("launchBootMs",launch).put("launchDelayMs",launch-cold)
                    result.putString("cold",measured.toString())
                    trace("cold")
                    val firstH3 = java.util.concurrent.atomic.AtomicLong(-1)
                    val observer = observeSelection("h3",firstH3)
                    marker("ready-unblock"); awaitSignal("arm-unblock")
                    marker("armed-unblock")
                    val restored = origin("restored")
                    if (args.getString("recovery") != "false") {
                        // Keep real requests flowing while the existing VPN probes for H3.
                        val running = java.util.concurrent.atomic.AtomicBoolean(true)
                        val replies = java.util.concurrent.atomic.AtomicInteger()
                        val worker = kotlin.concurrent.thread {
                            while (running.get() && !available("h3")) {
                                if (runCatching { traffic.echoSocket().use(traffic::ping) }.isSuccess) replies.incrementAndGet()
                                SystemClock.sleep(300)
                            }
                        }
                        try {
                            val recovered = traffic.measure("recover","h3",restored,restored+300000)
                            recovered.put("firstH3ObservedBootMs",firstH3.get())
                            if (firstH3.get() in 0 until restored) recovered.put("state","ineligible-prior-H3")
                            result.putString("recover",recovered.toString())
                        } finally { running.set(false); worker.join() }
                        result.putInt("recovery-background-echo-replies",replies.get())
                        trace("recover")
                    }
                    observer.cancel()
                }
            }
        }
    } finally {
        try { stop() } finally {
            clearSource()
            prefs.edit().let { if (oldSplit==null) it.remove(SplitRouting.KEY) else it.putBoolean(SplitRouting.KEY,oldSplit) }.commit()
            runOnMainSync { home.finish() }
        }
    }
}
