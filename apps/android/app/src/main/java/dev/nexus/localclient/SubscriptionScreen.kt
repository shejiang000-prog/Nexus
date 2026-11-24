package dev.nexus.localclient

import android.content.Intent
import android.net.VpnService
import android.app.Activity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.animation.*
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.foundation.horizontalScroll
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.foundation.lazy.itemsIndexed
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
import kotlinx.coroutines.Job
import android.net.ConnectivityManager
import android.net.Network
import java.text.DateFormat
import java.util.Date
import org.json.JSONObject

internal data class NodeRow(val source: Subscription, val index: Int, val node: JSONObject) {
    val type get() = node.getString("type")
    val region by lazy {
        val data = node.optJSONObject("config")
        data?.optString("country")?.takeIf { it.isNotBlank() }?.take(32)
            ?: regionFromName(node.getString("name"))
            ?: Regex("[\\x{1F1E6}-\\x{1F1FF}]{2}").find(node.getString("name"))?.value ?: "未提供"
    }
    /** Name without leading flag emoji; the region badge already shows where the node is. */
    val title by lazy { displayName(node.getString("name")) }
    val favorite by lazy { nodeKey(source.id,node) }
}

// Providers often put a misleading flag on Taiwan, Hong Kong and Macau nodes, so the written
// place name wins over the flag emoji.
private val placeCodes = listOf(
    "台湾" to "TW", "臺灣" to "TW", "香港" to "HK", "澳门" to "MO", "日本" to "JP", "东京" to "JP", "大阪" to "JP",
    "韩国" to "KR", "首尔" to "KR", "新加坡" to "SG", "狮城" to "SG", "美国" to "US", "洛杉矶" to "US", "纽约" to "US",
    "硅谷" to "US", "西雅图" to "US", "英国" to "GB", "伦敦" to "GB", "德国" to "DE", "法兰克福" to "DE", "法国" to "FR",
    "荷兰" to "NL", "加拿大" to "CA", "澳大利亚" to "AU", "澳洲" to "AU", "俄罗斯" to "RU", "印度" to "IN", "土耳其" to "TR",
    "马来西亚" to "MY", "泰国" to "TH", "越南" to "VN", "菲律宾" to "PH", "印尼" to "ID", "印度尼西亚" to "ID",
    "巴西" to "BR", "阿根廷" to "AR", "意大利" to "IT", "西班牙" to "ES", "瑞士" to "CH", "瑞典" to "SE", "爱尔兰" to "IE",
    "以色列" to "IL", "阿联酋" to "AE", "迪拜" to "AE", "南非" to "ZA", "墨西哥" to "MX", "新西兰" to "NZ", "回国" to "CN", "中国" to "CN")
internal fun regionFromName(name: String): String? = placeCodes.firstOrNull { name.contains(it.first) }?.second
internal fun displayName(name: String): String =
    name.replace(Regex("^(?:[\\x{1F1E6}-\\x{1F1FF}]{2}|\\x{1F3F4}[\\x{E0061}-\\x{E007F}]+|[\\s|·_-])+"), "").ifBlank { name }

/** Binary units, as panels count them: 1288490188800 bytes is 1200 GB on the provider's page. */
internal fun bytes(n: Long): String = when {
    n >= 100L shl 30 -> String.format(java.util.Locale.ROOT, "%.0f GB", n / 1073741824.0)
    n >= 1L shl 30 -> String.format(java.util.Locale.ROOT, "%.1f GB", n / 1073741824.0)
    else -> String.format(java.util.Locale.ROOT, "%.0f MB", n / 1048576.0)
}

internal fun rankedNodes(context: android.content.Context, rows: List<NodeRow>, problems: Map<String,String>? = null): List<NodeRow> {
    val store = Subscriptions(context)
    val quality = NodeQuality(context, TunnelService.state.value.physical ?: NodeProbe.physical(context.getSystemService(ConnectivityManager::class.java)))
    return rows.filter { it.type in CompatEngine.types && store.appearance.getBoolean("auto/"+it.source.id,true) &&
        it.source.hasAllowance() && quality.lastProbe(it.favorite)?.let { p -> !p.ok && p.recent(3600000) } != true && (if(problems==null) CompatEngine.problem(it.node)==null else it.favorite !in problems) }.sortedWith(compareByDescending<NodeRow> { store.appearance.getBoolean("pin/"+it.source.id,false) }
            .thenByDescending { quality.score(it.favorite) })
}

internal fun fastestNode(context: android.content.Context, rows: List<NodeRow>, problems: Map<String,String>? = null): NodeRow? {
    val eligible=rankedNodes(context,rows,problems)
    val physical=TunnelService.state.value.physical ?: NodeProbe.physical(context.getSystemService(ConnectivityManager::class.java))
    val quality=NodeQuality(context,physical)
    return eligible.mapNotNull { row -> quality.lastProbe(row.favorite)?.takeIf { it.ok && it.recent(86400000) }?.let { row to it.ms } }
        .minByOrNull { it.second }?.first ?: eligible.firstOrNull()
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

internal fun sourceRows(sources: List<Subscription>) = sources.flatMap { source ->
    (0 until source.nodes.length()).map { NodeRow(source,it,source.nodes.getJSONObject(it)) }
}

internal fun nodeProblems(rows: List<NodeRow>) = rows.mapNotNull { row ->
    CompatEngine.problem(row.node)?.let { row.favorite to it }
}.toMap()

@Composable
internal fun rememberDefaultPhysical(connectivity: ConnectivityManager): Network? {
    var physical by remember { mutableStateOf(NodeProbe.physical(connectivity)) }
    DisposableEffect(connectivity) {
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, caps: android.net.NetworkCapabilities) { physical = NodeProbe.physical(connectivity) }
            override fun onLost(network: Network) { physical = NodeProbe.physical(connectivity) }
        }
        connectivity.registerDefaultNetworkCallback(callback)
        onDispose { connectivity.unregisterNetworkCallback(callback) }
    }
    return physical
}

@Composable
internal fun SubscriptionScreen(bypass: Boolean, automatic:Boolean=false, onAutomatic:(Boolean,List<NodeRow>)->Unit={_,_->}, addInitially: Boolean = false, manageSource: String? = null, onManageOpened: () -> Unit = {}, onInvite: () -> Unit, onV2Link:(String)->Boolean={false}, onSelected: (NodeRow) -> Unit = {}, onImported: () -> Unit = {}, onPick: (NodeRow,List<NodeRow>) -> Unit = { _,_ -> }, home: @Composable (List<NodeRow>) -> Unit) {
    val context = LocalContext.current
    val store = remember { Subscriptions(context) }
    val scope = rememberCoroutineScope()
    val scroll = androidx.compose.foundation.lazy.rememberLazyListState()
    var sources by remember { mutableStateOf(emptyList<Subscription>()) }
    var showSources by remember { mutableStateOf(manageSource!=null) }
    var tools by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf<String?>(null) }
    var search by remember { mutableStateOf("") }
    var menu by remember { mutableStateOf(false) }
    var adding by remember { mutableStateOf(addInitially) }
    var manualInput by remember(adding) { mutableStateOf(false) }
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
    var probeJob by remember { mutableStateOf<Job?>(null) }
    var tested by remember { mutableIntStateOf(0) }
    // Nodes in the running batch that have not reported yet; their rows show "测试中".
    var probing by remember { mutableStateOf(emptySet<String>()) }
    var measuring by remember { mutableStateOf(false) }
    val local = remember { context.getSharedPreferences("local",0) }
    var total by remember { mutableIntStateOf(0) }
    var chosen by remember { mutableStateOf<NodeRow?>(null) }
    var dns by remember { mutableStateOf("1.1.1.1") }
    var smart by remember { mutableStateOf(false) }
    val allRows = remember(sources) { sourceRows(sources) }
    // Reasons a node cannot be used, keyed by NodeRow.favorite. Conversion runs off the main thread.
    var problems by remember(allRows) { mutableStateOf<Map<String,String>?>(null) }
    LaunchedEffect(allRows) {
        problems = withContext(Dispatchers.Default) {
            nodeProblems(allRows)
        }
    }
    val tunnel by TunnelService.state.collectAsState()
    val connectivity = context.getSystemService(android.net.ConnectivityManager::class.java)
    val physical=rememberDefaultPhysical(connectivity)
    val quality = remember(tunnel.physical,physical) { NodeQuality(context, tunnel.physical ?: physical) }
    // Stored results load once per list and network, off the main thread; a running batch adds only
    // the node that just finished, so large subscriptions do not reread every node per result.
    val stored by produceState(emptyMap<String,ProbeResult>(),allRows,quality) {
        value = withContext(Dispatchers.IO) { allRows.mapNotNull { row ->
            quality.lastProbe(row.favorite)?.let { row.favorite to it }
        }.toMap() }
    }
    var live by remember(quality) { mutableStateOf(emptyMap<String,ProbeResult>()) }
    val metrics = remember(stored,live) { if (live.isEmpty()) stored else stored + live }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) { while (true) { now = System.currentTimeMillis(); kotlinx.coroutines.delay(60000) } }
    var speedResult by remember(tunnel.connectedAt) { mutableStateOf("") }
    fun connect(row: NodeRow) {
        prefs.edit().putLong("recent/"+row.favorite,System.currentTimeMillis()).apply()
        val request = subscriptionRequest(context,row,allRows,dns,smart)
        onSelected(row)
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
    val revision by SubscriptionRefresh.revision.collectAsState()
    LaunchedEffect(revision) { sources = withContext(Dispatchers.IO) { store.all() } }
    LaunchedEffect(manageSource,sources) {
        sources.firstOrNull { it.id==manageSource }?.let { editSource=it;onManageOpened() }
    }
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
    fun importAndTest(value: String) {
        adding=false
        if (onV2Link(value.trim())) {input=""; return}
        val name=label.trim().ifEmpty { "机场订阅" }
        update {
            val source=store.import(name,value)
            context.getSharedPreferences("local",0).edit().putString("simple-node","auto").apply()
            SubscriptionRefresh.testImported(context,source.id)
            android.os.Handler(android.os.Looper.getMainLooper()).post(onImported)
        }
        input=""
    }
    val scan = rememberLauncherForActivityResult(com.journeyapps.barcodescanner.ScanContract()) {
        it.contents?.let { value ->
            if (onV2Link(value)) adding=false else importAndTest(value)
        }
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
            "真连延迟" -> rows.sortedBy { metrics[it.favorite]?.takeIf { r -> r.ok }?.ms ?: Long.MAX_VALUE }
            "最近选择" -> rows.sortedByDescending { prefs.getLong("recent/"+it.favorite,0) }
            "收藏优先" -> rows.sortedByDescending { it.favorite in favorites }
            "网络质量" -> rows.sortedByDescending { quality.score(it.favorite) }
            else -> rows
        }
    }
    val eligible = remember(visible,problems) { problems?.let { map -> visible.filter { it.favorite !in map } }.orEmpty() }
    fun measure(batch: List<NodeRow> = eligible) {
        if (busy || tunnel.active || physical == null || batch.isEmpty()) return
        busy = true
        tested = 0
        total = batch.size
        errorMessage = ""
        probing = batch.mapTo(HashSet()) { it.favorite }
        probeJob = scope.launch {
            try {
                NodeProbe.run(context,batch) { row,value ->
                    scope.launch(Dispatchers.Main.immediate) { tested++; live = live + (row.favorite to value); probing = probing - row.favorite }
                }
                message = "已测 $tested / $total"
            } catch (e: kotlinx.coroutines.CancellationException) { message = "测试已停止"; throw e }
            catch (_: Exception) { errorMessage = "测试引擎未就绪，请检查网络后重试" }
            finally { busy = false; probeJob = null; probing = emptySet() }
        }
    }
    val theme = MaterialTheme.colorScheme
    val motion = LocalMotion.current
    // One read per composition instead of one per row.
    val chosenNode = local.getString("simple-node",null)
    LazyColumn(Modifier.fillMaxSize().padding(horizontal = Space.page),
        state = scroll, contentPadding = PaddingValues(top = Space.xs, bottom = Space.lg)) {
        item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text(if(sources.isEmpty()) "暂无订阅" else "${allRows.size} 个节点 · ${sources.size} 个订阅",Modifier.weight(1f),
                    color=theme.onSurfaceVariant,style=MaterialTheme.typography.bodyMedium)
                CompactAction(enabled = !busy, onClick = { input = ""; label = ""; adding = true }) { Text("添加节点") }
            }
            // A running batch shows how far it has got; other work shows an indeterminate bar.
            AnimatedVisibility(busy,enter=fadeIn(motionTween(Motion.standard))+expandVertically(motionTween(Motion.standard)),
                exit=fadeOut(motionTween(Motion.standard))+shrinkVertically(motionTween(Motion.standard))) {
                if (probeJob != null && total > 0) {
                    val fraction by animateFloatAsState(tested.toFloat()/total,motionTween(Motion.standard),label="测试进度")
                    LinearProgressIndicator({ fraction },Modifier.fillMaxWidth().height(6.dp),color=signal,trackColor=theme.surfaceVariant,
                        strokeCap=StrokeCap.Round,gapSize=0.dp,drawStopIndicator={})
                } else LinearProgressIndicator(Modifier.fillMaxWidth().height(6.dp),color=signal,trackColor=theme.surfaceVariant,
                    strokeCap=StrokeCap.Round,gapSize=0.dp)
            }
            if (message.isNotEmpty()) Text(message, color = theme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium)
            if (errorMessage.isNotEmpty()) Text(errorMessage, color = theme.error, style = MaterialTheme.typography.bodyMedium)
            val current = tunnel
            current.node?.let { node ->
                SecondaryAction(enabled = !busy, onClick = {
                    busy = true
                    measuring = true
                    scope.launch {
                        try {
                            speedResult = withContext(Dispatchers.IO) {
                                val vpn = requireNotNull(connectivity.activeNetwork)
                                require(connectivity.getNetworkCapabilities(vpn)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true)
                                quality.measure(vpn,node) { TunnelService.state.value.connectedAt == current.connectedAt }
                            }
                        } catch (_: Exception) { speedResult = "测速中断，请检查连接后重试" }
                        finally { busy = false; measuring = false }
                    }
                }) {
                    if (measuring) { CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp, color = theme.onSurfaceVariant); Spacer(Modifier.width(Space.sm)) }
                    Text(if (measuring) "正在测量…" else "测量当前连接速度")
                }
                AnimatedContent(speedResult,transitionSpec={ fadeThrough(motion) },label="测速结果") { text ->
                    if (text.isNotEmpty()) Text(text,style=numberStyle.copy(fontSize=15.sp)) else Spacer(Modifier.fillMaxWidth())
                }
            }
            if (sources.isEmpty() && !busy) {
                Spacer(Modifier.height(Space.md))
                Text("添加机场订阅后，会自动测试并选择最快节点。",color=theme.onSurfaceVariant)
            }
        }}
        if(sources.isNotEmpty()) item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
            Segment(listOf("节点列表","订阅管理"),if(showSources)1 else 0){showSources=it==1}
        }}
        if(!showSources && allRows.isNotEmpty())item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
            AutoSelection(automatic,enabled=true,onChange={value->onAutomatic(value,allRows)})
        }}
        if(!showSources)item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) { home(allRows) } }
        if (showSources && sources.isNotEmpty()) item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
            SubscriptionOverview(sources, allRows, problems, store, metrics, maxOf(now, metrics.values.maxOfOrNull { it.at } ?: 0L),onSource={editSource=it})
        }}
        if(showSources) items(sources, key = { it.id }) { source ->
            var folded by remember { mutableStateOf(store.appearance.getBoolean("fold/"+source.id,false)) }
            val color = store.appearance.getInt("color/"+source.id,0)
            val palette = listOf(theme.primary,theme.tertiary,theme.secondary)
            NexusCard(Modifier.padding(bottom = Space.sm).animateItem()) {
                Column(Modifier.fillMaxWidth().padding(start = Space.lg, end = Space.sm, top = 6.dp, bottom = Space.md), verticalArrangement = Arrangement.spacedBy(Space.xs)) {
                    Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                        Text(listOf(store.appearance.getString("mark/"+source.id,"").orEmpty(),source.label)
                            .filter(String::isNotEmpty).joinToString(" "), color = palette[color.coerceIn(0,2)],
                            style = MaterialTheme.typography.titleMedium,
                            modifier = Modifier.weight(1f).pointerInput(source.id,sources) {
                                var distance = 0f
                                detectDragGesturesAfterLongPress(onDragStart = { distance=0f },onDragEnd = {
                                    if (!busy && kotlin.math.abs(distance)>32.dp.toPx()) update { store.move(sources,source.id,if(distance>0) 1 else -1) }
                                }) { change,delta -> change.consume(); distance+=delta.y }
                            })
                        val turn by animateFloatAsState(if (folded) 0f else 90f,motionSpring(),label="展开箭头")
                        TextButton(onClick = { folded=!folded; store.appearance.edit().putBoolean("fold/"+source.id,folded).apply() }) {
                            Text(if (folded) "展开" else "收起", color = theme.onSurfaceVariant)
                            GlyphIcon(Glyph.Chevron,Modifier.padding(start=Space.xxs).graphicsLayer{ rotationZ=turn },size=14.dp,tint=theme.onSurfaceVariant)
                        }
                        TextButton(onClick = { editSource=source }) { Text("管理", color = theme.primary) }
                    }
                    AnimatedVisibility(!folded,
                        enter=fadeIn(motionTween(Motion.standard))+expandVertically(motionTween(Motion.standard)),
                        exit=fadeOut(motionTween(Motion.quick,Motion.leave))+shrinkVertically(motionTween(Motion.standard,Motion.slide))) {
                        Column(verticalArrangement = Arrangement.spacedBy(Space.xs)) {
                            val blocked = problems?.let { map -> (0 until source.nodes.length()).count { nodeKey(source.id,source.nodes.getJSONObject(it)) in map } } ?: 0
                            Text("${source.nodes.length()} 个节点" + (if (blocked > 0) "（$blocked 个暂不支持）" else "") +
                                " · 更新于 ${DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT).format(Date(source.updated))}",
                                color = theme.onSurfaceVariant, style = MaterialTheme.typography.detail)
                            Row(Modifier.padding(top = 6.dp, end = Space.sm), horizontalArrangement = Arrangement.spacedBy(Space.sm)) {
                                if (source.url.isNotEmpty()) SecondaryAction(modifier = Modifier.weight(1f), enabled = !busy && synchronized(SubscriptionRefresh) { source.id !in SubscriptionRefresh.protectedSources },
                                    onClick = { update { SubscriptionRefresh.refresh(store, source, force = true) } }) { Text("更新") }
                                SecondaryAction(modifier = Modifier.weight(1f), enabled = !busy, onClick = { exporting = source; exportConsent = true }) { Text("导出") }
                                SecondaryAction(modifier = Modifier.weight(1f), enabled = !busy, onClick = { deleting = source }) { Text("删除", color = theme.error) }
                            }
                        }
                    }
                }
            }
        }
        if(!showSources && sources.isNotEmpty()) item { Column(Modifier.fillMaxWidth().padding(bottom = Space.sm), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
            SecondaryAction(onClick={tools=!tools}) {
                Text(if(tools) "收起筛选与测速" else "筛选与测速")
                val turn by animateFloatAsState(if (tools) -90f else 90f,motionSpring(),label="筛选箭头")
                GlyphIcon(Glyph.Chevron,Modifier.padding(start=6.dp).graphicsLayer{ rotationZ=turn },size=14.dp)
            }
        }}
        if(!showSources && sources.isNotEmpty()) item { AnimatedVisibility(tools,
            enter=fadeIn(motionTween(Motion.standard))+expandVertically(motionTween(Motion.emphasized),expandFrom=androidx.compose.ui.Alignment.Top),
            exit=fadeOut(motionTween(Motion.quick,Motion.leave))+shrinkVertically(motionTween(Motion.standard,Motion.slide),shrinkTowards=androidx.compose.ui.Alignment.Top)) {
          Column(Modifier.fillMaxWidth().padding(bottom = Space.sm)) {
            NexusCard {
                Column(Modifier.fillMaxWidth().padding(Space.md), verticalArrangement = Arrangement.spacedBy(Space.sm)) {
                    OutlinedTextField(search, { search = it.take(96) }, Modifier.fillMaxWidth(), label = { Text("搜索名称或协议") }, singleLine = true,
                        shape = Shapes.control,
                        colors = OutlinedTextFieldDefaults.colors(unfocusedBorderColor = theme.outlineVariant, unfocusedContainerColor = theme.background, focusedContainerColor = theme.background))
                    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(Space.sm)) {
                        val chipColors = FilterChipDefaults.filterChipColors(selectedContainerColor = signalSoft, selectedLabelColor = theme.primary)
                        Box {
                            FilterChip(selected = selected != null, onClick = { menu = true }, label = { Text(sources.find { it.id == selected }?.label ?: "全部来源") },
                                shape = Shapes.chip, colors = chipColors)
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }, shape = Shapes.control) {
                                DropdownMenuItem(text = { Text("全部来源") }, onClick = { selected = null; menu = false })
                                sources.forEach { s -> DropdownMenuItem(text = { Text(s.label) }, onClick = { selected = s.id; menu = false }) }
                            }
                        }
                        listOf("协议","地区","排序").forEach { tag ->
                            Box {
                                val value = when(tag) { "协议" -> protocol; "地区" -> region; else -> sort.takeUnless { it == "默认" }.orEmpty() }
                                FilterChip(selected = value.isNotEmpty(), onClick = { filterMenu = tag },
                                    label = { Text(if (value.isEmpty()) tag else if (tag == "排序") value else "$tag · $value") }, shape = Shapes.chip, colors = chipColors)
                                DropdownMenu(expanded = filterMenu==tag,onDismissRequest = { filterMenu="" }, shape = Shapes.control) {
                                    val choices = when(tag) { "协议" -> listOf("") + allRows.map { it.type }.distinct().sorted()
                                        "地区" -> listOf("") + allRows.map { it.region }.distinct().sorted()
                                        else -> listOf("默认","真连延迟","网络质量","最近选择","收藏优先") }
                                    choices.forEach { choice -> DropdownMenuItem(text = { Text(choice.ifEmpty { "全部" }) },onClick = {
                                        when(tag) { "协议" -> protocol=choice; "地区" -> region=choice; else -> sort=choice }
                                        filterMenu=""
                                    }) }
                                }
                            }
                        }
                        FilterChip(selected = favoritesOnly, onClick = { favoritesOnly = !favoritesOnly }, label = { Text("仅收藏") }, shape = Shapes.chip, colors = chipColors)
                    }
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(Space.sm)) {
                        SecondaryAction(enabled=!tunnel.active && !busy && visible.isNotEmpty(),modifier=Modifier.weight(1f),onClick={
                                val best=rankedNodes(context,visible).firstOrNull()
                                if (best==null) message="请在可用订阅的管理页启用「参与自动选择」"
                                else { smart=true; chosen=best }
                        }) { Text("选质量最好的", maxLines = 1) }
                        if (probeJob != null) SecondaryAction(modifier=Modifier.weight(1f),onClick = { probeJob?.cancel() }) { Text("取消测试", maxLines = 1) }
                        else PrimaryAction(enabled = !tunnel.active && physical != null && !busy && eligible.isNotEmpty(), modifier=Modifier.weight(1f),
                            onClick = { measure() }) { Text("测试真实连通", maxLines = 1) }
                    }
                    if (probeJob != null) Text("已测 $tested / $total", color = theme.onSurfaceVariant, style = numberStyle.copy(fontSize = 14.sp))
                    else Text("测试筛选结果的真实连通：逐个经节点访问网页，结果用于自动选择。", color = theme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall)
                    if (tunnel.vpn || connectivity.getNetworkCapabilities(connectivity.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true)
                        Text("请先断开 VPN", color = signalAmber, style = MaterialTheme.typography.detail)
                }
            }
          }
        }}
        if(!showSources) itemsIndexed(visible,key={_,row->row.favorite}) { index,row ->
            val type=row.type
            val problem=problems?.get(row.favorite)
            val metric=metrics[row.favorite]
            val usable=type in CompatEngine.types && problems!=null && problem==null
            val info=listOfNotNull(metric?.takeUnless { it.ok }?.label,problem?.let { if(it=="信息") it else "无法使用：$it" },
                SplitRouting.SHOES.takeIf {bypass && type in ShoesEngine.types && usable}).joinToString(" · ")
            val grouped=sort=="默认"
            val starts=grouped && (index==0 || visible[index-1].source.id!=row.source.id)
            val ends=grouped && (index==visible.lastIndex || visible[index+1].source.id!=row.source.id)
            // Sorting, filtering and refreshes move rows to their new place instead of jumping.
            Box(Modifier.animateItem(fadeInSpec=motionTween(Motion.standard),placementSpec=motionSpring(Spring.StiffnessMediumLow,1f),
                fadeOutSpec=motionTween(Motion.quick))) { ListEntrance(index) { Column {
                if(starts) Row(Modifier.fillMaxWidth().padding(start=Space.xs,end=Space.xs,top=if(index==0) 0.dp else Space.md,bottom=6.dp),
                    verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) {
                    Text(row.source.label,Modifier.weight(1f),style=MaterialTheme.typography.titleMedium)
                    Text("${visible.count { it.source.id==row.source.id }} 个",color=theme.onSurfaceVariant,style=MaterialTheme.typography.bodyMedium)
                }
                SignalNode(row.region,row.title,if(grouped) type.uppercase() else type.uppercase()+" · "+row.source.label,
                    metric?.takeIf {it.ok}?.ms,chosenNode==row.favorite,info,{detail=row},
                    if(!busy && usable) ({
                        onPick(row,allRows)
                    }) else null,
                    groupPlace(index,visible.lastIndex,starts,ends),testing=row.favorite in probing)
            } } }
        }
    }
    detail?.let { row -> AlertDialog(onDismissRequest = { detail = null },
        title = { Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            RegionBadge(row.region)
            Column { Text(row.title, style = MaterialTheme.typography.titleLarge, fontWeight = androidx.compose.ui.text.font.FontWeight.Bold)
                Text(row.type.uppercase() + " · " + row.source.label, color = MaterialTheme.colorScheme.onSurfaceVariant, fontSize = 14.sp) }
        } },
        text = { Column(Modifier.verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(10.dp)) {
            val theme = MaterialTheme.colorScheme
            val metric = metrics[row.favorite]
            @Composable fun fact(label: String, value: String, color: androidx.compose.ui.graphics.Color = theme.onSurface) =
                Row { Text(label, Modifier.width(76.dp), color = theme.onSurfaceVariant, fontSize = 14.sp); Text(value, color = color, fontSize = 15.sp) }
            fact("地区", row.region)
            fact("真连测试", metric?.label ?: "尚未测试", if (metric == null) theme.onSurfaceVariant else if (metric.ok) theme.primary else signalAmber)
            if (metric != null) {
                fact("测试时间", DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT).format(Date(metric.at)))
                if (metric.detail.isNotBlank()) Text(metric.detail, color = theme.onSurfaceVariant, fontSize = 13.sp, lineHeight = 18.sp)
            }
            problems?.get(row.favorite)?.let { Text("无法使用：$it", color = theme.error, fontSize = 14.sp) }
            TextButton({
                favorites=if(row.favorite in favorites) favorites-row.favorite else favorites+row.favorite
                prefs.edit().putStringSet("items",favorites).apply()
            }, contentPadding = PaddingValues(0.dp)) { Text(if(row.favorite in favorites) "★ 已收藏（点按取消）" else "☆ 收藏节点", color = theme.primary) }
        } },
        confirmButton = {
            if(problems!=null && problems?.get(row.favorite)==null) PrimaryAction({
                detail=null
                onPick(row,allRows)
            }) { Text("设为当前节点") }
        },dismissButton={SecondaryAction({detail=null}) {Text("关闭")}}) }
    chosen?.let { row -> AlertDialog(onDismissRequest = { chosen = null }, title = { Text("使用订阅节点连接 VPN？") },
        text = { Column { Text(row.source.label + " · " + row.node.getString("name"))
            OutlinedTextField(dns,{ dns = it.take(15) },singleLine = true,label = { Text("经节点访问的 DNS 地址") })
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(smart,{ smart=it });Text("验证网页并尝试备用节点") }
            if (smart) Text("每 20 秒经 VPN 向 example.com 验证网页。连续失败两次会尝试备用节点，出口可能变化。")
        } },
        confirmButton = { PrimaryAction(onClick = {
            val prompt = VpnService.prepare(context)
            if (prompt == null) connect(row) else consent.launch(prompt)
        }) { Text("连接 VPN") } }, dismissButton = { SecondaryAction(onClick = { chosen = null }) { Text("取消") } }) }
    if (adding) AlertDialog(onDismissRequest = { adding = false; input = "" }, title = { Text("添加节点") },
        text = { Column(Modifier.verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(8.dp)) {
            PrimaryAction(onClick={
                val clipboard=context.getSystemService(android.content.ClipboardManager::class.java)
                val clip=clipboard.primaryClip?.takeIf { it.itemCount==1 }?.getItemAt(0)?.text?.toString()?.trim()
                if(clip!=null && onV2Link(clip)) { adding=false;input="" }
                else if(clip!=null && clip.startsWith("https://") && clip.length<=8192) importAndTest(clip)
                else errorMessage="请复制订阅链接，或选择手动输入"
            },modifier=Modifier.fillMaxWidth()) { Text("粘贴并添加") }
            if(errorMessage.isNotBlank()) Text(errorMessage,color=MaterialTheme.colorScheme.error)
            SecondaryAction(onClick = { scan.launch(com.journeyapps.barcodescanner.ScanOptions().setDesiredBarcodeFormats("QR_CODE").setOrientationLocked(false).setBeepEnabled(false).setPrompt("扫描节点或邀请二维码")) },modifier=Modifier.fillMaxWidth()) { Text("用相机扫码") }
            SecondaryAction(onClick = { open.launch(arrayOf("*/*")) },modifier=Modifier.fillMaxWidth()) { Text("从文件或图片导入") }
            HorizontalDivider()
            Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.SpaceBetween) {
                TextButton({manualInput=!manualInput}) { Text("手动输入") }
                TextButton({adding=false;onInvite()}) { Text("导入设备邀请") }
            }
            androidx.compose.animation.AnimatedVisibility(manualInput) {
                Column(verticalArrangement=Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(input, { input = it.take(524288) }, Modifier.fillMaxWidth(),singleLine=true,
                        visualTransformation=PasswordVisualTransformation(),label={Text("粘贴订阅链接")})
                    OutlinedTextField(label, { label = it.take(64) }, Modifier.fillMaxWidth(),singleLine=true,label={Text("来源名称（可选）")})
                }
            }
        } }, confirmButton = { if(manualInput) PrimaryAction(enabled=input.isNotBlank(),onClick={importAndTest(input)}) { Text("导入") } },
        dismissButton = { SecondaryAction(onClick = { adding=false;input="" }) { Text("关闭") } })
    editSource?.let { source ->
        var interval by remember(source.id) { mutableIntStateOf(store.interval(source)) }
        var mark by remember(source.id) { mutableStateOf(store.appearance.getString("mark/"+source.id,"").orEmpty()) }
        var color by remember(source.id) { mutableStateOf(store.appearance.getInt("color/"+source.id,0)) }
        var pin by remember(source.id) { mutableStateOf(store.appearance.getBoolean("pin/"+source.id,false)) }
        var auto by remember(source.id) { mutableStateOf(store.appearance.getBoolean("auto/"+source.id,true)) }
        AlertDialog(onDismissRequest = { editSource=null },title = { Text(source.label) },text = { Column {
            OutlinedTextField(mark,{ mark=it.take(8) },singleLine=true,label = { Text("图标或简称") })
            Row { listOf("主色","强调","次色").forEachIndexed { idx, label ->
                FilterChip(selected=color==idx,onClick={color=idx},label={Text(label)}) } }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(pin,{pin=it}); Text("置顶") }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) { Checkbox(auto,{auto=it}); Text("参与自动选择") }
            if (source.url.isEmpty()) Text("本地来源无法自动更新") else {
                var expanded by remember { mutableStateOf(false) }
                Box {
                    fun caption(hours: Int) = if (hours == 0) "关闭" else "$hours 小时"
                    SecondaryAction(onClick = { expanded=true }) { Text("自动更新 · "+caption(interval)) }
                    DropdownMenu(expanded, { expanded=false }) {
                        listOf(0,6,12,24).forEach { hours -> DropdownMenuItem(text={ Text(caption(hours)) },
                            onClick={ interval=hours; expanded=false }) }
                    }
                }
            }
            Row { for ((name,step) in listOf("上移" to -1,"下移" to 1)) SecondaryAction(onClick={
                update { store.move(sources,source.id,step) };editSource=null
            }) { Text(name) } }
        } },confirmButton = { PrimaryAction(onClick = {
            val layout=store.appearance
            update { check(layout.edit().putString("mark/"+source.id,mark).putInt("color/"+source.id,color)
                .putInt("refresh/"+source.id,interval).putBoolean("pin/"+source.id,pin).putBoolean("auto/"+source.id,auto).commit()) }
            editSource=null
        }) { Text("保存") } })
    }
    if (exportConsent) AlertDialog(onDismissRequest = { exportConsent = false; exporting = null },
        title = { Text("导出节点连接凭据？") },text = { Text("所选文件将包含明文节点凭据。请保存到受信任的位置。") },
        confirmButton = { PrimaryAction(onClick = { exportConsent = false; export.launch("nexus-nodes.json") }) { Text("选择保存位置") } },
        dismissButton = { SecondaryAction(onClick = { exportConsent = false; exporting = null }) { Text("取消") } })
    deleting?.let { source -> AlertDialog(onDismissRequest = { deleting = null }, title = { Text("删除“${source.label}”？") },
        text = { Text("将移除这个来源及其节点。") }, confirmButton = { PrimaryAction(onClick = {
            update { store.remove(source.id) }; if (selected == source.id) selected = null; deleting = null
        }) { Text("删除") } }, dismissButton = { SecondaryAction(onClick = { deleting = null }) { Text("取消") } }) }
}
