package dev.nexus.localclient

import android.net.Network
import android.net.Uri
import android.os.ParcelFileDescriptor
import com.shoesproxy.ShoesNative
import org.json.JSONArray
import org.json.JSONObject

object ShoesEngine {
    val types = setOf("tuic","anytls")
    // Link parameters panels add that do not change what shoes sends, or that it already does.
    private val ignoredLinkKeys = setOf("congestion_control", "congestion-control", "udp_relay_mode", "disable_sni",
        "reduce_rtt", "fp", "security", "type", "heartbeat", "zero_rtt_handshake")
    private val pinKeys = setOf("pcs", "pinSHA256")

    fun outbound(node: JSONObject): JSONObject {
        val type = node.getString("type")
        ensure(type in types) { "协议 $type 暂不支持" }
        val raw = node.get("config")
        val host: String
        val port: Int
        val sni: String
        val password: String
        val uuid: String?
        val alpn: List<String>
        val pin: String?
        if (raw is String) {
            val uri = Uri.parse(raw.trim())
            ensure(uri.scheme == type) { "节点格式无法识别" }
            ensure(listOf("allowInsecure", "insecure", "allow_insecure").all { key ->
                uri.getQueryParameters(key).all { it.lowercase() in setOf("false", "0") }
            }) { "节点要求跳过证书验证，已拒绝" }
            for (key in uri.queryParameterNames) ensure(key in ignoredLinkKeys || key in pinKeys ||
                key in setOf("sni", "peer", "alpn", "allowInsecure", "insecure", "allow_insecure")) { "参数 $key 暂不支持" }
            ensure(uri.getQueryParameter("udp_relay_mode") in setOf(null, "", "native")) { "TUIC 仅支持 native UDP 转发" }
            ensure(uri.getQueryParameter("security") in setOf(null, "", "tls")) { "安全类型暂不支持" }
            val pins = pinKeys.flatMap { uri.getQueryParameters(it) }
            ensure(pins.size <= 1) { "证书指纹格式无效" }
            pin = pins.firstOrNull()
            host = (uri.host ?: unsupported("缺少服务器地址")).removeSurrounding("[", "]")
            port = if (uri.port == -1) 443 else uri.port
            val auth = uri.userInfo ?: unsupported("缺少密码")
            uuid = if (type == "tuic") auth.substringBefore(':') else null
            password = if (type == "tuic") auth.substringAfter(':', "") else auth
            sni = uri.getQueryParameter("sni")?.takeIf { it.isNotEmpty() } ?: uri.getQueryParameter("peer")?.takeIf { it.isNotEmpty() } ?: host
            alpn = uri.getQueryParameter("alpn")?.split(',')?.map(String::trim)?.filter(String::isNotEmpty) ?: emptyList()
        } else {
            val j = raw as JSONObject
            pin = j.text("certificate_sha256", "fingerprint")
            val tls = j.optJSONObject("tls")
            if (tls != null) for (key in tls.keys()) ensure(key in setOf("enabled", "insecure", "server_name", "alpn", "utls",
                "disable_sni", "min_version", "max_version")) { "TLS 参数 $key 暂不支持" }
            ensure(listOf("ca", "ca-str", "certificate", "ech-opts").none(j::has)) { "节点使用自定义 CA 或 ECH，暂不支持" }
            ensure(!j.flag("skip-cert-verify") && tls?.flag("insecure") != true) { "节点要求跳过证书验证，已拒绝" }
            ensure(j.text("detour", "dialer-proxy") == null) { "节点依赖其他代理链路，暂不支持" }
            ensure(!j.has("ip")) { "节点指定了独立连接 IP，暂不支持" }
            ensure(type != "tuic" || !j.has("token")) { "TUIC v4 暂不支持" }
            ensure((j.text("udp-relay-mode", "udp_relay_mode") ?: "native") == "native") { "TUIC 仅支持 native UDP 转发" }
            host = (j.text("server") ?: unsupported("缺少服务器地址")).removeSurrounding("[", "]")
            port = j.text("port", "server_port")?.toIntOrNull() ?: unsupported("缺少端口")
            sni = j.text("sni", "servername") ?: tls?.text("server_name") ?: host
            uuid = if (type == "tuic") j.text("uuid") ?: unsupported("缺少 UUID") else null
            password = j.text("password") ?: unsupported("缺少密码")
            alpn = j.list("alpn").ifEmpty { tls?.list("alpn").orEmpty() }
        }
        ensure(host.matches(Regex("[A-Za-z0-9.:_-]{1,253}")) && port in 1..65535) { "服务器地址或端口无效" }
        ensure(password.length in 1..1024) { "密码长度无效" }
        ensure(sni.matches(Regex("[A-Za-z0-9.:_-]{1,253}")) && alpn.size <= 8 && alpn.all { it.length in 1..32 }) { "SNI 或 ALPN 格式无效" }
        val uuidText = uuid?.let { runCatching { java.util.UUID.fromString(it).toString() }.getOrNull() ?: unsupported("UUID 格式无效") }
        val proto = JSONObject().put("type",type).put("password",password).put("udp_enabled",true)
        val tls = JSONObject().put("sni_hostname",sni).put("verify",true)
        if (pin != null) {
            val hex = pin.replace(":", "").lowercase()
            ensure(hex.matches(Regex("[a-f0-9]{64}"))) { "证书指纹格式无效" }
            tls.put("server_fingerprints", JSONArray().put(hex)).put("verify", false)
        }
        if (alpn.isNotEmpty()) tls.put("alpn_protocols",JSONArray(alpn))
        val client = JSONObject().put("address",(if (':' in host) "[$host]" else host)+":"+port)
        if (type=="tuic") {
            proto.put("uuid",uuidText)
            client.put("protocol",proto).put("quic_settings",tls)
        } else client.put("protocol",tls.put("type","tls").put("protocol",proto))
        return JSONObject().put("shoesClient",client).put("bootstrap",host)
    }
    internal fun resolvedClient(out: JSONObject, physical: Network): JSONObject {
        val client = JSONObject(out.getJSONObject("shoesClient").toString())
        val ip = nodeAddress(physical,out.getString("bootstrap")).hostAddress!!
        val port = client.getString("address").substringAfterLast(':')
        client.put("address",(if (':' in ip) "[$ip]" else ip)+":"+port)
        return client
    }

    fun run(out: JSONObject, physical: Network, tun: ParcelFileDescriptor, cancel: ParcelFileDescriptor,
            protect: (Int) -> Boolean, health: (() -> Boolean)?, ready: () -> Unit): Int {
        val client = resolvedClient(out, physical)
        val rule = JSONObject().put("masks",JSONArray().put("0.0.0.0/0").put("::/0")).put("action","allow").put("client_chain",client)
        val settings = JSONObject().put("device_fd",tun.fd).put("mtu",1500).put("tcp_enabled",true).put("udp_enabled",true)
            .put("icmp_enabled",false).put("rules",JSONArray().put(rule))
        check(ShoesNative.init("off")==0)
        val handle = ShoesNative.start(JSONArray().put(settings).toString()) { fd -> protect(fd) }
        check(handle>0)
        try {
            check(ShoesNative.isRunning())
            ready()
            CompatEngine.waitForStop(cancel, health) { ShoesNative.isRunning() }
            return 0
        } finally { ShoesNative.stop(handle) }
    }
}
