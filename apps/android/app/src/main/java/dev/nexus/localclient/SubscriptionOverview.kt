package dev.nexus.localclient

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.ui.unit.sp
import androidx.compose.runtime.remember
import androidx.compose.runtime.getValue
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.ui.Alignment
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import java.text.SimpleDateFormat
import java.util.Locale
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import java.util.Date

private fun amount(value: Long?) = value?.let(::bytes) ?: "未提供"
internal fun Usage.traffic() = if (used == null && total == null && remaining == null) "流量未提供"
    else "已用 ${amount(used)} / 总量 ${amount(total)} · 剩余 ${amount(remaining)}"

internal data class HomeCatalog(val sources: List<Subscription>, val rows: List<NodeRow>,
                                val problems: Map<String,String>, val results: Map<String,ProbeResult>, val best: NodeRow?)

@Composable
internal fun SubscriptionOverview(sources: List<Subscription>, rows: List<NodeRow>, problems: Map<String,String>?, store: Subscriptions,
                                  results: Map<String,ProbeResult>, now: Long, onAdd: (() -> Unit)? = null,
                                  onSource: (Subscription) -> Unit) {
    val theme=MaterialTheme.colorScheme
    val date=remember { SimpleDateFormat("yyyy-MM-dd",Locale.ROOT) }
    val time=remember { SimpleDateFormat("yyyy-MM-dd HH:mm",Locale.ROOT) }
    val bySource=remember(rows) { rows.groupBy { it.source.id } }
    val supported=remember(rows,problems) {
        problems?.let { map -> rows.filter { it.favorite !in map }.groupingBy { it.source.id }.eachCount() }
    }
    fun tested(nodes: List<NodeRow>): String {
        val recent=nodes.mapNotNull { results[it.favorite] }.filter { it.recent(86400000,now) }
        return if(recent.isEmpty()) "未测试" else "真连通过 "+recent.count { it.ok }
    }
    Column(Modifier.fillMaxWidth(),verticalArrangement=Arrangement.spacedBy(Space.sm)) {
        Row(Modifier.fillMaxWidth(),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)) {
            Text("订阅总览",style=MaterialTheme.typography.titleMedium,fontWeight=FontWeight.SemiBold,modifier=Modifier.weight(1f))
            if(onAdd!=null) CompactAction(onAdd,filled=false) { Text("添加订阅") }
        }
        for(source in sources) {
            val usage=source.allowance
            val fraction=usage.total?.let { total -> usage.remaining?.toDouble()?.div(total) }
            val low=fraction!=null && fraction<0.1
            val left=usage.expires?.let { it*1000-now }
            val expiry=if(left==null) "到期未提供" else date.format(Date(now+left))+" 到期 · "+
                if(left<=0) "已过期" else "剩余 ${(left+86399999)/86400000} 天"
            val reason=store.appearance.getString("error/"+source.id,null)
            val updated=if(source.updated>0) time.format(Date(source.updated)) else "未提供"
            NexusCard(onClick={ onSource(source) },onClickLabel="管理"+source.label) {
                Column(Modifier.fillMaxWidth().padding(horizontal=Space.lg,vertical=Space.md),verticalArrangement=Arrangement.spacedBy(Space.xxs)) {
                    Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
                        Text(source.label,style=MaterialTheme.typography.titleMedium,modifier=Modifier.weight(1f),maxLines=1)
                        GlyphIcon(Glyph.Chevron,size=18.dp,tint=theme.outline)
                    }
                    if(usage.remaining!=null) Row(verticalAlignment=Alignment.Bottom,horizontalArrangement=Arrangement.spacedBy(6.dp)) {
                        Text(amount(usage.remaining),style=numberStyle.copy(fontSize=24.sp,lineHeight=30.sp),fontWeight=FontWeight.SemiBold,
                            color=if(low) theme.error else theme.onSurface)
                        Text("剩余",Modifier.padding(bottom=4.dp),color=theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
                    } else Text("余量未提供",color=theme.onSurfaceVariant)
                    if(fraction!=null) {
                        val used by animateFloatAsState((1-fraction).toFloat().coerceIn(0.02f,1f),motionTween(Motion.emphasized),label="用量")
                        Box(Modifier.fillMaxWidth().padding(vertical=Space.xs).height(6.dp).background(theme.surfaceVariant,CircleShape)) {
                            Box(Modifier.fillMaxWidth(used).fillMaxHeight().background(if(low) theme.error else signal,CircleShape))
                        }
                    }
                    Text(if(usage.used==null && usage.total==null) "用量和总量未提供" else "已用 ${amount(usage.used)} / 总量 ${amount(usage.total)}",
                        color=theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
                    Text("支持 ${supported?.getOrDefault(source.id,0) ?: "…"} / ${source.nodes.length()} · "+tested(bySource[source.id].orEmpty()),
                        color=theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
                    Text(expiry,color=if(left!=null && left<7L*86400000) theme.error else theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
                    Text(if(source.updated==0L) "来源无法读取" else if(reason==null) "更新成功 · "+updated else "上次更新 · "+updated,
                        color=if(source.updated==0L) theme.error else theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
                    if(reason!=null) {
                        val attempt=store.appearance.getLong("attempt/"+source.id,0)
                        Text("更新失败："+reason+if(attempt>0) " · "+time.format(Date(attempt)) else "",color=theme.error,style=MaterialTheme.typography.detail)
                    }
                }
            }
        }
        if(sources.size>1) Surface(shape=Shapes.card,color=theme.surfaceVariant) { Column(Modifier.fillMaxWidth().padding(Space.lg),verticalArrangement=Arrangement.spacedBy(Space.xs)) {
            Text("合计 · ${sources.size} 个来源 · 支持 ${supported?.values?.sum() ?: "…"} / ${rows.size} · "+tested(rows),
                fontWeight=FontWeight.SemiBold)
            val usedIds=sources.filter { it.allowance.used!=null }.map { it.id }
            val sameCoverage=usedIds==sources.filter { it.allowance.total!=null }.map { it.id } &&
                usedIds==sources.filter { it.allowance.remaining!=null }.map { it.id }
            fun subtotal(field: (Usage) -> Long?): String {
                val values = sources.mapNotNull { field(it.allowance) }
                if (values.isEmpty()) return "未提供"
                val sum = values.fold(java.math.BigInteger.ZERO) { acc, n -> acc + n.toBigInteger() }
                val text = if (sum.bitLength() <= 63) bytes(sum.toLong()) else "> " + bytes(Long.MAX_VALUE)
                return if (sameCoverage || values.size == sources.size) text else "$text（${values.size}/${sources.size} 来源）"
            }
            if(sameCoverage && usedIds.isEmpty()) Text("流量未提供",color=theme.onSurfaceVariant) else {
                Text("已用 ${subtotal { it.used }} / 总量 ${subtotal { it.total }} · 剩余 ${subtotal { it.remaining }}",style=numberStyle.copy(fontSize=14.sp),color=theme.onSurfaceVariant)
                if(sameCoverage && usedIds.size<sources.size) Text("流量合计包含 ${usedIds.size} / ${sources.size} 个来源",color=theme.onSurfaceVariant,style=MaterialTheme.typography.detail)
            }
        } }
    }
}
