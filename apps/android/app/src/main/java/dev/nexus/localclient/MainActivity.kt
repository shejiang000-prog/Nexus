package dev.nexus.localclient

import android.content.Intent
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    private var v2Invite by mutableStateOf<V2Invite?>(null)
    private var inviteError by mutableStateOf<String?>(null)

    private fun acceptV2Link(text:String):Boolean {
        val link=text.trim().removePrefix(INVITE_MESSAGE+"\n")
        if (!link.startsWith("nexus://invite/v2#")) return false
        val parsed=runCatching { V2Invite.parse(link) }
        v2Invite=parsed.getOrNull()
        inviteError=parsed.exceptionOrNull()?.let {
            "请使用邀请人发来的完整 V2 链接或二维码。"
        }
        return true
    }

    private var foundSource by mutableStateOf<String?>(null)
    private var foundName by mutableStateOf("")
    private var checkedClip = -1L
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || v2Invite != null || foundSource != null) return
        val clipboard=getSystemService(android.content.ClipboardManager::class.java)
        runCatching {
            val desc=clipboard.primaryClipDescription ?: return
            if (desc.timestamp==checkedClip || desc.label=="Nexus 发出的邀请" || !desc.hasMimeType("text/plain")) return
            checkedClip=desc.timestamp
            // Reading the clip shows Android's paste notice; only look at text copied in the last 10 minutes.
            if (System.currentTimeMillis()-desc.timestamp !in 0..600_000L) return
            val item=clipboard.primaryClip?.takeIf { it.itemCount==1 }?.getItemAt(0)?.text ?: return
            if(item.length<=6000 && acceptV2Link(item.toString().trim())) return
            subscriptionLink(item.toString())?.takeIf { link -> Subscriptions(this).all().none { it.url==link } }?.let { foundName=""; foundSource=it }
        }
    }
    private fun readInvitation(value: Intent?) {
        if (value?.action != Intent.ACTION_VIEW) return
        val url = value.data ?: return
        if (acceptV2Link(url.toString())) { value.data=null; return }
        importLink(url.toString())?.let { (link, name) ->
            value.data=null
            if (Subscriptions(this).all().any { it.url==link }) android.widget.Toast.makeText(this,"这个订阅已经添加过了",android.widget.Toast.LENGTH_SHORT).show()
            else { foundName=name; foundSource=link }
        }
    }
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        readInvitation(intent)
    }
    override fun onStart() {
        super.onStart()
        V2Session.foregroundEvents.incrementAndGet()
    }

    override fun onResume() {
        super.onResume()
        SubscriptionRefresh.schedule(this)
        SubscriptionRefresh.testStale(this)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState==null) readInvitation(intent)
        // The UI is always light, so the bars always use dark icons, whatever the system theme.
        val bars = androidx.activity.SystemBarStyle.light(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT)
        enableEdgeToEdge(bars, bars)
        setContent { Home() }
    }

    @Composable private fun Home() {
        val scope = rememberCoroutineScope()
        val tunnel by TunnelService.state.collectAsState()
        var bypass by remember { mutableStateOf(SplitRouting.enabled(this)) }
        fun changeMode(value: Boolean) {
            if (value == bypass) return
            getSharedPreferences("local",0).edit().putBoolean(SplitRouting.KEY,value).apply()
            bypass = value
            if (tunnel.vpn) startService(Intent(this, TunnelService::class.java).setAction("split-mode"))
        }
        var page by rememberSaveable { mutableStateOf("连接") }
        var addSubscription by remember { mutableStateOf(false) }
        var manageSource by rememberSaveable { mutableStateOf<String?>(null) }
        var message by remember { mutableStateOf("") }
        val prefs=remember { getSharedPreferences("local",0) }
        var choice by remember { mutableStateOf(prefs.getString("simple-node",null)) }
        fun choose(key:String) { choice=key; prefs.edit().putString("simple-node",key).apply() }
        val store=remember { Subscriptions(this) }
        var catalog by remember { mutableStateOf<HomeCatalog?>(null) }
        var v2Nodes by remember { mutableStateOf(emptyList<V2Node>()) }
        var deviceKey by rememberSaveable { mutableStateOf<String?>(null) }
        val selectedV2=v2Nodes.firstOrNull { it.key==choice }
        val activeV2=v2Nodes.firstOrNull { it.key==tunnel.node }
        val rows=catalog?.rows.orEmpty()
        val best=catalog?.best
        val loaded=catalog!=null
        val defaultNetwork=rememberDefaultPhysical(getSystemService(android.net.ConnectivityManager::class.java))
        val physical=tunnel.physical ?: defaultNetwork
        val revision by SubscriptionRefresh.revision.collectAsState()
        LaunchedEffect(page,revision,tunnel.active,physical) {
            if(page=="连接") {
                choice=prefs.getString("simple-node",null)
                catalog=withContext(Dispatchers.IO) {
                    val sources=store.all()
                    val all=sourceRows(sources)
                    val problems=nodeProblems(all)
                    val quality=NodeQuality(this@MainActivity,physical)
                    val results=all.mapNotNull { row -> quality.lastProbe(row.favorite)?.let { row.favorite to it } }.toMap()
                    HomeCatalog(sources,all,problems,results,fastestNode(this@MainActivity,all,problems))
                }
            }
        }
        val manual=rows.firstOrNull { it.favorite==choice }
        val selected=manual ?: best
        val deviceRevision by V2Devices.revision.collectAsState()
        LaunchedEffect(deviceRevision) {
            v2Nodes=withContext(Dispatchers.IO) { V2Devices.list(this@MainActivity) }
        }
        var pending by rememberSaveable { mutableStateOf<Intent?>(null) }
        val consent=rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            val request=pending;pending=null
            if(result.resultCode==RESULT_OK && request!=null) startForegroundService(request) else message="未获得 VPN 授权，可以再次点连接"
        }
        fun start(request: Intent) {
            val prompt=VpnService.prepare(this)
            if(prompt==null) startForegroundService(request) else { pending=request;consent.launch(prompt) }
        }
        fun switchConnection(request: Intent) {
            if(!tunnel.active) { start(request);return }
            startService(Intent(this,TunnelService::class.java).setAction("stop"))
            scope.launch {
                val stopped=kotlinx.coroutines.withTimeoutOrNull(8000) { TunnelService.state.first { !it.active } }
                if(stopped!=null) start(request) else message="请断开当前连接后重试"
            }
        }

        val goBack: ()->Unit = { page=when(page) { "设备信息"->"节点"; "服务器高级"->"我的服务器";"诊断","关于"->"设置";else->"连接" };manageSource=null;message="" }
        androidx.activity.compose.BackHandler(page!="连接",onBack=goBack)
        NexusTheme {
            ReleaseStartup()
            val colors=MaterialTheme.colorScheme
            SimpleShell(page,{ target -> page=target;addSubscription=false;manageSource=null;message="" },goBack) { displayed ->
                when(displayed) {
                    "设备信息" -> v2Nodes.firstOrNull { it.key==deviceKey }?.let { node ->
                        V2DeviceScreen(node,tunnel,select={
                            choose(node.key);page="连接"; if(tunnel.active) switchConnection(v2Request(this,node.key))
                        },remove={
                            scope.launch {
                                runCatching { withContext(Dispatchers.IO) { V2Devices.remove(this@MainActivity,node.key);V2Devices.list(this@MainActivity) } }
                                    .onSuccess { v2Nodes=it;if(choice==node.key) choose("auto");page="节点" }
                                    .onFailure { message="移除失败，请重试" }
                            }
                        })
                        if(message.isNotEmpty()) Text(message,color=colors.error)
                    }
                    "节点" -> SubscriptionScreen(bypass,automatic=choice=="auto" || choice==null,onAutomatic={ enabled,all ->
                        scope.launch {
                            val next=withContext(Dispatchers.IO){fastestNode(this@MainActivity,all)}
                            if(enabled)choose("auto") else if(next!=null)choose(next.favorite) else message="请点选一个节点以关闭自动选择"
                            if(tunnel.active && next!=null)switchConnection(subscriptionRequest(this@MainActivity,next,all,"1.1.1.1",enabled))
                        }
                    },addInitially=addSubscription,manageSource=manageSource,onManageOpened={manageSource=null},onInvite={page="领取"},onV2Link=::acceptV2Link,onSelected={choose(it.favorite);page="连接"},onImported={choose("auto")},onPick={row,all ->
                        choose(row.favorite);page="连接"
                        if(tunnel.active) switchConnection(subscriptionRequest(this,row,all,"1.1.1.1",false))
                    }) { _ ->
                        Column(verticalArrangement=Arrangement.spacedBy(8.dp)) {
                            v2Nodes.forEach { node ->
                                ListRow(node.name,"V2 · "+node.mode+" · "+node.expiryText()+" · "+node.statusText(tunnel)) {
                                    deviceKey=node.key;message="";page="设备信息"
                                }
                            }
                        }
                    }
                    "我的服务器" -> SimpleServer({choose(it.key)}, {page="服务器高级"})
                    "服务器高级" -> ServerScreen(goBack) {
                        choose(it.key)
                        page="连接"
                    }
                    "领取" -> ReceiveInvite(::acceptV2Link)
                    "关于" -> AboutScreen()
                    "诊断" -> NetworkScreen(tunnel,bypass,::changeMode,goBack,selectedV2Key=selectedV2?.key)
                    "连接" -> {
                        if(!loaded && !tunnel.active) HomeSkeleton() else {
                            val first=catalog?.sources.isNullOrEmpty() && v2Nodes.isEmpty() && !tunnel.active
                            val shownV2=if(tunnel.active) activeV2 else selectedV2
                            val activeRow=tunnel.node?.let { key -> rows.firstOrNull { it.favorite==key } }
                            val shownRow=if(tunnel.active) activeRow else selected.takeUnless { shownV2!=null }
                            val node=shownV2?.name ?: shownRow?.node?.optString("name") ?: tunnel.subscription
                            val notice=tunnel.error.ifEmpty { message }.ifEmpty {
                                if(!first && shownV2==null && selected==null && !tunnel.active) "请添加可用节点，或重新测试现有节点" else ""
                            }
                            ConnectionHome(tunnel,first,tunnel.active || shownV2!=null || selected!=null,node,bypass,notice,
                                toggle={
                                    if(tunnel.active) startService(Intent(this@MainActivity,TunnelService::class.java).setAction("stop"))
                                    else if(selectedV2!=null) start(v2Request(this@MainActivity,selectedV2.key))
                                    else selected?.let { start(subscriptionRequest(this@MainActivity,it,rows,"1.1.1.1",manual==null)) }
                                },pick={addSubscription=false;page="节点"},mode=::changeMode,
                                addSubscription={addSubscription=true;page="节点"},addServer={page="我的服务器"},
                                receive={page="领取"},
                                nodeInfo=if(shownV2!=null) shownV2.mode+" · "+shownV2.statusText(tunnel)+" · "+shownV2.expiryText() else shownRow?.let { it.type.uppercase()+" · "+it.source.label }.orEmpty(),
                                region=if(shownV2!=null) "V2" else shownRow?.region.orEmpty(),
                                latency=shownRow?.let { catalog?.results?.get(it.favorite) }?.takeIf {it.ok}?.ms,
                                overview=catalog?.takeIf { shownV2==null && it.sources.isNotEmpty() }?.let { data -> {
                                    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
                                    LaunchedEffect(Unit) { while(true) { now=System.currentTimeMillis();kotlinx.coroutines.delay(60_000) } }
                                    SubscriptionOverview(data.sources,data.rows,data.problems,store,data.results,now,
                                        onAdd={addSubscription=true;page="节点"},
                                        onSource={source -> manageSource=source.id;addSubscription=false;page="节点"})
                                } })
                        }
                    }
                    else -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal=Space.page,vertical=Space.sm),
                        verticalArrangement=Arrangement.spacedBy(Space.sm)) {
                        when(displayed) {
                            "设置" -> {
                                Column {
                                    SectionLabel("分流模式")
                                    ModeSwitch(bypass,::changeMode)
                                    Text(if(bypass)"国内网站直接访问，其余走加密通道" else "所有网站都走当前节点",Modifier.padding(start=Space.xs,top=6.dp),
                                        color=colors.onSurfaceVariant,style=MaterialTheme.typography.detail)
                                    SectionLabel("自动恢复")
                                    val alwaysOn=remember(tunnel.connectedAt) { prefs.getBoolean("always-on",false) }
                                    RecoverySettings(alwaysOn) {
                                        runCatching { startActivity(Intent(android.provider.Settings.ACTION_VPN_SETTINGS)) }
                                            .onFailure { message="请在系统设置 → 网络 → VPN 中开启" }
                                    }
                                    if (Build.VERSION.SDK_INT >= 33) {
                                        SectionLabel("快捷开关")
                                        ListRow("添加到下拉快捷开关","下拉通知栏点一下即可连接或断开，不用打开 App",GroupPlace.Single) {
                                            ConnectTile.requestAdd(this@MainActivity) { message=it }
                                        }
                                    }
                                    SectionLabel("更新")
                                    UpdateSettings()
                                    SectionLabel("更多")
                                    ListRow("按应用代理与出口诊断","选择哪些应用走代理，检查出口与连通",GroupPlace.First) { page="诊断" }
                                    ListRow("导出诊断信息","生成连接摘要，选择分享给谁",GroupPlace.Middle) {
                                        DiagnosticExport.share(this@MainActivity, tunnel.physical)
                                    }
                                    ListRow("关于 Nexus","版本 "+BuildConfig.VERSION_NAME+" · 隐私与开源许可",GroupPlace.Last) { page="关于" }
                                }
                            }
                        }
                        if(message.isNotEmpty() || tunnel.error.isNotEmpty()) Text(tunnel.error.ifEmpty {message},color=colors.error)
                    }
                }
            }
            v2Invite?.let { invite ->
                V2InviteConfirmation(invite,completed={ node ->
                    v2Invite=null
                    v2Nodes=v2Nodes.filterNot { it.key==node.key }+node
                    choose(node.key)
                    message="已保存 V2 节点";page="连接"
                },dismiss={v2Invite=null})
            }
            inviteError?.let { reason -> AlertDialog(onDismissRequest={inviteError=null},title={Text("无法导入 V2 邀请")},
                text={Text(reason)},confirmButton={TextButton({inviteError=null}){Text("知道了")}}) }
            foundSource?.let { link -> AlertDialog(onDismissRequest={foundSource=null},title={Text(if (foundName.isBlank()) "发现订阅链接，添加？" else "添加「$foundName」的订阅？")},
                text={Text("来自 "+(runCatching { Uri.parse(link).host }.getOrNull() ?: "剪贴板")+"。添加后会自动测试全部节点，并开启「自动选最快」。")},
                confirmButton={PrimaryAction({
                    foundSource=null
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { store.import(foundName.ifBlank { "机场订阅" },link) } }.onSuccess { source ->
                            choose("auto"); SubscriptionRefresh.testImported(this@MainActivity,source.id)
                            store.appearance.edit().putLong("tested/"+source.id,System.currentTimeMillis()).apply()
                            synchronized(SubscriptionRefresh) { SubscriptionRefresh.revision.value++ }
                            message="已添加订阅，正在后台测试节点"
                        }.onFailure { message="订阅添加失败，请检查链接或网络后重试" }
                    }
                }) {Text("添加")}},
                dismissButton={SecondaryAction({foundSource=null}) {Text("忽略")}}) }
        }
    }
}
