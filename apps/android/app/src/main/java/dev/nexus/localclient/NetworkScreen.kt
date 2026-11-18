package dev.nexus.localclient

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.SystemClock
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.InetSocketAddress
import java.net.URL
import java.text.DateFormat
import java.util.Date
import javax.net.ssl.HttpsURLConnection

@Composable
fun NetworkScreen(current: Profile?, tunnel: TunnelState, onSelect: (Profile) -> Unit, close: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("local", 0) }
    val scope = rememberCoroutineScope()
    var servers by remember { mutableStateOf(emptyList<Profile>()) }
    var pinned by remember { mutableStateOf(prefs.getString("pinned", null)) }
    var failover by remember { mutableStateOf(prefs.getBoolean("failover",false)) }
    var result by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var ipConsent by remember { mutableStateOf(false) }
    var pickApps by remember { mutableStateOf(false) }
    val selected = tunnel.activeProfile ?: current
    val subscription = tunnel.subscription.isNotEmpty()
    val key = if (subscription) tunnel.subscription else selected?.let { it.server + "/" + it.fingerprint }
    var apps by remember(key) { mutableStateOf(prefs.getStringSet("apps/$key",emptySet()).orEmpty().toSet()) }
    var monitoring by remember(key) { mutableStateOf(prefs.getBoolean("monitor/$key",false)) }
    var hold by remember(key) { mutableStateOf(prefs.getBoolean("hold/$key",false)) }
    var monitorConsent by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { servers = withContext(Dispatchers.IO) { Profile.servers(context) } }
    LaunchedEffect(tunnel.connectedAt,tunnel.waiting,tunnel.error,key) { result = "" }
    fun diagnose(exit: Boolean) {
        val profile = selected
        busy = true
        scope.launch {
            result = withContext(Dispatchers.IO) {
                val cm = context.getSystemService(ConnectivityManager::class.java)
                val lines = mutableListOf(DateFormat.getDateTimeInstance().format(Date()))
                if (!exit && !subscription && profile != null) {
                    val physical = cm.allNetworks.firstOrNull {
                        val caps = cm.getNetworkCapabilities(it)
                        caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true &&
                            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    }
                    val start = SystemClock.elapsedRealtime()
                    val tcp = runCatching {
                        requireNotNull(physical).socketFactory.createSocket().use {
                            val host = profile.server.substringBeforeLast(':').removeSurrounding("[", "]")
                            it.connect(InetSocketAddress(host, profile.server.substringAfterLast(':').toInt()), 3000)
                        }
                    }.isSuccess
                    lines += if (tcp) "节点 TCP 端口可达 · ${SystemClock.elapsedRealtime() - start} ms" else "节点 TCP 端口检测失败"
                }
                val network = cm.activeNetwork
                if (network == null || cm.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) != true ||
                    (!subscription && profile?.dns == null) || !tunnel.vpn || tunnel.starting || tunnel.waiting || tunnel.error.isNotEmpty()) {
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
                            check(c.responseCode == 200)
                            val data = c.inputStream.bounded(32768).toString(Charsets.UTF_8)
                            if (exit) {
                                val body = JSONObject(data)
                                val ip = body.getString("ip")
                                require(ip.length in 2..45 && ip.matches(Regex("[0-9a-fA-F:.]+")))
                                val previous = prefs.getString("exit/$key", null)
                                if (previous != null && previous != ip) lines += "出口已变化 · 上次 $previous"
                                lines += "$ip · ${body.optString("country").take(8)} · ${body.optString("org").take(100)}"
                                prefs.edit().putString("exit/$key", ip).apply()
                            } else {
                                check(data.contains("Example Domain"))
                                lines += "HTTPS 网页验证成功 · ${SystemClock.elapsedRealtime() - start} ms"
                            }
                        } finally { c.disconnect() }
                    }.onFailure { lines += "请求失败，请检查节点和所选服务" }
                }
                lines.joinToString("\n")
            }
            busy = false
        }
    }
    BackHandler(onBack = close)
    Column(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()).padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)) {
        TextButton(onClick = close) { Text("返回我的网络") }
        Text("出口与诊断", style = MaterialTheme.typography.headlineMedium)
        servers.forEach { server ->
            val chosen = server.server == current?.server && server.fingerprint == current.fingerprint
            ListItem(headlineContent = { Text(server.name) }, supportingContent = { Text(server.server) },
                trailingContent = { TextButton(enabled = !chosen && !tunnel.active && pinned == null, onClick = {
                    runCatching { server.save(context) }.onSuccess { onSelect(server) }.onFailure { result = "切换失败" }
                }) { Text(if (chosen) "当前出口" else "选择") } })
        }
        if (current?.dns != null) {
            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text("锁定当前私人节点", Modifier.weight(1f))
                Switch(enabled = !tunnel.active, checked = pinned != null, onCheckedChange = { lock ->
                    val next = if (lock) key else null
                    if (prefs.edit().putString("pinned", next).commit()) pinned = next
                })
            }
            Text("锁定期间固定使用这台服务器，网络中断时保留 VPN 路由。主动断开会恢复系统网络。")
            TextButton(enabled = !tunnel.active,onClick = { pickApps=true }) { Text(if (apps.isEmpty()) "应用范围 · 全设备" else "业务应用 · ${apps.size} 个") }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) {
                Text("持续检查出口 IP",Modifier.weight(1f))
                Switch(enabled=!tunnel.active,checked=monitoring,onCheckedChange={
                    if (it) monitorConsent=true else if (prefs.edit().putBoolean("monitor/$key",false).commit()) monitoring=false
                })
            }
            if (monitoring) Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) {
                Text("出口变化时暂停业务流量",Modifier.weight(1f))
                Switch(enabled=!tunnel.active,checked=hold,onCheckedChange={
                    if (prefs.edit().putBoolean("hold/$key",it).commit()) hold=it
                })
            }
            TextButton(onClick = {
                runCatching { context.startActivity(android.content.Intent(android.provider.Settings.ACTION_VPN_SETTINGS)) }
                    .onFailure { result = "请在系统设置中打开 VPN" }
            }) { Text("系统级断线保护设置") }
            Text("如需在进程退出或重启时阻止直连，请在系统 VPN 设置中启用始终开启和阻止未使用 VPN 的连接。系统将使用当前保存的全设备私人节点。")
            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text("连接失败时切换到其他已保存的私人节点", Modifier.weight(1f))
                Switch(enabled = !tunnel.active && pinned == null && apps.isEmpty(), checked = failover && pinned == null && apps.isEmpty(), onCheckedChange = {
                    if (prefs.edit().putBoolean("failover",it).commit()) failover = it
                })
            }
        }
        HorizontalDivider()
        Button(enabled = !busy && (current != null || subscription), onClick = { diagnose(false) }) { Text(if (subscription) "检测 DNS 和网页" else "检测端口、DNS 和网页") }
        TextButton(enabled = !busy && (selected?.dns != null || subscription) && tunnel.vpn, onClick = { ipConsent = true }) { Text("查询公网出口") }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (result.isNotEmpty()) Text(result)
    }
    if (monitorConsent) AlertDialog(onDismissRequest={monitorConsent=false},title={Text("允许定期查询公网出口？")},
        text={Text("连接此节点时，Nexus 每五分钟通过 VPN 向 ipinfo.io 查询一次公网 IP，地址变化时发出通知。业务应用模式需把 Nexus 加入所选应用。")},
        confirmButton={TextButton(onClick={monitoring=prefs.edit().putBoolean("monitor/$key",true).commit();monitorConsent=false}){Text("启用")}},
        dismissButton={TextButton(onClick={monitorConsent=false}){Text("取消")}})
    if (pickApps) {
        var draft by remember { mutableStateOf(apps) }
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
            } }, confirmButton = { TextButton(onClick = {
                if (prefs.edit().putStringSet("apps/$key",draft).commit()) { apps=draft; pickApps=false }
            }) { Text("保存") } }, dismissButton = { TextButton(onClick = { pickApps=false }) { Text("取消") } })
    }
    if (ipConsent) AlertDialog(onDismissRequest = { ipConsent = false }, title = { Text("通过 ipinfo.io 查询出口？") },
        text = { Text("ipinfo.io 会看到私人节点的公网地址。返回的国家、网络运营方和出口地址仅在本机显示。") },
        confirmButton = { TextButton(onClick = { ipConsent = false; diagnose(true) }) { Text("查询一次") } },
        dismissButton = { TextButton(onClick = { ipConsent = false }) { Text("取消") } })
}
