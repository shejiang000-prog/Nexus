package dev.nexus.localclient

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

@Composable
fun ServerScreen(onProfile: (Profile) -> Unit, close: () -> Unit, boundHost: String? = null) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var host by remember { mutableStateOf(boundHost.orEmpty()) }; var sshPort by remember { mutableStateOf("22") }
    var user by remember { mutableStateOf("root") }; var password by remember { mutableStateOf("") }
    var sshKey by remember { mutableStateOf<ByteArray?>(null) }
    DisposableEffect(sshKey) { val key = sshKey; onDispose { key?.fill(0) } }
    var pin by remember { mutableStateOf("") }; var name by remember { mutableStateOf("") }
    var dns by remember { mutableStateOf("") }; var port by remember { mutableStateOf("8443") }
    var claimPort by remember { mutableStateOf("8444") }
    var days by remember { mutableStateOf("30") }; var quota by remember { mutableStateOf("") }
    var devices by remember { mutableStateOf<JSONArray?>(null) }
    var busy by remember { mutableStateOf(false) }; var feedback by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var selected by remember { mutableStateOf<JSONObject?>(null) }
    fun work(block: suspend () -> Unit) {
        if (busy) return
        busy = true; feedback = ""
        scope.launch {
            try { block() }
            catch (e: Exception) { feedback = friendlyError(e) }
            finally { busy = false }
        }
    }
    fun manager() = boundHost?.let { OwnerAccess(context).manager(it) } ?: NodeManager(context, host.trim(), sshPort.toInt(), user.trim(), password, pin.trim(), sshKey)
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
        require(it in 1..1000000000); it * 1000000
    }
    fun seconds(): Long { val n = days.toInt(); require(n in 1..365); return n * 86400L }
    BackHandler(enabled = !busy, onBack = close)
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("服务器高级管理", style = MaterialTheme.typography.headlineMedium)
        Text("调整部署、迁移或恢复服务器。端口是服务使用的网络入口，DNS 用于查找网站地址。")
        OutlinedTextField(name,{ name = it.take(64) }, singleLine = true, label = { Text("服务器名称") })
            if (boundHost == null) {
            OutlinedTextField(host,{ host = it.take(80) },singleLine = true,label = { Text("服务器 IP") })
            OutlinedTextField(sshPort,{ sshPort = it.take(5) },singleLine = true,label = { Text("SSH 端口") })
            OutlinedTextField(user,{ user = it.take(32) },singleLine = true,label = { Text("SSH 用户") })
            OutlinedTextField(password,{ password = it.take(512) },singleLine = true,visualTransformation = PasswordVisualTransformation(),label = { Text(if (sshKey == null) "SSH 密码（仅本次）" else "私钥口令（可空）") })
            SecondaryAction(enabled = !busy, onClick = { if (sshKey == null) keyPicker.launch(arrayOf("*/*")) else sshKey = null }) {
                Text(if (sshKey == null) "使用 SSH 私钥文件" else "移除本次 SSH 私钥")
            }
            OutlinedTextField(pin,{ pin = it.take(64) },singleLine = true,label = { Text("SSH 主机指纹 SHA256") })
            } else Text("已绑定服务器 · "+boundHost)
            HorizontalDivider()
            OutlinedTextField(dns,{ dns = it.take(15) }, singleLine = true,label = { Text("节点可用的 IPv4 DNS") })
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(port,{ port = it.take(5) }, Modifier.weight(1f),singleLine = true,label = { Text("隧道端口") })
                OutlinedTextField(claimPort,{ claimPort = it.take(5) }, Modifier.weight(1f),singleLine = true,label = { Text("邀请端口") })
            }
            PrimaryAction(enabled = !busy && name.isNotBlank() && host.isNotBlank() && dns.isNotBlank() && (boundHost != null || pin.isNotBlank()),onClick = { confirm = "部署私人节点" }) { Text("预览部署") }
            Row {
                for (action in listOf("更新节点程序","回滚节点程序")) SecondaryAction(enabled = !busy && host.isNotBlank() && (boundHost != null || pin.isNotBlank()),
                    onClick = { confirm = action }) { Text(action) }
            }
            HorizontalDivider()
            Text("设备授权", style = MaterialTheme.typography.titleMedium)
            Row {
                SecondaryAction(enabled=!busy,onClick={confirm="移除部署"}) { Text("移除部署") }
                SecondaryAction(enabled=!busy,onClick={confirm="恢复已移除部署"}) { Text("恢复已移除部署") }
            }
            Row {
                SecondaryAction(enabled = !busy,onClick = { work { feedback = withContext(Dispatchers.IO) { manager().service(false) } } }) { Text("服务状态") }
                SecondaryAction(enabled = !busy,onClick = { confirm = "重启服务" }) { Text("重启服务") }
            }
            Row {
                SecondaryAction(enabled = !busy,onClick = { confirm = "备份授权" }) { Text("备份授权") }
                SecondaryAction(enabled = !busy,onClick = { confirm = "恢复授权" }) { Text("恢复授权") }
            }
            OutlinedTextField(migrationPassword,{ migrationPassword=it.take(256) },singleLine=true,
                visualTransformation=PasswordVisualTransformation(),label={ Text("整机迁移口令（至少 12 字符）") })
            Row {
                SecondaryAction(enabled=!busy && migrationPassword.length>=12,onClick={confirm="导出整机迁移文件"}) { Text("迁出服务器") }
                SecondaryAction(enabled=!busy && migrationPassword.length>=12,onClick={confirm="将节点迁入此服务器"}) { Text("迁入服务器") }
            }
            Text("设备有效期与流量上限")
            OutlinedTextField(days,{days=it.take(3)},singleLine=true,label={Text("有效天数")})
            OutlinedTextField(quota,{quota=it.take(10)},singleLine=true,label={Text("流量上限（MB，可空）")})
            SecondaryAction(enabled=!busy,onClick={work {
                val m=manager();devices=JSONArray(withContext(Dispatchers.IO) {m.owner("devices")})
            }}) {Text("查看设备")}
            devices?.let { rows -> for (i in 0 until rows.length()) {
                val row = rows.getJSONObject(i)
                Text(row.getString("name") + " · " + (if (row.getInt("revoked")==1) "已撤销" else "已授权") + " · 已用 " + usageAmount(row.getLong("used")))
                if (row.getInt("revoked")==0) Row {
                    SecondaryAction(enabled = !busy,onClick = { selected = row; confirm = "修改设备期限和额度" }) { Text("应用上方限额") }
                    SecondaryAction(enabled = !busy,onClick = { selected = row; confirm = "撤销设备" }) { Text("撤销") }
                }
            } }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (feedback.isNotEmpty()) Text(feedback)
    }
    if (confirm.isNotEmpty()) AlertDialog(onDismissRequest = { confirm = "" }, title = { Text(confirm) },
        text = { Text(when (confirm) {
            "部署私人节点" -> "将在 $host 上验证 SSH 指纹（Debian 12/13 或 Ubuntu 24.04），创建独立账户和两个 systemd 服务。需要 root 或免密 sudo。隧道端口 $port，邀请端口 $claimPort。请确保网络允许这些端口。"
            "更新节点程序","回滚节点程序" -> "将重启 $host 的两个 Nexus 服务，当前连接会中断。程序更新保留设备授权、证书和配额。"
            "重启服务" -> "将重启 $host 的隧道与邀请服务，中断当前连接。"
            "移除部署" -> "将停止 $host 上的 Nexus 服务，移除启动项并归档程序。现有连接会中断，设备授权和密钥保留在服务器，可恢复。"
            "恢复已移除部署" -> "将恢复并启动 $host 上先前归档的两个 Nexus 服务，沿用服务器上现有的身份和授权。"
            "备份授权" -> "授权数据库和配置标识将备份到这台 VPS，替换上次快照。"
            "导出整机迁移文件" -> "将停止并归档 $host 的两个服务，使用口令加密保存节点 CA 私钥、设备权限、配额和撤销记录。旧服务器保留数据。"
            "将节点迁入此服务器" -> "将在空白服务器 $host 按当前端口和 DNS 启动原节点身份。请确认旧节点已停用，向成员更新连接地址并重新分享邀请。"
            "恢复授权" -> "将此 VPS 的设备权限、邀请和已用额度恢复至快照时点。此后撤销的授权可能重新有效。"
            else -> selected?.optString("name").orEmpty()
        }) }, confirmButton = { SecondaryAction(onClick = {
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
                    "部署私人节点" -> {
                        val p = withContext(Dispatchers.IO) { manager().deploy(name,dns,port.toInt(),claimPort.toInt()) }
                        onProfile(p)
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
        }) { Text("确认") } }, dismissButton = { SecondaryAction(onClick = { confirm = "" }) { Text("取消") } })
}
