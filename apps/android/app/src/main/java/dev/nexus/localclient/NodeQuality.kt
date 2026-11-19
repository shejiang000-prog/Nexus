package dev.nexus.localclient

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.SystemClock
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import kotlin.math.abs

internal class NodeQuality(context: Context, network: Network?) {
    private val history = context.getSharedPreferences("node-quality", 0)
    private val caps = network?.let { context.getSystemService(ConnectivityManager::class.java).getNetworkCapabilities(it) }
    private val group = listOf(NetworkCapabilities.TRANSPORT_WIFI, NetworkCapabilities.TRANSPORT_CELLULAR,
        NetworkCapabilities.TRANSPORT_ETHERNET).firstOrNull { caps?.hasTransport(it) == true }

    private fun key(node: String, metric: String) = "$group/$node/$metric"
    private fun read(node: String, metric: String) = history.getLong(key(node, metric), 0)
    private fun fresh(at: Long) = System.currentTimeMillis() - at in 0L..86_400_000L

    fun lastProbe(node: String): ProbeResult? {
        val time = read(node, "probeTime")
        if (time == 0L) return null
        return ProbeResult(read(node,"probeMs"), history.getString(key(node,"reason"),"").orEmpty(),
            history.getString(key(node,"detail"),"").orEmpty(), time)
    }

    fun record(node: String, ok: Boolean, ms: Long, probe: ProbeResult? = null) {
        val prior = fresh(read(node, "time"))
        val before = if (prior) read(node, "success") else 500
        val edit = history.edit().putLong(key(node, "time"), System.currentTimeMillis())
            .putLong(key(node, "success"), (before * 3 + if (ok) 1000 else 0) / 4)
        if (ok) {
            val previous = if (prior) read(node, "latency") else 0
            edit.putLong(key(node, "latency"), ms)
                .putLong(key(node, "jitter"), if (previous > 0) (read(node, "jitter") * 3 + abs(ms - previous)) / 4 else 0)
        }
        if (probe != null) edit.putLong(key(node,"probeTime"),probe.at).putLong(key(node,"probeMs"),probe.ms)
            .putString(key(node,"reason"),probe.reason).putString(key(node,"detail"),probe.detail)
        edit.apply()
    }

    fun score(node: String): Long {
        val link = if (fresh(read(node, "speedTime"))) (read(node, "down") + read(node, "up")).coerceAtMost(100_000) / 1000 else 0
        if (!fresh(read(node, "time"))) return 500 + link
        return read(node, "success") - read(node, "latency").coerceAtMost(6000) / 40 -
            read(node, "jitter").coerceAtMost(3000) / 40 + link
    }

    fun measure(vpn: Network, node: String, stillConnected: () -> Boolean): String {
        val bytes = 1_048_576
        fun transfer(upload: Boolean): Long {
            check(stillConnected())
            val endpoint = if (upload) "__up" else "__down?bytes=$bytes"
            val conn = vpn.openConnection(URL("https://speed.cloudflare.com/$endpoint")) as HttpsURLConnection
            try {
                conn.useCaches = false
                conn.instanceFollowRedirects = false
                conn.connectTimeout = 8000
                conn.readTimeout = 15000
                val start = SystemClock.elapsedRealtime()
                if (upload) {
                    conn.requestMethod = "POST"
                    conn.doOutput = true
                    conn.setFixedLengthStreamingMode(bytes)
                    conn.outputStream.use { output ->
                        val buffer = ByteArray(16384)
                        repeat(bytes / buffer.size) { output.write(buffer) }
                    }
                }
                check(conn.responseCode in 200..299)
                if (!upload) check(conn.inputStream.bounded(bytes).size == bytes)
                check(stillConnected())
                return bytes * 8L / (SystemClock.elapsedRealtime() - start).coerceAtLeast(1)
            } finally { conn.disconnect() }
        }
        val down = transfer(false)
        val up = transfer(true)
        history.edit().putLong(key(node, "down"), down).putLong(key(node, "up"), up)
            .putLong(key(node, "speedTime"), System.currentTimeMillis()).apply()
        return "下载 %.2f Mbps · 上传 %.2f Mbps".format(down / 1000.0, up / 1000.0)
    }
}
