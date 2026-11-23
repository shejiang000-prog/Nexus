package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
import org.json.JSONObject
import java.io.File
import java.net.InetSocketAddress

// Run with the emulator's wall clock ahead of the fixture's initial eight-hour account.
internal fun Instrumentation.m32ExpiryCheck(result: Bundle) {
    val ctx=targetContext
    check(ctx.packageName == "dev.nexus.lab.m0" && VpnService.prepare(ctx) == null)
    val cfg=JSONObject(File(ctx.noBackupFilesDir,"m3.json").readText())
    val network=ctx.getSystemService(ConnectivityManager::class.java)
    val physical=requireNotNull(network.activeNetwork)
    val node=V2Devices.enroll(ctx,V2Invite.parse(cfg.getString("invitation")),cfg.getString("endpoint"),cfg.getString("ca").toByteArray())
    val expires=requireNotNull(node.expiresAt)
    val choice=ctx.getSharedPreferences("local",0)
    val prior=choice.getString("simple-node",null)
    val oldSplit=choice.all[SplitRouting.KEY] as? Boolean
    choice.edit().putString("simple-node",node.key).putBoolean(SplitRouting.KEY,false).commit()
    val activity=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    fun echo(): Boolean = runCatching {
        val vpn=requireNotNull(network.activeNetwork)
        check(network.getNetworkCapabilities(vpn)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true)
        vpn.socketFactory.createSocket().use { socket ->
            socket.soTimeout=800
            socket.connect(InetSocketAddress(cfg.getString("target"),cfg.getInt("echoPort")),800)
            socket.getOutputStream().write(65)
            check(socket.getInputStream().read()==65)
        }
    }.isSuccess
    fun until(condition: () -> Boolean) {
        val deadline=SystemClock.elapsedRealtime()+12000
        do { if(condition()) return; Thread.sleep(100) } while(SystemClock.elapsedRealtime()<deadline)
        val state=TunnelService.state.value
        result.putString("timeout","vpn=${state.vpn}, starting=${state.starting}, bearer=${state.bearer}, error=${state.error}")
        error("account check timed out")
    }
    fun start() { ctx.startForegroundService(v2Request(ctx,node.key)); until(::echo) }
    fun stop() { ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop")); until { !TunnelService.state.value.active } }
    try {
        check(System.currentTimeMillis()/1000 > expires) { "advance the emulator clock past cached expiry" }
        splitLabel(node.expiryText(),false)
        start()
        result.putString("clock","node accepted an account whose cached expiry was in the device's past")
        physical.socketFactory.createSocket().use { s ->
            s.soTimeout=3000
            s.connect(InetSocketAddress(cfg.getString("host"),cfg.getInt("controlPort")),1500)
            s.getOutputStream().write((cfg.getString("control")+" extend "+node.deviceId+"\n").toByteArray())
            check(s.getInputStream().read()==1)
        }
        until {
            echo() && V2Devices.get(ctx,node.key)?.expiresAt == expires+86400
        }
        val updated=requireNotNull(V2Devices.get(ctx,node.key))
        check(updated.deviceId==node.deviceId)
        splitLabel(updated.expiryText(),false)
        stop()
        start()
        result.putString("renewal","next authenticated flow updated the visible expiry; same device reconnected")
    } finally {
        stop()
        V2Devices.remove(ctx,node.key)
        choice.edit().putString("simple-node",prior).apply {
            if (oldSplit==null) remove(SplitRouting.KEY) else putBoolean(SplitRouting.KEY,oldSplit)
        }.commit()
        runOnMainSync { activity.finish() }
    }
}
