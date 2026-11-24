package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
import android.view.accessibility.AccessibilityNodeInfo
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.io.File
import java.net.InetSocketAddress
import java.text.SimpleDateFormat
import java.time.Instant
import java.util.Date
import java.util.Locale
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.TimeUnit

internal fun Instrumentation.m34ShareCheck(result: Bundle) {
    val ctx = targetContext
    check(ctx.packageName == "dev.nexus.lab.m0" && VpnService.prepare(ctx) == null)
    val cfg = JSONObject(File(ctx.noBackupFilesDir, "m3.json").readText())
    val cm = ctx.getSystemService(ConnectivityManager::class.java)
    val physical = requireNotNull(cm.activeNetwork)
    fun ledger(command: String): String = physical.socketFactory.createSocket().use { s ->
        s.soTimeout = 5000
        s.connect(InetSocketAddress(cfg.getString("host"), cfg.getInt("controlPort")), 1500)
        s.getOutputStream().write((cfg.getString("control") + " share " + command + "\n").toByteArray())
        val input = s.getInputStream()
        check(input.read() == 1) { "sharing fixture rejected command" }
        requireNotNull(input.bufferedReader().readLine())
    }
    fun <T> withActivity(block: (MainActivity) -> T): T {
        val activity = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        try { return block(activity) } finally {
            runOnMainSync { activity.finish() }
            waitForIdleSync()
        }
    }
    fun screenshot(name: String) {
        uiAutomation.waitForIdle(250, 2000)
        val shot = requireNotNull(uiAutomation.takeScreenshot())
        try {
            File(ctx.noBackupFilesDir, "m34-share-$name.png").outputStream().use {
                check(shot.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        } finally { shot.recycle() }
    }
    fun button(name: String): AccessibilityNodeInfo =
        generateSequence(splitLabel(name, false)) { it.parent }.first { it.isClickable || it.className == "android.widget.Button" }
    fun tap(item: AccessibilityNodeInfo) {
        val bounds = android.graphics.Rect().also(item::getBoundsInScreen)
        val now = SystemClock.uptimeMillis()
        for (action in listOf(android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP)) {
            val event = android.view.MotionEvent.obtain(now, SystemClock.uptimeMillis(), action,
                bounds.exactCenterX(), bounds.exactCenterY(), 0)
            try { sendPointerSync(event) } finally { event.recycle() }
        }
    }
    fun field(): AccessibilityNodeInfo {
        fun find(n: AccessibilityNodeInfo): AccessibilityNodeInfo? {
            if (n.className == "android.widget.EditText" || n.isEditable) return n
            for (i in 0 until n.childCount) n.getChild(i)?.let { find(it) }?.let { return it }
            return null
        }
        return requireNotNull(find(requireNotNull(uiAutomation.rootInActiveWindow)))
    }
    // Exercise the same Compose input, invitation button and managed device row as the manager.
    fun choose(row: JSONObject?, duration: Int?): V2ShareValidity = withActivity { activity ->
        val selected = ArrayBlockingQueue<V2ShareValidity>(1)
        runOnMainSync { activity.setContent { NexusTheme { Surface(Modifier.fillMaxSize()) {
            Column(Modifier.fillMaxWidth().statusBarsPadding().padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                var days by remember { mutableStateOf("") }
                var permanent by remember { mutableStateOf(false) }
                val validity = V2ShareValidity.parse(days, permanent)
                V2ValidityInput(days, permanent, true, { days = it }, { permanent = it }, { selected.offer(it) })
                if (row != null) V2ManagedDeviceRow(row, GroupPlace.Single, false, validity != null,
                    { selected.offer(requireNotNull(validity)) }, {})
            }
        } } } }
        val inviteLabel = "邀请朋友或我的设备"
        val action = if (row == null) inviteLabel else "续期"
        check(!button(inviteLabel).isEnabled && !button(action).isEnabled && field().isEnabled)
        if (row != null && row.isNull("expires_at")) screenshot("empty")
        uiAutomation.waitForIdle(300, 2000)
        if (duration == null) {
            tap(button("永久"))
        } else {
            tap(field())
            waitForIdleSync()
            sendStringSync(duration.toString())
            runOnMainSync {
                activity.getSystemService(android.view.inputmethod.InputMethodManager::class.java)
                    .hideSoftInputFromWindow(activity.window.decorView.windowToken, 0)
            }
        }
        val deadline = SystemClock.elapsedRealtime() + 3000
        while ((!button(inviteLabel).isEnabled || !button(action).isEnabled) &&
            SystemClock.elapsedRealtime() < deadline) Thread.sleep(100)
        if (!button(inviteLabel).isEnabled || !button(action).isEnabled) {
            screenshot("input-failure")
            result.putString("input-state", "days=$duration field=${field().text} invite=${button(inviteLabel).isEnabled} action=$action enabled=${button(action).isEnabled}")
            error("validity controls did not update")
        }
        check(field().isEnabled == (duration != null))
        if (row != null) screenshot(if (duration == null) "permanent" else "days")
        clickSplitLabel(action)
        requireNotNull(selected.poll(2, TimeUnit.SECONDS)).also { check(it.days == duration) }
    }
    fun expectedText(expires: Long?) = expires?.let {
        SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.ROOT).format(Date(it * 1000)) + " 到期"
    } ?: "长期有效"
    fun waitUntil(message: String, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + 15000
        do { if (condition()) return; Thread.sleep(100) } while (SystemClock.elapsedRealtime() < deadline)
        error(message)
    }
    fun echo(): Boolean = runCatching {
        val vpn = requireNotNull(cm.activeNetwork)
        check(cm.getNetworkCapabilities(vpn)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true)
        vpn.socketFactory.createSocket().use { s ->
            s.soTimeout = 800
            s.connect(InetSocketAddress(cfg.getString("target"), cfg.getInt("echoPort")), 800)
            s.getOutputStream().write(65)
            check(s.getInputStream().read() == 65)
        }
    }.isSuccess
    val prefs = ctx.getSharedPreferences("local", 0)
    val oldChoice = prefs.getString("simple-node", null)
    val oldSplit = prefs.all[SplitRouting.KEY] as? Boolean
    try {
        prefs.edit().putBoolean(SplitRouting.KEY, false).commit()
        val validity = choose(null, null)
        val created = ledger("now").toLong()
        val account = ledger(validity.createUser(created))
        val inviteCommand = validity.invite(account, created)
        check(Instant.parse(inviteCommand.substringAfterLast(' ')).epochSecond == created + 7 * 86400L)
        val invite = V2Invite.parse(ledger(inviteCommand))
        val node = V2Devices.enroll(ctx, invite, cfg.getString("endpoint"), cfg.getString("ca").toByteArray())
        try {
            check(node.expiryKnown && node.expiresAt == null)
            prefs.edit().putString("simple-node", node.key).commit()
            withActivity { splitLabel(expectedText(null), false) }
            for (days in listOf(1, null)) {
                val row = JSONObject(ledger("device $account"))
                check(row.getString("id") == node.deviceId)
                val expires = if (row.isNull("expires_at")) null else row.getLong("expires_at")
                val next = choose(row, days)
                val now = ledger("now").toLong()
                ledger(next.renew(account, now, expires))
                val expected = days?.let { now + 86400L }
                withActivity {
                    ctx.startForegroundService(v2Request(ctx, node.key))
                    try {
                        waitUntil("new validity did not reach the same device") {
                            echo() && V2Devices.get(ctx, node.key)?.let {
                                it.deviceId == node.deviceId && it.expiryKnown && it.expiresAt == expected
                            } == true
                        }
                        splitLabel(expectedText(expected), false)
                        screenshot(if (days == null) "home-permanent" else "home-date")
                    } finally {
                        ctx.startService(Intent(ctx, TunnelService::class.java).setAction("stop"))
                        waitUntil("VPN failed to stop") { !TunnelService.state.value.active &&
                            cm.getNetworkCapabilities(cm.activeNetwork)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) != true }
                    }
                }
            }
            result.putString("sharing", "永久邀请 → 1 天 → 长期有效；同一设备两次认证后的首页与节点到期值一致")
        } finally { V2Devices.remove(ctx, node.key) }
    } finally {
        prefs.edit().putString("simple-node", oldChoice).apply {
            if (oldSplit == null) remove(SplitRouting.KEY) else putBoolean(SplitRouting.KEY, oldSplit)
        }.commit()
    }
}
