package dev.nexus.localclient

import org.json.JSONArray
import java.time.LocalDate
import java.time.ZoneId

internal data class Usage(val used: Long?, val total: Long?, val remaining: Long?, val expires: Long?) {
    companion object {
        private val balance = Regex("""剩余流量\s*[:：]\s*([0-9]{1,12}(?:\.[0-9]{1,3})?)\s*(KB|MB|GB|TB|B)""")
        private val expiry = Regex("""套餐到期\s*[:：]\s*([0-9]{4}-[0-9]{2}-[0-9]{2})""")
        private val reset = Regex("""距离下次重置剩余\s*[:：]\s*[0-9]{1,4}\s*天""")

        private fun remaining(name: String): Long? {
            val m = balance.matchEntire(name) ?: return null
            val shift = listOf("B","KB","MB","GB","TB").indexOf(m.groupValues[2]) * 10
            val amount = m.groupValues[1].toBigDecimal() * (1L shl shift).toBigDecimal()
            return amount.takeIf { it <= Long.MAX_VALUE.toBigDecimal() }?.toLong()
        }
        private fun expiration(name: String): Long? = expiry.matchEntire(name)?.let {
            runCatching { LocalDate.parse(it.groupValues[1]).atStartOfDay(ZoneId.systemDefault()).toEpochSecond() }.getOrNull()
        }
        fun isInfo(name: String) = remaining(name) != null || expiration(name) != null || reset.matches(name)

        /** Header fields win; info node names fill only what the header leaves out (often `expire`). */
        fun parse(header: String, nodes: JSONArray): Usage {
            val names = (0 until nodes.length()).map { nodes.getJSONObject(it).optString("name") }
            val fromNames = Usage(null, null, names.firstNotNullOfOrNull(::remaining), names.firstNotNullOfOrNull(::expiration))
            if (header.isBlank()) return fromNames
            val fromHeader = fromHeader(header)
            return fromHeader.copy(remaining = fromHeader.remaining ?: fromNames.remaining.takeIf { fromHeader.total == null },
                expires = fromHeader.expires ?: fromNames.expires)
        }

        private fun fromHeader(header: String): Usage {
            val fields = header.split(';').mapNotNull { field ->
                val pair = field.trim().split('=',limit=2)
                pair.getOrNull(1)?.toLongOrNull()?.takeIf { it >= 0 }?.let { pair[0] to it }
            }.toMap()
            val total = fields["total"]?.takeIf { it > 0 }
            val used = if ("upload" in fields || "download" in fields) {
                val up = fields["upload"] ?: 0
                up + minOf(fields["download"] ?: 0, Long.MAX_VALUE - up)
            } else null
            return Usage(used,total,used?.let { total?.minus(it)?.coerceAtLeast(0) },fields["expire"]?.takeIf { it in 1..253402300799 })
        }
    }
}

internal fun Subscription.hasAllowance() = allowance.run {
    (expires == null || expires > System.currentTimeMillis()/1000) && remaining != 0L
}
