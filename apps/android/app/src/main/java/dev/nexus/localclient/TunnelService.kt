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
import org.json.JSONObject

private data class Plan(val outbound: JSONObject, val name: String, val dns: String, val checkKey: String? = null, val node: String? = checkKey, val v2: V2Node? = null)

class Tunnel(private val protectSocket: (Int) -> Boolean, private val ready: (Int) -> Unit) {
    companion object { init { System.loadLibrary("nexus_android") } }
    fun onReady(port: Int) = ready(port)
    fun protectTransport(fd: Int) = protectSocket(fd)
    external fun split(cancelFd: Int, tunFd: Int, engineFd: Int, dnsSocket: String, rules: ByteArray): Int
}

data class TunnelState(val starting: Boolean = false, val error: String = "", val subscription: String = "",
                       val vpn: Boolean = false, val waiting: Boolean = false,
                       val connectedAt: Long = 0, val node: String? = null, val physical: Network? = null, val shoes: Boolean = false, val bearer: String = "") {
    val active get() = starting || vpn
}

class TunnelService : VpnService() {
    companion object { val state = MutableStateFlow(TunnelState())
        /** The last user-started VPN request, replayed when Android restarts a killed service. */
        internal const val RESUME = "resume-request"
        /** Last request the user started; unlike [RESUME] it survives a manual disconnect. */
        internal const val LAST = "last-request"
        @Volatile internal var interfaceName: String? = null
    }
    private val main = Handler(Looper.getMainLooper())
    private var cancel: ParcelFileDescriptor? = null
    private var serial = 0
    private var tun: ParcelFileDescriptor? = null
    private var network: Network? = null
    private var selected: Plan? = null
    private val alternates = ArrayDeque<JSONObject>()
    private var failures = 0
    private var callback: ConnectivityManager.NetworkCallback? = null
    private val connectivity by lazy { getSystemService(ConnectivityManager::class.java) }
    private val channelId = "local_forward"
    private fun notification(text: String): Notification {
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val stop = PendingIntent.getService(this, 1, Intent(this, TunnelService::class.java).setAction("stop"), flags)
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), flags)
        return Notification.Builder(this, channelId).setSmallIcon(R.drawable.nexus_notification)
            .setContentTitle(getString(R.string.app_name)).setContentText(text).setContentIntent(open)
            .addAction(Notification.Action.Builder(null, "断开", stop).build()).setOngoing(true).build()
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val prefs = getSharedPreferences("local", 0)
        val boot = intent?.action == "resume-boot"
        if (boot && (!prefs.getBoolean("boot-connect",false) || VpnService.prepare(this)!=null || tun!=null)) {
            if(tun==null) stopSelf(startId)
            return if(tun==null) START_NOT_STICKY else START_STICKY
        }
        if (intent?.action == "stop") prefs.edit().remove(RESUME).apply()
        // After Android kills the process it restarts a sticky service with a null intent (or the
        // always-on interface action). Resume the connection the user last started, not a default.
        val resumed = if (boot || intent == null || intent.action == SERVICE_INTERFACE) prefs.getString(RESUME, null)?.let {
            runCatching { Intent.parseUri(it, 0).setClass(this, TunnelService::class.java) }.getOrNull()
        } else null
        if(boot && resumed?.action !in setOf("subscription","v2")) {stopSelf(startId);return START_NOT_STICKY}
        val request = resumed ?: intent
        handle(request)
        val running = tun != null
        if (running && request?.action in setOf("subscription", "v2")) request!!.toUri(0).let {
            prefs.edit().putString(RESUME, it).putString(LAST, it).apply()
            ConnectTile.refresh(this)
        }
        return if (running) START_STICKY else START_NOT_STICKY
    }

    private fun handle(intent: Intent?): Int {
        if (intent?.action in setOf("subscription", "v2") && tun == null && cancel == null) SubscriptionRefresh.stopTests(this)
        if (intent?.action == "split-mode") {
            val plan = selected
            if (plan != null && tun != null && network != null) {
                cancelWorker()
                failures = 0
                startWorker(plan)
            }
            return START_NOT_STICKY
        }
        val compat = intent?.action == "subscription"
        val v2 = intent?.action == "v2"
        if (!compat && !v2) { disconnect(); return START_NOT_STICKY }
        if (cancel != null || tun != null) return START_NOT_STICKY
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(channelId, "VPN 连接", NotificationManager.IMPORTANCE_LOW))
        startForeground(1, notification("正在验证服务器和设备"))
        val plan = runCatching {
            if (v2) {
                val key = requireNotNull(intent!!.getStringExtra("v2-key"))
                val node = requireNotNull(V2Devices.get(this, key))
                Plan(JSONObject().put("protocol", "blackhole").put("tag", "selected"), node.authority,
                    intent.getStringExtra("dns") ?: "1.1.1.1", node = key, v2 = node)
            } else {
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
                Plan(CompatEngine.outbound(node), source.label + " · " + node.getString("name"), dns,
                    if (refs==null) null else nodeKey(source.id,node),nodeKey(source.id,node))
            }
        }.getOrElse { failure ->
            state.value = TunnelState(error = (failure as? UnsupportedNode)?.let { "无法连接此节点：" + it.message }
                ?: "节点配置无法使用，请检查节点参数")
            disconnect()
            return START_NOT_STICKY
        }
        try {
            check(prepare(this) == null)
            tun = establishFor(plan)
            // Only always-on VPN is brought back by Android after the process is killed; remember
            // whether it is set so the settings page can offer it.
            getSharedPreferences("local",0).edit()
                .putBoolean("always-on", android.os.Build.VERSION.SDK_INT >= 29 && isAlwaysOn()).apply()
            selected = plan
            state.value = TunnelState(vpn = true, waiting = true, node = plan.node)
            val observer = object : ConnectivityManager.NetworkCallback() {
                private val candidates = linkedMapOf<Network, Int>()
                private fun selectNetwork() {
                    val next = candidates.maxByOrNull { it.value }?.key
                    if (callback !== this || next == network) return
                    cancelWorker()
                    failures = 0
                    network = next
                    setUnderlyingNetworks(listOfNotNull(next).toTypedArray())
                    if (next != null) startWorker(requireNotNull(selected)) else {
                        state.value = TunnelState(vpn = true, waiting = true, node = selected?.node)
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
        cancel?.close()
        cancel = null
    }

    private fun establishFor(plan: Plan): ParcelFileDescriptor {
        val builder = Builder().setSession(plan.name).setMtu(1500).addAddress("192.0.2.2", 32).setBlocking(false)
        builder.addRoute("0.0.0.0", 0).addAddress("fd42:6e78::2", 128).addRoute("::", 0).addDnsServer(plan.dns)
        plan.v2?.key?.let { key ->
            val apps = getSharedPreferences("local",0).getStringSet("apps/$key",emptySet()).orEmpty()
            for (name in apps) builder.addAllowedApplication(name)
        }
        return checkNotNull(builder.establish()).also { fd ->
            interfaceName = runCatching { java.io.File("/proc/self/fdinfo/"+fd.fd).readLines().firstOrNull { it.startsWith("iff:") }?.substringAfter(':')?.trim() }.getOrNull()
        }
    }

    private fun nextPlan(plan: Plan): Plan {
        if (plan.checkKey!=null) {
            val store=Subscriptions(this)
            repeat(alternates.size) {
                val ref=alternates.removeFirst()
                val next=runCatching {
                    val source=store.get(ref.getString("source"))
                    require(source.updated==ref.getLong("updated") && source.hasAllowance() && store.appearance.getBoolean("auto/"+source.id,true))
                    val node=source.nodes.getJSONObject(ref.getInt("index"))
                    Plan(CompatEngine.outbound(node),source.label+" · "+node.getString("name"),plan.dns,nodeKey(source.id,node))
                }.getOrNull()
                if (next!=null) {
                    alternates.addLast(ref)
                    if (next.node!=plan.node) { selected=next;return next }
                }
            }
        }
        return plan
    }

    private fun startWorker(plan: Plan, attempt: Int = 0) {
        val bypass = SplitRouting.enabled(this)
        val physical = network
        val token = ++serial
        val pipe = ParcelFileDescriptor.createSocketPair()
        val workerTun = ParcelFileDescriptor.dup(requireNotNull(tun).fileDescriptor)
        cancel = pipe[1]
        state.value = TunnelState(starting = true, vpn = true, node = plan.node, shoes = plan.outbound.has("shoesClient"))
        Thread {
            val result = runCatching {
                val ready = { main.post {
                    if (token == serial) {
                        failures = 0
                        state.value = TunnelState(vpn = true, subscription = plan.name,
                            bearer = state.value.bearer, shoes = plan.outbound.has("shoesClient"), node = plan.node, physical = physical, connectedAt = android.os.SystemClock.elapsedRealtime())
                        getSystemService(NotificationManager::class.java).notify(1, notification("VPN · " + plan.name))
                    }
                }; Unit }
                val rules = if (bypass && !plan.outbound.has("shoesClient"))
                    SplitRouting.data(this, plan.dns, physical?.let { connectivity.getLinkProperties(it) }) else byteArrayOf()
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
                val session = plan.v2?.let { node ->
                    V2Session(node, this, net) { bearer ->
                        main.post {
                            if (serial == token) {
                                DiagnosticExport.bearerChanged(this, state.value.bearer, bearer)
                                state.value = state.value.copy(bearer = bearer)
                            }
                        }
                    }
                }
                try { CompatEngine.run(this,net,if (':' in dns) "[$dns]:53" else "$dns:53",plan.outbound,workerTun,pipe[0],health,rules,session) {
                    check(health?.invoke() != false)
                    ready()
                } } finally { session?.close() }
            }.getOrElse { (it as? V2Failure)?.code ?: if (plan.v2 != null) 6 else 1 }
            workerTun.close()
            pipe[0].close()
            main.post {
                if (token == serial) {
                    cancel?.close()
                    cancel = null
                    failures = (failures + 1).coerceAtMost(32)
                    if (plan.v2 != null) DiagnosticExport.recordError(this, result)
                    if (plan.v2 != null && result != 0 && !V2Connection.retry(result)) {
                        getSharedPreferences("local", 0).edit().remove(RESUME).apply()
                        disconnect()
                        state.value = TunnelState(node = plan.node, error = V2Connection.error(result))
                    } else if (result != 0 && network != null && (failures <= 8 || plan.checkKey != null || plan.v2 != null)) {
                        state.value = TunnelState(vpn = true, node = plan.node, error = if (plan.v2 != null) V2Connection.error(result) else "节点不可用，正在等待重试")
                        main.postDelayed({
                            if (token == serial) {
                                val next = if (attempt >= 1 || plan.checkKey!=null) runCatching { nextPlan(plan) }.getOrDefault(plan) else plan
                                startWorker(next, if (next === plan) minOf(attempt + 1,5) else 0)
                            }
                        }, 1000L shl maxOf(attempt, if (plan.checkKey==null) 0 else (failures / 4).coerceAtMost(5)))
                    } else {
                        state.value = TunnelState(vpn = true, error = "节点不可用，请检查配置后重新连接")
                        getSystemService(NotificationManager::class.java).notify(1, notification("节点不可用"))
                    }
                }
            }
        }.start()
    }
    override fun onRevoke() { main.post { getSharedPreferences("local",0).edit().remove(RESUME).apply(); disconnect() } }
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
