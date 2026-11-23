package dev.nexus.localclient

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.os.Bundle
import android.util.Base64
import android.view.accessibility.AccessibilityNodeInfo
import java.io.File
import java.security.KeyStore
import org.json.JSONObject

internal fun Instrumentation.m3UiCheck(result: Bundle) {
    val ctx = targetContext
    check(ctx.packageName == "dev.nexus.lab.m0")
    val cfg = JSONObject(File(ctx.noBackupFilesDir, "m3-ui.json").readText())
    val prefs = ctx.getSharedPreferences("local", 0)
    val oldChoice = prefs.getString("simple-node", null)
    val invite = V2Invite.parse(cfg.getString("invitation"))
    val node = V2Devices.enroll(ctx, invite, cfg.getString("endpoint"), cfg.getString("ca").toByteArray())
    prefs.edit().putString("simple-node", node.key).commit()
    val activity = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    fun shot(name: String) {
        uiAutomation.waitForIdle(250, 2000)
        val image = requireNotNull(uiAutomation.takeScreenshot())
        try { File(ctx.cacheDir, "m3-ui-$name.png").outputStream().use { check(image.compress(Bitmap.CompressFormat.PNG,100,it)) } }
        finally { image.recycle() }
    }
    fun edit(value: String) {
        fun editable(item: AccessibilityNodeInfo): AccessibilityNodeInfo? {
            if (item.isEditable && item.isVisibleToUser) return item
            for (i in 0 until item.childCount) item.getChild(i)?.let { editable(it) }?.let { return it }
            return null
        }
        val field = requireNotNull(editable(requireNotNull(uiAutomation.rootInActiveWindow)))
        val arguments = Bundle().apply { putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,value) }
        check(field.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT,arguments))
    }
    // The only invitation sent through Android's text/scan UI is an inert example.
    val sample = JSONObject().put("v",2).put("host","friend.example").put("port",443)
        .put("invite",Base64.encodeToString(ByteArray(32),Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP))
    fun link() = "nexus://invite/v2#" + Base64.encodeToString(sample.toString().toByteArray(),Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)
    try {
        splitLabel(node.expiryText(), false)
        shot("home")
        clickSplitLabel(node.name)
        splitLabel("添加节点",false)
        clickSplitLabel(node.name)
        splitLabel("设备 ID", false)
        splitLabel(node.deviceId.take(8), false)
        shot("device")
        clickSplitLabel("使用此节点")
        splitLabel("点按圆环即可连接",false)
        ctx.startForegroundService(v2Request(ctx,node.key))
        splitLabel("H3 已连接",false)
        val cm = ctx.getSystemService(android.net.ConnectivityManager::class.java)
        val network = requireNotNull(cm.activeNetwork)
        check(cm.getNetworkCapabilities(network)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true)
        val routes = requireNotNull(cm.getLinkProperties(network))
        check(routes.dnsServers.any { it.hostAddress == "1.1.1.1" })
        check(routes.routes.count { it.isDefaultRoute } >= 2)
        splitLabel("上传",false)
        shot("connected")
        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
        splitLabel("点按圆环即可连接",false)
        clickSplitLabel(node.name)
        clickSplitLabel("添加节点")
        clickSplitLabel("导入设备邀请",true)
        splitLabel("粘贴邀请消息", true)
        edit(link())
        splitLabel("friend.example:443", false)
        shot("confirm")
        clickSplitLabel("取消")
        splitLabel("粘贴邀请消息",true)
        sample.put("host","192.0.2.17").put("pin","A".repeat(43))
        edit(link())
        splitLabel("192.0.2.17:443",false)
        splitLabel("确认兑换",false)
        clickSplitLabel("取消")
        clickSplitLabel("返回")
        clickSplitLabel(node.name)
        clickSplitLabel("添加节点")
        // Supply the same QR result contract as the camera without requiring a physical camera.
        val scan = Intent().putExtra("SCAN_RESULT", link()).putExtra("SCAN_RESULT_FORMAT", "QR_CODE")
        val camera = addMonitor("com.journeyapps.barcodescanner.CaptureActivity",
            Instrumentation.ActivityResult(Activity.RESULT_OK,scan), true)
        try {
            clickSplitLabel("用相机扫码", true)
            splitLabel("确认兑换",false)
            check(camera.hits == 1)
            clickSplitLabel("取消")
        } finally { removeMonitor(camera) }
        splitLabel("添加节点",false)
        clickSplitLabel(node.name)
        clickSplitLabel("在本机移除此节点")
        splitLabel("在本机移除此节点？",false)
        clickSplitLabel("移除")
        splitLabel("添加节点",false)
        check(V2Devices.get(ctx,node.key) == null)
        check(!KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.containsAlias(node.key))
        result.putString("m3-ui","Home, bearer, expiration, device ID, invite confirmation, QR result, pinned IP invitation and local removal passed")
    } finally {
        ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
        if (V2Devices.get(ctx,node.key) != null) V2Devices.remove(ctx,node.key)
        prefs.edit().putString("simple-node",oldChoice).commit()
        runOnMainSync { activity.finish() }
    }
}
