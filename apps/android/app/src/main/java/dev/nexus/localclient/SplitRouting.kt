package dev.nexus.localclient

import android.content.Context
import android.net.LinkProperties
import android.os.Build
import android.os.ParcelFileDescriptor
import android.system.Os
import android.system.OsConstants
import java.io.File
import java.io.FileDescriptor
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread
import org.json.JSONArray
import org.json.JSONObject

internal object SplitRouting {
    const val KEY = "bypassMainland"
    const val SHOES = "此节点不支持绕过大陆，将全局代理"
    fun enabled(context: Context) = context.getSharedPreferences("local", 0).getBoolean(KEY, true)
    fun label(enabled: Boolean) = if (enabled) "绕过大陆" else "全局代理"

    fun data(context: Context, vpnDns: String? = null, physical: LinkProperties? = null): ByteArray {
        val rules = run {
            if (BuildConfig.DEBUG) {
                val fixture = File(context.filesDir, "split-routing-test.txt")
                if (fixture.isFile) return@run fixture.readBytes().also { require(it.isNotEmpty()) }
            }
            context.assets.open("split-rules.txt").use { it.readBytes() }
        }
        // Automatic Private DNS would validate DoT on the VPN resolver and hide every query
        // name from the domestic split. Refusing that port makes Android use port 53. A
        // hostname (strict) setting is the user's explicit choice and stays untouched.
        val strict = Build.VERSION.SDK_INT >= 28 && physical?.privateDnsServerName != null
        if (vpnDns == null || strict) return rules
        return "refuse-dot:$vpnDns\n".toByteArray() + rules
    }

    // Xray still handles a TUN inbound. A datagram socketpair preserves IP packet
    // boundaries while the native layer intercepts domestic DNS and learned IPs.
    fun withXray(context: Context, tun: ParcelFileDescriptor, bytes: ByteArray, protect: (Int) -> Boolean,
                 run: (Int, File, () -> Boolean) -> Unit) {
        val a = FileDescriptor()
        val b = FileDescriptor()
        Os.socketpair(OsConstants.AF_UNIX, OsConstants.SOCK_DGRAM, 0, a, b)
        try {
            val stop = ParcelFileDescriptor.createSocketPair()
            val ready = CountDownLatch(1)
            val dns = File(context.cacheDir, "split-dns.sock")
            val native = Tunnel(protect) { ready.countDown() }
            val worker = thread(name = "Nexus split TUN") {
                try {
                    native.split(stop[0].detachFd(), ParcelFileDescriptor.dup(tun.fileDescriptor).detachFd(),
                        ParcelFileDescriptor.dup(a).detachFd(), dns.absolutePath, bytes)
                } finally { ready.countDown() }
            }
            try {
                check(ready.await(10, TimeUnit.SECONDS) && worker.isAlive)
                ParcelFileDescriptor.dup(b).use { run(it.fd, dns) { worker.isAlive } }
            } finally {
                stop[1].close()
                worker.join()
                stop[0].close()
                dns.delete()
            }
        } finally { Os.close(a); Os.close(b) }
    }

    /**
     * Connect by the sniffed domain instead of the address the app picked. Many subscription
     * nodes have no IPv6 egress; the TUN accepts an IPv6 connection locally before the node
     * fails, so the app never falls back to IPv4. With the domain, the node resolves it and
     * uses whatever family it has. Used in both bypass and global mode.
     */
    fun sniff(inbound: JSONObject) {
        inbound.put("sniffing", JSONObject().put("enabled", true)
            .put("destOverride", JSONArray(listOf("http","tls","quic"))).put("routeOnly", false))
    }

    fun configure(config: JSONObject, bytes: ByteArray, socket: File) {
        val domains = JSONArray()
        val ips = JSONArray()
        bytes.toString(Charsets.UTF_8).lineSequence().filter { it.isNotEmpty() }.forEach {
            when {
                it.startsWith("ip:") -> ips.put(it.removePrefix("ip:"))
                it.startsWith("resolvers:") || it.startsWith("refuse-dot:") -> Unit // native directives
                else -> domains.put(it)
            }
        }
        val inputs = config.getJSONArray("inbounds")
        sniff(inputs.getJSONObject(0))
        inputs.put(JSONObject().put("protocol", "http").put("listen", socket.absolutePath)
            .put("tag", "dns-tcp"))
        config.getJSONArray("outbounds").put(JSONObject().put("protocol","freedom").put("tag","direct"))
        fun rule(field: String, value: Any, out: String) =
            JSONObject().put("type","field").put(field,value).put("outboundTag",out)
        // Unmatched DNS is always proxied, even when the requested resolver is in a direct CIDR.
        config.put("routing", JSONObject().put("domainStrategy","AsIs").put("rules", JSONArray()
            .put(rule("inboundTag",JSONArray(listOf("dns-tcp")),"selected"))
            .put(rule("port","53","selected"))
            .put(rule("domain",domains,"direct")).put(rule("ip",ips,"direct"))))
    }
}
