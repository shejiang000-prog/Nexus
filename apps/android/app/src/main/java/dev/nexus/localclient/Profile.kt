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
    @Synchronized fun publicKey(): ByteArray {
        val keys = store()
        if (!keys.containsAlias(alias)) {
            KeyPairGenerator.getInstance("EC", "AndroidKeyStore").apply {
                initialize(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                    .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                    .setDigests(KeyProperties.DIGEST_SHA256).build())
            }.generateKeyPair()
        }
        return keys.getCertificate(alias).publicKey.encoded
    }
    fun sign(bytes: ByteArray): ByteArray = Signature.getInstance("SHA256withECDSA").run {
        initSign(store().getKey(alias, null) as PrivateKey)
        update(bytes)
        sign()
    }
}

data class Profile(val name: String, val server: String, val tlsName: String, val target: String,
                   val ca: ByteArray, val device: ByteArray) {
    val fingerprint: String get() = MessageDigest.getInstance("SHA-256").digest(ca)
        .joinToString(":") { "%02X".format(it) }
    private fun json() = JSONObject().put("version", 1).put("name", name).put("server", server)
        .put("tls_name", tlsName).put("target", target)
        .put("ca_der", Base64.encodeToString(ca, Base64.NO_WRAP))
        .put("device_der", Base64.encodeToString(device, Base64.NO_WRAP))
    fun save(context: Context) {
        check(context.getSharedPreferences("local", 0).edit().putString("profile", json().toString()).commit())
    }
    companion object {
        fun parse(text: String): Profile {
            require(text.toByteArray().size <= 32768) { "配置文件超过 32 KiB" }
            val j = JSONObject(text)
            require(j.getInt("version") == 1) { "配置版本不支持" }
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
            require(device.publicKey.encoded.contentEquals(Identity.publicKey())) { "此配置属于另一台设备" }
            return Profile(field("name", 64), endpoint("server"), field("tls_name", 253), endpoint("target"),
                ca.encoded, device.encoded)
        }
        fun load(context: Context) = context.getSharedPreferences("local", 0).getString("profile", null)?.let(::parse)
    }
}
