package dev.nexus.localclient

import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.ui.graphics.asImageBitmap
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter

@Composable
fun ServerScreen(onProfile: (Profile) -> Unit, close: () -> Unit, fromLink: Invitation? = null) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var owner by remember { mutableStateOf(false) }
    var host by remember { mutableStateOf("") }; var sshPort by remember { mutableStateOf("22") }
    var user by remember { mutableStateOf("root") }; var password by remember { mutableStateOf("") }
    var sshKey by remember { mutableStateOf<ByteArray?>(null) }
    DisposableEffect(sshKey) { val key = sshKey; onDispose { key?.fill(0) } }
    var pin by remember { mutableStateOf("") }; var name by remember { mutableStateOf("") }
    var dns by remember { mutableStateOf("") }; var port by remember { mutableStateOf("8443") }
    var claimPort by remember { mutableStateOf("8444") }; var link by remember { mutableStateOf("") }
    var days by remember { mutableStateOf("30") }; var quota by remember { mutableStateOf("") }
    var count by remember { mutableStateOf("1") }
    var devices by remember { mutableStateOf<JSONArray?>(null) }
    var busy by remember { mutableStateOf(false) }; var feedback by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }; var invitation by remember { mutableStateOf<Invitation?>(null) }
    LaunchedEffect(fromLink) { if (fromLink!=null) { owner=false;invitation=fromLink } }
    var selected by remember { mutableStateOf<JSONObject?>(null) }
    var shareLink by remember { mutableStateOf("") }
    var showQr by remember { mutableStateOf(false) }
    fun work(block: suspend () -> Unit) {
        if (busy) return
        busy = true; feedback = ""
        scope.launch {
            try { block() }
            catch (_: Exception) { feedback = "操作失败。请检查主机指纹、权限、端口、邀请有效期或输入格式。" }
            finally { busy = false }
        }
    }
    fun manager() = NodeManager(context, host.trim(), sshPort.toInt(), user.trim(), password, pin.trim(), sshKey)
    var migrationPassword by remember { mutableStateOf("") }
    val exportMigration = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        if (uri != null) work {
            val m = manager()
            try {
                withContext(Dispatchers.IO) {
                    requireNotNull(context.contentResolver.openOutputStream(uri,"wt")).use { m.exportMigration(migrationPassword.toCharArray(),it) }
                }
                feedback = "迁移文件已保存，旧服务器已停用。请在目标服务器导入。"
            } catch (e: Exception) {
                feedback = "导出未完成。请检查旧服务器状态；如已归档，可使用「恢复已移除部署」重新启用。"
            } finally { migrationPassword = "" }
        }
    }
    val importMigration = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) work {
            val m = manager()
            try {
                val profile = withContext(Dispatchers.IO) {
                    val snapshot = requireNotNull(context.contentResolver.openInputStream(uri)).use { readMigration(it,migrationPassword.toCharArray()) }
                    m.deploy(name,dns,port.toInt(),claimPort.toInt(),snapshot)
                }
                onProfile(profile)
            } finally { migrationPassword = "" }
        }
    }
    val keyPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) work { sshKey = withContext(Dispatchers.IO) {
            require(uri.scheme == "content")
            requireNotNull(context.contentResolver.openInputStream(uri)).bounded(32768)
        } }
    }
    fun limit(): Long? = quota.trim().takeIf { it.isNotEmpty() }?.toLong()?.let {
        require(it in 1..1000000000); it * 1048576
    }
    fun seconds(): Long { val n = days.toInt(); require(n in 1..365); return n * 86400L }
    fun receive(input: String) {
        runCatching { Invitation.parse(input.trim()) }.onSuccess { invitation = it; confirm = "领取邀请" }
            .onFailure { feedback = "邀请链接格式错误" }
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) work { receive(withContext(Dispatchers.IO) { Subscriptions(context).readFile(uri) }) }
    }
    val scanner = rememberLauncherForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { result -> result.contents?.let(::receive) }
    BackHandler(enabled = !busy, onBack = close)
    Column(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)) {
        TextButton(onClick = close, enabled = !busy) { Text("返回我的网络") }
        Text("私人服务器", style = MaterialTheme.typography.headlineMedium)
        Row { TextButton(onClick = { owner = false }) { Text("领取邀请") }; TextButton(onClick = { owner = true }) { Text("拥有者管理") } }
        OutlinedTextField(name,{ name = it.take(64) }, singleLine = true, label = { Text(if (owner) "节点或邀请名称" else "本机设备名称") })
        if (!owner) {
            if (fromLink!=null) {
                Text("收到私人邀请 · "+fromLink.name+"\n"+fromLink.url.host)
                Button(enabled = !busy && name.isNotBlank(),onClick = { invitation=fromLink;confirm="领取邀请" }) { Text("核对收到的邀请") }
            }
            TextButton(enabled = !busy && name.isNotBlank(),onClick = { scanner.launch(com.journeyapps.barcodescanner.ScanOptions()
                .setDesiredBarcodeFormats("QR_CODE").setBeepEnabled(false).setPrompt("扫描私人邀请")) }) { Text("用相机扫邀请") }
            OutlinedTextField(link,{ link = it.take(4096) }, singleLine = true, visualTransformation = PasswordVisualTransformation(), label = { Text("Nexus 邀请链接") })
            Row {
                Button(onClick = { receive(link) }, enabled = !busy && name.isNotBlank() && link.isNotBlank()) { Text("领取") }
                TextButton(onClick = { picker.launch(arrayOf("image/*","text/plain")) }, enabled = !busy && name.isNotBlank()) { Text("读取邀请二维码") }
            }
        } else {
            OutlinedTextField(host,{ host = it.take(80) },singleLine = true,label = { Text("服务器 IP") })
            OutlinedTextField(sshPort,{ sshPort = it.take(5) },singleLine = true,label = { Text("SSH 端口") })
            OutlinedTextField(user,{ user = it.take(32) },singleLine = true,label = { Text("SSH 用户") })
            OutlinedTextField(password,{ password = it.take(512) },singleLine = true,visualTransformation = PasswordVisualTransformation(),label = { Text(if (sshKey == null) "SSH 密码（仅本次）" else "私钥口令（可空）") })
            TextButton(enabled = !busy, onClick = { if (sshKey == null) keyPicker.launch(arrayOf("*/*")) else sshKey = null }) {
                Text(if (sshKey == null) "使用 SSH 私钥文件" else "移除本次 SSH 私钥")
            }
            OutlinedTextField(pin,{ pin = it.take(64) },singleLine = true,label = { Text("SSH 主机指纹 SHA256") })
            HorizontalDivider()
            OutlinedTextField(dns,{ dns = it.take(15) }, singleLine = true,label = { Text("节点可用的 IPv4 DNS") })
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(port,{ port = it.take(5) }, Modifier.weight(1f),singleLine = true,label = { Text("隧道端口") })
                OutlinedTextField(claimPort,{ claimPort = it.take(5) }, Modifier.weight(1f),singleLine = true,label = { Text("邀请端口") })
            }
            Button(enabled = !busy && name.isNotBlank() && host.isNotBlank() && dns.isNotBlank() && pin.isNotBlank(),onClick = { confirm = "部署私人节点" }) { Text("预览部署") }
            Row {
                for (action in listOf("更新节点程序","回滚节点程序")) TextButton(enabled = !busy && host.isNotBlank() && pin.isNotBlank(),
                    onClick = { confirm = action }) { Text(action) }
            }
            HorizontalDivider()
            Text("设备授权", style = MaterialTheme.typography.titleMedium)
            Row {
                TextButton(enabled=!busy,onClick={confirm="移除部署"}) { Text("移除部署") }
                TextButton(enabled=!busy,onClick={confirm="恢复已移除部署"}) { Text("恢复已移除部署") }
            }
            Row {
                TextButton(enabled = !busy,onClick = { work { feedback = withContext(Dispatchers.IO) { manager().service(false) } } }) { Text("服务状态") }
                TextButton(enabled = !busy,onClick = { confirm = "重启服务" }) { Text("重启服务") }
            }
            Row {
                TextButton(enabled = !busy,onClick = { confirm = "备份授权" }) { Text("备份授权") }
                TextButton(enabled = !busy,onClick = { confirm = "恢复授权" }) { Text("恢复授权") }
            }
            OutlinedTextField(migrationPassword,{ migrationPassword=it.take(256) },singleLine=true,
                visualTransformation=PasswordVisualTransformation(),label={ Text("整机迁移口令（至少 12 字符）") })
            Row {
                TextButton(enabled=!busy && migrationPassword.length>=12,onClick={confirm="导出整机迁移文件"}) { Text("迁出服务器") }
                TextButton(enabled=!busy && migrationPassword.length>=12,onClick={confirm="将节点迁入此服务器"}) { Text("迁入服务器") }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(days,{ days = it.take(3) },Modifier.weight(1f),singleLine = true,label = { Text("有效天数") })
                OutlinedTextField(count,{ count = it.take(3) },Modifier.weight(1f),singleLine = true,label = { Text("设备数") })
            }
            OutlinedTextField(quota,{ quota = it.take(10) },singleLine = true,label = { Text("每台额度 MiB（可空）") })
            Row {
                TextButton(enabled = !busy, onClick = { work {
                    val limits = JSONObject().put("name",name).put("valid_seconds",86400).put("device_seconds",seconds())
                        .put("uses",count.toInt()).put("max_devices",count.toInt()).put("quota",limit())
                    val manager = manager()
                    shareLink = JSONObject(withContext(Dispatchers.IO) { manager.owner("invite",limits) }).getString("link")
                } }) { Text("创建邀请") }
                TextButton(enabled = !busy,onClick = { work { val m = manager(); devices = JSONArray(withContext(Dispatchers.IO) { m.owner("devices") }) } }) { Text("查看设备") }
            }
            if (shareLink.isNotEmpty()) TextButton(onClick = {
                context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT,shareLink),"分享私人邀请"))
            }) { Text("分享邀请链接") }
            if (shareLink.isNotEmpty()) TextButton(onClick = { showQr = true }) { Text("显示邀请二维码") }
            devices?.let { rows -> for (i in 0 until rows.length()) {
                val row = rows.getJSONObject(i)
                Text(row.getString("name") + " · " + (if (row.getInt("revoked")==1) "已撤销" else "已授权") + " · 已用 " + row.getLong("used") + " B")
                if (row.getInt("revoked")==0) Row {
                    TextButton(enabled = !busy,onClick = { selected = row; confirm = "修改设备期限和额度" }) { Text("应用上方限额") }
                    TextButton(enabled = !busy,onClick = { selected = row; confirm = "撤销设备" }) { Text("撤销") }
                }
            } }
        }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (feedback.isNotEmpty()) Text(feedback)
    }
    if (showQr) {
        val image = remember(shareLink) {
            val matrix = QRCodeWriter().encode(shareLink, BarcodeFormat.QR_CODE, 512, 512)
            val pixels = IntArray(512 * 512) { if (matrix[it % 512,it / 512]) android.graphics.Color.BLACK else android.graphics.Color.WHITE }
            Bitmap.createBitmap(pixels,512,512,Bitmap.Config.ARGB_8888)
        }
        DisposableEffect(image) { onDispose { image.recycle() } }
        AlertDialog(onDismissRequest = { showQr = false }, title = { Text("私人邀请二维码") },
            text = { Image(image.asImageBitmap(), "用于领取本次私人节点邀请", Modifier.fillMaxWidth().aspectRatio(1f)) },
            confirmButton = { TextButton(onClick = { showQr = false }) { Text("关闭") } })
    }
    if (confirm.isNotEmpty()) AlertDialog(onDismissRequest = { confirm = "" }, title = { Text(confirm) },
        text = { Text(when (confirm) {
            "部署私人节点" -> "将在 $host 上验证 SSH 指纹（Debian 12/13 或 Ubuntu 24.04），创建独立账户和两个 systemd 服务。需要 root 或免密 sudo。隧道端口 $port，邀请端口 $claimPort。请确保网络允许这些端口。"
            "领取邀请" -> "${invitation?.name}\n${invitation?.url?.host}\n请通过可信渠道核对服务器证书指纹\n${invitation?.pin}"
            "更新节点程序","回滚节点程序" -> "将重启 $host 的两个 Nexus 服务，当前连接会中断。程序更新保留设备授权、证书和配额。"
            "重启服务" -> "将重启 $host 的隧道与邀请服务，中断当前连接。"
            "移除部署" -> "将停止 $host 上的 Nexus 服务，移除启动项并归档程序。现有连接会中断，设备授权和密钥保留在服务器，可恢复。"
            "恢复已移除部署" -> "将恢复并启动 $host 上先前归档的两个 Nexus 服务，沿用服务器上现有的身份和授权。"
            "备份授权" -> "授权数据库和配置标识将备份到这台 VPS，替换上次快照。"
            "导出整机迁移文件" -> "将停止并归档 $host 的两个服务，使用口令加密保存节点 CA 私钥、设备权限、配额和撤销记录。旧服务器保留数据。"
            "将节点迁入此服务器" -> "将在空白服务器 $host 按当前端口和 DNS 启动原节点身份。请确认旧节点已停用，向成员更新连接地址并重新分享邀请。"
            "恢复授权" -> "将此 VPS 的设备权限、邀请和已用额度恢复至快照时点。此后撤销的授权可能重新有效。"
            else -> selected?.optString("name").orEmpty()
        }) }, confirmButton = { TextButton(onClick = {
            val action = confirm; confirm = ""
            if (action=="导出整机迁移文件") exportMigration.launch("nexus-migration.nxm")
            else if (action=="将节点迁入此服务器") importMigration.launch(arrayOf("*/*"))
            else work {
                when (action) {
                    "移除部署","恢复已移除部署" -> {
                        withContext(Dispatchers.IO) { manager().maintain(if (action=="移除部署") "archive" else "reactivate") }
                        feedback = action+"完成"
                    }
                    "重启服务" -> feedback = withContext(Dispatchers.IO) { manager().service(true) }
                    "备份授权","恢复授权" -> {
                        withContext(Dispatchers.IO) { manager().owner(if (action=="备份授权") "backup" else "restore") }
                        feedback = action + "完成"
                    }
                    "更新节点程序","回滚节点程序" -> {
                        val m = manager()
                        withContext(Dispatchers.IO) { m.maintain(if (action=="更新节点程序") "upgrade" else "rollback") }
                        feedback = "程序已切换，两个服务均已启动"
                    }
                    "部署私人节点","领取邀请" -> {
                        val p = withContext(Dispatchers.IO) {
                            if (action=="部署私人节点") manager().deploy(name, dns, port.toInt(), claimPort.toInt())
                            else requireNotNull(invitation).claim(name)
                        }
                        onProfile(p); link = ""; invitation = null; feedback = "设备配置已收到，请核对并导入"
                    }
                    else -> {
                        val m = manager()
                        val args = JSONObject().put("id",requireNotNull(selected).getString("id"))
                        val op = if (action=="撤销设备") "revoke" else "edit"
                        if (op=="edit") args.put("expires",System.currentTimeMillis()/1000 + seconds()).put("quota",limit())
                        devices = JSONArray(withContext(Dispatchers.IO) { m.owner(op,args); m.owner("devices") })
                    }
                }
            }
        }) { Text("确认") } }, dismissButton = { TextButton(onClick = { confirm = "" }) { Text("取消") } })
}
