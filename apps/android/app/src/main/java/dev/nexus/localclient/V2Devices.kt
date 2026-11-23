package dev.nexus.localclient

import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import enrollment.Enrollment
import enrollment.Invitation
import mobile.Signer
import org.json.JSONObject
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.util.Base64

internal class V2Invite private constructor(internal val value: Invitation) {
    val host: String get() = value.host()
    val authority: String get() = value.authority()
    val name: String get() = value.name()

    companion object {
        fun parse(raw: String): V2Invite {
            try {
                return V2Invite(Enrollment.parse(raw.trim()))
            } catch (e: Exception) {
                // Never include input, native exception text, or a chained cause in a report.
                throw IllegalArgumentException(if (e.message == "此版本尚不支持带证书固定值的邀请") e.message else "V2 邀请格式无效")
            }
        }
    }
}

internal class V2Node(
    val key: String, val authority: String, val name: String, val deviceId: String,
    val expiresAt: Long?, val expiryKnown: Boolean, val endpoint: String, val ca: ByteArray
) {
    val host: String get() = authority.substringBeforeLast(':')
    val deviceIdBytes: ByteArray get() = ByteArray(16) { deviceId.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    fun signer(): Signer {
        val privateKey = keyStore().getKey(key, null) as? PrivateKey ?: error("本机设备密钥不可用")
        return Signer { message -> sign(privateKey, message) }
    }
}

private fun keyStore(): KeyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

private fun sign(key: PrivateKey, message: ByteArray): ByteArray =
    Signature.getInstance("SHA256withECDSA").run { initSign(key); update(message); sign() }

private fun makeKey(ctx: Context, alias: String) {
    fun generate(strong: Boolean) {
        val spec = KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
        if (strong && Build.VERSION.SDK_INT >= 28) spec.setIsStrongBoxBacked(true)
        KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore").apply {
            initialize(spec.build())
        }.generateKeyPair()
    }
    if (Build.VERSION.SDK_INT >= 28 && ctx.packageManager.hasSystemFeature(PackageManager.FEATURE_STRONGBOX_KEYSTORE)) {
        try {
            generate(true)
            return
        } catch (_: StrongBoxUnavailableException) {
            // Advertised StrongBox can be busy or lack a usable signing slot.
        }
    }
    generate(false)
}

internal object V2Devices {
    private fun prefs(ctx: Context) = ctx.getSharedPreferences("v2-devices", Context.MODE_PRIVATE)
    private val enc = Base64.getEncoder()
    private val dec = Base64.getDecoder()

    fun list(ctx: Context): List<V2Node> = prefs(ctx).all.keys.filter { it.startsWith("v2:") }.sorted().mapNotNull { get(ctx, it) }

    fun get(ctx: Context, key: String): V2Node? {
        if (!key.startsWith("v2:")) return null
        val j = prefs(ctx).getString(key, null)?.let(::JSONObject) ?: return null
        val authority = j.getString("authority")
        return V2Node(key, authority, j.getString("name"), j.getString("id"),
            if (j.has("expires")) j.getLong("expires") else null, j.getBoolean("expiryKnown"),
            j.optString("endpoint", authority), dec.decode(j.optString("ca", "")))
    }

    // The pending record contains only a request ID. A lost reply retries the same proof
    // identity with a fresh TLS exporter. The invitation bearer is never written to disk.
    @Synchronized
    fun enroll(context: Context, invite: V2Invite, endpoint: String = invite.authority, ca: ByteArray = byteArrayOf()): V2Node {
        val key = "v2:" + invite.value.localID()
        get(context, key)?.let { return it }
        val prefs = prefs(context)
        val store = keyStore()
        if (!store.containsAlias(key)) makeKey(context, key)
        val privateKey = store.getKey(key, null) as? PrivateKey ?: error("设备密钥不可用")
        val pub = store.getCertificate(key).publicKey as ECPublicKey
        val xb = pub.w.affineX.toByteArray()
        val compressed = ByteArray(33)
        compressed[0] = if (pub.w.affineY.testBit(0)) 3 else 2
        val n = minOf(32, xb.size)
        xb.copyInto(compressed, 33 - n, xb.size - n)
        val pending = "request/" + key
        val request = prefs.getString(pending, null)?.let(dec::decode) ?: ByteArray(16).also {
            SecureRandom().nextBytes(it)
            check(prefs.edit().putString(pending, enc.encodeToString(it)).commit()) { "无法保存注册请求" }
        }
        val dial = if (endpoint == invite.authority) {
            val cm = context.getSystemService(ConnectivityManager::class.java)
            val network = requireNotNull(cm.activeNetwork) { "当前无可用网络" }
            val ip = nodeAddress(network, invite.host).hostAddress ?: error("无法解析节点域名")
            (if (':' in ip) "[$ip]" else ip) + ":" + invite.authority.substringAfterLast(':')
        } else endpoint
        val result = Enrollment.redeem(invite.value, dial, ca, request, compressed) { message -> sign(privateKey, message) }
        val data = JSONObject().put("authority", invite.authority).put("name", invite.name.ifBlank { invite.host })
            .put("id", result.deviceID).put("expiryKnown", result.expiryKnown)
        if (result.expiresAt > 0) data.put("expires", result.expiresAt)
        if (endpoint != invite.authority) data.put("endpoint", endpoint)
        if (ca.isNotEmpty()) data.put("ca", enc.encodeToString(ca))
        check(prefs.edit().putString(key, data.toString()).remove(pending).commit()) { "无法保存设备信息，请重试" }
        return checkNotNull(get(context, key))
    }

    @Synchronized
    fun remove(context: Context, key: String) {
        require(key.matches(Regex("v2:[a-f0-9]{64}"))) { "设备记录无效" }
        val local = context.getSharedPreferences("local", Context.MODE_PRIVATE)
        val edit = local.edit().remove("apps/$key").remove("exit/$key")
        for (field in listOf(TunnelService.LAST, TunnelService.RESUME)) {
            val saved = local.getString(field, null)?.let { runCatching { android.content.Intent.parseUri(it, 0) }.getOrNull() }
            if (saved?.action == "v2" && saved.getStringExtra("v2-key") == key) edit.remove(field)
        }
        check(edit.commit()) { "无法删除节点设置" }
        keyStore().deleteEntry(key)
        check(prefs(context).edit().remove(key).remove("request/" + key).commit()) { "无法删除设备记录" }
    }
}
