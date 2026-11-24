package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
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

private fun until(ms: Long, condition: () -> Boolean) {
    val end = now() + ms
    do {
        if (condition()) return
        SystemClock.sleep(50)
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
    require(phase in setOf("bench","block","v2","manage","restart"))
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
        return s.connectedAt > 0 && s.vpn && (if (kind == "h2" || kind == "h3") bearer() == kind
            else s.subscription.isNotBlank() && !s.shoes)
    }
    fun usable(kind: String, ms: Long) {
        until(ms) {
            val before = available(kind)
            val response = runCatching { fresh() }.isSuccess
            before && response && available(kind)
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
            check(available(if(kind=="v2") "h3" else "xray"))
            val rate = 8000.0*bytes/elapsed
            val sample = "$kind-$index"
            result.putDouble(sample,rate)
            sendStatus(1,Bundle().apply { putDouble(sample,rate) })
            return rate
        } finally { conn.disconnect() }
    }
    val home = startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    try {
        check(prefs.edit().putBoolean(SplitRouting.KEY,false).commit())
        when (phase) {
            "bench" -> {
                val samples = requireNotNull(args.getString("pairs")).toInt().also { require(it in 1..60) }
                val rates = mapOf("v2" to mutableListOf<Double>(),"reality" to mutableListOf<Double>())
                repeat(samples) { n ->
                    for(kind in if(n%2==0) rates.keys.toList() else rates.keys.reversed()) {
                        marker("bench-$kind-${n+1}")
                        start(kind); usable(if(kind=="v2") "h3" else "xray",30000)
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
                start("v2"); usable("h3",30000)
                result.putString("https","verified on VPN")
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
                result.putLong("one-day-ssh-and-device-expiry",expiry)
                stop()
                admin.v2Renew(account,V2ShareValidity(null))
                check(deviceRow(freshDevice.deviceId).isNull("expires_at"))
                marker("permanent-again")
                start("v2"); usable("h3",30000)
                until(6000) { v2().expiryKnown && v2().expiresAt == null }
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
                start("v2"); usable("h3",30000)
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
                        val ok = runCatching { fresh() }.isSuccess
                        if (!ok) {
                            if (firstFailure < 0) firstFailure = now()-began
                            latestFailure = now()-began
                            resumed = -1
                        } else if (firstFailure >= 0 && resumed < 0) resumed = now()-began
                        val done = resumed >= 0 && available("h3") && signal.takeIf { it.exists() }?.readText()?.trim()=="node-up"
                        if (!done) SystemClock.sleep(150)
                        done
                    }
                    check(originalPid == android.os.Process.myPid())
                    result.putLong("restart-first-failure-ms-from-arm",firstFailure)
                    result.putLong("restart-last-failure-ms-from-arm",latestFailure)
                    result.putLong("restart-first-recovered-https-ms-from-arm",resumed)
                    result.putBoolean("same-android-vpn-network",!originalLost.get() && vpn()==initialNetwork)
                    result.putBoolean("same-app-process",true)
                } finally { cm.unregisterNetworkCallback(cb) }
            }
            "block" -> {
                start("v2"); usable("h3",30000)
                HeldHttps(vpn(),endpoint(),2000).use { old ->
                    old.ping()
                    marker("ready-drop"); awaitSignal("arm-drop")
                    val began = now()
                    marker("armed-drop")
                    var lost = -1L
                    val latestOldResponse = java.util.concurrent.atomic.AtomicLong()
                    val running = java.util.concurrent.atomic.AtomicBoolean(true)
                    val worker = kotlin.concurrent.thread {
                        try { while(running.get()) { old.ping(); latestOldResponse.set(now()-began); SystemClock.sleep(100) } }
                        catch (_: Exception) { lost=now()-began }
                    }
                    try {
                        usable("h2",35000)
                        result.putLong("hot-H2-fresh-https-ms-from-arm",now()-began)
                    } finally {
                        running.set(false); worker.join(3500)
                    }
                    result.putLong("old-H3-last-HTTPS-ms-from-arm",latestOldResponse.get())
                    result.putLong("old-H3-first-failed-HTTPS-ms-from-arm",lost)
                }
                awaitSignal("blocked")
                stop()
                val cold = now()
                start("v2"); usable("h2",30000)
                result.putLong("cold-H2-fresh-https-ms",now()-cold)
                HeldHttps(vpn(),endpoint(),2000).use { held ->
                    held.ping(); marker("ready-unblock"); awaitSignal("arm-unblock")
                    val began = now()
                    marker("armed-unblock")
                    var replies = 0
                    until(160000) {
                        held.ping(); replies++
                        if(available("h3")) true else { SystemClock.sleep(300); false }
                    }
                    fresh(); held.ping()
                    result.putLong("restored-H3-fresh-https-ms-from-arm",now()-began)
                    result.putInt("same-H2-TCP-replies",replies+2)
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
