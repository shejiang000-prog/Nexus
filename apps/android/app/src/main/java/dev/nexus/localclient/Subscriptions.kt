package dev.nexus.localclient

import android.content.Context
import android.graphics.BitmapFactory
import android.net.Uri
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import com.google.zxing.BinaryBitmap
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import org.json.JSONArray
import org.json.JSONObject
import org.yaml.snakeyaml.LoaderOptions
import org.yaml.snakeyaml.Yaml
import org.yaml.snakeyaml.DumperOptions
import org.yaml.snakeyaml.constructor.SafeConstructor
import org.yaml.snakeyaml.nodes.Tag
import org.yaml.snakeyaml.representer.Representer
import org.yaml.snakeyaml.resolver.Resolver
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.net.URL
import java.security.KeyStore
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.net.ssl.HttpsURLConnection

private const val MAX_BYTES = 512 * 1024
private val protocols = setOf("vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "tuic", "anytls")
internal fun nodeKey(source: String, node: JSONObject) = source + ":" + Base64.encodeToString(
    java.security.MessageDigest.getInstance("SHA-256").digest(node.toString().toByteArray()),Base64.NO_WRAP)
internal fun InputStream.bounded(maximum: Int): ByteArray = use { input ->
    val output = ByteArrayOutputStream()
    val buffer = ByteArray(8192)
    while (true) {
        val n = input.read(buffer)
        if (n < 0) break
        require(output.size() + n <= maximum)
        output.write(buffer, 0, n)
    }
    output.toByteArray()
}
/**
 * Keeps every YAML scalar except true/false and null as text. YAML 1.1 would otherwise read a
 * REALITY short-id such as 0123 as octal 83, or a password such as 1_000 as the number 1000.
 */
private class TextResolver : Resolver() {
    override fun addImplicitResolvers() {
        addImplicitResolver(Tag.BOOL, java.util.regex.Pattern.compile("^(?:true|True|TRUE|false|False|FALSE)$"), "tTfF")
        addImplicitResolver(Tag.NULL, NULL, "~nN\u0000")
        addImplicitResolver(Tag.NULL, EMPTY, null)
    }
}

private fun decode64(s: String): String = runCatching { Base64.decode(s, Base64.DEFAULT) }
    .recoverCatching { Base64.decode(s, Base64.URL_SAFE) }.getOrThrow().toString(Charsets.UTF_8)

data class Subscription(val id: String, val label: String, val url: String, val nodes: JSONArray,
                        val usage: String, val updated: Long) {
    internal val allowance by lazy { Usage.parse(usage, nodes) }
    fun json() = JSONObject().put("label", label).put("url", url).put("nodes", nodes)
        .put("usage", usage).put("updated", updated)
}

class Subscriptions(context: Context, private val download: ((String) -> Pair<String,String>)? = null) {
    internal fun interval(source: Subscription) = if (source.url.isEmpty()) 0 else appearance.getInt("refresh/"+source.id,12)
    private val prefs = context.getSharedPreferences("subscriptions", Context.MODE_PRIVATE)
    internal val appearance = context.getSharedPreferences("subscription-layout",Context.MODE_PRIVATE)
    private val resolver = context.contentResolver
    private val key: SecretKey get() = synchronized(Subscriptions::class.java) {
        val alias = "nexus-subscriptions-aes-v1"
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        store.getKey(alias, null) as? SecretKey ?: KeyGenerator.getInstance("AES", "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }
    private fun save(s: Subscription) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key)
        cipher.updateAAD(s.id.toByteArray())
        val bytes = cipher.iv + cipher.doFinal(s.json().toString().toByteArray())
        check(prefs.edit().putString(s.id, Base64.encodeToString(bytes, Base64.NO_WRAP)).commit())
    }
    fun get(id: String): Subscription {
        val bytes = Base64.decode(requireNotNull(prefs.getString(id,null)), Base64.NO_WRAP)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, bytes, 0, 12))
        cipher.updateAAD(id.toByteArray())
        val j = JSONObject(cipher.doFinal(bytes, 12, bytes.size - 12).toString(Charsets.UTF_8))
        return Subscription(id, j.getString("label"), j.getString("url"), j.getJSONArray("nodes"),
            j.getString("usage"), j.getLong("updated"))
    }
    fun all(): List<Subscription> = prefs.all.keys.map { id ->
        runCatching { get(id) }.getOrElse { Subscription(id,"本机来源无法读取","",JSONArray(),"",0L) }
    }.sortedWith(compareByDescending<Subscription> { appearance.getBoolean("pin/"+it.id,false) }
        .thenBy { appearance.getInt("order/"+it.id,Int.MAX_VALUE) }.thenBy { it.label })

    fun move(sources: List<Subscription>, id: String, step: Int) {
        val order = sources.toMutableList()
        val at = order.indexOfFirst { it.id==id }
        val next = at+step
        if (at<0 || next !in order.indices || appearance.getBoolean("pin/"+id,false)!=appearance.getBoolean("pin/"+order[next].id,false)) return
        order.add(next,order.removeAt(at))
        val edit = appearance.edit()
        order.forEachIndexed { i, s -> edit.putInt("order/"+s.id,i) }
        check(edit.commit())
    }

    fun remove(id: String) = synchronized(SubscriptionRefresh) {
        check(prefs.edit().remove(id).commit())
        val edit = appearance.edit()
        for (field in listOf("pin/","order/","fold/","mark/","color/","auto/","refresh/","attempt/","error/","failures/")) edit.remove(field+id)
        edit.apply()
    }

    fun readFile(uri: Uri): String {
        require(uri.scheme == "content")
        if (resolver.getType(uri)?.startsWith("image/") != true)
            return requireNotNull(resolver.openInputStream(uri)).bounded(MAX_BYTES).toString(Charsets.UTF_8)
        val bytes = requireNotNull(resolver.openInputStream(uri)).bounded(8 * 1024 * 1024)
        val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)
        require(options.outWidth in 1..16000 && options.outHeight in 1..16000)
        options.inJustDecodeBounds = false
        while (maxOf(options.outWidth, options.outHeight) / options.inSampleSize.coerceAtLeast(1) > 1600)
            options.inSampleSize = options.inSampleSize.coerceAtLeast(1) * 2
        val bitmap = requireNotNull(BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options))
        return try {
            val pixels = IntArray(bitmap.width * bitmap.height)
            bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
            QRCodeReader().decode(BinaryBitmap(HybridBinarizer(RGBLuminanceSource(bitmap.width, bitmap.height, pixels)))).text
        } finally { bitmap.recycle() }
    }

    fun import(label: String, input: String, previous: Subscription? = null): Subscription {
        require(label.isNotBlank() && label.length <= 64 && input.toByteArray().size <= MAX_BYTES)
        require(previous != null || prefs.all.size < 32)
        val source = input.trim()
        val url = if (!source.contains('\n') && source.startsWith("https://")) source else ""
        var usage = ""
        val content = if (url.isEmpty()) source else (download?.invoke(url) ?: fetch(url)).let { (body, info) -> usage = info; body }
        val nodes = parse(content)
        synchronized(SubscriptionRefresh) {
            if (previous != null) {
                check(previous.id !in SubscriptionRefresh.protectedSources)
                check(get(previous.id).updated == previous.updated)
            }
            return Subscription(previous?.id ?: UUID.randomUUID().toString(), label.trim(), url, nodes, usage,
                maxOf(System.currentTimeMillis(), (previous?.updated ?: 0) + 1)).also(::save)
        }
    }

    /** Downloads a subscription. Providers commonly redirect to a CDN; only HTTPS hops are followed. */
    private fun fetch(url: String): Pair<String, String> {
        var target = URL(url)
        repeat(4) { hop ->
            val connection = target.openConnection() as HttpsURLConnection
            try {
                connection.instanceFollowRedirects = false
                connection.connectTimeout = 10000
                connection.readTimeout = 15000
                connection.useCaches = false
                // Panels pick the format by User-Agent; "v2rayN" selects Base64 share links.
                connection.setRequestProperty("User-Agent", "Nexus/0.1.0 (compatible; v2rayN)")
                val code = connection.responseCode
                if (code in setOf(301, 302, 303, 307, 308) && hop < 3) {
                    val next = URL(target, requireNotNull(connection.getHeaderField("Location")))
                    require(next.protocol == "https" && next.userInfo == null)
                    target = next
                    return@repeat
                }
                check(code == 200)
                val usage = connection.getHeaderField("subscription-userinfo").orEmpty().take(256)
                    .split(';').map { it.trim() }.filter { it.matches(Regex("(upload|download|total|expire)=[0-9]{1,19}")) }.joinToString(";")
                return connection.inputStream.bounded(MAX_BYTES).toString(Charsets.UTF_8) to usage
            } finally { connection.disconnect() }
        }
        error("too many redirects")
    }
    private fun parse(content: String): JSONArray {
        val raw = content.trim().removePrefix("\uFEFF").trim()
        val body = if (raw.matches(Regex("[A-Za-z0-9+/=_\\s-]+"))) decode64(raw).trim() else raw
        val nodes = JSONArray()
        fun add(type: String, name: String, data: Any) {
            require(nodes.length() < 2048)
            if (type in protocols) {
                val label = name.filterNot { it.isISOControl() }.take(96).ifBlank { type.uppercase() }
                nodes.put(JSONObject().put("type", type).put("name", label).put("config", data))
            }
        }
        if (body.startsWith("{") && runCatching { JSONObject(body).optString("format") }.getOrNull() == "nexus-nodes/1") {
            val items = JSONObject(body).getJSONArray("nodes")
            require(items.length() in 1..2048)
            for (index in 0 until items.length()) {
                val n = items.getJSONObject(index)
                val config = n.get("config")
                require(config is String || config is JSONObject)
                add(n.getString("type"),n.getString("name"),config)
            }
        } else if (!body.startsWith("{") && body.lineSequence().any { it.trim().substringBefore("://") in protocols }) {
            for (line in body.lineSequence().map { it.trim() }.filter { it.isNotEmpty() }) {
                require(line.length <= 16384)
                val type = line.substringBefore("://")
                if (type !in protocols) continue
                val uri = Uri.parse(line)
                val name = if (type == "vmess") runCatching {
                    JSONObject(decode64(line.substringAfter("://").substringBefore('#'))).optString("ps")
                }.getOrDefault("") else ""
                add(type, uri.fragment ?: name, line)
            }
        } else {
            val options = LoaderOptions().apply { codePointLimit = MAX_BYTES; maxAliasesForCollections = 0
                nestingDepthLimit = 24; isAllowDuplicateKeys = false }
            val root = Yaml(SafeConstructor(options), Representer(DumperOptions()), DumperOptions(), options, TextResolver()).load<Any>(body)
            val entries = if (root is Map<*, *>) root["proxies"] ?: root["outbounds"] else root
            for (entry in entries as List<*>) {
                val item = entry as? Map<*, *> ?: continue
                val type = item["type"]?.toString() ?: continue
                add(if (type == "shadowsocks") "ss" else type, (item["name"] ?: item["tag"] ?: "").toString(), JSONObject(item))
            }
        }
        require(nodes.length() > 0)
        return nodes
    }
}
