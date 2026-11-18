package dev.nexus.localclient

import android.net.Network
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.system.Os
import android.system.OsConstants
import android.system.StructPollfd
import com.shoesproxy.ShoesNative
import org.json.JSONArray
import org.json.JSONObject

object ShoesEngine {
    val types = setOf("tuic","anytls")
    fun outbound(node: JSONObject): JSONObject {
        val type = node.getString("type")
        require(type in types)
        val raw = node.get("config")
        val host: String
        val port: Int
        val sni: String
        val password: String
        val uuid: String?
        val alpn: List<String>
        if (raw is String) {
            val uri = Uri.parse(raw)
            require(uri.scheme == type && uri.getQueryParameter("allowInsecure") !in setOf("1","true") &&
                uri.getQueryParameter("insecure") !in setOf("1","true"))
            require(uri.queryParameterNames.all { it in setOf("sni","alpn","allowInsecure","insecure","udp_relay_mode") })
            require(uri.getQueryParameter("udp_relay_mode") in setOf(null,"native"))
            host = requireNotNull(uri.host).removeSurrounding("[","]")
            port = if (uri.port == -1) 443 else uri.port
            val auth = requireNotNull(uri.userInfo)
            uuid = if (type=="tuic") auth.substringBefore(':') else null
            password = if (type=="tuic") auth.substringAfter(':',"") else auth
            sni = uri.getQueryParameter("sni") ?: host
            alpn = uri.getQueryParameter("alpn")?.split(',') ?: emptyList()
        } else {
            val j = raw as JSONObject
            val tls = j.optJSONObject("tls")
            require(tls==null || tls.keys().asSequence().all { it in setOf("enabled","insecure","server_name","alpn") })
            require(!j.has("ca") && !j.has("ca-str") && !j.has("certificate") && !j.has("fingerprint") && !j.has("ech-opts"))
            require(!j.optBoolean("skip-cert-verify") && tls?.optBoolean("insecure") != true &&
                !j.has("detour") && !j.has("dialer-proxy") && !j.has("ip") && !j.has("disable-sni") &&
                j.optString("udp-relay-mode","native")=="native")
            host = j.getString("server").removeSurrounding("[","]")
            port = j.optInt("port",j.optInt("server_port"))
            sni = j.optString("sni",tls?.optString("server_name",host) ?: host)
            uuid = if (type=="tuic") j.getString("uuid") else null
            password = j.getString("password")
            val values = j.optJSONArray("alpn") ?: tls?.optJSONArray("alpn")
            alpn = if (values == null) emptyList() else (0 until values.length()).map(values::getString)
        }
        require(host.matches(Regex("[A-Za-z0-9.:_-]{1,253}")) && port in 1..65535 && password.length in 1..1024)
        require(sni.matches(Regex("[A-Za-z0-9.:_-]{1,253}")) && alpn.all { it.length in 1..32 })
        val proto = JSONObject().put("type",type).put("password",password).put("udp_enabled",true)
        val tls = JSONObject().put("sni_hostname",sni).put("verify",true)
        if (alpn.isNotEmpty()) tls.put("alpn_protocols",JSONArray(alpn))
        val client = JSONObject().put("address",(if (':' in host) "[$host]" else host)+":"+port)
        if (type=="tuic") {
            proto.put("uuid",java.util.UUID.fromString(uuid).toString())
            client.put("protocol",proto).put("quic_settings",tls)
        } else client.put("protocol",tls.put("type","tls").put("protocol",proto))
        return JSONObject().put("shoesClient",client).put("bootstrap",host)
    }
    fun run(out: JSONObject, physical: Network, tun: ParcelFileDescriptor, cancel: ParcelFileDescriptor,
            protect: (Int) -> Boolean, ready: () -> Unit): Int {
        val client = JSONObject(out.getJSONObject("shoesClient").toString())
        val ip = physical.getAllByName(out.getString("bootstrap")).first().hostAddress!!
        val port = client.getString("address").substringAfterLast(':')
        client.put("address",(if (':' in ip) "[$ip]" else ip)+":"+port)
        val rule = JSONObject().put("masks",JSONArray().put("0.0.0.0/0").put("::/0")).put("action","allow").put("client_chain",client)
        val settings = JSONObject().put("device_fd",tun.fd).put("mtu",1500).put("tcp_enabled",true).put("udp_enabled",true)
            .put("icmp_enabled",false).put("rules",JSONArray().put(rule))
        check(ShoesNative.init("off")==0)
        val handle = ShoesNative.start(JSONArray().put(settings).toString()) { fd -> protect(fd) }
        check(handle>0)
        try {
            val poll = StructPollfd().apply { fd=cancel.fileDescriptor; events=OsConstants.POLLIN.toShort() }
            var started = false
            while (Os.poll(arrayOf(poll), if (started) 1000 else 300)==0) {
                check(ShoesNative.isRunning())
                if (!started) { ready(); started = true }
            }
            return 0
        } finally { ShoesNative.stop(handle) }
    }
}
