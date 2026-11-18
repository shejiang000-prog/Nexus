package dev.nexus.localclient

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec

object Identity {
    private const val alias = "nexus-device-v1"
    private fun store() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    private fun aliases(keys: KeyStore) = keys.aliases().toList().filter { it == alias || it.startsWith("$alias-") }.sortedDescending()
    @Synchronized fun publicKey(): ByteArray = key(false)
    @Synchronized fun rotate(): ByteArray = key(true)
    private fun key(rotate: Boolean): ByteArray {
        val keys = store()
        val known = aliases(keys)
        require(!rotate || known.size < 32)
        val selected = if (rotate) "$alias-${System.currentTimeMillis()}" else known.firstOrNull() ?: alias
        if (!keys.containsAlias(selected)) {
            KeyPairGenerator.getInstance("EC", "AndroidKeyStore").apply {
                initialize(KeyGenParameterSpec.Builder(selected, KeyProperties.PURPOSE_SIGN)
                    .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                    .setDigests(KeyProperties.DIGEST_SHA256).build())
            }.generateKeyPair()
        }
        return keys.getCertificate(selected).publicKey.encoded
    }
    fun owner(publicKey: ByteArray): String? {
        val keys = store()
        return aliases(keys).firstOrNull { keys.getCertificate(it).publicKey.encoded.contentEquals(publicKey) }
    }
    fun sign(bytes: ByteArray, signingAlias: String): ByteArray = Signature.getInstance("SHA256withECDSA").run {
        initSign(store().getKey(signingAlias, null) as PrivateKey)
        update(bytes)
        sign()
    }
}

data class Profile(val name: String, val server: String, val tlsName: String, val target: String,
                   val ca: ByteArray, val device: ByteArray, val signingAlias: String, val dns: String? = null) {
    // Loopback belongs to the phone itself. Represent server-local and IPv6 targets with an IPv4 alias.
    val vpnHost: String get() = target.substringBeforeLast(':').let {
        if (it.startsWith("127.") || it.startsWith("[") || it == "192.0.2.2") "192.0.2.1" else it
    }
    val vpnEndpoint: String get() = vpnHost + ":" + target.substringAfterLast(':')
    val fingerprint: String get() = MessageDigest.getInstance("SHA-256").digest(ca)
        .joinToString(":") { "%02X".format(it) }
    val vpnSummary: String get() = dns?.let { "全设备 VPN · DNS $it" } ?: vpnEndpoint
    private fun json() = JSONObject().put("version", if (dns == null) 1 else 2).put("dns", dns).put("name", name).put("server", server)
        .put("tls_name", tlsName).put("target", target)
        .put("ca_der", Base64.encodeToString(ca, Base64.NO_WRAP))
        .put("device_der", Base64.encodeToString(device, Base64.NO_WRAP))
    fun save(context: Context) {
        val prefs = context.getSharedPreferences("local", 0)
        val data = json().toString()
        val saved = JSONObject(prefs.getString("servers", "{}")!!)
        val key = server + "/" + fingerprint
        prefs.getString("profile", null)?.let { old ->
            val previous = runCatching { parse(old) }.getOrNull()
            if (previous != null) saved.put(previous.server + "/" + previous.fingerprint, JSONObject(old))
        }
        val pinned = prefs.getString("pinned", null)
        require(pinned == null || (pinned == key && dns != null)) { "请先解除固定出口" }
        require(saved.has(key) || saved.length() < 64)
        saved.put(key, JSONObject(data))
        check(prefs.edit().putString("servers", saved.toString()).putString("profile", data).commit())
    }
    fun moveEndpoint(context: Context, address: String): Profile {
        val prefs = context.getSharedPreferences("local",0)
        require(!prefs.contains("pinned") && load(context)?.server == server)
        val data = json().put("server",address)
        val moved = parse(data.toString())
        val old = "$server/$fingerprint"
        val next = "$address/$fingerprint"
        val servers = JSONObject(prefs.getString("servers","{}")!!)
        require(next != old && !servers.has(next))
        servers.remove(old)
        servers.put(next,data)
        val edit = prefs.edit().putString("servers",servers.toString()).putString("profile",data.toString())
        prefs.getStringSet("apps/$old",null)?.let { edit.putStringSet("apps/$next",it.toSet()).remove("apps/$old") }
        for (setting in listOf("monitor","hold")) if (prefs.contains("$setting/$old"))
            edit.putBoolean("$setting/$next",prefs.getBoolean("$setting/$old",false)).remove("$setting/$old")
        prefs.getString("exit/$old",null)?.let { edit.putString("exit/$next",it).remove("exit/$old") }
        check(edit.commit())
        return moved
    }
    companion object {
        fun servers(context: Context): List<Profile> {
            val prefs = context.getSharedPreferences("local", 0)
            val saved = JSONObject(prefs.getString("servers", "{}")!!)
            return (saved.keys().asSequence().mapNotNull { runCatching { parse(saved.getJSONObject(it).toString()) }.getOrNull() }.toList() +
                listOfNotNull(runCatching { load(context) }.getOrNull())).distinctBy { it.server + "/" + it.fingerprint }
        }
        fun parse(text: String): Profile {
            require(text.toByteArray().size <= 32768) { "配置文件超过 32 KiB" }
            val j = JSONObject(text)
            val version = j.getInt("version")
            require(version in 1..2) { "配置版本不支持" }
            fun field(name: String, max: Int) = j.getString(name).also {
                require(it.length in 1..max && it.none { c -> c.isISOControl() }) { "配置字段无效" }
            }
            fun endpoint(key: String): String = field(key, 80).also {
                val host = it.substringBeforeLast(':')
                val v4 = host.split('.').let { parts -> parts.size == 4 && parts.all { p ->
                    p.toIntOrNull() in 0..255 && p == p.toIntOrNull().toString() } }
                val v6 = host.startsWith("[") && host.endsWith("]") && host.contains(':') &&
                    host.substring(1, host.length - 1).matches(Regex("[0-9a-fA-F:.]+"))
                require((v4 || v6) && it.substringAfterLast(':').toIntOrNull() in 1..65535) { "请使用 IP 地址及有效端口" }
            }
            val cf = CertificateFactory.getInstance("X.509")
            fun certificate(key: String) = (cf.generateCertificate(Base64.decode(field(key, 16000),
                Base64.DEFAULT).inputStream()) as X509Certificate).apply { checkValidity() }
            val ca = certificate("ca_der")
            val device = certificate("device_der")
            require(ca.basicConstraints >= 0) { "信任根需要 CA 证书" }
            device.verify(ca.publicKey)
            val signingAlias = requireNotNull(Identity.owner(device.publicKey.encoded)) { "此配置属于另一台设备" }
            return Profile(field("name", 64), endpoint("server"), field("tls_name", 253), endpoint("target"),
                ca.encoded, device.encoded, signingAlias, if (version == 2) field("dns", 15).also { ip ->
                    require(ip.split('.').let { parts -> parts.size == 4 && parts.all { p ->
                        p.toIntOrNull() in 0..255 && p == p.toIntOrNull().toString() } } &&
                        ip.substringBefore('.').toInt() in 1..223 && !ip.startsWith("127.")) { "DNS 需要可路由的 IPv4 地址" }
                } else null)
        }
        fun load(context: Context) = context.getSharedPreferences("local", 0).getString("profile", null)?.let(::parse)
    }
}
