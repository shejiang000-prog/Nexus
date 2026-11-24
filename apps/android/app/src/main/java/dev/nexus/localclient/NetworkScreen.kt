package dev.nexus.localclient

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.SystemClock
import android.system.Os
import android.system.OsConstants
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.URL
import java.text.DateFormat
import java.util.Date
import javax.net.ssl.HttpsURLConnection

internal data class DiagnosticResponse(val message: String, val ip: String? = null)

internal fun diagnosticResponse(site: String, status: Int, content: String?, elapsedMs: Long): DiagnosticResponse {
    if (status != 200) {
        val message = when {
            site == "ipinfo.io" && status == 429 -> "ipinfo.io 暂时限制了这个出口的查询（429）。稍后再试或换个节点，不代表节点故障。"
            site == "ipinfo.io" && status == 403 -> "ipinfo.io 拒绝了这个出口的查询（403）。这是服务方的限制，不代表节点故障。"
            else -> "$site 返回 HTTP $status，请稍后再试。"
        }
        return DiagnosticResponse(message)
    }
    if (site == "ipinfo.io" && content != null) {
        val body = try { JSONObject(content) } catch (_: org.json.JSONException) { null }
        val ip = body?.opt("ip") as? String
        if (ip != null && ip.length in 2..45 && '\u0000' !in ip &&
            Os.inet_pton(if (':' in ip) OsConstants.AF_INET6 else OsConstants.AF_INET, ip) != null) {
            return DiagnosticResponse("$ip · ${body.optString("country").take(8)} · ${body.optString("org").take(100)}", ip)
        }
    } else if (site == "example.com" && content?.contains("Example Domain") == true) {
        return DiagnosticResponse("HTTPS 网页验证成功 · $elapsedMs ms")
    }
    return DiagnosticResponse("$site 返回了无法识别的内容。")
}

@Composable
fun NetworkScreen(tunnel: TunnelState, bypass: Boolean, changeMode: (Boolean) -> Unit, close: () -> Unit, selectedV2Key: String? = null) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("local", 0) }
    val scope = rememberCoroutineScope()
    var result by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var ipConsent by remember { mutableStateOf(false) }
    var pickApps by remember { mutableStateOf(false) }
    val v2Key = (if (tunnel.active) tunnel.node else selectedV2Key)?.takeIf { it.startsWith("v2:") }
    val subscription = v2Key == null && tunnel.subscription.isNotEmpty()
    val key = v2Key ?: tunnel.subscription.takeIf { subscription }
    var apps by remember(key) { mutableStateOf(prefs.getStringSet("apps/$key",emptySet()).orEmpty().toSet()) }
    LaunchedEffect(tunnel.connectedAt,tunnel.waiting,tunnel.error,key) { result = "" }
    fun diagnose(exit: Boolean) {
        busy = true
        scope.launch {
            result = withContext(Dispatchers.IO) {
                val cm = context.getSystemService(ConnectivityManager::class.java)
                val lines = mutableListOf(DateFormat.getDateTimeInstance().format(Date()))
                val network = cm.activeNetwork
                if (network == null || cm.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) != true ||
                    (v2Key == null && !subscription) || !tunnel.vpn || tunnel.starting || tunnel.waiting || tunnel.error.isNotEmpty()) {
                    lines += if (tunnel.vpn && apps.isNotEmpty() && context.packageName !in apps) "当前策略只接管所选应用。要在 Nexus 内检测，请将 Nexus 加入业务应用。" else "请连接全设备 VPN 后验证网页和出口"
                } else {
                    lines += if (subscription) "订阅节点 · ${tunnel.subscription}" else "设备认证和节点 OPEN 已完成"
                    val host = if (exit) "ipinfo.io" else "example.com"
                    val path = if (exit) "/json" else "/"
                    runCatching {
                        val start = SystemClock.elapsedRealtime()
                        check(network.getAllByName(host).isNotEmpty())
                        lines += "DNS 解析成功 · ${SystemClock.elapsedRealtime() - start} ms"
                        val c = network.openConnection(URL("https://$host$path")) as HttpsURLConnection
                        try {
                            c.instanceFollowRedirects = false; c.useCaches = false
                            c.connectTimeout = 6000; c.readTimeout = 6000
                            val status = c.responseCode
                            val data = if (status == 200) try {
                                c.inputStream.bounded(32768).toString(Charsets.UTF_8)
                            } catch (_: IllegalArgumentException) { null } else null
                            val (message, ip) = diagnosticResponse(host, status, data, SystemClock.elapsedRealtime() - start)
                            if (ip != null) {
                                val previous = prefs.getString("exit/$key", null)
                                if (previous != null && previous != ip) lines += "出口已变化 · 上次 $previous"
                                prefs.edit().putString("exit/$key", ip).apply()
                            }
                            lines += message
                        } finally { c.disconnect() }
                    }.onFailure { lines += "请求失败，请检查节点和所选服务" }
                }
                lines.joinToString("\n")
            }
            busy = false
        }
    }
    BackHandler(onBack = close)
    val theme = MaterialTheme.colorScheme
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp)) {
        Text("出口与诊断", style = MaterialTheme.typography.headlineMedium)
        SectionLabel("分流模式")
        ModeSwitch(bypass, changeMode)
        if (v2Key != null) {
            SectionLabel("V2 节点设置")
            val scopeLabel = if (apps.isEmpty()) "全设备都走代理" else "只有选中的 ${apps.size} 个应用走代理"
            ListRow("应用范围", if (tunnel.active) "$scopeLabel · 断开后可修改" else scopeLabel,
                enabled = !tunnel.active) { pickApps = true }
        }
        SectionLabel("检测")
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            PrimaryAction(enabled = !busy && (v2Key != null || subscription), modifier = Modifier.fillMaxWidth(), onClick = { diagnose(false) }) {
                Text("检测 DNS 和网页") }
            SecondaryAction(enabled = !busy && (v2Key != null || subscription) && tunnel.vpn, modifier = Modifier.fillMaxWidth(), onClick = { ipConsent = true }) {
                Text("查询公网出口") }
            if (!tunnel.vpn) Text("连接 VPN 后可以检测连通性、查询公网出口", color = theme.onSurfaceVariant, fontSize = 13.sp)
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (result.isNotEmpty()) Notice(result)
        }
        Spacer(Modifier.height(16.dp))
    }
    if (pickApps) {
        var draft by remember(key) { mutableStateOf(apps) }
        var launchers by remember { mutableStateOf(emptyList<Pair<String,String>>()) }
        LaunchedEffect(Unit) {
            launchers = withContext(Dispatchers.IO) {
                val pm = context.packageManager
                pm.queryIntentActivities(android.content.Intent(android.content.Intent.ACTION_MAIN)
                    .addCategory(android.content.Intent.CATEGORY_LAUNCHER),0)
                    .map { it.activityInfo.packageName to it.loadLabel(pm).toString() }
                    .distinctBy { it.first }.sortedBy { it.second }
            }
        }
        AlertDialog(onDismissRequest = { pickApps=false },title = { Text("选择此节点的业务应用") },
            text = { Column(Modifier.heightIn(max=360.dp).verticalScroll(rememberScrollState())) {
                Text("留空时接管全设备。选择后仅所选应用经此节点，其他应用使用系统网络。")
                TextButton(onClick = { draft = emptySet() }) { Text("恢复全设备") }
                launchers.forEach { (pkg,label) -> Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                    Checkbox(checked = pkg in draft,onCheckedChange = { draft = if (it) draft+pkg else draft-pkg })
                    Text(label)
                } }
            } }, confirmButton = { PrimaryAction(onClick = {
                if (prefs.edit().putStringSet("apps/$key",draft).commit()) { apps=draft; pickApps=false }
            }) { Text("保存") } }, dismissButton = { SecondaryAction(onClick = { pickApps=false }) { Text("取消") } })
    }
    if (ipConsent) AlertDialog(onDismissRequest = { ipConsent = false }, title = { Text("通过 ipinfo.io 查询出口？") },
        text = { Text("ipinfo.io 会看到当前节点的公网出口地址。返回的国家、网络运营方和出口地址仅在本机显示。") },
        confirmButton = { PrimaryAction(onClick = { ipConsent = false; diagnose(true) }) { Text("查询一次") } },
        dismissButton = { SecondaryAction(onClick = { ipConsent = false }) { Text("取消") } })
}
