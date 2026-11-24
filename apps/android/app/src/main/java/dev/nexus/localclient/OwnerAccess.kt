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
    fun forget(address: String) {
        val host = SshEndpoint.parse(address).authority
        val edit = prefs.edit().remove("key/$host").remove("ready/$host").remove("v2/$host")
        if (current == host) edit.remove("current")
        check(edit.commit())
    }

    fun checkHost(address: String): String {
        val endpoint = SshEndpoint.parse(address)
        val pin = NodeManager.discover(endpoint)
        knownPin(endpoint.authority)?.let { if (it != pin) throw ChangedServer() }
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

    fun manager(address: String): NodeManager {
        val endpoint = SshEndpoint.parse(address)
        val host = endpoint.authority
        val json = entry(host)
        val pin = json.getString("pin")
        return NodeManager(context,endpoint.host,endpoint.port,"root","",pin, loadKey = {
        val blob = Base64.decode(json.getString("key"),Base64.DEFAULT)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE,wrappingKey(), GCMParameterSpec(128,blob,0,12))
        cipher.updateAAD("$host/$pin".toByteArray())
        cipher.doFinal(blob,12,blob.size-12)
        })
    }


    fun bindPrivate(address: String, pin: String, password: String, deploy: Boolean, domain: String, extraProtocols: Set<String>): V2Node? {
        val endpoint = SshEndpoint.parse(address)
        val host = endpoint.authority
        val json = prepare(host, pin)
        val bootstrap = NodeManager(context, endpoint.host, endpoint.port, "root", password, pin)
        bootstrap.authorize(json.getString("public"), deploy)
        val manager = manager(host)
        if (deploy) manager.installV2(domain, extraProtocols)
        manager.v2Devices()
        check(prefs.edit().putBoolean("ready/$host", true).putBoolean("v2/$host", true).putString("current", host).commit())
        return if (deploy) V2Devices.enroll(context, V2Invite.parse(manager.v2Invite())) else null
    }

}

internal const val INVITE_MESSAGE = "复制这条消息后打开 Nexus"
internal fun friendlyError(e: Exception): String = when {
    e is ChangedServer -> "服务器身份已变化，操作已停止。请先向服务器提供商核实。"
    // JSch's TCP dial uses this marker exclusively for Socket.connect timeouts.
    e is com.jcraft.jsch.JSchException && e.message == "timeout: socket is not established" &&
        e.cause is java.net.SocketTimeoutException -> "无法连接到服务器。这个 IP 可能在国内无法访问，可以在服务商处更换 IP 后重试。"
    e is com.jcraft.jsch.JSchException && e.cause is java.net.ConnectException &&
        listOf(e.message, e.cause?.message).any {
            it?.contains("Connection refused", ignoreCase = true) == true || it?.contains("ECONNREFUSED") == true
        } -> "连不上服务器的 SSH。如果服务商给的 SSH 端口不是 22，请填写 IP:端口。"
    e is CompatLinkFailure -> e.message!!
    e is com.jcraft.jsch.JSchException && e.message.orEmpty().contains("auth",true) -> "登录失败，请检查服务器密码或本机的管理授权。"
    e.message in listOf("请填写有效的服务器 IP","请填写有效的域名","这台服务器已经部署，请选择绑定","尚未找到部署，请选择部署") -> e.message!!
    else -> "操作未完成。请检查网络和服务器状态后重试。"
}
