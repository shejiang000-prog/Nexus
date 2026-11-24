package dev.nexus.localclient

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import com.jcraft.jsch.JSch
import com.jcraft.jsch.KeyPair
import java.io.ByteArrayOutputStream
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import org.json.JSONObject

internal class ChangedServer : Exception()
internal class OwnerAccess(private val context: Context) {
    private val prefs = context.getSharedPreferences("owner-access", 0)
    val hosts get() = prefs.all.keys.filter { it.startsWith("ready/") }.map { it.removePrefix("ready/") }.sorted()
    var current: String?
        get() = prefs.getString("current", null)
        set(value) { prefs.edit().putString("current", value).apply() }

    private fun wrappingKey(): SecretKey {
        val alias = "nexus-owner-ssh-v1"
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        return store.getKey(alias,null) as? SecretKey ?: KeyGenerator.getInstance("AES","AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes("GCM").setEncryptionPaddings("NoPadding").build())
            generateKey()
        }
    }

    private fun encode(data: ByteArray) = Base64.encodeToString(data, Base64.NO_WRAP)
    private fun entry(host: String) = JSONObject(requireNotNull(prefs.getString("key/$host", null)))

    fun knownPin(host: String) = prefs.getString("key/$host",null)?.let { JSONObject(it).getString("pin") }

    /** Drops the pinned identity and local management key after the owner says the server was reinstalled. */
    fun forget(host: String) {
        val edit = prefs.edit().remove("key/$host").remove("ready/$host").remove("v2/$host")
        if (current == host) edit.remove("current")
        check(edit.commit())
    }

    fun checkHost(host: String): String {
        require(nodeIP(host) != null) { "请填写有效的服务器 IP" }
        val pin = NodeManager.discover(host)
        knownPin(host)?.let { if (it != pin) throw ChangedServer() }
        return pin
    }

    @Synchronized private fun prepare(host: String, pin: String): JSONObject {
        if (prefs.contains("key/$host")) return entry(host).also { if (it.getString("pin") != pin) throw ChangedServer() }
        val pair = try { KeyPair.genKeyPair(JSch(), KeyPair.ED25519) }
            catch (_: Exception) { KeyPair.genKeyPair(JSch(), KeyPair.ECDSA, 256) }
        try {
            val output = object:ByteArrayOutputStream(){
                fun wipe(){buf.fill(0);reset()}
            }
            val raw = try {pair.writePrivateKey(output);output.toByteArray()} finally {output.wipe()}
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            val locked = try {
                cipher.init(Cipher.ENCRYPT_MODE,wrappingKey())
                cipher.updateAAD("$host/$pin".toByteArray())
                cipher.doFinal(raw)
            } finally { raw.fill(0) }
            output.reset()
            pair.writePublicKey(output,"Nexus")
            val json = JSONObject().put("pin",pin).put("public",output.toString("US-ASCII").trim())
                .put("key",encode(cipher.iv + locked))
            check(prefs.edit().putString("key/$host",json.toString()).commit())
            return json
        } finally { pair.dispose() }
    }

    fun manager(host: String): NodeManager {
        val json = entry(host)
        val pin = json.getString("pin")
        return NodeManager(context,host,22,"root","",pin, loadKey = {
        val blob = Base64.decode(json.getString("key"),Base64.DEFAULT)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE,wrappingKey(), GCMParameterSpec(128,blob,0,12))
        cipher.updateAAD("$host/$pin".toByteArray())
        cipher.doFinal(blob,12,blob.size-12)
        })
    }

    fun isV2(host: String) = prefs.getBoolean("v2/$host", false)

    fun bindPrivate(host: String, pin: String, password: String, deploy: Boolean, domain: String, extraProtocols: Set<String>): V2Node? {
        val json = prepare(host, pin)
        val bootstrap = NodeManager(context, host, 22, "root", password, pin)
        bootstrap.authorize(json.getString("public"), deploy, v2 = true)
        val manager = manager(host)
        if (deploy) manager.installV2(domain, extraProtocols)
        val v2 = manager.isV2()
        if (v2) manager.v2Devices() else manager.owner("devices")
        check(prefs.edit().putBoolean("ready/$host", true).putBoolean("v2/$host", v2).putString("current", host).commit())
        return if (deploy && v2) V2Devices.enroll(context, V2Invite.parse(manager.v2Invite())) else null
    }

    fun enroll(host: String, pin: String, password: String, deploy: Boolean, dns: String, port: Int, claim: Int): Profile? {
        val json = prepare(host,pin)
        val bootstrap = NodeManager(context,host,22,"root",password,pin)
        bootstrap.authorize(json.getString("public"), deploy)
        return manager(host).let { manager ->
            val result = if (deploy) manager.deploy("我的服务器",dns,port,claim) else {
                manager.owner("devices")
                null
            }
            check(prefs.edit().putBoolean("ready/$host",true).putString("current",host).commit())
            result
        }
    }
}

internal fun invitationLimits(days: Int = 7, count: Int = 1, quota: Long? = null): JSONObject {
    require(days in 1..365 && count in 1..128 && (quota == null || quota in 1..(1L shl 60)))
    return JSONObject().put("name","私人邀请").put("valid_seconds",minOf(days,7)*86400)
        .put("device_seconds",days*86400).put("uses",count).put("max_devices",count).put("quota",quota)
}

internal const val INVITE_MESSAGE = "复制这条消息后打开 Nexus"
internal fun sharedInvitation(text: String): Invitation? {
    if (text.length > 4200) return null
    val link = text.trim().removePrefix(INVITE_MESSAGE+"\n")
    return runCatching { Invitation.parse(link) }.getOrNull()
}
internal fun friendlyError(e: Exception): String = when {
    e is ChangedServer -> "服务器身份已变化，操作已停止。请先向服务器提供商核实。"
    e is CompatLinkFailure -> e.message!!
    e is com.jcraft.jsch.JSchException && e.message.orEmpty().contains("auth",true) -> "登录失败，请检查服务器密码或本机的管理授权。"
    e.message in listOf("请填写有效的服务器 IP","请填写有效的域名","请先解除固定出口","这台服务器已经部署，请选择绑定","尚未找到部署，请选择部署") -> e.message!!
    else -> "操作未完成。请检查网络和服务器状态后重试。"
}
