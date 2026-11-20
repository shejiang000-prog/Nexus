package dev.nexus.localclient

import android.net.Uri
import android.util.Base64
import org.json.JSONArray
import org.json.JSONObject

/** A node this build cannot use. The message is shown to the user as is. */
class UnsupportedNode(reason: String) : IllegalArgumentException(reason)

internal fun unsupported(reason: String): Nothing = throw UnsupportedNode(reason)

internal inline fun ensure(ok: Boolean, reason: () -> String) {
    if (!ok) unsupported(reason())
}

/** A scalar that YAML or JSON may give as text, a number or a one-element list. */
internal fun JSONObject.text(vararg keys: String): String? {
    for (key in keys) {
        val value = when (val v = opt(key)) {
            null, JSONObject.NULL, is JSONObject -> null
            is JSONArray -> v.opt(0)?.takeUnless { it is JSONObject || it is JSONArray || it == JSONObject.NULL }
            else -> v
        }?.toString()?.trim()
        if (!value.isNullOrEmpty()) return value
    }
    return null
}

internal fun JSONObject.flag(key: String) = when (val v = opt(key)) {
    is Boolean -> v
    is Number -> v.toInt() != 0
    is String -> v.trim().lowercase() in setOf("true", "1", "yes")
    else -> false
}

internal fun JSONObject.list(key: String): List<String> = when (val v = opt(key)) {
    is JSONArray -> (0 until v.length()).mapNotNull { i ->
        v.opt(i)?.takeUnless { it is JSONObject || it is JSONArray || it == JSONObject.NULL }?.toString()?.trim()?.takeIf(String::isNotEmpty)
    }
    null, JSONObject.NULL, is JSONObject -> emptyList()
    else -> v.toString().split(',').map(String::trim).filter(String::isNotEmpty)
}

/**
 * Converts Clash/mihomo proxies, sing-box outbounds and v2rayN VMess JSON into the share links
 * libXray parses. Anything that would weaken certificate checks or needs a feature the engine
 * lacks is refused with a reason instead of being silently dropped.
 */
internal object NodeFormats {
    private val fingerprints = setOf("chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq",
        "random", "randomized", "randomizednoalpn")
    private val ssCiphers = setOf("aes-128-gcm", "aes-256-gcm", "chacha20-poly1305", "chacha20-ietf-poly1305",
        "xchacha20-poly1305", "xchacha20-ietf-poly1305", "none", "plain",
        "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305")
    private val vmessCiphers = setOf("auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero")
    private val hostPattern = Regex("[A-Za-z0-9._:-]{1,253}")
    private val tlsKeys = setOf("enabled", "server_name", "insecure", "alpn", "utls", "reality", "min_version",
        "max_version", "disable_sni", "ech")
    private val portList = Regex("[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*")

    private fun address(host: String, port: String) = (if (':' in host) "[$host]" else host) + ":" + port

    private fun build(scheme: String, id: String, host: String, port: String, query: Map<String, String>): String {
        val builder = Uri.Builder()
        for ((k, v) in query) builder.appendQueryParameter(k, v)
        val encoded = builder.build().encodedQuery
        return "$scheme://${Uri.encode(id)}@${address(host, port)}" + if (encoded.isNullOrEmpty()) "" else "?$encoded"
    }

    private fun alpn(values: List<String>): String? {
        if (values.isEmpty()) return null
        ensure(values.size <= 8 && values.all { it.matches(Regex("[A-Za-z0-9./_-]{1,32}")) }) { "ALPN 格式无效" }
        return values.joinToString(",")
    }

    /** Clash `fingerprint` pins the leaf certificate's SHA-256, the same check as Xray `pcs`. */
    private fun pin(value: String?): String? {
        if (value == null) return null
        val hex = value.replace(":", "").lowercase()
        ensure(hex.matches(Regex("[0-9a-f]{64}"))) { "证书指纹格式无效" }
        return hex
    }

    private fun seconds(value: String?): String? {
        if (value == null) return null
        val match = Regex("([0-9]{1,5})(s|m)?").matchEntire(value) ?: unsupported("端口跳跃间隔格式无效")
        val n = match.groupValues[1].toInt() * if (match.groupValues[2] == "m") 60 else 1
        ensure(n in 5..3600) { "端口跳跃间隔超出范围" }
        return n.toString()
    }

    fun link(s: JSONObject, type: String): String {
        val singBox = s.has("server_port") || s.opt("tls") is JSONObject || s.opt("transport") is JSONObject
        val host = s.text("server")?.removeSurrounding("[", "]") ?: unsupported("缺少服务器地址")
        ensure(host.matches(hostPattern)) { "服务器地址格式无效" }
        ensure(!s.flag("skip-cert-verify")) { "节点要求跳过证书验证，已拒绝" }
        ensure(s.text("dialer-proxy", "detour") == null) { "节点依赖其他代理链路，暂不支持" }
        ensure(s.text("plugin") == null) { "Shadowsocks 插件（obfs、v2ray-plugin 等）暂不支持" }
        ensure(listOf("ca", "ca-str", "certificate", "certificate_path").none(s::has)) { "节点使用自定义 CA，暂不支持" }
        ensure(s.optJSONObject("ech-opts")?.flag("enable") != true) { "ECH 暂不支持" }
        val tls = s.optJSONObject("tls")
        if (tls != null) {
            for (key in tls.keys()) ensure(key in tlsKeys) { "节点包含不支持的 TLS 参数" }
            ensure(!tls.flag("insecure")) { "节点要求跳过证书验证，已拒绝" }
            ensure(tls.optJSONObject("ech")?.flag("enabled") != true) { "ECH 暂不支持" }
        }
        val hysteria = type == "hy2" || type == "hysteria2"
        val reality = s.optJSONObject("reality-opts") ?: tls?.optJSONObject("reality")?.takeIf { it.flag("enabled") }
        val secure = type == "trojan" || hysteria || s.flag("tls") || tls?.flag("enabled") == true || reality != null
        val q = linkedMapOf<String, String>()

        // Transport. sing-box's `network` field restricts tcp/udp; only Clash uses it for transports.
        var hostHeader: String? = null
        if (!hysteria) {
            val transport = s.optJSONObject("transport")
            val network = (if (transport != null) transport.text("type") else if (singBox) null else s.text("network"))
                ?.lowercase() ?: "tcp"
            when (network) {
                "tcp", "raw" -> q["type"] = "tcp"
                "ws", "websocket", "httpupgrade" -> {
                    val opts = transport ?: s.optJSONObject("ws-opts") ?: JSONObject()
                    val headers = opts.optJSONObject("headers") ?: s.optJSONObject("ws-headers")
                    val upgrade = network == "httpupgrade" || opts.flag("v2ray-http-upgrade")
                    var path = opts.text("path") ?: s.text("ws-path") ?: "/"
                    hostHeader = headers?.text("Host", "host") ?: opts.text("host")
                    val early = opts.text("max-early-data", "max_early_data")?.toIntOrNull() ?: 0
                    val earlyHeader = opts.text("early-data-header-name", "early_data_header_name")
                    // Xray carries WebSocket early data in Sec-WebSocket-Protocol, configured as ?ed=.
                    if (!upgrade && early > 0 && earlyHeader == "Sec-WebSocket-Protocol" && "ed=" !in path)
                        path += (if ('?' in path) "&" else "?") + "ed=$early"
                    q["type"] = if (upgrade) "httpupgrade" else "ws"
                    q["path"] = path
                    hostHeader?.let { q["host"] = it }
                }
                "grpc" -> {
                    q["type"] = "grpc"
                    q["serviceName"] = s.optJSONObject("grpc-opts")?.text("grpc-service-name")
                        ?: transport?.text("service_name").orEmpty()
                }
                "http" -> {
                    ensure(transport == null) { "HTTP/2 传输已被 Xray 移除，暂不支持" }
                    // Clash `network: http` is the TCP HTTP-header disguise, not HTTP/2.
                    val opts = s.optJSONObject("http-opts") ?: JSONObject()
                    q["type"] = "tcp"
                    q["headerType"] = "http"
                    opts.list("path").takeIf { it.isNotEmpty() }?.let { q["path"] = it.joinToString(",") }
                    opts.optJSONObject("headers")?.list("Host")?.takeIf { it.isNotEmpty() }?.let { q["host"] = it.joinToString(",") }
                }
                "xhttp", "splithttp" -> {
                    val opts = s.optJSONObject("xhttp-opts") ?: JSONObject()
                    q["type"] = "xhttp"
                    q["path"] = opts.text("path") ?: "/"
                    hostHeader = opts.text("host")
                    hostHeader?.let { q["host"] = it }
                    opts.text("mode")?.let { mode ->
                        ensure(mode in setOf("auto", "packet-up", "stream-up", "stream-one")) { "XHTTP 模式 $mode 暂不支持" }
                        q["mode"] = mode
                    }
                }
                "h2" -> unsupported("HTTP/2 传输已被 Xray 移除，暂不支持")
                else -> unsupported("传输方式 $network 暂不支持")
            }
        }

        // Security. mihomo falls back to the WebSocket Host header for SNI; sing-box does not.
        if (reality != null) {
            val key = reality.text("public-key", "public_key") ?: unsupported("REALITY 缺少公钥")
            ensure(key.matches(Regex("[A-Za-z0-9_-]{43}=?"))) { "REALITY 公钥格式无效" }
            val sid = reality.text("short-id", "short_id").orEmpty()
            ensure(sid.matches(Regex("[0-9a-fA-F]{0,16}"))) { "REALITY short-id 格式无效" }
            q["security"] = "reality"
            q["pbk"] = key
            if (sid.isNotEmpty()) q["sid"] = sid
        } else if (secure) q["security"] = "tls"
        if (secure) {
            val sni = s.text("servername", "sni") ?: tls?.text("server_name") ?: hostHeader?.takeIf { !singBox }
            sni?.let { ensure(it.matches(hostPattern)) { "SNI 格式无效" }; q["sni"] = it }
            alpn(s.list("alpn").ifEmpty { tls?.list("alpn").orEmpty() })?.let { q["alpn"] = it }
            pin(s.text("fingerprint"))?.let { q["pcs"] = it }
            if (!hysteria) {
                val fp = s.text("client-fingerprint")
                    ?: tls?.optJSONObject("utls")?.takeIf { it.flag("enabled") }?.text("fingerprint")
                fp?.lowercase()?.takeIf { it in fingerprints }?.let { q["fp"] = it }
            }
        }

        val scheme = if (type == "hysteria2") "hysteria2" else type
        var port = s.text("port", "server_port")
        var hopping = false
        val id: String = when (type) {
            "vless" -> {
                val flow = s.text("flow")
                if (flow != null) {
                    ensure(flow.matches(Regex("xtls-rprx-vision(-udp443)?"))) { "节点包含不支持的 VLESS 流控参数" }
                    q["flow"] = flow
                }
                s.text("encryption")?.takeIf { it != "none" }?.let {
                    ensure(it.length <= 2048) { "VLESS encryption 过长" }
                    q["encryption"] = it
                }
                s.text("uuid") ?: unsupported("缺少 UUID")
            }
            "vmess" -> {
                ensure((s.text("alterId", "alter_id", "aid")?.toIntOrNull() ?: 0) == 0) { "VMess alterId 不为 0（旧版认证）暂不支持" }
                val cipher = s.text("cipher", "security")?.lowercase() ?: "auto"
                ensure(cipher in vmessCiphers) { "VMess 加密方式 $cipher 暂不支持" }
                q["encryption"] = cipher
                s.text("uuid") ?: unsupported("缺少 UUID")
            }
            "ss" -> {
                val method = s.text("cipher", "method")?.lowercase() ?: unsupported("缺少加密方式")
                ensure(method in ssCiphers) { "Shadowsocks 加密方式 $method 暂不支持" }
                val password = s.text("password") ?: unsupported("缺少密码")
                Base64.encodeToString("$method:$password".toByteArray(), Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
            }
            "hy2", "hysteria2" -> {
                q.remove("type")
                val obfs = s.opt("obfs")
                val obfsType = if (obfs is JSONObject) obfs.text("type") else s.text("obfs")
                val obfsPassword = if (obfs is JSONObject) obfs.text("password") else s.text("obfs-password")
                if (obfsType != null && obfsType != "none") {
                    ensure(obfsType == "salamander") { "Hysteria2 混淆 $obfsType 暂不支持" }
                    ensure((obfsPassword?.length ?: 0) >= 4) { "Hysteria2 混淆密码过短" }
                    q["obfs"] = "salamander"
                    q["obfs-password"] = obfsPassword!!
                }
                val hops = s.text("ports")?.replace('/', ',')?.replace(" ", "")
                    ?: s.list("server_ports").map { it.replace(':', '-') }.takeIf { it.isNotEmpty() }?.joinToString(",")
                if (hops != null) {
                    ensure(hops.matches(portList) && hops.split(',', '-').all { it.toInt() in 1..65535 }) { "端口跳跃范围格式无效" }
                    port = hops
                    hopping = true
                    seconds(s.text("hop-interval", "hop_interval"))?.let { q["hop-interval"] = it }
                }
                s.text("password", "auth") ?: unsupported("缺少密码")
            }
            else -> s.text("password") ?: unsupported("缺少密码")
        }
        val portText = port ?: unsupported("缺少端口")
        ensure(hopping || portText.toIntOrNull() in 1..65535) { "端口无效" }
        return build(scheme, id, host, portText, q)
    }

    /** v2rayN's base64 VMess JSON. Empty `sni` falls back to the Host header, as v2rayN does. */
    fun vmessJson(raw: String): String {
        val encoded = raw.substringAfter("vmess://").substringBefore('#').trim()
        val bytes = runCatching { Base64.decode(encoded, Base64.DEFAULT) }.getOrElse { Base64.decode(encoded, Base64.URL_SAFE) }
        val old = checkedObject(bytes.toString(Charsets.UTF_8))
        val host = old.text("add") ?: unsupported("缺少服务器地址")
        ensure(host.matches(hostPattern)) { "服务器地址格式无效" }
        val port = old.text("port")?.toIntOrNull() ?: unsupported("缺少端口")
        ensure(port in 1..65535) { "端口无效" }
        ensure((old.text("aid")?.toIntOrNull() ?: 0) == 0) { "VMess alterId 不为 0（旧版认证）暂不支持" }
        val cipher = old.text("scy")?.lowercase() ?: "auto"
        ensure(cipher in vmessCiphers) { "VMess 加密方式 $cipher 暂不支持" }
        val q = linkedMapOf("encryption" to cipher)
        val net = old.text("net")?.lowercase() ?: "tcp"
        val header = old.text("type")?.lowercase() ?: "none"
        val hostHeader = old.text("host")
        val path = old.text("path")
        when (net) {
            "tcp", "raw" -> {
                q["type"] = "tcp"
                if (header == "http") {
                    q["headerType"] = "http"
                    path?.let { q["path"] = it }
                    hostHeader?.let { q["host"] = it }
                } else ensure(header == "none") { "TCP 伪装类型 $header 暂不支持" }
            }
            "ws", "httpupgrade" -> {
                q["type"] = net
                q["path"] = path ?: "/"
                hostHeader?.let { q["host"] = it }
            }
            "grpc" -> {
                q["type"] = "grpc"
                q["serviceName"] = path.orEmpty()
                if (header in setOf("gun", "multi")) q["mode"] = header
                hostHeader?.let { q["authority"] = it }
            }
            "xhttp", "splithttp" -> {
                q["type"] = "xhttp"
                q["path"] = path ?: "/"
                hostHeader?.let { q["host"] = it }
                if (header != "none") q["mode"] = header
            }
            "h2", "http" -> unsupported("HTTP/2 传输已被 Xray 移除，暂不支持")
            else -> unsupported("传输方式 $net 暂不支持")
        }
        when (old.text("tls")?.lowercase()) {
            null, "none" -> {}
            "tls" -> {
                q["security"] = "tls"
                (old.text("sni") ?: hostHeader?.substringBefore(','))?.let { sni ->
                    ensure(sni.matches(hostPattern)) { "SNI 格式无效" }
                    q["sni"] = sni
                }
                alpn(old.list("alpn"))?.let { q["alpn"] = it }
                old.text("fp")?.lowercase()?.takeIf { it in fingerprints }?.let { q["fp"] = it }
            }
            else -> unsupported("VMess 安全类型 ${old.text("tls")} 暂不支持")
        }
        return build("vmess", old.text("id") ?: unsupported("缺少 UUID"), host, port.toString(), q)
    }

    /**
     * v2rayN uses the Host header as SNI when a TLS share link omits `sni`; libXray instead uses the
     * server address, which breaks CDN-fronted nodes whose address is an IP or a shared CDN name.
     */
    fun hostAsSni(link: String): String {
        val uri = Uri.parse(link)
        if (uri.scheme !in setOf("vless", "vmess", "trojan") || uri.getQueryParameter("security") != "tls" ||
            !uri.getQueryParameter("sni").isNullOrEmpty()) return link
        val sni = uri.getQueryParameter("host")?.substringBefore(',')?.trim()?.takeIf { it.matches(hostPattern) } ?: return link
        val base = link.substringBefore('#')
        val fragment = link.substringAfter('#', "")
        return base + "&sni=" + Uri.encode(sni) + if (fragment.isEmpty()) "" else "#$fragment"
    }
}
