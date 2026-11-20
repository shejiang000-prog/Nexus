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
import androidx.compose.ui.unit.sp
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
fun NetworkScreen(current: Profile?, tunnel: TunnelState, bypass: Boolean, changeMode: (Boolean) -> Unit, onSelect: (Profile) -> Unit, close: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("local", 0) }
    val scope = rememberCoroutineScope()
    var servers by remember { mutableStateOf(emptyList<Profile>()) }
    var pinned by remember { mutableStateOf(prefs.getString("pinned", null)) }
    var failover by remember { mutableStateOf(prefs.getBoolean("failover",false)) }
    var result by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var newAddress by remember { mutableStateOf<String?>(null) }
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
                                if (!prefs.getBoolean("monitor/$key",false)) prefs.edit().putString("exit/$key", ip).apply()
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
    val theme = MaterialTheme.colorScheme
    @Composable fun toggle(checked: Boolean, enabled: Boolean, change: (Boolean) -> Unit) =
        Switch(checked, change, enabled = enabled, colors = switchColors())
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp)) {
        Text("出口与诊断", style = MaterialTheme.typography.headlineMedium)
        SectionLabel("分流模式")
        ModeSwitch(bypass, changeMode)
        if (servers.isNotEmpty()) {
            SectionLabel("私人节点出口")
            servers.forEachIndexed { i, server ->
                val chosen = server.server == current?.server && server.fingerprint == current.fingerprint
                val open = !chosen && !tunnel.active && pinned == null
                ListRow(server.name, server.server, groupPlace(i, servers.lastIndex, false, false),
                    trailing = { Text(if (chosen) "当前出口" else "选择", color = if (chosen) theme.onSurfaceVariant else theme.primary,
                        fontWeight = androidx.compose.ui.text.font.FontWeight.SemiBold) },
                    onClick = if (open) ({ runCatching { server.save(context) }.onSuccess { onSelect(server) }.onFailure { result = "切换失败" } }) else null)
            }
        }
        if (current != null) {
            SectionLabel("私人节点设置")
            val rows = buildList<Pair<String, @Composable (GroupPlace) -> Unit>> {
                if (current.dns != null) {
                    add("lock" to { place -> ListRow("锁定当前私人节点", "固定使用这台服务器，网络中断时保留 VPN 路由", place,
                        trailing = { toggle(pinned != null, !tunnel.active) { lock ->
                            val next = if (lock) key else null
                            if (prefs.edit().putString("pinned", next).commit()) pinned = next
                        } }) })
                    add("apps" to { place -> ListRow("应用范围", if (apps.isEmpty()) "全设备都走代理" else "只有选中的 ${apps.size} 个应用走代理", place,
                        enabled = !tunnel.active) { pickApps = true } })
                    add("monitor" to { place -> ListRow("持续检查出口 IP", "每五分钟经 VPN 查询一次，出口变化时通知", place,
                        trailing = { toggle(monitoring, !tunnel.active) {
                            if (it) monitorConsent = true else if (prefs.edit().putBoolean("monitor/$key", false).commit()) monitoring = false
                        } }) })
                    if (monitoring) add("hold" to { place -> ListRow("出口变化时暂停业务流量", "", place,
                        trailing = { toggle(hold, !tunnel.active) { if (prefs.edit().putBoolean("hold/$key", it).commit()) hold = it } }) })
                    add("failover" to { place -> ListRow("失败时切换到其他私人节点", if (pinned != null || apps.isNotEmpty()) "锁定或限定应用时不可用" else "", place,
                        trailing = { toggle(failover && pinned == null && apps.isEmpty(), !tunnel.active && pinned == null && apps.isEmpty()) {
                            if (prefs.edit().putBoolean("failover", it).commit()) failover = it
                        } }) })
                }
                add("move" to { place -> ListRow("更新迁移后的服务器地址", "服务器换了 IP 或端口时在这里修改", place,
                    enabled = !tunnel.active && pinned == null) { newAddress = current.server } })
            }
            rows.forEachIndexed { i, (_, row) -> row(groupPlace(i, rows.lastIndex, false, false)) }
            newAddress?.let { address ->
                AlertDialog(onDismissRequest={newAddress=null},title={Text("更新私人节点地址")},text={Column(verticalArrangement=Arrangement.spacedBy(10.dp)) {
                    Text("向拥有者确认新地址及端口。下次连接继续校验原 CA 和本机设备证书。")
                    OutlinedTextField(address,{newAddress=it.take(96)},singleLine=true,label={Text("新 IP:端口")})
                }},confirmButton={ PrimaryAction(onClick={
                    runCatching { current.moveEndpoint(context,address.trim()) }.onSuccess {
                        onSelect(it); servers=Profile.servers(context);newAddress=null
                    }.onFailure { result="地址无效、已存在或节点已锁定";newAddress=null }
                }) { Text("更新地址") } },dismissButton={SecondaryAction(onClick={newAddress=null}) { Text("取消") }})
            }
        }
        SectionLabel("检测")
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            PrimaryAction(enabled = !busy && (current != null || subscription), modifier = Modifier.fillMaxWidth(), onClick = { diagnose(false) }) {
                Text(if (subscription) "检测 DNS 和网页" else "检测端口、DNS 和网页") }
            SecondaryAction(enabled = !busy && (selected?.dns != null || subscription) && tunnel.vpn, modifier = Modifier.fillMaxWidth(), onClick = { ipConsent = true }) {
                Text("查询公网出口") }
            if (!tunnel.vpn) Text("连接 VPN 后可以检测连通性、查询公网出口", color = theme.onSurfaceVariant, fontSize = 13.sp)
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (result.isNotEmpty()) Notice(result)
        }
        Spacer(Modifier.height(16.dp))
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
            } }, confirmButton = { PrimaryAction(onClick = {
                if (prefs.edit().putStringSet("apps/$key",draft).commit()) { apps=draft; pickApps=false }
            }) { Text("保存") } }, dismissButton = { SecondaryAction(onClick = { pickApps=false }) { Text("取消") } })
    }
    if (ipConsent) AlertDialog(onDismissRequest = { ipConsent = false }, title = { Text("通过 ipinfo.io 查询出口？") },
        text = { Text("ipinfo.io 会看到私人节点的公网地址。返回的国家、网络运营方和出口地址仅在本机显示。") },
        confirmButton = { PrimaryAction(onClick = { ipConsent = false; diagnose(true) }) { Text("查询一次") } },
        dismissButton = { SecondaryAction(onClick = { ipConsent = false }) { Text("取消") } })
}
