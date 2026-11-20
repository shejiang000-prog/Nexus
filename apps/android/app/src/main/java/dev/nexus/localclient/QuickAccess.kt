package dev.nexus.localclient

import android.app.Activity
import android.app.PendingIntent
import android.app.StatusBarManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.graphics.drawable.Icon
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * Quick Settings tile: one tap connects with the last connection the user started, or
 * disconnects. Anything that needs a decision (first VPN consent, no saved connection) opens
 * the app instead of guessing.
 */
class ConnectTile : TileService() {
    private var watch: Job? = null

    override fun onStartListening() {
        watch?.cancel()
        watch = CoroutineScope(Dispatchers.Main).launch { TunnelService.state.collect { show(it) } }
    }

    override fun onStopListening() { watch?.cancel(); watch = null }

    private fun show(state: TunnelState) {
        val tile = qsTile ?: return
        tile.state = if (state.vpn) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
        tile.label = "Nexus"
        if (Build.VERSION.SDK_INT >= 29) tile.subtitle = when {
            state.vpn && state.connectedAt > 0 && !state.waiting -> "已连接"
            state.vpn || state.starting -> "正在连接"
            else -> "未连接"
        }
        tile.updateTile()
    }

    override fun onClick() {
        afterTileUnlock(isLocked,{ action -> unlockAndRun { action() } },::toggle)
    }

    private fun toggle() {
        val state = TunnelService.state.value
        if (state.active) {
            startService(Intent(this, TunnelService::class.java).setAction("stop"))
            return
        }
        val saved = getSharedPreferences("local", 0).getString(TunnelService.LAST, null)
        val request = saved?.let { runCatching { Intent.parseUri(it, 0).setClass(this, TunnelService::class.java) }.getOrNull() }
        if (request == null || VpnService.prepare(this) != null) { openApp(); return }
        startForegroundService(request)
    }

    private fun openApp() {
        val intent = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= 34) startActivityAndCollapse(PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE))
        else @Suppress("DEPRECATION") startActivityAndCollapse(intent)
    }

    companion object {
        fun refresh(context: Context) {
            runCatching { requestListeningState(context, ComponentName(context, ConnectTile::class.java)) }
        }

        /** Android 13+ can add the tile with a single system prompt. */
        fun requestAdd(activity: Activity, report: (String) -> Unit) {
            if (Build.VERSION.SDK_INT < 33) { report("请在下拉快捷开关的编辑页里添加 Nexus"); return }
            val bar = activity.getSystemService(StatusBarManager::class.java)
            bar.requestAddTileService(ComponentName(activity, ConnectTile::class.java), "Nexus",
                Icon.createWithResource(activity, R.drawable.nexus_notification), activity.mainExecutor) { result ->
                report(when (result) {
                    StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ADDED -> "已添加，下拉通知栏即可使用"
                    StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ALREADY_ADDED -> "快捷开关已经在下拉栏里了"
                    else -> "未添加。也可以在下拉快捷开关的编辑页里手动添加 Nexus"
                })
            }
        }
    }
}

/** On the lock screen, toggling waits for the system unlock prompt. */
internal fun afterTileUnlock(locked:Boolean, unlock: (() -> Unit) -> Unit, toggle:() -> Unit) {
    if(locked) unlock(toggle) else toggle()
}

/**
 * A clipboard text that looks like a subscription address: one https URL whose path or query
 * names a subscription or carries a long access token. It only ever leads to a prompt.
 */
internal fun subscriptionLink(text: String): String? {
    val link = text.trim()
    if (link.length > 2048 || !link.startsWith("https://") || link.any { it.isWhitespace() }) return null
    val uri = runCatching { android.net.Uri.parse(link) }.getOrNull() ?: return null
    if (uri.host.isNullOrBlank()) return null
    val tail = (uri.encodedPath.orEmpty() + "?" + uri.encodedQuery.orEmpty()).lowercase()
    val marker = Regex("(^|[/?&=_.-])(sub|subscribe|subscription|clash|v2ray|token|flag|feed)([/?&=_.-]|$)|/api/v1/client|/link/")
    // Panels also issue bare token paths such as /feed/<32 characters>.
    val token = (uri.pathSegments + uri.queryParameterNames.mapNotNull { uri.getQueryParameter(it) })
        .any { it.length in 16..128 && it.all { c -> c.isLetterOrDigit() && c.code < 128 || c == '_' } && it.any(Char::isDigit) && it.any(Char::isLetter) }
    return link.takeIf { marker.containsMatchIn(tail) || token }
}
