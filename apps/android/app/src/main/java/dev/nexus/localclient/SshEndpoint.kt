package dev.nexus.localclient

import java.net.URI
import java.net.URISyntaxException

internal data class SshEndpoint(val host: String, val port: Int) {
    // Default-port entries use the original IP spelling in preferences and encrypted-key AAD.
    val authority: String get() = when {
        port == 22 -> host
        ':' in host -> "[$host]:$port"
        else -> "$host:$port"
    }

    companion object {
        private val address = Regex("""(\[[0-9a-fA-F:.]+]|[0-9.]+)(?::([0-9]{1,5}))?""")

        fun parse(value: String): SshEndpoint {
            fun invalid(): Nothing = throw IllegalArgumentException("请填写有效的服务器 IP")
            val text = value.trim()
            val authority = if (!text.startsWith('[') && text.count { it == ':' } > 1) "[$text]" else text
            val (literal, portText) = address.matchEntire(authority)?.destructured ?: invalid()
            val host = literal.removeSurrounding("[", "]")
            if (literal.startsWith('[')) {
                if (':' !in host) invalid()
                try { URI("ssh://$literal").parseServerAuthority() } catch (_: URISyntaxException) { invalid() }
            } else {
                val octets = host.split('.')
                if (octets.size != 4 || octets.any {
                    it.length !in 1..3 || (it.length > 1 && it[0] == '0') || it.toInt() > 255
                }) invalid()
            }
            val port = if (portText.isEmpty()) 22 else portText.toInt()
            if (port !in 1..65535) invalid()
            return SshEndpoint(host, port)
        }
    }
}
