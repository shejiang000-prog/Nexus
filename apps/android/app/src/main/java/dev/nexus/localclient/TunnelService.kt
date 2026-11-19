package dev.nexus.localclient

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Handler
import android.os.Looper
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.*
import org.json.JSONObject

private data class Plan(val profile: Profile?, val outbound: JSONObject?, val name: String, val dns: String?, val checkKey: String? = null, val node: String? = checkKey)

class Tunnel(private val signingAlias: String, private val protectSocket: (Int) -> Boolean, private val ready: (Int) -> Unit) {
    var path: (Long,Long,Long) -> Unit = { _,_,_ -> }
    fun onPath(rtt: Long,lost: Long,sent: Long) = path(rtt,lost,sent)
    companion object { init { System.loadLibrary("nexus_android") } }
    fun onReady(port: Int) = ready(port)
    fun protectTransport(fd: Int) = protectSocket(fd)
    fun sign(message: ByteArray): ByteArray = Identity.sign(message, signingAlias)
    external fun run(cancelFd: Int, tunFd: Int, netHandle: Long, route: String, dns: String, server: String, name: String, target: String, ca: ByteArray, device: ByteArray): Int
}

data class TunnelState(val starting: Boolean = false, val port: Int = 0, val error: String = "", val subscription: String = "",
                       val vpn: Boolean = false, val waiting: Boolean = false, val activeProfile: Profile? = null,
                       val connectedAt: Long = 0, val node: String? = null, val physical: Network? = null, val path: String = "") {
    val active get() = starting || port != 0 || vpn
}

class TunnelService : VpnService() {
    companion object { val state = MutableStateFlow(TunnelState()) }
    private val main = Handler(Looper.getMainLooper())
    private var cancel: ParcelFileDescriptor? = null
    private var serial = 0
    private var tun: ParcelFileDescriptor? = null
    private var network: Network? = null
    private var selected: Plan? = null
    private val alternates = ArrayDeque<JSONObject>()
    private var failures = 0
    private var exitMonitor: Job? = null
    private var exitHeld = false
    private var callback: ConnectivityManager.NetworkCallback? = null
    private val connectivity by lazy { getSystemService(ConnectivityManager::class.java) }
    private val channelId = "local_forward"
    private fun notification(text: String): Notification {
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val stop = PendingIntent.getService(this, 1, Intent(this, TunnelService::class.java).setAction("stop"), flags)
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), flags)
        return Notification.Builder(this, channelId).setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle(getString(R.string.app_name)).setContentText(text).setContentIntent(open)
            .addAction(Notification.Action.Builder(null, "断开", stop).build()).setOngoing(true).build()
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val systemStart = intent?.action == SERVICE_INTERFACE || intent?.action == null
        val compat = intent?.action == "subscription"
        val vpn = intent?.action == "vpn" || compat || systemStart
        if (!vpn && intent?.action != "connect") { disconnect(); return START_NOT_STICKY }
        if (cancel != null || tun != null) return START_NOT_STICKY
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(channelId, "本地转发", NotificationManager.IMPORTANCE_LOW))
        startForeground(1, notification("正在验证服务器和设备"))
        val plan = runCatching {
            if (compat) {
                check(!getSharedPreferences("local",0).contains("pinned"))
                val index = intent!!.getIntExtra("index",-1)
                val refs = intent.getStringExtra("alternates")?.let { org.json.JSONArray(it) }
                require((refs?.length() ?: 0) <= 7)
                synchronized(SubscriptionRefresh) {
                    SubscriptionRefresh.protectedSources = buildSet {
                        add(requireNotNull(intent.getStringExtra("source")))
                        if (refs != null) for (i in 0 until refs.length()) add(refs.getJSONObject(i).getString("source"))
                    }
                }
                val source = Subscriptions(this).get(requireNotNull(intent.getStringExtra("source")))
                check(source.updated == intent.getLongExtra("updated",-1))
                val node = source.nodes.getJSONObject(index)
                val dns = requireNotNull(intent.getStringExtra("dns"))
                require(dns.split('.').let { parts -> parts.size == 4 && parts.all { p -> p.toIntOrNull() in 0..255 && p == p.toIntOrNull().toString() } })
                if (refs!=null) {
                    for (i in 0 until refs.length()) alternates.addLast(refs.getJSONObject(i))
                    alternates.addLast(JSONObject().put("source",source.id).put("updated",source.updated).put("index",index))
                }
                Plan(null, CompatEngine.outbound(node), source.label + " · " + node.getString("name"), dns,
                    if (refs==null) null else nodeKey(source.id,node),nodeKey(source.id,node))
            } else {
                val p = requireNotNull(Profile.load(this))
                if (systemStart) require(p.dns != null)
                Plan(p,null,p.name,p.dns)
            }
        }.getOrElse { failure ->
            state.value = TunnelState(error = (failure as? UnsupportedNode)?.let { "无法连接此节点：" + it.message }
                ?: "节点配置无法使用，请检查参数及固定出口设置")
            disconnect()
            return START_NOT_STICKY
        }
        if (!vpn) {
            startWorker(plan)
            return START_NOT_STICKY
        }
        try {
            check(prepare(this) == null)
            tun = establishFor(plan)
            selected = plan
            state.value = TunnelState(vpn = true, waiting = true)
            val observer = object : ConnectivityManager.NetworkCallback() {
                private val candidates = linkedMapOf<Network, Int>()
                private fun selectNetwork() {
                    val next = candidates.maxByOrNull { it.value }?.key
                    if (callback !== this || next == network || exitHeld) return
                    cancelWorker()
                    failures = 0
                    network = next
                    setUnderlyingNetworks(listOfNotNull(next).toTypedArray())
                    if (next != null) startWorker(requireNotNull(selected)) else {
                        state.value = TunnelState(vpn = true, waiting = true)
                        manager.notify(1, notification("等待网络恢复"))
                    }
                }
                override fun onCapabilitiesChanged(available: Network, capabilities: NetworkCapabilities) {
                    // Prefer a validated, unmetered physical path; a private LAN may be unvalidated.
                    val verified = capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
                    val unmetered = capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
                    candidates[available] = (if (verified) 2 else 0) + (if (unmetered) 1 else 0)
                    selectNetwork()
                }
                override fun onLost(lost: Network) {
                    candidates.remove(lost)
                    selectNetwork()
                }
            }
            callback = observer
            val physical = NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build()
            connectivity.registerNetworkCallback(physical, observer, main)
        } catch (_: Exception) {
            state.value = TunnelState(error = "无法建立 VPN，请检查系统授权后重试")
            disconnect()
        }
        return START_NOT_STICKY
    }

    private fun cancelWorker() {
        serial++
        exitMonitor?.cancel()
        exitMonitor = null
        cancel?.close()
        cancel = null
    }

    private fun monitorExit(plan: Plan, token: Int) {
        val profile = plan.profile ?: return
        val key = profile.server+"/"+profile.fingerprint
        val prefs = getSharedPreferences("local",0)
        if (profile.dns==null || !prefs.getBoolean("monitor/"+key,false)) return
        exitMonitor?.cancel()
        exitMonitor = CoroutineScope(Dispatchers.IO).launch {
            while (isActive) {
                val active = connectivity.activeNetwork
                if (active != null && connectivity.getNetworkCapabilities(active)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true) {
                    val ip = runCatching {
                        val c = active.openConnection(java.net.URL("https://ipinfo.io/json")) as javax.net.ssl.HttpsURLConnection
                        try {
                            c.instanceFollowRedirects=false; c.connectTimeout=6000; c.readTimeout=6000; c.useCaches=false
                            check(c.responseCode==200)
                            JSONObject(c.inputStream.bounded(32768).toString(Charsets.UTF_8)).getString("ip")
                                .also { require(it.matches(Regex("[0-9a-fA-F:.]{2,45}"))) }
                        } finally { c.disconnect() }
                    }.getOrNull()
                    if (ip != null) main.post {
                        if (serial==token && prefs.getBoolean("monitor/"+key,false)) {
                            val prior = prefs.getString("exit/"+key,null)
                            prefs.edit().putString("exit/"+key,ip).apply()
                            if (prior!=null && prior!=ip) {
                                val hold = prefs.getBoolean("hold/"+key,false)
                                if (hold) {
                                    exitHeld=true
                                    cancelWorker()
                                    state.value=TunnelState(vpn=true,error="出口 IP 已变化，业务流量已暂停。请核对后断开并重新连接。")
                                }
                                getSystemService(NotificationManager::class.java).notify(1,notification(if (hold) "出口已变化，流量已暂停" else "私人节点出口 IP 已变化"))
                            }
                        }
                    }
                }
                delay(300_000)
            }
        }
    }

    private fun establishFor(plan: Plan): ParcelFileDescriptor {
        val builder = Builder().setSession(plan.name).setMtu(1500).addAddress("192.0.2.2", 32).setBlocking(false)
        if (plan.dns != null) {
            builder.addRoute("0.0.0.0", 0).addAddress("fd42:6e78::2", 128).addRoute("::", 0).addDnsServer(plan.dns)
            plan.profile?.let { profile ->
                val apps = getSharedPreferences("local",0).getStringSet("apps/"+profile.server+"/"+profile.fingerprint,emptySet()).orEmpty()
                for (name in apps) builder.addAllowedApplication(name)
            }
        } else builder.addRoute(requireNotNull(plan.profile).vpnHost, 32).allowFamily(android.system.OsConstants.AF_INET6)
        return checkNotNull(builder.establish())
    }

    private fun nextPlan(plan: Plan): Plan {
        val prefs = getSharedPreferences("local",0)
        if (plan.checkKey!=null && !prefs.contains("pinned")) {
            val store=Subscriptions(this)
            repeat(alternates.size) {
                val ref=alternates.removeFirst()
                val next=runCatching {
                    val source=store.get(ref.getString("source"))
                    require(source.updated==ref.getLong("updated") && source.hasAllowance() && store.appearance.getBoolean("auto/"+source.id,false))
                    val node=source.nodes.getJSONObject(ref.getInt("index"))
                    Plan(null,CompatEngine.outbound(node),source.label+" · "+node.getString("name"),plan.dns,nodeKey(source.id,node))
                }.getOrNull()
                if (next!=null) {
                    alternates.addLast(ref)
                    if (next.node!=plan.node) { selected=next;return next }
                }
            }
        }
        if (plan.profile == null || plan.dns == null || prefs.contains("pinned") || !prefs.getBoolean("failover",false)) return plan
        if (!prefs.getStringSet("apps/"+plan.profile.server+"/"+plan.profile.fingerprint,emptySet()).isNullOrEmpty()) return plan
        val nodes = Profile.servers(this).filter { it.dns != null && prefs.getStringSet("apps/"+it.server+"/"+it.fingerprint,emptySet()).isNullOrEmpty() }
        val index = nodes.indexOfFirst { it.server == plan.profile.server && it.fingerprint == plan.profile.fingerprint }
        if (nodes.size < 2 || index < 0) return plan
        val next = nodes[(index + 1) % nodes.size]
        val candidate = Plan(next,null,next.name,next.dns)
        val replacement = establishFor(candidate)
        val previous = tun
        tun = replacement
        previous?.close()
        selected = candidate
        return candidate
    }

    private fun startWorker(plan: Plan, attempt: Int = 0) {
        val vpn = tun != null
        val netHandle = network?.networkHandle ?: 0L
        val physical = network
        val token = ++serial
        val pipe = ParcelFileDescriptor.createSocketPair()
        val workerTun = tun?.let { ParcelFileDescriptor.dup(it.fileDescriptor) }
        cancel = pipe[1]
        state.value = TunnelState(starting = true, vpn = vpn)
        Thread {
            val result = runCatching {
                val ready: (Int) -> Unit = { port -> main.post {
                    if (token == serial) {
                        failures = 0
                        state.value = TunnelState(port = port, vpn = vpn, subscription = if (plan.outbound != null) plan.name else "",
                            activeProfile = plan.profile, node = plan.node, physical = physical, connectedAt = android.os.SystemClock.elapsedRealtime())
                        val label = if (vpn) "VPN" else "本机端口 $port"
                        getSystemService(NotificationManager::class.java).notify(1, notification(label + " · " + plan.name))
                        if (vpn) monitorExit(plan,token)
                    }
                }; Unit }
                if (plan.outbound != null) {
                    val net = requireNotNull(physical)
                    val dns = requireNotNull(connectivity.getLinkProperties(net)?.dnsServers?.firstOrNull()?.hostAddress)
                    val health = plan.checkKey?.let { key -> {
                            val vpnNet = connectivity.activeNetwork
                            val start=android.os.SystemClock.elapsedRealtime()
                            val passed=runCatching {
                                check(vpnNet != null && connectivity.getNetworkCapabilities(vpnNet)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true)
                                val conn=vpnNet.openConnection(java.net.URL("https://example.com/")) as javax.net.ssl.HttpsURLConnection
                                try {
                                    conn.setRequestProperty("Connection","close")
                                    conn.connectTimeout=6000;conn.readTimeout=6000;conn.useCaches=false;conn.instanceFollowRedirects=false
                                    check(conn.responseCode==200 && conn.inputStream.bounded(32768).toString(Charsets.UTF_8).contains("Example Domain"))
                                } finally { conn.disconnect() }
                            }.isSuccess
                            NodeQuality(this,net).record(key,passed,android.os.SystemClock.elapsedRealtime()-start)
                            passed
                        }
                    }
                    CompatEngine.run(this,net,if (':' in dns) "[$dns]:53" else "$dns:53",plan.outbound,requireNotNull(workerTun),pipe[0],health) {
                        check(health?.invoke() != false)
                        ready(0)
                    }
                } else {
                    val profile = requireNotNull(plan.profile)
                    val native = Tunnel(profile.signingAlias, { fd -> protect(fd) }, ready)
                    native.path = { rtt,lost,sent -> main.post {
                        if (serial==token && state.value.connectedAt!=0L) state.value=state.value.copy(
                            path=if (rtt>=0 && sent>0) "QUIC RTT $rtt ms · 丢失 $lost / $sent 包" else "")
                    }; Unit }
                    native.run(pipe[0].detachFd(), workerTun?.detachFd() ?: -1, netHandle,
                        if (profile.dns == null) profile.vpnHost else "", profile.dns.orEmpty(),
                        profile.server, profile.tlsName, profile.target, profile.ca, profile.device)
                }
            }.getOrDefault(1)
            workerTun?.close()
            pipe[0].close()
            main.post {
                if (token == serial) {
                    cancel?.close()
                    cancel = null
                    failures = (failures + 1).coerceAtMost(32)
                    if (vpn && result != 0 && network != null && (failures <= 8 || plan.checkKey != null)) {
                        state.value = TunnelState(vpn = true, error = "节点不可用，正在等待重试")
                        main.postDelayed({
                            if (token == serial) {
                                val next = if (attempt >= 1 || plan.checkKey!=null) runCatching { nextPlan(plan) }.getOrDefault(plan) else plan
                                startWorker(next, if (next === plan) minOf(attempt + 1,5) else 0)
                            }
                        }, 1000L shl maxOf(attempt, if (plan.checkKey==null) 0 else (failures / 4).coerceAtMost(5)))
                    } else if (vpn) {
                        state.value = TunnelState(vpn = true, error = "私人节点不可用，请检查设备授权及目标许可后重新连接")
                        getSystemService(NotificationManager::class.java).notify(1, notification("私人节点不可用"))
                    } else {
                        state.value = TunnelState(error = if (result == 0) "" else "连接失败，请检查服务器地址、设备授权及目标许可")
                        disconnect()
                    }
                }
            }
        }.start()
    }
    override fun onRevoke() { main.post { disconnect() } }
    private fun disconnect() {
        // Android keeps a VPN service bound while its TUN is open.
        releaseSession()
        stopSelf()
    }
    private fun releaseSession() {
        callback?.let { runCatching { connectivity.unregisterNetworkCallback(it) } }
        callback = null
        selected = null
        alternates.clear()
        exitHeld = false
        network = null
        cancelWorker()
        tun?.close()
        tun = null
        synchronized(SubscriptionRefresh) {
            if (SubscriptionRefresh.protectedSources.isNotEmpty()) {
                SubscriptionRefresh.protectedSources = emptySet()
                SubscriptionRefresh.enqueue(this)
            }
        }
        if (state.value.active) state.value = TunnelState()
        stopForeground(STOP_FOREGROUND_REMOVE)
    }
    override fun onDestroy() {
        releaseSession()
        super.onDestroy()
    }
}
