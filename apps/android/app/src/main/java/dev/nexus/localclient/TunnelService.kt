package dev.nexus.localclient

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.flow.MutableStateFlow

class Tunnel(private val ready: (Int) -> Unit) {
    companion object { init { System.loadLibrary("nexus_android") } }
    fun onReady(port: Int) = ready(port)
    fun sign(message: ByteArray): ByteArray = Identity.sign(message)
    external fun run(cancelFd: Int, server: String, name: String, target: String, ca: ByteArray, device: ByteArray): Int
}

data class TunnelState(val starting: Boolean = false, val port: Int = 0, val error: String = "") {
    val active get() = starting || port != 0
}

class TunnelService : Service() {
    companion object { val state = MutableStateFlow(TunnelState()) }
    private val main = Handler(Looper.getMainLooper())
    private var cancel: ParcelFileDescriptor? = null
    private var serial = 0
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
        if (intent?.action != "connect") { stopSelf(); return START_NOT_STICKY }
        if (cancel != null) return START_NOT_STICKY
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(channelId, "本地转发", NotificationManager.IMPORTANCE_LOW))
        startForeground(1, notification("正在验证服务器和设备"))
        val profile = runCatching { Profile.load(this) }.getOrNull()
        if (profile == null) { state.value = TunnelState(error = "请重新导入有效的设备配置"); stopSelf(); return START_NOT_STICKY }
        val token = ++serial
        val pipe = ParcelFileDescriptor.createSocketPair()
        cancel = pipe[1]
        state.value = TunnelState(starting = true)
        Thread {
            val result = runCatching {
                val tunnel = Tunnel { port -> main.post {
                    if (token == serial) {
                        state.value = TunnelState(port = port)
                        manager.notify(1, notification("本机端口 $port · " + profile.name))
                    }
                }; Unit }
                tunnel.run(pipe[0].detachFd(), profile.server, profile.tlsName, profile.target, profile.ca, profile.device)
            }.getOrDefault(1)
            pipe[0].close()
            main.post {
                if (token == serial) {
                    state.value = TunnelState(error = if (result == 0) "" else "连接失败，请检查服务器地址、设备授权及目标许可")
                    stopSelf()
                }
            }
        }.start()
        return START_NOT_STICKY
    }
    override fun onDestroy() {
        serial++
        cancel?.close()
        cancel = null
        if (state.value.active) state.value = TunnelState()
        stopForeground(STOP_FOREGROUND_REMOVE)
        super.onDestroy()
    }
    override fun onBind(intent: Intent?): IBinder? = null
}
