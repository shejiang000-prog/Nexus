package dev.nexus.localclient

import kotlin.concurrent.withLock
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
    @Volatile private var probing = false
    private val listener = DialerController { fd -> probing || socketProtection?.invoke(fd.toInt()) == true }
    private val controller = DialerController { fd -> socketProtection?.invoke(fd.toInt()) == true }

    internal fun invoke(method: String, payload: JSONObject = JSONObject()): JSONObject {
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
        if (Usage.isInfo(node.optString("name"))) throw UnsupportedNode("信息")
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
        ensure(!Uri.decode(link.substringBefore('#')).any(Char::isISOControl)) { "节点参数含控制字符" }
        val allowed = setOf("type","security","sni","alpn","fp","flow","encryption","pbk","sid","spx","pcs","path","host",
            "headerType","serviceName","authority","mode","obfs","obfs-password","hop-interval","allowInsecure","insecure",
            "allow_insecure","peer","pinSHA256","mport","ports","obfsPassword","congestion","remarks")
        ensure(uri.queryParameterNames.all { uri.getQueryParameters(it).size==1 && it.lowercase() !in setOf("extra","dialerproxy","sockopt","fakedns","api","inbounds") }) { "节点包含不支持的参数" }
        ensure(uri.queryParameterNames.filter { it.lowercase() in setOf("allowinsecure", "insecure", "allow_insecure") }
            .all { uri.getQueryParameters(it).all { value -> value.lowercase() in setOf("0", "false") } }) { "节点要求跳过证书验证，已拒绝" }
        ensure(uri.getQueryParameter("type")?.lowercase() in setOf(null,"","tcp","raw","ws","grpc","httpupgrade","xhttp","splithttp")) { "传输类型暂不支持" }
        fun convertLink(value:String):JSONObject {
            val result=JSONObject(LibXray.invoke(JSONObject().put("apiVersion",3).put("method","convertShareLinksToXrayJson")
                .put("payload",JSONObject().put("text",value)).toString()))
            ensure(result.optBoolean("success")) { "节点参数不受 Xray 核心支持" }
            val list=result.getJSONObject("data").getJSONArray("outbounds")
            ensure(list.length()==1) { "节点格式无法识别" }
            return list.getJSONObject(0)
        }
        val outbound=convertLink(link)
        // Providers append metadata. Accept unknown fields only if removing them leaves the
        // native converter's entire outbound unchanged, including all TLS settings.
        if(uri.queryParameterNames.any {it !in allowed}){
            val query=uri.encodedQuery.orEmpty().split('&').filter {Uri.decode(it.substringBefore('=')) in allowed}
            val clean=convertLink(uri.buildUpon().encodedQuery(query.joinToString("&")).build().toString())
            ensure(clean.toString()==outbound.toString()) { "节点包含不支持的连接参数" }
        }
        ensure(outbound.getString("protocol") in setOf("vless", "vmess", "trojan", "shadowsocks", "hysteria")) { "节点格式无法识别" }
        ensure(outbound.keys().asSequence().all {it in setOf("tag","protocol","settings","streamSettings","mux")}) { "节点包含不支持的出站设置" }
        val stream=outbound.optJSONObject("streamSettings")
        ensure(stream?.optString("network")?.lowercase() in setOf(null,"","tcp","raw","ws","grpc","httpupgrade","xhttp","splithttp","hysteria")) { "传输类型暂不支持" }
        fun validate(value:Any) {
            when(value){
                is JSONObject -> for(key in value.keys()){
                    ensure(key.lowercase() !in setOf("api","inbounds","outbounds","routing","sockopt","dialerproxy","fakedns",
                        "certificatefile","keyfile","certificates","downloadsettings","proxysettings")) { "节点包含不支持的连接设置" }
                    if(key.equals("allowInsecure",true))ensure(!value.optBoolean(key)) { "节点要求跳过证书验证，已拒绝" }
                    validate(value.get(key))
                }
                is JSONArray -> for(i in 0 until value.length()) validate(value.get(i))
                is String -> ensure(value.none(Char::isISOControl)) { "节点参数含控制字符" }
            }
        }
        validate(outbound)
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
        if (copy.has("shoesClient")) return copy
        val settings = copy.getJSONObject("settings")
        for(key in listOf("vnext","servers")) settings.optJSONArray(key)?.let {ensure(it.length()==1) { "节点地址数量无效" }}
        val server = settings.takeIf { it.has("address") } ?: settings.optJSONArray("vnext")?.optJSONObject(0)
            ?: settings.optJSONArray("servers")?.optJSONObject(0) ?: unsupported("节点地址无效")
        val host = server.optString("address").removeSurrounding("[", "]")
        val address = nodeAddress(network,host)
        copy.optJSONObject("streamSettings")?.optJSONObject("tlsSettings")?.let { tls ->
            if (tls.optString("serverName").isEmpty()) tls.put("serverName", host)
        }
        server.put("address", address.hostAddress)
        return copy
    }

    internal val engineLock = java.util.concurrent.locks.ReentrantLock()

    // libXray retains its registered controllers after a VPN session.
    internal fun probeSockets(bind: ((Int) -> Boolean)?) {
        probing = bind != null
        socketProtection = bind
        if (bind != null && !registered) {
            LibXray.registerDialerController(controller)
            LibXray.registerListenerController(listener)
            registered = true
        }
    }

    fun run(service: VpnService, network: Network, systemDns: String, outbound: JSONObject,
                          tun: ParcelFileDescriptor, cancel: ParcelFileDescriptor, health: (() -> Boolean)?, split: ByteArray = byteArrayOf(), ready: () -> Unit): Int = engineLock.withLock {
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
                LibXray.registerListenerController(listener)
                registered = true
            }
            LibXray.setDNS(controller, systemDns)
            val config = JSONObject().put("env", JSONObject().put("xray.tun.fd", tun.fd.toString()))
                .put("log",JSONObject().put("loglevel","none"))
                .put("inbounds",JSONArray().put(JSONObject().put("protocol","tun").put("settings",JSONObject().put("name","nexus").put("mtu",1500))))
                .put("outbounds",JSONArray().put(resolved(outbound, network)))
            fun session(running: (() -> Boolean)? = null) {
                try {
                    invoke("runXray",JSONObject().put("xrayJson",config.toString()))
                    ready()
                    waitForStop(cancel, health, running)
                } finally { runCatching { invoke("stopXray") } }
            }
            if (split.isEmpty()) {
                SplitRouting.sniff(config.getJSONArray("inbounds").getJSONObject(0))
                session()
            } else {
                SplitRouting.withXray(service,tun,split,requireNotNull(socketProtection)) { fd, dns, running ->
                    config.getJSONObject("env").put("xray.tun.fd",fd.toString())
                    SplitRouting.configure(config,split,dns)
                    session(running)
                }
            }
            return 0
        } finally {
            if (!outbound.has("shoesClient")) {
                runCatching { LibXray.resetDNS() }
            }
            socketProtection = null
        }
    }
}
