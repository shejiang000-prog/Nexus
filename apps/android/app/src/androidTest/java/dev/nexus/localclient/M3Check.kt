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
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import org.json.JSONObject

internal fun Instrumentation.m3Check(result: Bundle, phase: String, probePackage: String) {
    require(phase in setOf("all", "switch", "recovery", "regression", "blackhole", "rate", "loss", "idle-h2", "idle-h3", "healthy-download"))
    val ctx = targetContext
    check(VpnService.prepare(ctx) == null) { "missing VPN consent" }
    val dir = ctx.noBackupFilesDir
    val cfg = JSONObject(File(dir, "m3.json").readText())
    val addr = InetSocketAddress(cfg.getString("target"), cfg.getInt("echoPort"))
    val cm = ctx.getSystemService(ConnectivityManager::class.java)
    val physical = requireNotNull(cm.activeNetwork)
    val url = V2Invite.parse(cfg.getString("invitation"))
    val node = V2Devices.enroll(ctx, url, cfg.getString("endpoint"), cfg.getString("ca").toByteArray())
    check(node.expiryKnown && node.expiresAt != null)
    val keyStore = java.security.KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    check(keyStore.getKey(node.key,null)?.let { it.encoded == null } == true)
    check(V2Devices.get(ctx,node.key)?.deviceId == node.deviceId)
    result.putString("device", "Keystore private key non-exportable; enrolled ID persisted")
    val stage = File(dir, "m3-stage")
    val prefs = ctx.getSharedPreferences("local", 0)
    val original = prefs.all[SplitRouting.KEY] as? Boolean
    val home = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    val store = Subscriptions(ctx)
    var source: String? = null
    val clock = SystemClock::elapsedRealtime
    fun waitFor(ms: Long, pred: () -> Boolean) {
        val limit = clock() + ms
        do { if (pred()) return; Thread.sleep(20) } while (clock() < limit)
        error("timed out at " + stage.takeIf { it.exists() }?.readText())
    }
    fun control(command: String) {
        physical.socketFactory.createSocket().use { s ->
            s.soTimeout = 4000
            s.connect(InetSocketAddress(cfg.getString("host"), cfg.getInt("controlPort")), 1200)
            s.getOutputStream().write((cfg.getString("control") + " " + command + "\n").toByteArray())
            check(s.getInputStream().read() == 1) { "lab control failed" }
        }
    }
    fun vpn(): Network? = cm.activeNetwork?.takeIf {
        cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
    }
    fun connect(): Socket {
        val s = requireNotNull(vpn()).socketFactory.createSocket()
        try { s.soTimeout = 400; s.connect(addr, 350); return s }
        catch(e: Exception) {s.close(); throw e}
    }
    fun echo(s: Socket) { s.getOutputStream().write(65); check(s.getInputStream().read() == 65) }
    fun usable(bearer: String, max: Long): Long {
        val start = clock()
        waitFor(max) { runCatching { connect().use(::echo) }.isSuccess && TunnelService.state.value.bearer.equals(bearer, true) }
        return clock() - start
    }
    fun startV2() = ctx.startForegroundService(Intent(ctx,TunnelService::class.java).setAction("v2").putExtra("v2-key",node.key))
    fun stop() {
        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
        waitFor(7000) { !TunnelService.state.value.active && vpn() == null }
    }
    fun download(): String = connect().use { s ->
        s.soTimeout=12000
        val start=SystemClock.elapsedRealtimeNanos()
        s.getOutputStream().write(68)
        val input=DataInputStream(s.getInputStream()); val chunk=ByteArray(65536)
        repeat(64) { input.readFully(chunk); check(chunk.all { it == 42.toByte() }) }
        "%.2f".format(java.util.Locale.ROOT, 4194304.0 * 8000 / (SystemClock.elapsedRealtimeNanos()-start))
    }
    fun udp() {
        DatagramSocket().use { s ->
            requireNotNull(vpn()).bindSocket(s)
            s.soTimeout=3500;s.connect(addr)
            val sent=java.security.SecureRandom().generateSeed(16);s.send(DatagramPacket(sent,sent.size))
            val answer=DatagramPacket(ByteArray(64),64);s.receive(answer)
            check(answer.data.copyOf(answer.length).contentEquals(sent))
        }
    }
    fun external(label: String) {
        File(dir,"m3-probe-ok").delete()
        stage.writeText(label)
        waitFor(75000){ File(dir,"m3-probe-ok").isFile }
    }
    val cold=mutableListOf<Long>();val hot=mutableListOf<Long>()
    try {
        prefs.edit().putBoolean(SplitRouting.KEY,false).commit()
        control("clear")
        if (phase == "all") {
            stage.writeText("startup")
            startV2();usable("h3",12000);udp()
            connect().use { healthy ->
                val since = clock()
                while (clock()-since < 5000) {
                    echo(healthy); check(TunnelService.state.value.bearer.equals("h3",true))
                    Thread.sleep(150)
                }
            }
            result.putString("healthy-H3","5 s continuous TCP activity on open UDP; H3 retained")
            result.putString("4MiB-H3-Mbps",download());external("probe-v2")
            stop()
        }
        if (phase in setOf("rate","loss","healthy-download","idle-h2","idle-h3")) {
            val times = mutableListOf<Long>()
            repeat(if(phase=="rate" || phase=="loss") 10 else 1) { n ->
                stage.writeText("$phase-${n+1}")
                val bearer = if(phase=="idle-h2") "h2" else "h3"
                control(if(bearer=="h2") "drop" else "clear")
                startV2();usable(bearer,12000)
                if(phase.startsWith("idle-")) {
                    // Exclude setup exchanges and the recent-business window. No polling or open business flows.
                    runOnMainSync { home.moveTaskToBack(true) }
                    Thread.sleep(35000)
                    control("capture-$bearer")
                    try { idleBearer(bearer,result) } finally { control("capture-stop") }
                    usable(bearer,5000)
                } else if(phase=="healthy-download") {
                    val began=clock()
                    var received=0L
                    var sampleAt=began
                    var sampleBytes=0L
                    val rates=java.util.ArrayDeque<Long>()
                    try { connect().use { s ->
                        s.soTimeout=8000
                        val buf=ByteArray(65536)
                        val input=DataInputStream(s.getInputStream())
                        while(clock()-began<600000) {
                            s.getOutputStream().write(68)
                            repeat(64) {
                                input.readFully(buf)
                                check(buf.first()==42.toByte() && buf.last()==42.toByte())
                                received+=buf.size
                                val t=clock()
                                if(t-sampleAt>=500) {
                                    rates.addLast(8*(received-sampleBytes)/(t-sampleAt)) // kbit/s
                                    if(rates.size>20) rates.removeFirst()
                                    sampleAt=t;sampleBytes=received
                                }
                                check(TunnelService.state.value.bearer.equals("h3",true))
                            }
                        }
                    }
                    } finally {
                        result.putLong("normal-download-ms",clock()-began)
                        result.putLong("normal-download-bytes",received)
                        result.putString("normal-download-last-kbps",rates.joinToString(","))
                    }
                } else {
                    connect().use { demand ->
                        demand.soTimeout=15000
                        val finished=java.util.concurrent.atomic.AtomicBoolean()
                        val worker=kotlin.concurrent.thread(name="Lab download") {
                            try {
                                val input=DataInputStream(demand.getInputStream());val buf=ByteArray(65536)
                                while(!finished.get()) {
                                    demand.getOutputStream().write(68)
                                    repeat(64){input.readFully(buf)}
                                }
                            } catch(_:java.io.IOException){}
                        }
                        try {
                            val begin=clock()
                            control(phase)
                            val measured=runCatching {
                                waitFor(12000){TunnelService.state.value.bearer.equals("h2",true)}
                                usable("h2",3000);clock()-begin
                            }.getOrDefault(-1L)
                            times.add(measured)
                            result.putString("$phase-ms",times.joinToString(","))
                            sendStatus(1,Bundle().apply{putString("$phase-try","${n+1}: $measured")})
                        } finally {
                            finished.set(true);demand.close();worker.join(2000)
                        }
                    }
                }
                stop();control("clear")
            }
            check(times.all{it in 0..10000}){"UDP impairment switch threshold failed"}
        }
        if (phase == "all" || phase == "switch" || phase == "blackhole") {
            repeat(if (phase == "switch") 1 else 10){ n ->
                stage.writeText("cold-" + (n+1));control("drop")
                var begin=clock()
                startV2()
                val c=runCatching { usable("h2",10000);clock()-begin }.getOrDefault(-1L)
                cold.add(c)
                if(n==0 && c>=0){ result.putString("4MiB-H2-Mbps",download());udp() }
                stop();control("clear")
                stage.writeText("hot-" + (n+1));startV2();usable("h3",12000)
                connect().use { initial ->
                    echo(initial);begin=clock();control("drop")
                    val h=runCatching { usable("h2",10000);clock()-begin }.getOrDefault(-1L)
                    hot.add(h)
                }
                stop();control("clear")
            }
            result.putString("cold-ms", cold.joinToString(","));result.putString("hot-ms", hot.joinToString(","))
        }
        if (phase == "all" || phase == "regression") {
            val appKey = "apps/" + node.key
            val allowed = prefs.getStringSet(appKey, null)?.toSet()
            try {
                check(prefs.edit().putStringSet(appKey,setOf(probePackage)).commit())
                startV2()
                waitFor(12000) { TunnelService.state.value.bearer.equals("h3",true) && TunnelService.state.value.vpn }
                check(cm.activeNetwork == physical && vpn() == null)
                external("probe-scoped")
                check(cm.activeNetwork == physical && vpn() == null)
                result.putString("app-scope","probe UID uses V2 TCP/UDP; app UID stays on its physical network")
            } finally {
                stop()
                prefs.edit().let { if (allowed == null) it.remove(appKey) else it.putStringSet(appKey,allowed) }.commit()
            }
        }
        if (phase == "all" || phase == "recovery") {
            stage.writeText("recovery");control("drop");startV2();usable("h2",12000)
            val resumed = runCatching {
                connect().use { held ->
                    echo(held)
                    control("clear")
                    stage.writeText("recovery-open")
                    val began = clock()
                    waitFor(135000) {
                        echo(held)
                        Thread.sleep(300)
                        TunnelService.state.value.bearer.equals("h3",true)
                    }
                    echo(held)
                    usable("h3",5000)
                    result.putLong("recovery-ms",clock()-began)
                }
            }.isSuccess
            result.putString("recovery", if (resumed) "H3 restored, existing H2 TCP echoed before/during/after" else "failed")
            if (phase == "recovery") check(resumed) { "H3 recovery" }
        }
        if (phase == "all" || phase == "regression") {
            if (phase == "regression") { startV2(); usable("h3",12000) }
            stage.writeText("revoke");control("revoke " + node.deviceId)
            waitFor(25000) {
                runCatching { connect().use(::echo) }
                val s=TunnelService.state.value
                !s.vpn && s.error.contains("撤销")
            }
            Thread.sleep(2500)
            check(!TunnelService.state.value.vpn && !prefs.contains(TunnelService.RESUME))
            result.putString("revoked","terminal Chinese error, no resume")
            stage.writeText("xray")
            val sub=store.import("Nexus-QA-M3",cfg.getString("subscription"))
            source=sub.id
            val row=NodeRow(sub,0,sub.nodes.getJSONObject(0))
            ctx.startForegroundService(subscriptionRequest(ctx,row,listOf(row),addr.address.hostAddress!!,false))
            waitFor(12000){runCatching {connect().use(::echo)}.isSuccess}
            udp();external("probe-xray")
            result.putString("subscription","TCP/UDP through Xray")
        }
        check(result.getString("recovery") != "failed") { "H3 recovery" }
        check(cold.all { it in 0..3000 } && hot.all { it in 0..5000 }) { "latency threshold failed" }
    } finally {
        result.putString("cold-ms",cold.joinToString(","));result.putString("hot-ms",hot.joinToString(","))
        runCatching {control("clear")};runCatching {stop()};source?.let(store::remove)
        V2Devices.remove(ctx,node.key)
        prefs.edit().let { if(original==null) it.remove(SplitRouting.KEY) else it.putBoolean(SplitRouting.KEY,original) }.commit()
        runOnMainSync { home.finish() }
    }
}
