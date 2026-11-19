package dev.nexus.localclient

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import java.text.DateFormat
import java.util.Date

private fun amount(value: Long?) = value?.let(::bytes) ?: "未提供"
internal fun Usage.traffic() = if (used == null && total == null && remaining == null) "流量未提供"
    else "已用 ${amount(used)} / 总量 ${amount(total)} · 剩余 ${amount(remaining)}"

@Composable
internal fun SubscriptionOverview(sources: List<Subscription>, rows: List<NodeRow>, problems: Map<String,String>?, store: Subscriptions,
                                  results: Map<String,ProbeResult>, now: Long) {
    val theme = MaterialTheme.colorScheme
    fun tested(nodes: List<NodeRow>): String {
        val recent = nodes.mapNotNull { results[it.favorite] }.filter { it.recent(86400000,now) }
        return if (recent.isEmpty()) "未测试" else "真连通过 "+recent.count { it.ok }
    }
    val available = problems?.let { verdicts -> rows.filter { it.favorite !in verdicts }.groupingBy { it.source.id }.eachCount() }
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text("订阅总览", style = MaterialTheme.typography.titleLarge)
        for (source in sources) {
            val usage = source.allowance
            val fraction = usage.total?.let { total -> usage.remaining?.toDouble()?.div(total) }
            val trafficColor = if (fraction != null && fraction < 0.1) theme.error else theme.onSurface
            Text(source.label + " · 支持 " + (available?.getOrDefault(source.id,0) ?: "…") + " / " + source.nodes.length() + " · " + tested(rows.filter { it.source.id == source.id }), style = MaterialTheme.typography.titleMedium)
            Text(usage.traffic(), color = trafficColor)
            if (fraction != null) LinearProgressIndicator(progress = { (1-fraction).toFloat() },
                modifier = Modifier.fillMaxWidth(), color = if (fraction < 0.1) theme.error else theme.primary)
            val left = usage.expires?.let { it * 1000 - now }
            val expiry = if (left == null) "到期时间未提供" else
                "到期 " + DateFormat.getDateInstance().format(Date(now+left)) +
                if (left <= 0) " · 已过期" else " · 剩余 ${(left+86399999)/86400000} 天"
            Text(expiry,color = if (left != null && left < 7L*86400000) theme.error else theme.onSurfaceVariant)
            val reason = store.appearance.getString("error/"+source.id,null)
            val time = DateFormat.getDateTimeInstance().format(Date(store.appearance.getLong("attempt/"+source.id,source.updated)))
            Text(if (source.updated == 0L) "来源无法读取" else (if (reason != null) "上次尝试 " else "上次更新 ") + "$time · " + (reason?.let { "更新失败：$it" } ?: "更新成功"),
                style = MaterialTheme.typography.bodySmall, color = if (reason != null) theme.error else theme.onSurfaceVariant)
            HorizontalDivider()
        }
        Text("合计 · ${sources.size} 个来源 · 支持 ${available?.values?.sum() ?: "…"} / ${rows.size} 个节点 · " + tested(rows),
            style = MaterialTheme.typography.titleMedium)
        fun subtotal(field: (Usage) -> Long?): String {
            val values = sources.mapNotNull { field(it.allowance) }
            if (values.isEmpty()) return "未提供"
            val sum = values.fold(java.math.BigInteger.ZERO) { acc, n -> acc + n.toBigInteger() }
            val text = if (sum.bitLength() <= 63) bytes(sum.toLong()) else "> " + bytes(Long.MAX_VALUE)
            return if (values.size == sources.size) text else "$text（${values.size}/${sources.size} 来源）"
        }
        Text("已用 ${subtotal { it.used }} / 总量 ${subtotal { it.total }} · 剩余 ${subtotal { it.remaining }}")
    }
}
