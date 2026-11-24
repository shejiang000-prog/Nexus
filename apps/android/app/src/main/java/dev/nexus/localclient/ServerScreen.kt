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

@Composable
internal fun ServerScreen(close: () -> Unit, onV2: (V2Node) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var host by remember { mutableStateOf("") }; var sshPort by remember { mutableStateOf("22") }
    var user by remember { mutableStateOf("root") }; var password by remember { mutableStateOf("") }
    var sshKey by remember { mutableStateOf<ByteArray?>(null) }
    DisposableEffect(sshKey) { val key = sshKey; onDispose { key?.fill(0) } }
    var pin by remember { mutableStateOf("") }
    var domain by remember { mutableStateOf("") }
    var extraProtocols by remember { mutableStateOf(v2CompatProtocols.keys) }
    var busy by remember { mutableStateOf(false) }; var feedback by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    fun work(block: suspend () -> Unit) {
        if (busy) return
        busy = true; feedback = ""
        scope.launch {
            try { block() }
            catch (e: Exception) { feedback = friendlyError(e) }
            finally { busy = false }
        }
    }
    fun manager() = NodeManager(context, host.trim(), sshPort.toInt(), user.trim(), password, pin.trim(), sshKey)
    val keyPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) work { sshKey = withContext(Dispatchers.IO) {
            require(uri.scheme == "content")
            requireNotNull(context.contentResolver.openInputStream(uri)).bounded(32768)
        } }
    }
    BackHandler(enabled = !busy, onBack = close)
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("服务器高级管理", style = MaterialTheme.typography.headlineMedium)
        Text("使用 SSH 私钥或自定义端口部署 V2 节点。")
        OutlinedTextField(host,{ host = it.take(80) },singleLine = true,label = { Text("服务器 IP") })
        OutlinedTextField(sshPort,{ sshPort = it.take(5) },singleLine = true,label = { Text("SSH 端口") })
        OutlinedTextField(user,{ user = it.take(32) },singleLine = true,label = { Text("SSH 用户") })
        OutlinedTextField(password,{ password = it.take(512) },singleLine = true,visualTransformation = PasswordVisualTransformation(),label = { Text(if (sshKey == null) "SSH 密码（仅本次）" else "私钥口令（可空）") })
        SecondaryAction(enabled = !busy, onClick = { if (sshKey == null) keyPicker.launch(arrayOf("*/*")) else sshKey = null }) {
            Text(if (sshKey == null) "使用 SSH 私钥文件" else "移除本次 SSH 私钥")
        }
        OutlinedTextField(pin,{ pin = it.take(64) },singleLine = true,label = { Text("SSH 主机指纹 SHA256") })
        DeploymentDomain(domain,{domain=it},extraProtocols,{extraProtocols=it},!busy)
        PrimaryAction(enabled=!busy && host.isNotBlank() && pin.isNotBlank(),onClick={confirm="部署私人节点"}) {Text("预览部署")}
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (feedback.isNotEmpty()) Text(feedback)
    }
    if (confirm.isNotEmpty()) AlertDialog(onDismissRequest = { confirm = "" }, title = { Text(confirm) },
        text = { Text(
            if (domain.isBlank()) "将在 $host 上安装 V2 和 Reality。V2 使用 IP 地址和 443 端口，Reality 默认使用 8443/TCP。" else "将在 $host 上安装 V2，并为 $domain 申请证书。请先将域名解析到服务器并放行 80 和 443。"
        ) }, confirmButton = { SecondaryAction(onClick = {
            confirm = ""
            work {
            val node = withContext(Dispatchers.IO) {
                val m=manager()
                m.installV2(domain,extraProtocols)
                V2Devices.enroll(context,V2Invite.parse(m.v2Invite()))
            }
            onV2(node)
            }
        }) { Text("确认") } }, dismissButton = { SecondaryAction(onClick = { confirm = "" }) { Text("取消") } })
}
