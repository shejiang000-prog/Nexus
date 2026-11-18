package dev.nexus.localclient

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.util.Base64
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { Home() }
    }

    private fun readProfile(uri: Uri): Profile {
        require(uri.scheme == "content")
        val data = ByteArrayOutputStream()
        requireNotNull(contentResolver.openInputStream(uri)).use { input ->
            val chunk = ByteArray(4096)
            while (true) {
                val n = input.read(chunk)
                if (n < 0) break
                require(data.size() + n <= 32768) { "配置文件超过 32 KiB" }
                data.write(chunk, 0, n)
            }
        }
        return Profile.parse(data.toString("UTF-8"))
    }

    @Composable private fun Home() {
        val dark = isSystemInDarkTheme()
        val colors = if (dark) darkColorScheme(primary = Color(0xFFA1D9BD), onPrimary = Color(0xFF13392F),
            background = Color(0xFF121C19), surface = Color(0xFF1B2824), onSurface = Color(0xFFEDF3EE))
        else lightColorScheme(primary = Color(0xFF245C49), onPrimary = Color.White,
            background = Color(0xFFF5F6F1), surface = Color(0xFFFBFCF8), onSurface = Color(0xFF182820))
        val scope = rememberCoroutineScope()
        val tunnel by TunnelService.state.collectAsState()
        var profile by remember { mutableStateOf<Profile?>(null) }
        var candidate by remember { mutableStateOf<Profile?>(null) }
        var confirmConnect by remember { mutableStateOf(false) }
        var useVpn by rememberSaveable { mutableStateOf(true) }
        var message by remember { mutableStateOf("") }
        LaunchedEffect(Unit) {
            runCatching { withContext(Dispatchers.IO) { Profile.load(this@MainActivity) } }
                .onSuccess { profile = it }.onFailure { message = "原有设备配置已失效，请重新导入" }
        }
        val export = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
            if (uri != null) scope.launch {
                runCatching { withContext(Dispatchers.IO) {
                    val pem = "-----BEGIN PUBLIC KEY-----\n" +
                        Base64.encodeToString(Identity.publicKey(), Base64.NO_WRAP).chunked(64).joinToString("\n") +
                        "\n-----END PUBLIC KEY-----\n"
                    requireNotNull(contentResolver.openOutputStream(uri, "wt")).use { it.write(pem.toByteArray()) }
                } }.onSuccess { message = "公钥已导出，可交给服务器拥有者签发设备配置" }
                    .onFailure { message = "无法保存公钥，请选择可写位置" }
            }
        }
        val import = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
            if (uri != null) scope.launch {
                runCatching { withContext(Dispatchers.IO) { readProfile(uri) } }.onSuccess { candidate = it }
                    .onFailure { message = when(it.message) {
                        "此配置属于另一台设备", "配置文件超过 32 KiB", "配置版本不支持", "请使用 IP 地址及有效端口" -> it.message!!
                        else -> "配置无效，请检查文件、证书有效期和签发者"
                    } }
            }
        }
        fun connect() { startForegroundService(Intent(this, TunnelService::class.java).setAction(if (useVpn) "vpn" else "connect")) }
        val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { connect() }
        fun requestNotification() {
            if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED)
                notifications.launch(Manifest.permission.POST_NOTIFICATIONS) else connect()
        }
        val consent = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
            if (it.resultCode == RESULT_OK) requestNotification() else message = "VPN 授权已取消"
        }
        MaterialTheme(colorScheme = colors) {
            Surface(modifier = Modifier.fillMaxSize(), color = colors.background) {
                Column(Modifier.safeDrawingPadding().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp)
                    .widthIn(max = 640.dp), horizontalAlignment = Alignment.Start) {
                    Spacer(Modifier.height(24.dp))
                    Text(getString(R.string.app_name), fontWeight = FontWeight.Bold, fontSize = 18.sp, color = colors.primary)
                    Spacer(Modifier.height(12.dp))
                    Text("我的网络", fontSize = 34.sp, fontWeight = FontWeight.SemiBold,
                        modifier = Modifier.semantics { heading() })
                    Spacer(Modifier.height(20.dp))
                    Column(Modifier.fillMaxWidth().background(if (dark) Color(0xFF244439) else Color(0xFFDAE9DF),
                        RoundedCornerShape(16.dp)).padding(20.dp)) {
                        Text(if (tunnel.vpn || (!tunnel.active && useVpn)) "私人节点 VPN" else "本地 TCP 转发", style = MaterialTheme.typography.labelLarge)
                        Spacer(Modifier.height(8.dp))
                        Text(when { tunnel.waiting -> "等待网络"; tunnel.starting -> "正在验证"; tunnel.vpn && tunnel.error.isNotEmpty() -> "节点不可用"; tunnel.vpn -> "VPN 已连接"; tunnel.port > 0 -> "转发已开启"; else -> "未连接" },
                            fontSize = 28.sp, fontWeight = FontWeight.Medium, modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite })
                        Text(if (tunnel.port > 0) "127.0.0.1:${tunnel.port}" else if (tunnel.vpn) profile?.vpnEndpoint.orEmpty() else profile?.name ?: "尚未添加私人节点",
                            fontFamily = if (tunnel.port > 0) FontFamily.Monospace else FontFamily.Default)
                        if (profile != null || tunnel.active) {
                            Spacer(Modifier.height(12.dp))
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Button(onClick = {
                                    if (tunnel.active) startService(Intent(this@MainActivity, TunnelService::class.java).setAction("stop"))
                                    else { useVpn = true; confirmConnect = true }
                                }) { Text(if (tunnel.active) "断开" else "连接 VPN") }
                                if (!tunnel.active) TextButton(onClick = { useVpn = false; confirmConnect = true }) { Text("本地转发") }
                            }
                        }
                    }
                    Spacer(Modifier.height(24.dp))
                    Text("我的服务器", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold,
                        modifier = Modifier.semantics { heading() })
                    Spacer(Modifier.height(8.dp))
                    Text(profile?.let { it.name + "\n" + it.server + "\n转发目标  " + it.target }
                        ?: "先导出本机公钥，再导入服务器拥有者为这台设备签发的配置。")
                    Spacer(Modifier.height(12.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        OutlinedButton(enabled = !tunnel.active, onClick = { export.launch("nexus-device.pub") }) { Text("导出公钥") }
                        Button(enabled = !tunnel.active, onClick = { import.launch(arrayOf("*/*")) }) { Text("导入配置") }
                    }
                    Spacer(Modifier.height(16.dp))
                    HorizontalDivider()
                    Spacer(Modifier.height(16.dp))
                    Text("设备身份", style = MaterialTheme.typography.titleMedium)
                    Text("设备私钥保存在 Android 密钥库中。", style = MaterialTheme.typography.bodyMedium)
                    if (message.isNotEmpty() || tunnel.error.isNotEmpty()) {
                        Spacer(Modifier.height(12.dp))
                        Text(tunnel.error.ifEmpty { message }, color = if (tunnel.error.isEmpty()) colors.primary else colors.error,
                            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite })
                    }
                    Spacer(Modifier.height(24.dp))
                }
            }
            candidate?.let { item ->
                AlertDialog(onDismissRequest = { candidate = null }, title = { Text("信任这台服务器？") },
                    text = { Column(Modifier.verticalScroll(rememberScrollState())) {
                        Text(item.name + "\n" + item.server + "\n转发目标  " + item.target)
                        Spacer(Modifier.height(12.dp)); Text("请与服务器拥有者核对 CA 指纹（SHA-256）")
                        Text(item.fingerprint, fontFamily = FontFamily.Monospace, fontSize = 13.sp)
                        if (profile != null) Text("确认后将替换当前私人节点。")
                    } }, confirmButton = { TextButton(onClick = {
                        runCatching { item.save(this@MainActivity) }.onSuccess { profile = item; message = "设备配置已保存" }
                            .onFailure { message = "保存配置失败" }
                        candidate = null
                    }) { Text("确认导入") } }, dismissButton = { TextButton(onClick = { candidate = null }) { Text("取消") } })
            }
            if (confirmConnect) AlertDialog(onDismissRequest = { confirmConnect = false }, title = { Text(if (useVpn) "连接私人节点 VPN？" else "开启本地端口？") },
                text = { Text(if (useVpn) "应用访问 ${profile?.vpnEndpoint} 时，TCP 请求将通过私人节点前往 ${profile?.target}。其他地址继续使用原网络。"
                    else "转发期间，本机应用可通过此端口访问 ${profile?.target}。其他流量继续使用原网络。") },
                confirmButton = { TextButton(onClick = {
                    confirmConnect = false
                    val prompt = if (useVpn) VpnService.prepare(this@MainActivity) else null
                    if (prompt == null) requestNotification() else consent.launch(prompt)
                }) { Text("连接") } }, dismissButton = { TextButton(onClick = { confirmConnect = false }) { Text("取消") } })
        }
    }
}
