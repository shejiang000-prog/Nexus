package dev.nexus.localclient

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

@Composable
internal fun SimpleServer(saved: (Profile) -> Unit, advanced: (String?) -> Unit) {
    val ctx = LocalContext.current
    val access = remember { OwnerAccess(ctx) }
    val scope = rememberCoroutineScope()
    var host by remember { mutableStateOf(access.current.orEmpty()) }
    var adding by remember { mutableStateOf(host.isEmpty()) }
    var password by remember { mutableStateOf("") }
    var pin by remember { mutableStateOf("") }
    var deploy by remember { mutableStateOf(false) }
    var expanded by remember { mutableStateOf(false) }
    var dns by remember { mutableStateOf("1.1.1.1") }
    var port by remember { mutableStateOf("8443") }
    var claim by remember { mutableStateOf("8444") }
    var days by remember { mutableStateOf("7") }
    var count by remember { mutableStateOf("1") }
    var mb by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var status by remember { mutableStateOf("") }
    var link by remember { mutableStateOf("") }
    var devices by remember { mutableStateOf<JSONArray?>(null) }
    var revoke by remember { mutableStateOf<JSONObject?>(null) }
    var changed by remember { mutableStateOf(false) }
    var retrust by remember { mutableStateOf(false) }
    fun work(block: suspend () -> Unit) {
        if (busy) return
        busy=true; status=""; changed=false
        scope.launch {
            try { block() } catch (e: Exception) { status=friendlyError(e); changed=e is ChangedServer } finally { busy=false }
        }
    }
    LaunchedEffect(host, adding) {
        if (!adding && host in access.hosts) work { devices=withContext(Dispatchers.IO) { JSONArray(access.manager(host).owner("devices")) } }
    }
    fun checkHost(create: Boolean) = work {
        deploy=create
        pin=withContext(Dispatchers.IO) { access.checkHost(host.trim()) }
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),
        verticalArrangement=Arrangement.spacedBy(12.dp)) {
        if (adding) {
            Text("接入服务器",style=MaterialTheme.typography.headlineSmall,fontWeight=androidx.compose.ui.text.font.FontWeight.SemiBold)
            Text("使用一次服务器密码，绑定后即可在本机管理。",color=MaterialTheme.colorScheme.onSurfaceVariant)
            HorizontalDivider()
            OutlinedTextField(host,{ host=it.trim().take(64) },Modifier.fillMaxWidth(),enabled=!busy,singleLine=true,label={ Text("服务器 IP") })
            OutlinedTextField(password,{ password=it.take(512) },Modifier.fillMaxWidth(),enabled=!busy,singleLine=true,
                visualTransformation=PasswordVisualTransformation(),label={ Text("root 密码（仅用一次）") })
            val ready=host.isNotBlank() && password.isNotEmpty() && !busy
            if (!ready && !busy) Text(if (host.isBlank()) "先填写服务器 IP" else "请输入服务器密码")
            PrimaryAction({ checkHost(true) },Modifier.fillMaxWidth(),enabled=ready) { Text("部署新服务器") }
            SecondaryAction({ checkHost(false) },Modifier.fillMaxWidth(),enabled=ready) { Text("绑定已部署的服务器") }
            HorizontalDivider()
            TextButton({ expanded=!expanded }) { Text(if(expanded) "收起高级设置" else "高级设置") }
            androidx.compose.animation.AnimatedVisibility(expanded) { Column(verticalArrangement=Arrangement.spacedBy(8.dp)) {
                Text("端口是服务器的网络入口，DNS 用来查找网站地址。")
                OutlinedTextField(dns,{dns=it},singleLine=true,label={Text("DNS")})
                OutlinedTextField(port,{port=it},singleLine=true,label={Text("连接端口")})
                OutlinedTextField(claim,{claim=it},singleLine=true,label={Text("邀请端口")})
                SecondaryAction({ advanced(null) }) { Text("其他登录方式与迁移") }
            } }
            if (access.current!=null) SecondaryAction({ host=access.current!!; password=""; adding=false }) { Text("返回已绑定服务器") }
        } else {
            ServerSummary(host,devices){advanced(host)}
            val limits=runCatching {
                invitationLimits(days.toInt(),count.toInt(),mb.takeIf { it.isNotBlank() }?.let { Math.multiplyExact(it.toLong(),1000000L) })
            }.getOrNull()
            PrimaryAction({ work {
                link=withContext(Dispatchers.IO) { JSONObject(access.manager(host).owner("invite",requireNotNull(limits))).getString("link") }
            } },Modifier.fillMaxWidth(),enabled=!busy && limits!=null) { Text("邀请朋友或我的设备") }
            if (limits==null) Text("请检查天数、设备数和流量上限")
            SecondaryAction({ expanded=!expanded }) { Text("限制") }
            androidx.compose.animation.AnimatedVisibility(expanded) { Column(verticalArrangement=Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(days,{days=it},singleLine=true,label={Text("设备有效天数（1–365）")})
                Text("邀请链接最多可在 7 天内领取")
                OutlinedTextField(count,{count=it},singleLine=true,label={Text("设备数（1–128）")})
                OutlinedTextField(mb,{mb=it},singleLine=true,label={Text("每台流量上限（MB，留空为不限）")})
            } }
            SecondaryAction({ work { devices=withContext(Dispatchers.IO) { JSONArray(access.manager(host).owner("devices")) } } },enabled=!busy) { Text("已授权设备") }
            devices?.let { list -> repeat(list.length()) { i ->
                val row=list.getJSONObject(i)
                DeviceCard(row,busy){revoke=row}
            } }
            SecondaryAction({ advanced(host) }) { Text("高级") }
            access.hosts.filter { it!=host }.forEach { other ->
                SecondaryAction({ host=other; access.current=other; devices=null; status="" },enabled=!busy) { Text("管理 "+other) }
            }
            SecondaryAction({ host="";adding=true;expanded=false;devices=null;status="" },enabled=!busy) { Text("添加其他服务器") }
        }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (status.isNotBlank()) Text(status, color=MaterialTheme.colorScheme.onSurface)
        if (changed && !busy) SecondaryAction({ retrust=true },Modifier.fillMaxWidth()) { Text("我重装过这台服务器") }
    }
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
                val result=withContext(Dispatchers.IO) { access.enroll(host,trusted,secret,deploy,dns,port.toInt(),claim.toInt()) }
                result?.let(saved)
                expanded=false; adding=false; status=if (deploy) "部署成功" else "绑定成功"
            }
        }) {Text("确认")}},dismissButton={SecondaryAction({pin=""}) {Text("取消")}})
    if (link.isNotEmpty()) ShareInvite(link,listOf(days+" 天",count+" 台",mb.takeIf { it.isNotBlank() }?.plus(" MB") ?: "不限")) { link="" }
    revoke?.let { row -> AlertDialog(onDismissRequest={revoke=null},title={Text("撤销这台设备？")},
        text={Text(row.getString("name")+" 将失去连接权限。")},confirmButton={PrimaryAction({
            revoke=null; work { devices=withContext(Dispatchers.IO) { val m=access.manager(host)
                m.owner("revoke",JSONObject().put("id",row.getString("id"))); JSONArray(m.owner("devices")) } }
        }) {Text("确认撤销")}},dismissButton={SecondaryAction({revoke=null}) {Text("取消")}}) }
}

internal fun usageAmount(bytes: Long) = java.lang.String.format(java.util.Locale.ROOT,
    if(bytes>=1000000000L) "%.2f GB" else "%.2f MB", bytes / if(bytes>=1000000000L) 1e9 else 1e6)

@Composable
internal fun ServerSummary(name:String,devices:JSONArray?,manage:()->Unit){
    val c=MaterialTheme.colorScheme
    Surface(shape=signalCard,color=c.surface,border=androidx.compose.foundation.BorderStroke(1.dp,c.outlineVariant)){
        Column(Modifier.fillMaxWidth().padding(14.dp),verticalArrangement=Arrangement.spacedBy(10.dp)){
            Text("当前服务器",color=c.onSurfaceVariant)
            Row(verticalAlignment=androidx.compose.ui.Alignment.CenterVertically){
                Text(name,Modifier.weight(1f),style=MaterialTheme.typography.titleLarge)
                TextButton(manage){Text("管理",color=c.onSurfaceVariant)}
            }
            val authorized=devices?.let { rows-> (0 until rows.length()).count{rows.getJSONObject(it).optInt("revoked")==0}.toString() }
            val used=devices?.let {rows->usageAmount((0 until rows.length()).sumOf {rows.getJSONObject(it).optLong("used")})}
            Row(horizontalArrangement=Arrangement.spacedBy(8.dp)){
                listOf("已授权设备" to authorized,"累计流量" to used).forEach{(label,value)->
                    Surface(Modifier.weight(1f),shape=androidx.compose.foundation.shape.RoundedCornerShape(14.dp),color=c.surfaceVariant){
                        Column(Modifier.padding(10.dp)){Text(value?:"—",style=numberStyle);Text(label,color=c.onSurfaceVariant)}
                    }
                }
            }
            Text("已用本机密钥绑定，管理时不用再输密码",color=c.onSurfaceVariant)
        }
    }
}

@Composable
internal fun DeviceCard(row:JSONObject,busy:Boolean,revoke:()->Unit) {
    val used=row.getLong("used")
    val quota=row.optLong("quota",0L)
    Surface(shape=signalCard,color=MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().padding(16.dp),verticalArrangement=Arrangement.spacedBy(6.dp)){
            Text(row.getString("name"),style=MaterialTheme.typography.titleMedium)
            Text("最近使用 · 服务器未提供",color=MaterialTheme.colorScheme.onSurfaceVariant)
            Text("已用 "+usageAmount(used)+(if(quota>0)" / "+usageAmount(quota) else ""),style=numberStyle)
            if(quota>0)LinearProgressIndicator(progress={ (used.toDouble()/quota).toFloat().coerceIn(0f,1f) },modifier=Modifier.fillMaxWidth(),color=MaterialTheme.colorScheme.onSurfaceVariant)
            if(row.getInt("revoked")==0)SecondaryAction(revoke,enabled=!busy){Text("撤销")} else Text("已撤销")
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
        shape=androidx.compose.foundation.shape.RoundedCornerShape(topStart=30.dp,topEnd=30.dp),containerColor=MaterialTheme.colorScheme.surface) {
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
                listOf("有效期","设备","流量").zip(limits).forEach{(label,value)->
                    Surface(Modifier.weight(1f),shape=androidx.compose.foundation.shape.RoundedCornerShape(14.dp),color=MaterialTheme.colorScheme.surfaceVariant){
                        Column(Modifier.padding(8.dp),horizontalAlignment=androidx.compose.ui.Alignment.CenterHorizontally){
                            Text(label,color=MaterialTheme.colorScheme.onSurfaceVariant);Text(value,style=numberStyle)
                        }
                    }
                }
            }
            PrimaryAction({context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain")
                .putExtra(Intent.EXTRA_TEXT,message),"发送邀请"))},Modifier.fillMaxWidth()){Text("发送邀请")}
            SecondaryAction({
                val clip=ClipData.newPlainText("Nexus 发出的邀请",message)
                if(Build.VERSION.SDK_INT>=33)clip.description.extras=android.os.PersistableBundle().apply{putBoolean("android.content.extra.IS_SENSITIVE",true)}
                context.getSystemService(ClipboardManager::class.java).setPrimaryClip(clip);copied=true
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

@Composable
internal fun ReceiveInvite(initial: Invitation?, accepted: (Profile) -> Unit, connect: () -> Unit, close: () -> Unit) {
    val ctx=LocalContext.current
    val scope=rememberCoroutineScope()
    var invite by remember(initial) { mutableStateOf(initial) }
    var name by remember { mutableStateOf(Build.MODEL.take(64)) }
    var input by remember { mutableStateOf("") }
    var error by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var done by remember(initial){mutableStateOf(false)}
    val motion=LocalMotion.current
    LaunchedEffect(done){if(done){kotlinx.coroutines.delay(if(motion)750L else 150L);connect()}}
    fun read(value: String) { invite=sharedInvitation(value); if(invite==null) error="没有找到有效邀请，请重新复制或扫码。" else error="" }
    val scan=rememberLauncherForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { it.contents?.let(::read) }
    val file=rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> if(uri!=null) scope.launch {
        runCatching { withContext(Dispatchers.IO) { Subscriptions(ctx).readFile(uri) } }.onSuccess(::read).onFailure { error="请重新选择邀请文件" }
    } }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),verticalArrangement=Arrangement.spacedBy(12.dp)) {
        ClaimArt(done)
        Text(if(done)"加入成功" else if(busy)"正在加入…" else "收到一个邀请",style=MaterialTheme.typography.headlineMedium)
        Text(if(done)"已设为当前节点，即将连接" else "朋友邀请你使用一台私人服务器，接受后会自动连接",color=MaterialTheme.colorScheme.onSurfaceVariant)
        val current=invite
        if(current==null) {
            OutlinedTextField(input,{input=it.take(4200);read(input)},label={Text("粘贴邀请消息")})
            PrimaryAction({ scan.launch(com.journeyapps.barcodescanner.ScanOptions().setDesiredBarcodeFormats("QR_CODE").setOrientationLocked(false).setBeepEnabled(false)) }) { Text("扫描邀请") }
            SecondaryAction({ file.launch(arrayOf("image/*","text/plain")) }) { Text("从文件读取") }
        } else {
            val c=MaterialTheme.colorScheme
            Surface(shape=signalCard,color=c.surface,border=androidx.compose.foundation.BorderStroke(1.dp,c.outlineVariant)){
                Column(Modifier.fillMaxWidth().padding(horizontal=14.dp)){
                    listOf("服务器" to current.name,"可用时间" to "邀请未提供","流量" to "邀请未提供").forEachIndexed { i,(label,value)->
                        if(i>0)HorizontalDivider(color=c.outlineVariant)
                        Row(Modifier.padding(vertical=10.dp)){Text(label,Modifier.weight(1f),color=c.onSurfaceVariant);Text(value)}
                    }
                }
            }
            Text("本机名称（对方会在设备列表里看到）",color=c.onSurfaceVariant)
            OutlinedTextField(name,{name=it.take(64)},Modifier.fillMaxWidth(),singleLine=true,enabled=!busy && !done,
                shape=androidx.compose.foundation.shape.RoundedCornerShape(16.dp),
                colors=OutlinedTextFieldDefaults.colors(unfocusedContainerColor=c.surface,unfocusedBorderColor=c.outlineVariant))
            ClaimButton(done,busy) { busy=true;scope.launch {
                try { accepted(withContext(Dispatchers.IO) { current.claim(name.ifBlank {Build.MODEL.take(64)}) });done=true }
                catch(_:Exception) { error="领取未完成，请检查网络、邀请有效期和剩余名额。" } finally { busy=false }
            } }
            if(!busy && !done) SecondaryAction(close,Modifier.fillMaxWidth()){Text("不是我的邀请")}
        }
        if(busy) Text("正在领取邀请")
        if(error.isNotEmpty()) Text(error,color=MaterialTheme.colorScheme.error)
    }
}
