package dev.nexus.localclient

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

internal fun V2Node.expiryText():String {
    val seconds = expiresAt ?: return if (expiryKnown) "长期有效" else "到期时间未知"
    val date = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.ROOT).format(Date(seconds * 1000))
    return "$date 到期"
}

internal fun V2Node.statusText(tunnel:TunnelState):String = when {
    tunnel.node != key -> "未连接"
    tunnel.waiting -> "等待网络"
    tunnel.starting -> "连接中"
    tunnel.vpn && tunnel.bearer.isNotBlank() -> "${tunnel.bearer.uppercase(Locale.ROOT)} 已连接"
    else -> "未连接"
}

@Composable
internal fun V2InviteConfirmation(invite:V2Invite, completed:(V2Node)->Unit, dismiss:()->Unit) {
    val ctx=LocalContext.current.applicationContext
    val scope=rememberCoroutineScope()
    var busy by remember(invite) { mutableStateOf(false) }
    var error by remember(invite) { mutableStateOf("") }
    AlertDialog(onDismissRequest={if (!busy) dismiss()}, title={Text("加入 V2 私人节点？")},
        text={Column(verticalArrangement=Arrangement.spacedBy(Space.sm)) {
            Text(invite.authority,style=MaterialTheme.typography.titleMedium)
            Text("请与邀请人核对节点域名。确认后会为这台设备生成密钥并兑换邀请。",color=MaterialTheme.colorScheme.onSurfaceVariant)
            if (error.isNotEmpty()) Text(error,color=MaterialTheme.colorScheme.error)
        }}, confirmButton={PrimaryAction(enabled=!busy,onClick={
            busy=true
            scope.launch {
                try { completed(withContext(Dispatchers.IO) { V2Devices.enroll(ctx,invite) }) }
                catch (e:CancellationException) { throw e }
                catch (_:Exception) { error="兑换未完成，请检查网络、节点证书和邀请有效期。" }
                finally { busy=false }
            }
        }) {Text(if(busy) "正在兑换…" else "确认兑换")}},
        dismissButton={SecondaryAction(enabled=!busy,onClick=dismiss){Text("取消")}})
}

@Composable
internal fun V2DeviceScreen(node:V2Node, tunnel:TunnelState, select:()->Unit, remove:()->Unit) {
    var confirm by remember { mutableStateOf(false) }
    val active=tunnel.active && tunnel.node==node.key
    val identity:@Composable ()->Unit={
        ListRow(node.name,node.authority,GroupPlace.First)
        ListRow("设备 ID",node.deviceId.take(8)+"…"+node.deviceId.takeLast(4),GroupPlace.Last)
    }
    val status:@Composable ()->Unit={
        ListRow("有效期（本地时间）",node.expiryText(),GroupPlace.First)
        ListRow("连接状态","V2 · "+node.statusText(tunnel),GroupPlace.Last)
    }
    val removeRow:@Composable ()->Unit={
        ListRow("在本机移除此节点",if(active) "请先断开此节点" else "",enabled=!active,
            titleColor=MaterialTheme.colorScheme.error) { confirm=true }
    }
    BoxWithConstraints(Modifier.fillMaxSize().padding(horizontal=Space.page)) {
        val wide=maxWidth>=480.dp && maxWidth>maxHeight
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()),verticalArrangement=Arrangement.spacedBy(Space.sm)) {
            Text("设备信息",style=MaterialTheme.typography.headlineMedium)
            if(wide) {
                Row(horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
                    Column(Modifier.weight(1f)){identity()}
                    Column(Modifier.weight(1f)){status()}
                }
                Row(horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
                    if(!active) PrimaryAction(select,Modifier.weight(1f)) { Text("使用此节点") }
                    Box(Modifier.weight(1f)){removeRow()}
                }
            } else {
                Column {identity();status()}
                if(!active) PrimaryAction(select,Modifier.fillMaxWidth()) { Text("使用此节点") }
                removeRow()
            }
        }
    }
    if (confirm) AlertDialog(onDismissRequest={confirm=false},title={Text("在本机移除此节点？")},
        text={Text("本机保存的设备密钥将被删除。如需重新加入，请向邀请人索取新邀请。")},
        confirmButton={TextButton({confirm=false;remove()}){Text("移除",color=MaterialTheme.colorScheme.error)}},
        dismissButton={TextButton({confirm=false}){Text("取消")}})
}
