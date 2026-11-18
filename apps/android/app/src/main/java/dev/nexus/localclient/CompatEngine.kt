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
    private fun structuredLink(s: JSONObject, type: String): String {
        val host = s.getString("server")
        val port = s.optInt("port", s.optInt("server_port"))
        require(port in 1..65535 && host.matches(Regex("[a-zA-Z0-9.:-]{1,253}")))
        val address = (if (':' in host) "[$host]" else host) + ":" + port
        require(!s.optBoolean("skip-cert-verify") && !s.has("plugin") && !s.has("reality-opts") &&
            !s.has("obfs") && !s.has("transport") && !s.has("dialer-proxy") && !s.has("detour"))
        val tls = s.optJSONObject("tls")
        require(tls?.optBoolean("insecure") != true && tls?.has("reality") != true)
        require(tls == null || tls.keys().asSequence().all { it in setOf("enabled","server_name","insecure","alpn") })
        require(!s.has("ca") && !s.has("ca-str") && !s.has("certificate") && !s.has("fingerprint") &&
            !s.has("certificate_public_key_sha256") && !s.has("ech-opts") && !s.has("ech"))
        val network = s.optString("network","tcp")
        require(network in setOf("tcp","ws","grpc"))
        val id = when (type) {
            "vless","vmess" -> s.getString("uuid")
            "ss" -> Base64.encodeToString((s.optString("cipher",s.optString("method")) + ":" + s.getString("password")).toByteArray(),
                Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
            else -> s.getString("password")
        }
        val query = Uri.Builder().appendQueryParameter("type",network)
        if (s.optBoolean("tls") || tls?.optBoolean("enabled")==true || type in setOf("trojan","hy2","hysteria2"))
            query.appendQueryParameter("security","tls")
        val sni = s.optString("servername",s.optString("sni",tls?.optString("server_name").orEmpty()))
        if (sni.isNotEmpty()) query.appendQueryParameter("sni",sni)
        val alpn = s.optJSONArray("alpn") ?: tls?.optJSONArray("alpn")
        if (alpn != null) {
            val values=(0 until alpn.length()).map(alpn::getString)
            require(values.isNotEmpty() && values.all { it.matches(Regex("[a-zA-Z0-9./_-]{1,32}")) })
            query.appendQueryParameter("alpn",values.joinToString(","))
        }
        s.optJSONObject("ws-opts")?.let { options ->
            query.appendQueryParameter("path",options.optString("path","/"))
            options.optJSONObject("headers")?.optString("Host")?.let { query.appendQueryParameter("host",it) }
        }
        s.optJSONObject("grpc-opts")?.let { query.appendQueryParameter("serviceName",it.optString("grpc-service-name")) }
        if (s.has("flow")) query.appendQueryParameter("flow",s.getString("flow"))
        if (type=="vmess") require(s.optInt("alterId",s.optInt("alter_id"))==0)
        return "$type://${Uri.encode(id)}@$address?${query.build().encodedQuery}"
    }
    fun outbound(node: JSONObject): JSONObject {
        val type = node.getString("type")
        require(type in types)
        if (type in ShoesEngine.types) return ShoesEngine.outbound(node)
        val raw = node.get("config")
        val link = when {
            raw is JSONObject -> structuredLink(raw,type)
            raw is String && type == "vmess" && '@' !in raw -> {
                val encoded = raw.substringAfter("vmess://").substringBefore('#')
                val bytes = runCatching { Base64.decode(encoded,Base64.DEFAULT) }
                    .getOrElse { Base64.decode(encoded,Base64.URL_SAFE) }
                val old = JSONObject(bytes.toString(Charsets.UTF_8))
                val network = old.optString("net","tcp")
                require(old.optInt("aid",0)==0 && old.optString("tls") in setOf("","none","tls") &&
                    old.optString("type","none") in setOf("","none") && old.optString("scy","auto") in setOf("","auto"))
                val data = JSONObject().put("server",old.getString("add")).put("port",old.getInt("port"))
                    .put("uuid",old.getString("id")).put("network",network)
                    .put("tls",old.optString("tls")=="tls").put("sni",old.optString("sni",old.optString("host")))
                if (network=="ws") data.put("ws-opts",JSONObject().put("path",old.optString("path","/"))
                    .put("headers",JSONObject().put("Host",old.optString("host"))))
                if (network=="grpc") data.put("grpc-opts",JSONObject().put("grpc-service-name",old.optString("path")))
                structuredLink(data,type)
            }
            else -> raw as String
        }
        require(link.substringBefore("://")==type && link.length <= 16384)
        val uri = Uri.parse(link)
        require(uri.queryParameterNames.filter { it.equals("allowInsecure",true) || it.equals("insecure",true) }
            .all { uri.getQueryParameters(it).all { value -> value.lowercase() in setOf("0","false") } })
        val list = invoke("convertShareLinksToXrayJson",JSONObject().put("text",link)).getJSONObject("data").getJSONArray("outbounds")
        require(list.length()==1)
        val outbound = list.getJSONObject(0)
        require(outbound.getString("protocol") in setOf("vless","vmess","trojan","shadowsocks","hysteria"))
        return outbound.put("tag","selected")
    }
    @Synchronized fun run(service: VpnService, network: Network, systemDns: String, outbound: JSONObject,
                          tun: ParcelFileDescriptor, cancel: ParcelFileDescriptor, ready: () -> Unit): Int {
        socketProtection = { fd ->
            runCatching {
                check(service.protect(fd))
                ParcelFileDescriptor.fromFd(fd).use { network.bindSocket(it.fileDescriptor) }
                true
            }.getOrDefault(false)
        }
        try {
            if (outbound.has("shoesClient")) {
                try { return ShoesEngine.run(outbound, network, tun, cancel, requireNotNull(socketProtection), ready) }
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
                .put("outbounds",JSONArray().put(outbound))
            invoke("runXray",JSONObject().put("xrayJson",config.toString()))
            ready()
            ParcelFileDescriptor.AutoCloseInputStream(cancel).use { it.read() }
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
