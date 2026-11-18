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

class Tunnel(private val ready: (Int) -> Unit) {
    companion object { init { System.loadLibrary("nexus_android") } }
    fun onReady(port: Int) = ready(port)
    fun sign(message: ByteArray): ByteArray = Identity.sign(message)
    external fun run(cancelFd: Int, tunFd: Int, netHandle: Long, route: String, server: String, name: String, target: String, ca: ByteArray, device: ByteArray): Int
}

data class TunnelState(val starting: Boolean = false, val port: Int = 0, val error: String = "",
                       val vpn: Boolean = false, val waiting: Boolean = false) {
    val active get() = starting || port != 0 || vpn
}

class TunnelService : VpnService() {
    companion object { val state = MutableStateFlow(TunnelState()) }
    private val main = Handler(Looper.getMainLooper())
    private var cancel: ParcelFileDescriptor? = null
    private var serial = 0
    private var tun: ParcelFileDescriptor? = null
    private var network: Network? = null
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
        val vpn = intent?.action == "vpn"
        if (!vpn && intent?.action != "connect") { disconnect(); return START_NOT_STICKY }
        if (cancel != null || tun != null) return START_NOT_STICKY
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(channelId, "本地转发", NotificationManager.IMPORTANCE_LOW))
        startForeground(1, notification("正在验证服务器和设备"))
        val profile = runCatching { Profile.load(this) }.getOrNull()
        if (profile == null) { state.value = TunnelState(error = "请重新导入有效的设备配置"); disconnect(); return START_NOT_STICKY }
        if (!vpn) {
            startWorker(profile)
            return START_NOT_STICKY
        }
        try {
            check(prepare(this) == null)
            tun = checkNotNull(Builder().setSession(profile.name).setMtu(1500)
                .addAddress("192.0.2.2", 32).addRoute(profile.vpnHost, 32)
                .allowFamily(android.system.OsConstants.AF_INET6)
                .addDisallowedApplication(packageName).setBlocking(false).establish())
            state.value = TunnelState(vpn = true, waiting = true)
            val observer = object : ConnectivityManager.NetworkCallback() {
                private val candidates = linkedMapOf<Network, Int>()
                private fun selectNetwork() {
                    val next = candidates.maxByOrNull { it.value }?.key
                    if (callback !== this || next == network) return
                    cancelWorker()
                    network = next
                    setUnderlyingNetworks(listOfNotNull(next).toTypedArray())
                    if (next != null) startWorker(profile) else {
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
        cancel?.close()
        cancel = null
    }

    private fun startWorker(profile: Profile, attempt: Int = 0) {
        val vpn = tun != null
        val netHandle = network?.networkHandle ?: 0L
        val token = ++serial
        val pipe = ParcelFileDescriptor.createSocketPair()
        val workerTun = tun?.let { ParcelFileDescriptor.dup(it.fileDescriptor) }
        cancel = pipe[1]
        state.value = TunnelState(starting = true, vpn = vpn)
        Thread {
            val result = runCatching {
                val tunnel = Tunnel { port -> main.post {
                    if (token == serial) {
                        state.value = TunnelState(port = port, vpn = vpn)
                        val label = if (vpn) "私人节点 VPN" else "本机端口 $port"
                        getSystemService(NotificationManager::class.java).notify(1, notification(label + " · " + profile.name))
                    }
                }; Unit }
                tunnel.run(pipe[0].detachFd(), workerTun?.detachFd() ?: -1, netHandle, profile.vpnHost,
                    profile.server, profile.tlsName, profile.target, profile.ca, profile.device)
            }.getOrDefault(1)
            workerTun?.close()
            pipe[0].close()
            main.post {
                if (token == serial) {
                    cancel?.close()
                    cancel = null
                    if (vpn && result != 0 && attempt < 2 && network != null) {
                        main.postDelayed({ if (token == serial) startWorker(profile, attempt + 1) }, 1000L shl attempt)
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
        network = null
        cancelWorker()
        tun?.close()
        tun = null
        if (state.value.active) state.value = TunnelState()
        stopForeground(STOP_FOREGROUND_REMOVE)
    }
    override fun onDestroy() {
        releaseSession()
        super.onDestroy()
    }
}
