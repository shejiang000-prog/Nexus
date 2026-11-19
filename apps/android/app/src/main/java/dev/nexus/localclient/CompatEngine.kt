package dev.nexus.localclient

import android.net.Network
import android.net.Uri
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Base64
import libXray.DialerController
import libXray.LibXray
import org.json.JSONArray
import org.json.JSONObject

/** Only the converted outbound is accepted from a subscription. Nexus owns the TUN and routing. */
object CompatEngine {
    val types = setOf("vless","vmess","trojan","ss","hy2","hysteria2") + ShoesEngine.types
    private var registered = false
    @Volatile private var socketProtection: ((Int) -> Boolean)? = null
    private val controller = DialerController { fd -> socketProtection?.invoke(fd.toInt()) == true }

    private fun invoke(method: String, payload: JSONObject = JSONObject()): JSONObject {
        val result = JSONObject(LibXray.invoke(JSONObject().put("apiVersion",3).put("method",method).put("payload",payload).toString()))
        check(result.optBoolean("success")) { "节点参数不受支持" }
        return result
    }
    private val verdicts = object : LinkedHashMap<String, String>(256, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, String>?) = size > 4096
    }

    /** Why this node cannot be used, or null when it converts. Cached by node content. */
    fun problem(node: JSONObject): String? {
        val key = Base64.encodeToString(java.security.MessageDigest.getInstance("SHA-256")
            .digest(node.toString().toByteArray()), Base64.NO_WRAP)
        synchronized(verdicts) { verdicts[key] }?.let { return it.ifEmpty { null } }
        val verdict = try { outbound(node); "" } catch (e: UnsupportedNode) { e.message.orEmpty() }
        synchronized(verdicts) { verdicts[key] = verdict }
        return verdict.ifEmpty { null }
    }

    /** Converts a subscription node into one Xray outbound, or throws [UnsupportedNode] with a reason. */
    fun outbound(node: JSONObject): JSONObject = try {
        convert(node)
    } catch (e: UnsupportedNode) {
        throw e
    } catch (e: Exception) {
        throw UnsupportedNode("节点参数不完整或格式无法识别")
    }

    private fun convert(node: JSONObject): JSONObject {
        val type = node.getString("type")
        ensure(type in types) { "协议 $type 暂不支持" }
        if (type in ShoesEngine.types) return ShoesEngine.outbound(node)
        val raw = node.get("config")
        val link = when {
            raw is JSONObject -> NodeFormats.link(raw, type)
            raw is String && type == "vmess" && '@' !in raw -> NodeFormats.vmessJson(raw)
            raw is String -> NodeFormats.hostAsSni(raw.trim())
            else -> unsupported("节点格式无法识别")
        }
        ensure(link.substringBefore("://") == type && link.length <= 16384) { "节点格式无法识别" }
        val uri = Uri.parse(link)
        ensure(uri.queryParameterNames.filter { it.lowercase() in setOf("allowinsecure", "insecure", "allow_insecure") }
            .all { uri.getQueryParameters(it).all { value -> value.lowercase() in setOf("0", "false") } }) { "节点要求跳过证书验证，已拒绝" }
        val result = JSONObject(LibXray.invoke(JSONObject().put("apiVersion", 3).put("method", "convertShareLinksToXrayJson")
            .put("payload", JSONObject().put("text", link)).toString()))
        ensure(result.optBoolean("success")) { "节点参数不受 Xray 核心支持" }
        val list = result.getJSONObject("data").getJSONArray("outbounds")
        ensure(list.length() == 1) { "节点格式无法识别" }
        val outbound = list.getJSONObject(0)
        ensure(outbound.getString("protocol") in setOf("vless", "vmess", "trojan", "shadowsocks", "hysteria")) { "节点格式无法识别" }
        return outbound.put("tag", "selected")
    }
    fun waitForStop(cancel: ParcelFileDescriptor, health: (() -> Boolean)?, running: (() -> Boolean)? = null) {
        val poll = android.system.StructPollfd().apply {
            fd = cancel.fileDescriptor
            events = android.system.OsConstants.POLLIN.toShort()
        }
        var missed = 0
        var next = android.os.SystemClock.elapsedRealtime() + 20_000
        while (android.system.Os.poll(arrayOf(poll), if (running != null) 1000 else if (health != null) 20_000 else -1) == 0) {
            check(running?.invoke() != false)
            if (health != null && android.os.SystemClock.elapsedRealtime() >= next) {
                missed = if (health()) 0 else missed + 1
                check(missed < 2) { "节点网页连续两次验证失败" }
                next = android.os.SystemClock.elapsedRealtime() + 20_000
            }
        }
    }

    /**
     * Resolves the node's domain with the physical network's own resolver, which tries every DNS
     * server and honours Private DNS. Xray would otherwise ask only the first server, and one stale
     * or unreachable entry makes every domain-addressed node fail. TLS keeps the domain as SNI.
     */
    internal fun resolved(outbound: JSONObject, network: Network): JSONObject {
        val copy = JSONObject(outbound.toString())
        val settings = copy.optJSONObject("settings") ?: return copy
        val server = settings.takeIf { it.has("address") } ?: settings.optJSONArray("vnext")?.optJSONObject(0)
            ?: settings.optJSONArray("servers")?.optJSONObject(0) ?: return copy
        val host = server.optString("address").removeSurrounding("[", "]")
        if (host.isEmpty() || host.matches(Regex("[0-9.]+|.*:.*"))) return copy
        copy.optJSONObject("streamSettings")?.optJSONObject("tlsSettings")?.let { tls ->
            if (tls.optString("serverName").isEmpty()) tls.put("serverName", host)
        }
        val address = network.getAllByName(host).first()
        server.put("address", address.hostAddress)
        return copy
    }

    @Synchronized fun run(service: VpnService, network: Network, systemDns: String, outbound: JSONObject,
                          tun: ParcelFileDescriptor, cancel: ParcelFileDescriptor, health: (() -> Boolean)?, ready: () -> Unit): Int {
        socketProtection = { fd ->
            runCatching {
                check(service.protect(fd))
                ParcelFileDescriptor.fromFd(fd).use { network.bindSocket(it.fileDescriptor) }
                true
            }.getOrDefault(false)
        }
        try {
            if (outbound.has("shoesClient")) {
                try { return ShoesEngine.run(outbound, network, tun, cancel, requireNotNull(socketProtection), health, ready) }
                finally { socketProtection = null }
            }
            if (!registered) {
                LibXray.registerDialerController(controller)
                LibXray.registerListenerController(controller)
                registered = true
            }
            LibXray.setDNS(controller, systemDns)
            val config = JSONObject().put("env", JSONObject().put("xray.tun.fd", tun.fd.toString()))
                .put("log",JSONObject().put("loglevel","none"))
                .put("inbounds",JSONArray().put(JSONObject().put("protocol","tun").put("settings",JSONObject().put("name","nexus").put("mtu",1500))))
                .put("outbounds",JSONArray().put(resolved(outbound, network)))
            invoke("runXray",JSONObject().put("xrayJson",config.toString()))
            ready()
            waitForStop(cancel, health)
            return 0
        } finally {
            if (!outbound.has("shoesClient")) {
                runCatching { invoke("stopXray") }
                runCatching { LibXray.resetDNS() }
            }
            socketProtection = null
        }
    }
}
