package dev.nexus.localclient

import android.content.Intent
import android.net.VpnService
import android.app.Activity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import java.text.DateFormat
import java.util.Date
import org.json.JSONObject

internal data class NodeRow(val source: Subscription, val index: Int, val node: JSONObject) {
    val type get() = node.getString("type")
    val region by lazy {
        val data = node.optJSONObject("config")
        data?.optString("country")?.takeIf { it.isNotBlank() }?.take(32)
            ?: Regex("[\\x{1F1E6}-\\x{1F1FF}]{2}").find(node.getString("name"))?.value ?: "未提供"
    }
    val favorite by lazy { nodeKey(source.id,node) }
}

internal fun Subscription.hasAllowance(): Boolean {
    val usage = usage.split(';').mapNotNull { field ->
        val parts = field.trim().split('=',limit=2)
        val amount = parts.getOrNull(1)?.toLongOrNull()?.takeIf { it >= 0 } ?: return@mapNotNull null
        parts[0] to amount
    }.toMap()
    val expires = usage["expire"] ?: 0
    val quota = usage["total"] ?: 0
    return (expires == 0L || expires > System.currentTimeMillis()/1000) &&
        (quota == 0L || (usage["download"] ?: 0) < quota - (usage["upload"] ?: 0))
}

private fun rankedNodes(context: android.content.Context, rows: List<NodeRow>): List<NodeRow> {
    val store = Subscriptions(context)
    val quality = NodeQuality(context, context.getSystemService(android.net.ConnectivityManager::class.java).activeNetwork)
    return rows.filter { it.type in CompatEngine.types && store.appearance.getBoolean("auto/"+it.source.id,false) &&
        it.source.hasAllowance() && CompatEngine.problem(it.node) == null }.sortedWith(compareByDescending<NodeRow> { store.appearance.getBoolean("pin/"+it.source.id,false) }
            .thenByDescending { quality.score(it.favorite) })
}

internal fun subscriptionRequest(context: android.content.Context, row: NodeRow, allRows: List<NodeRow>,
                                dns: String, smart: Boolean): Intent {
    val request = Intent(context,TunnelService::class.java).setAction("subscription")
        .putExtra("source",row.source.id).putExtra("updated",row.source.updated).putExtra("index",row.index).putExtra("dns",dns)
    if (smart) {
        val candidates = rankedNodes(context,allRows).filter { it.favorite != row.favorite }.take(7)
        val refs = org.json.JSONArray()
        candidates.forEach { refs.put(JSONObject().put("source",it.source.id).put("updated",it.source.updated).put("index",it.index)) }
        request.putExtra("alternates",refs.toString())
    }
    return request
}

@Composable
fun SubscriptionScreen(home: @Composable () -> Unit) {
    val context = LocalContext.current
    val store = remember { Subscriptions(context) }
    val scope = rememberCoroutineScope()
    val scroll = androidx.compose.foundation.lazy.rememberLazyListState()
    var sources by remember { mutableStateOf(emptyList<Subscription>()) }
    var selected by remember { mutableStateOf<String?>(null) }
    var search by remember { mutableStateOf("") }
    var menu by remember { mutableStateOf(false) }
    var adding by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<Subscription?>(null) }
    var input by remember { mutableStateOf("") }
    var label by remember { mutableStateOf("") }
    var exporting by remember { mutableStateOf<Subscription?>(null) }
    var exportConsent by remember { mutableStateOf(false) }
    var editSource by remember { mutableStateOf<Subscription?>(null) }
    var busy by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf("") }
    var errorMessage by remember { mutableStateOf("") }
    val prefs = remember { context.getSharedPreferences("node-favorites",0) }
    var favorites by remember { mutableStateOf(prefs.getStringSet("items",emptySet()).orEmpty().toSet()) }
    var favoritesOnly by remember { mutableStateOf(false) }
    var protocol by remember { mutableStateOf("") }
    var region by remember { mutableStateOf("") }
    var sort by remember { mutableStateOf("默认") }
    var filterMenu by remember { mutableStateOf("") }
    var detail by remember { mutableStateOf<NodeRow?>(null) }
    var metrics by remember { mutableStateOf(emptyMap<String,Long>()) }
    var chosen by remember { mutableStateOf<NodeRow?>(null) }
    var dns by remember { mutableStateOf("1.1.1.1") }
    var smart by remember { mutableStateOf(false) }
    val allRows = remember(sources) { sources.flatMap { s -> (0 until s.nodes.length()).map { NodeRow(s,it,s.nodes.getJSONObject(it)) } } }
    // Reasons a node cannot be used, keyed by NodeRow.favorite. Conversion runs off the main thread.
    var problems by remember { mutableStateOf<Map<String,String>?>(null) }
    LaunchedEffect(allRows) {
        problems = withContext(Dispatchers.Default) {
            allRows.mapNotNull { row -> CompatEngine.problem(row.node)?.let { row.favorite to it } }.toMap()
        }
    }
    val tunnel by TunnelService.state.collectAsState()
    val connectivity = context.getSystemService(android.net.ConnectivityManager::class.java)
    val quality = NodeQuality(context, tunnel.physical ?: connectivity.activeNetwork)
    var speedResult by remember(tunnel.connectedAt) { mutableStateOf("") }
    fun connect(row: NodeRow) {
        prefs.edit().putLong("recent/"+row.favorite,System.currentTimeMillis()).apply()
        val request = subscriptionRequest(context,row,allRows,dns,smart)
        context.startForegroundService(request)
        chosen = null
        scope.launch { scroll.scrollToItem(0) }
    }
    val consent = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK) chosen?.let(::connect) else { message = "VPN 授权已取消"; chosen = null }
    }
    fun update(action: () -> Unit) {
        if (busy) return
        busy = true
        errorMessage = ""
        scope.launch {
            try {
                sources = withContext(Dispatchers.IO) { action(); store.all() }
                message = ""; errorMessage = ""
            } catch (_: Exception) { message = ""; errorMessage = "操作失败，请检查订阅格式、文件大小或 HTTPS 地址。" }
            finally { busy = false }
        }
    }
    LaunchedEffect(Unit) { update {} }
    val open = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) {
            adding = false
            busy = true
            scope.launch {
                val count = withContext(Dispatchers.IO) {
                    uris.take(32).count { uri -> runCatching {
                        val fileName = context.contentResolver.query(uri,arrayOf(android.provider.OpenableColumns.DISPLAY_NAME),null,null,null)
                            ?.use { if (it.moveToFirst()) it.getString(0) else null }
                        val name = if (uris.size==1 && label.isNotBlank()) label else fileName?.take(64) ?: "本地文件"
                        store.import(name,store.readFile(uri))
                    }.isSuccess }
                }
                sources = withContext(Dispatchers.IO) { store.all() }
                val summary = "已导入 $count / ${uris.size} 个文件"
                message = if (count==uris.size) summary else ""
                errorMessage = if (count==uris.size) "" else summary
                busy = false
            }
        }
    }
    val scan = rememberLauncherForActivityResult(com.journeyapps.barcodescanner.ScanContract()) {
        it.contents?.let { value -> input = value.take(524288) }
    }
    val export = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        val source = exporting
        exporting = null
        if (uri != null && source != null) update {
            val data = JSONObject().put("format","nexus-nodes/1").put("nodes",source.nodes).toString().toByteArray()
            require(data.size <= 524288)
            requireNotNull(context.contentResolver.openOutputStream(uri,"wt")).use { it.write(data) }
        }
    }
    val visible = remember(allRows, selected, search, favoritesOnly, favorites,protocol,region,sort,metrics) {
        val rows = allRows.filter { row ->
            (selected == null || row.source.id == selected) && (!favoritesOnly || row.favorite in favorites) &&
                (protocol.isEmpty() || protocol==row.type) && (region.isEmpty() || region==row.region) &&
                (row.node.getString("name").contains(search,true) || row.node.getString("type").contains(search,true))
        }
        when (sort) {
            "端口延迟" -> rows.sortedBy { metrics[it.favorite]?.takeIf { ms -> ms >= 0 } ?: Long.MAX_VALUE }
            "最近选择" -> rows.sortedByDescending { prefs.getLong("recent/"+it.favorite,0) }
            "收藏优先" -> rows.sortedByDescending { it.favorite in favorites }
            "网络质量" -> rows.sortedByDescending { quality.score(it.favorite) }
            else -> rows
        }
    }
    fun measure() {
        if (busy || tunnel.active) return
        busy = true
        errorMessage = ""
        scope.launch {
            try {
                val results = mutableMapOf<String,Long>()
                val tcp = visible.filter { it.type !in setOf("hy2","hysteria2","tuic") }
                for (batch in tcp.chunked(6)) {
                    val values = coroutineScope { batch.map { row -> async(Dispatchers.IO) {
                        val time = runCatching {
                            val raw = row.node.get("config")
                            val addr = if (raw is JSONObject) raw.getString("server") to raw.optInt("port",raw.optInt("server_port"))
                                else android.net.Uri.parse(raw.toString()).let { requireNotNull(it.host) to it.port }
                            require(addr.second in 1..65535)
                            val start = android.os.SystemClock.elapsedRealtime()
                            java.net.Socket().use { it.connect(java.net.InetSocketAddress(addr.first,addr.second),1800) }
                            android.os.SystemClock.elapsedRealtime()-start
                        }.getOrDefault(-1)
                        row.favorite to time
                    } }.awaitAll() }
                    results.putAll(values)
                    metrics = metrics + values
                }
                message = "TCP 端口可达 ${results.values.count { it>=0 }} / ${results.size}，UDP 协议需连接后验证"
            } finally { busy = false }
        }
    }
    LazyColumn(Modifier.fillMaxSize().safeDrawingPadding().padding(horizontal = 20.dp),
        state = scroll, contentPadding = PaddingValues(vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item { home() }
        item {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("订阅与节点", style = MaterialTheme.typography.headlineMedium)
                Button(enabled = !busy, onClick = { input = ""; label = ""; adding = true }) { Text("添加订阅") }
            }
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (message.isNotEmpty()) Text(message, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (errorMessage.isNotEmpty()) Text(errorMessage, color = MaterialTheme.colorScheme.error)
            val current = tunnel
            current.node?.let { node ->
                TextButton(enabled = !busy, onClick = {
                    busy = true
                    scope.launch {
                        try {
                            speedResult = withContext(Dispatchers.IO) {
                                val vpn = requireNotNull(connectivity.activeNetwork)
                                require(connectivity.getNetworkCapabilities(vpn)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true)
                                quality.measure(vpn,node) { TunnelService.state.value.connectedAt == current.connectedAt }
                            }
                        } catch (_: Exception) { speedResult = "测速中断，请检查连接后重试" }
                        finally { busy = false }
                    }
                }) { Text("通过当前节点测速（Cloudflare，上下行各 1 MiB）") }
                if (speedResult.isNotEmpty()) Text(speedResult)
            }
            if (sources.isEmpty() && !busy) Text("添加订阅后，可以按来源查看节点。")
        }
        items(sources, key = { it.id }) { source ->
            Column {
                var folded by remember { mutableStateOf(store.appearance.getBoolean("fold/"+source.id,false)) }
                val color = store.appearance.getInt("color/"+source.id,0)
                val palette = listOf(MaterialTheme.colorScheme.primary,MaterialTheme.colorScheme.tertiary,MaterialTheme.colorScheme.secondary)
                Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                    Text(listOf(store.appearance.getString("mark/"+source.id,"").orEmpty(),source.label)
                        .filter(String::isNotEmpty).joinToString(" "), color = palette[color.coerceIn(0,2)],
                        style = MaterialTheme.typography.titleMedium,modifier = Modifier.weight(1f).pointerInput(source.id,sources) {
                            var distance = 0f
                            detectDragGesturesAfterLongPress(onDragStart = { distance=0f },onDragEnd = {
                                if (!busy && kotlin.math.abs(distance)>32.dp.toPx()) update { store.move(sources,source.id,if(distance>0) 1 else -1) }
                            }) { change,delta -> change.consume(); distance+=delta.y }
                        })
                    TextButton(onClick = { folded=!folded; store.appearance.edit().putBoolean("fold/"+source.id,folded).apply() }) { Text(if (folded) "展开" else "收起") }
                    TextButton(onClick = { editSource=source }) { Text("管理") }
                }
                if (!folded) {
                if (source.url.isNotEmpty()) Text(android.net.Uri.parse(source.url).host.orEmpty(),style = MaterialTheme.typography.bodySmall)
                val blocked = problems?.let { map -> (0 until source.nodes.length()).count { nodeKey(source.id,source.nodes.getJSONObject(it)) in map } } ?: 0
                Text("${source.nodes.length()} 个节点" + (if (blocked > 0) "（$blocked 个暂不支持）" else "") +
                    " · ${DateFormat.getDateTimeInstance().format(Date(source.updated))}",
                    style = MaterialTheme.typography.bodySmall)
                val usage = source.usage.split(';').mapNotNull {
                    val n = it.substringAfter('=', "").toLongOrNull() ?: return@mapNotNull null
                    when (it.substringBefore('=')) {
                        "expire" -> if (n in 1..253402300799) "到期 " + DateFormat.getDateInstance().format(Date(n * 1000)) else null
                        "total" -> "总量 ${n / 1048576} MiB"
                        "download" -> "下载 ${n / 1048576} MiB"
                        "upload" -> "上传 ${n / 1048576} MiB"
                        else -> null
                    }
                }
                Text(usage.takeIf { it.isNotEmpty() }?.joinToString(" · ") ?: "流量与到期时间未提供", style = MaterialTheme.typography.bodySmall)
                Row {
                    if (source.url.isNotEmpty()) TextButton(enabled = !busy,
                        onClick = { update { store.import(source.label, source.url, source) } }) { Text("更新") }
                    TextButton(enabled = !busy, onClick = { deleting = source }) { Text("删除") }
                    TextButton(enabled = !busy, onClick = { exporting = source; exportConsent = true }) { Text("导出") }
                }
                }
                HorizontalDivider()
            }
        }
        item {
            Box {
                TextButton(onClick = { menu = true }) { Text(sources.find { it.id == selected }?.label ?: "全部来源") }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text("全部来源") }, onClick = { selected = null; menu = false })
                    sources.forEach { s -> DropdownMenuItem(text = { Text(s.label) },
                        onClick = { selected = s.id; menu = false }) }
                }
            }
            OutlinedTextField(search, { search = it.take(96) }, label = { Text("搜索名称或协议") }, singleLine = true)
            Row {
                listOf("协议","地区","排序").forEach { tag ->
                    Box {
                        TextButton(onClick = { filterMenu = tag }) { Text(when(tag) {
                            "协议" -> protocol.ifEmpty { tag }; "地区" -> region.ifEmpty { tag }; else -> sort
                        }) }
                        DropdownMenu(expanded = filterMenu==tag,onDismissRequest = { filterMenu="" }) {
                            val choices = when(tag) { "协议" -> listOf("") + allRows.map { it.type }.distinct().sorted()
                                "地区" -> listOf("") + allRows.map { it.region }.distinct().sorted()
                                else -> listOf("默认","端口延迟","网络质量","最近选择","收藏优先") }
                            choices.forEach { choice -> DropdownMenuItem(text = { Text(choice.ifEmpty { "全部" }) },onClick = {
                                when(tag) { "协议" -> protocol=choice; "地区" -> region=choice; else -> sort=choice }
                                filterMenu=""
                            }) }
                        }
                    }
                }
            }
            TextButton(enabled=!tunnel.active && !busy && visible.isNotEmpty(),onClick={
                if (context.getSharedPreferences("local",0).contains("pinned")) message="请先解除固定出口"
                else {
                    val best=rankedNodes(context,visible).firstOrNull()
                    if (best==null) message="请在可用订阅的管理页启用「参与自动选择」"
                    else { smart=true; chosen=best }
                }
            }) { Text("按当前网络质量选择节点") }
            TextButton(enabled = !tunnel.active && !busy && visible.isNotEmpty(),onClick = { measure() }) { Text("检测筛选结果的 TCP 端口") }
            FilterChip(selected = favoritesOnly, onClick = { favoritesOnly = !favoritesOnly }, label = { Text("仅收藏") })
        }
        items(visible) { row ->
            val type = row.node.getString("type")
            val problem = problems?.get(row.favorite)
            ListItem(headlineContent = { TextButton(onClick = { detail = row }) { Text(row.node.getString("name")) } },
                supportingContent = { Column {
                    Text("${type.uppercase()} · ${row.source.label}")
                    if (problem != null) Text("无法使用：$problem", color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall)
                } },
                trailingContent = { Row {
                    TextButton(onClick = {
                        favorites = if (row.favorite in favorites) favorites - row.favorite else favorites + row.favorite
                        prefs.edit().putStringSet("items",favorites).apply()
                    }) { Text(if (row.favorite in favorites) "已收藏" else "收藏") }
                    TextButton(enabled = !tunnel.active && type in CompatEngine.types && problem == null, onClick = {
                        if (context.getSharedPreferences("local",0).contains("pinned")) message = "请先解除固定出口"
                        else { smart=false; chosen = row }
                    }) { Text(if (type in CompatEngine.types && problem == null) "连接" else "仅导入") }
                } })
        }
    }
    detail?.let { row -> AlertDialog(onDismissRequest = { detail = null },title = { Text(row.node.getString("name")) },
        text = { Text(listOfNotNull(row.source.label,row.type.uppercase(),"地区 · "+row.region,
            metrics[row.favorite]?.let { if (it>=0) "TCP 端口 · $it ms" else "TCP 端口未连通" } ?: "尚无 TCP 端口检测结果",
            problems?.get(row.favorite)?.let { "无法使用：$it" }).joinToString("\n")) },
        confirmButton = { TextButton(onClick = { detail = null }) { Text("关闭") } }) }
    chosen?.let { row -> AlertDialog(onDismissRequest = { chosen = null }, title = { Text("使用订阅节点连接 VPN？") },
        text = { Column { Text(row.source.label + " · " + row.node.getString("name"))
            OutlinedTextField(dns,{ dns = it.take(15) },singleLine = true,label = { Text("经节点访问的 DNS 地址") })
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(smart,{ smart=it });Text("验证网页并尝试备用节点") }
            if (smart) Text("每 20 秒经 VPN 向 example.com 验证网页。连续失败两次会尝试备用节点，出口可能变化。")
        } },
        confirmButton = { TextButton(onClick = {
            val prompt = VpnService.prepare(context)
            if (prompt == null) connect(row) else consent.launch(prompt)
        }) { Text("连接 VPN") } }, dismissButton = { TextButton(onClick = { chosen = null }) { Text("取消") } }) }
    if (adding) AlertDialog(onDismissRequest = { adding = false; input = "" }, title = { Text("添加订阅") },
        text = { Column {
            OutlinedTextField(label, { label = it.take(64) }, singleLine = true, label = { Text("来源名称") })
            OutlinedTextField(input, { input = it.take(524288) }, singleLine = true, visualTransformation = PasswordVisualTransformation(),
                label = { Text("HTTPS 地址或节点链接") })
            TextButton(onClick = { open.launch(arrayOf("*/*")) }) { Text("选择文件或二维码图片（可多选）") }
            TextButton(onClick = { scan.launch(com.journeyapps.barcodescanner.ScanOptions().setDesiredBarcodeFormats("QR_CODE").setBeepEnabled(false).setPrompt("扫描订阅二维码")) }) { Text("用相机扫码") }
        } }, confirmButton = { TextButton(enabled = label.isNotBlank() && input.isNotBlank(), onClick = {
            val data = input; val name = label
            update { store.import(name, data) }; adding = false; input = ""
        }) { Text("导入") } }, dismissButton = { TextButton(onClick = { adding = false; input = "" }) { Text("取消") } })
    editSource?.let { source ->
        var mark by remember(source.id) { mutableStateOf(store.appearance.getString("mark/"+source.id,"").orEmpty()) }
        var color by remember(source.id) { mutableStateOf(store.appearance.getInt("color/"+source.id,0)) }
        var pin by remember(source.id) { mutableStateOf(store.appearance.getBoolean("pin/"+source.id,false)) }
        var auto by remember(source.id) { mutableStateOf(store.appearance.getBoolean("auto/"+source.id,false)) }
        AlertDialog(onDismissRequest = { editSource=null },title = { Text(source.label) },text = { Column {
            OutlinedTextField(mark,{ mark=it.take(8) },singleLine=true,label = { Text("图标或简称") })
            Row { listOf("主色","强调","次色").forEachIndexed { idx, label ->
                FilterChip(selected=color==idx,onClick={color=idx},label={Text(label)}) } }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(pin,{pin=it}); Text("置顶") }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(auto,{auto=it}); Text("参与自动选择") }
            Row { for ((name,step) in listOf("上移" to -1,"下移" to 1)) TextButton(onClick={
                update { store.move(sources,source.id,step) };editSource=null
            }) { Text(name) } }
        } },confirmButton = { TextButton(onClick = {
            val layout=store.appearance
            update { check(layout.edit().putString("mark/"+source.id,mark).putInt("color/"+source.id,color)
                .putBoolean("pin/"+source.id,pin).putBoolean("auto/"+source.id,auto).commit()) }
            editSource=null
        }) { Text("保存") } })
    }
    if (exportConsent) AlertDialog(onDismissRequest = { exportConsent = false; exporting = null },
        title = { Text("导出节点连接凭据？") },text = { Text("所选文件将包含明文节点凭据。请保存到受信任的位置。") },
        confirmButton = { TextButton(onClick = { exportConsent = false; export.launch("nexus-nodes.json") }) { Text("选择保存位置") } },
        dismissButton = { TextButton(onClick = { exportConsent = false; exporting = null }) { Text("取消") } })
    deleting?.let { source -> AlertDialog(onDismissRequest = { deleting = null }, title = { Text("删除“${source.label}”？") },
        text = { Text("将移除这个来源及其节点。") }, confirmButton = { TextButton(onClick = {
            update { store.remove(source.id) }; if (selected == source.id) selected = null; deleting = null
        }) { Text("删除") } }, dismissButton = { TextButton(onClick = { deleting = null }) { Text("取消") } }) }
}
