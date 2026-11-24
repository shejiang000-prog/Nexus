package dev.nexus.localclient

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Canvas
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

@Composable
internal fun DeploymentDomain(domain:String, change:(String)->Unit, protocols:Set<String>, select:(Set<String>)->Unit, enabled:Boolean) {
    OutlinedTextField(domain,{change(it.trim().take(253))},Modifier.fillMaxWidth(),enabled=enabled,singleLine=true,
        label={Text("域名（可选）")},supportingText={Text("留空部署 V2 和 Reality。填写域名后可同时部署 Hysteria2。")},shape=Shapes.control,
        colors=OutlinedTextFieldDefaults.colors(unfocusedBorderColor=MaterialTheme.colorScheme.outlineVariant,
            focusedBorderColor=MaterialTheme.colorScheme.primary,unfocusedContainerColor=MaterialTheme.colorScheme.background,
            focusedContainerColor=MaterialTheme.colorScheme.background))
    if (domain.isNotBlank()) {
        Text("随 V2 部署",style=MaterialTheme.typography.labelLarge)
        Row(horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
            for ((id,name) in v2CompatProtocols) {
                FilterChip(selected=id in protocols,enabled=enabled,
                    onClick={select(if(id in protocols) protocols-id else protocols+id)},label={Text(name)})
            }
        }
        if (protocols.isNotEmpty()) Text("兼容协议的凭据可以复制。管理员可轮换或禁用凭据。单设备即时撤销仅适用于 V2。",style=MaterialTheme.typography.bodySmall)
    }
}

@Composable
internal fun SimpleServer(saved: (V2Node) -> Unit, advanced: () -> Unit) {
    val ctx = LocalContext.current
    val access = remember { OwnerAccess(ctx) }
    val scope = rememberCoroutineScope()
    var host by remember { mutableStateOf(access.current.orEmpty()) }
    var adding by remember { mutableStateOf(host.isEmpty()) }
    var password by remember { mutableStateOf("") }
    var pin by remember { mutableStateOf("") }
    var deploy by remember { mutableStateOf(false) }
    var domain by remember { mutableStateOf("") }
    var extraProtocols by remember(adding) { mutableStateOf(v2CompatProtocols.keys) }
    var v2Days by remember(host) { mutableStateOf("") }
    var permanent by remember(host) { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var status by remember { mutableStateOf("") }
    var link by remember { mutableStateOf("") }
    var devices by remember { mutableStateOf<JSONArray?>(null) }
    var revoke by remember { mutableStateOf<JSONObject?>(null) }
    var compatAccount by remember(host) { mutableStateOf<JSONObject?>(null) }
    var changed by remember { mutableStateOf(false) }
    var confirmUpgrade by remember(host) { mutableStateOf(false) }
    var retrust by remember { mutableStateOf(false) }
    fun work(block: suspend () -> Unit) {
        if (busy) return
        busy=true; status=""; changed=false
        scope.launch {
            try { block() } catch (e: Exception) { status=friendlyError(e); changed=e is ChangedServer } finally { busy=false }
        }
    }
    LaunchedEffect(host, adding) {
        if (!adding && host in access.hosts) work { devices=withContext(Dispatchers.IO) {
            val manager = access.manager(host)
            manager.v2Devices()
        } }
    }
    fun checkHost(create: Boolean) = work {
        deploy=create
        pin=withContext(Dispatchers.IO) { access.checkHost(host.trim()) }
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),
        verticalArrangement=Arrangement.spacedBy(12.dp)) {
        if (adding) {
            val c=MaterialTheme.colorScheme
            Surface(shape=signalCard,color=c.surface,border=androidx.compose.foundation.BorderStroke(1.dp,c.outlineVariant)) {
                Column(Modifier.fillMaxWidth().padding(18.dp),verticalArrangement=Arrangement.spacedBy(12.dp)) {
                    Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)) {
                        Box(Modifier.size(44.dp).background(signalSoft,Shapes.control),
                            contentAlignment=androidx.compose.ui.Alignment.Center) { CompositionLocalProvider(LocalContentColor provides c.primary) { SectionIcon(2) } }
                        Column(Modifier.weight(1f)) {
                            Text("接入服务器",style=MaterialTheme.typography.titleLarge,fontWeight=androidx.compose.ui.text.font.FontWeight.Bold)
                            Text("只用一次密码，之后在本机直接管理",color=c.onSurfaceVariant,fontSize=14.sp)
                        }
                    }
                    val field=OutlinedTextFieldDefaults.colors(unfocusedBorderColor=c.outlineVariant,focusedBorderColor=c.primary,
                        unfocusedContainerColor=c.background,focusedContainerColor=c.background)
                    OutlinedTextField(host,{ host=it.trim().take(64) },Modifier.fillMaxWidth(),enabled=!busy,singleLine=true,label={ Text("服务器 IP") },
                        shape=Shapes.control,colors=field)
                    DeploymentDomain(domain,{domain=it},extraProtocols,{extraProtocols=it},!busy)
                    OutlinedTextField(password,{ password=it.take(512) },Modifier.fillMaxWidth(),enabled=!busy,singleLine=true,
                        visualTransformation=PasswordVisualTransformation(),label={ Text("root 密码（仅用一次）") },
                        shape=Shapes.control,colors=field)
                    Text("密码不会保存。App 会生成自己的管理密钥，以后用它登录。",color=c.onSurfaceVariant,fontSize=13.sp)
                }
            }
            val ready=host.isNotBlank() && password.isNotEmpty() && !busy
            PrimaryAction({ checkHost(true) },Modifier.fillMaxWidth(),enabled=ready) { Text("部署新服务器") }
            SecondaryAction({ checkHost(false) },Modifier.fillMaxWidth(),enabled=ready) { Text("绑定已部署的服务器") }
            if (!ready && !busy) Text(if (host.isBlank()) "先填写服务器 IP" else "请输入服务器密码",Modifier.fillMaxWidth(),
                color=c.onSurfaceVariant,fontSize=14.sp,textAlign=androidx.compose.ui.text.style.TextAlign.Center)
            Column {
                ListRow("其他登录方式","使用 SSH 私钥或自定义端口",if(access.current!=null) GroupPlace.First else GroupPlace.Single, onClick=advanced)
                if (access.current!=null) ListRow("返回已绑定服务器",access.current.orEmpty(),GroupPlace.Last) { host=access.current!!; password=""; adding=false }
            }
        } else {
            Text(host, style=MaterialTheme.typography.titleLarge)
            val validity=V2ShareValidity.parse(v2Days,permanent)
            V2ValidityInput(v2Days,permanent,!busy,{v2Days=it},{permanent=it}) { chosen ->
                work { link=withContext(Dispatchers.IO) { access.manager(host).v2Invite(chosen.days) } }
            }
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) {
                Text("已授权设备",Modifier.weight(1f),style=MaterialTheme.typography.titleMedium)
                TextButton({ work { devices=withContext(Dispatchers.IO) { access.manager(host).v2Devices() } } },enabled=!busy) { Text("刷新") }
            }
            val rows=devices
            if (rows==null || rows.length()==0) Text(if(rows==null) "正在读取设备…" else "还没有已授权设备",color=MaterialTheme.colorScheme.onSurfaceVariant)
            else repeat(rows.length()) { i ->
                val row=rows.getJSONObject(i)
                V2ManagedDeviceRow(row,groupPlace(i,rows.length()-1,false,false),busy,validity!=null,
                    renew={
                        if (validity!=null) work {
                            devices=withContext(Dispatchers.IO) {
                                val manager=access.manager(host)
                                manager.v2Renew(row.getString("user_id"),validity)
                                manager.v2Devices()
                            }
                            status="账号已续期"
                        }
                    }, revoke={ revoke=row }, compat={ status=""; compatAccount=row })
            }
            ToolRow("更新节点程序","通过 SSH 更新并重启节点服务",GroupPlace.Single,!busy) { confirmUpgrade=true }
            access.hosts.filter { it!=host }.forEach { other ->
                ListRow(other,"切换服务器",GroupPlace.Single,enabled=!busy) { host=other;access.current=other;devices=null;status="" }
            }
            SecondaryAction({host="";domain="";adding=true;devices=null;status=""},Modifier.fillMaxWidth(),enabled=!busy) { Text("添加其他服务器") }
        }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (status.isNotBlank()) Text(status, color=MaterialTheme.colorScheme.onSurface)
        if (changed && !busy) SecondaryAction({ retrust=true },Modifier.fillMaxWidth()) { Text("我重装过这台服务器") }
    }
    if (confirmUpgrade) AlertDialog(onDismissRequest={confirmUpgrade=false},title={Text("更新节点程序？")},
        text={Text("将更新 $host 上已部署的协议服务。现有连接会短暂中断，设备授权和证书将保留。")},
        confirmButton={PrimaryAction({
            confirmUpgrade=false
            val server=host
            work {
                withContext(Dispatchers.IO) { access.manager(server).upgradeV2() }
                status="节点程序已更新，服务已启动"
            }
        }) {Text("更新")}},dismissButton={SecondaryAction({confirmUpgrade=false}) {Text("取消")}})
    if (retrust) AlertDialog(onDismissRequest={retrust=false},title={Text("重新信任这台服务器？")},
        text={ Text("只有在你确定自己重装过系统或换过服务器时才继续。否则可能有人在冒充你的服务器。\n\n继续后需要再输入一次服务器密码。") },
        confirmButton={PrimaryAction({
            retrust=false; access.forget(host.trim())
            changed=false; status=""; password=""; devices=null; adding=true
        }) {Text("重新绑定")}},dismissButton={SecondaryAction({retrust=false}) {Text("取消")}})
    if (pin.isNotEmpty()) AlertDialog(onDismissRequest={pin=""},title={Text("确认连接这台服务器？")},
        text={ Text(host+"\n首次连接这台服务器，以后若发生变化会警告你。") },
        confirmButton={PrimaryAction({
            val trusted=pin; pin=""
            work {
                val secret=password; password=""
                val result=withContext(Dispatchers.IO) { access.bindPrivate(host,trusted,secret,deploy,domain,extraProtocols) }
                result?.let(saved)
                adding=false; status=if (deploy) "部署成功" else "绑定成功"
            }
        }) {Text("确认")}},dismissButton={SecondaryAction({pin=""}) {Text("取消")}})
    if (link.isNotEmpty()) ShareInvite(link,listOf(if(permanent) "长期有效" else "$v2Days 天","一次领取")) { link="" }
    compatAccount?.let { row ->
        AlertDialog(onDismissRequest={if (!busy) compatAccount=null},title={Text("兼容链接")},
            text={Column(Modifier.verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(Space.sm)) {
                Text(row.optString("name").ifBlank { row.getString("id").take(8) },style=MaterialTheme.typography.titleMedium)
                Text("为此设备所属账号启用协议并复制链接。持有链接的人可使用该账号，撤销单台设备无法收回链接。")
                for ((id,label) in v2CompatProtocols) {
                    SecondaryAction({
                        work {
                            val value=withContext(Dispatchers.IO) { access.manager(host).v2CompatLink(row.getString("user_id"),id) }
                            copySensitive(ctx,"Nexus $label",value)
                            status="$label 链接已复制"
                        }
                    },Modifier.fillMaxWidth(),enabled=!busy) { Text("启用并复制 $label 链接") }
                }
                if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (status.isNotBlank()) Text(status)
            }},confirmButton={TextButton({compatAccount=null},enabled=!busy){Text("关闭")}})
    }
    revoke?.let { row -> AlertDialog(onDismissRequest={revoke=null},title={Text("撤销这台设备？")},
        text={Text(row.getString("name")+" 将失去连接权限。")},confirmButton={PrimaryAction({
            revoke=null; work { devices=withContext(Dispatchers.IO) { val m=access.manager(host)
                m.v2Revoke(row.getString("id")); m.v2Devices()
            } }
        }) {Text("确认撤销")}},dismissButton={SecondaryAction({revoke=null}) {Text("取消")}}) }
}

@Composable
internal fun V2ValidityInput(days:String,permanent:Boolean,enabled:Boolean,changeDays:(String)->Unit,
                            changePermanent:(Boolean)->Unit,invite:(V2ShareValidity)->Unit) {
    val validity=V2ShareValidity.parse(days,permanent)
    Row(Modifier.fillMaxWidth(),verticalAlignment=androidx.compose.ui.Alignment.CenterVertically,
        horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
        OutlinedTextField(days,changeDays,Modifier.weight(1f),enabled=enabled && !permanent,singleLine=true,
            label={Text("账号有效天数（1–3650）")},isError=days.isNotEmpty() && validity==null,
            keyboardOptions=KeyboardOptions(keyboardType=KeyboardType.Number),shape=Shapes.control)
        Row(Modifier.heightIn(min=48.dp).toggleable(value=permanent,enabled=enabled,role=Role.Switch,onValueChange=changePermanent),
            verticalAlignment=androidx.compose.ui.Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.xs)) {
            Text("永久",style=MaterialTheme.typography.labelLarge)
            Switch(checked=permanent,onCheckedChange=null,enabled=enabled)
        }
    }
    PrimaryAction({validity?.let(invite)},Modifier.fillMaxWidth(),enabled=enabled && validity!=null) { Text("邀请朋友或我的设备") }
}

@Composable
internal fun V2ManagedDeviceRow(row:JSONObject,place:GroupPlace,busy:Boolean,validDays:Boolean,renew:()->Unit,revoke:()->Unit,compat:(()->Unit)?=null) {
    val active=row.isNull("revoked_at")
    val expires=if(row.isNull("expires_at")) null else row.getLong("expires_at")
    val expiry=v2ExpiryText(expires,row.has("expires_at"))
    ListRow(row.optString("name").ifBlank { row.getString("id").take(8) },
        if(!active) "已撤销 · $expiry" else if(compat!=null) "$expiry · 兼容链接" else expiry,place,enabled=active && !busy,trailing={
            if (active) Row {
                TextButton(renew,enabled=!busy && validDays) { Text("续期") }
                TextButton(revoke,enabled=!busy) { Text("撤销") }
            }
        },onClick=compat)
}

@Composable
internal fun ToolRow(title:String,detail:String,place:GroupPlace,enabled:Boolean,open:()->Unit) {
    val c=MaterialTheme.colorScheme
    Column(Modifier.fillMaxWidth().clip(place.shape()).background(c.surface)) {
        if(place==GroupPlace.Middle || place==GroupPlace.Last) HorizontalDivider(Modifier.padding(start=16.dp),color=c.outlineVariant)
        Row(Modifier.fillMaxWidth().clickable(enabled=enabled,role=androidx.compose.ui.semantics.Role.Button,onClick=open).heightIn(min=62.dp)
            .padding(horizontal=16.dp,vertical=10.dp),verticalAlignment=androidx.compose.ui.Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) { Text(title,fontWeight=androidx.compose.ui.text.font.FontWeight.SemiBold)
                Text(detail,color=c.onSurfaceVariant,fontSize=13.sp) }
            GlyphIcon(Glyph.Chevron,size=18.dp,tint=c.outline)
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ShareInvite(link:String,limits:List<String> = emptyList(),close:()->Unit) {
    val context=LocalContext.current
    val message=INVITE_MESSAGE+"\n"+link
    var copied by remember(link){mutableStateOf(false)}
    ModalBottomSheet(onDismissRequest=close,sheetState=rememberModalBottomSheetState(skipPartiallyExpanded=true),
        shape=Shapes.sheet,containerColor=MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(start=20.dp,end=20.dp,bottom=16.dp),
            horizontalAlignment=androidx.compose.ui.Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(10.dp)){
            Row(Modifier.fillMaxWidth(),verticalAlignment=androidx.compose.ui.Alignment.CenterVertically){
                Text("邀请朋友",Modifier.weight(1f),style=MaterialTheme.typography.headlineMedium)
                TextButton(close){Text("关闭",color=MaterialTheme.colorScheme.onSurfaceVariant)}
            }
            if(copied)Notice("已复制，发给朋友后请对方打开 Nexus")
            val actions:@Composable ()->Unit={
            Text("对方用 Nexus 扫码，或收到消息后打开 Nexus",color=MaterialTheme.colorScheme.onSurfaceVariant)
            if(limits.isNotEmpty())Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(8.dp)){
                listOf("有效期","设备").zip(limits).forEach{(label,value)->
                    Surface(Modifier.weight(1f),shape=Shapes.control,color=MaterialTheme.colorScheme.surfaceVariant){
                        Column(Modifier.padding(8.dp),horizontalAlignment=androidx.compose.ui.Alignment.CenterHorizontally){
                            Text(label,color=MaterialTheme.colorScheme.onSurfaceVariant);Text(value,style=numberStyle)
                        }
                    }
                }
            }
            PrimaryAction({context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain")
                .putExtra(Intent.EXTRA_TEXT,message),"发送邀请"))},Modifier.fillMaxWidth()){Text("发送邀请")}
            SecondaryAction({
                copySensitive(context,"Nexus 发出的邀请",message);copied=true
            },Modifier.fillMaxWidth()){Text("复制消息（发微信用）")}
            }
            BoxWithConstraints(Modifier.fillMaxWidth()){
                if(maxWidth>=540.dp)Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(20.dp),verticalAlignment=androidx.compose.ui.Alignment.CenterVertically){
                    InvitationCode(link)
                    Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(10.dp)){actions()}
                }else Column(Modifier.fillMaxWidth(),horizontalAlignment=androidx.compose.ui.Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(10.dp)){
                    InvitationCode(link);actions()
                }
            }
        }
    }
}

private fun copySensitive(context:Context,label:String,value:String) {
    val clip=ClipData.newPlainText(label,value)
    clip.description.extras=android.os.PersistableBundle().apply{putBoolean("android.content.extra.IS_SENSITIVE",true)}
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(clip)
}

@Composable
internal fun ReceiveInvite(onV2Link:(String)->Boolean) {
    val ctx=LocalContext.current
    val scope=rememberCoroutineScope()
    var input by remember { mutableStateOf("") }
    var error by remember { mutableStateOf("") }
    fun read(value: String) { if (onV2Link(value.trim())) { input="";error="" } else error="没有找到有效邀请，请重新复制或扫码。" }
    val scan=rememberLauncherForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { it.contents?.let(::read) }
    val file=rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> if(uri!=null) scope.launch {
        runCatching { withContext(Dispatchers.IO) { Subscriptions(ctx).readFile(uri) } }.onSuccess(::read).onFailure { error="请重新选择邀请文件" }
    } }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),verticalArrangement=Arrangement.spacedBy(12.dp)) {
        ClaimArt()
        Text("收到一个邀请",style=MaterialTheme.typography.headlineMedium)
        Text("扫描或粘贴 V2 邀请，确认节点后即可领取。",color=MaterialTheme.colorScheme.onSurfaceVariant)
        OutlinedTextField(input,{input=it.take(6000);read(input)},label={Text("粘贴邀请消息")})
        PrimaryAction({ scan.launch(com.journeyapps.barcodescanner.ScanOptions().setDesiredBarcodeFormats("QR_CODE").setOrientationLocked(false).setBeepEnabled(false)) }) { Text("扫描邀请") }
        SecondaryAction({ file.launch(arrayOf("image/*","text/plain")) }) { Text("从文件读取") }
        if(error.isNotEmpty()) Text(error,color=MaterialTheme.colorScheme.error)
    }
}
