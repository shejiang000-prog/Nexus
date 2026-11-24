package dev.nexus.localclient

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Build
import java.time.Instant

internal object DiagnosticExport {
    private fun history(ctx: Context) = ctx.getSharedPreferences("v2-diagnostics", Context.MODE_PRIVATE)

    fun bearerChanged(ctx: Context, before: String, after: String) {
        val key = when {
            before == "h3" && after == "h2" -> "fallback"
            before == "h2" && after == "h3" -> "recovery"
            else -> return
        }
        history(ctx).edit().putLong(key, System.currentTimeMillis()).apply()
    }

    fun recordError(ctx: Context, code: Int) {
        if (code in 1..7) history(ctx).edit().putInt("error", code).apply()
    }

    enum class Link(val label: String) {
        WIFI("Wi-Fi"), CELLULAR("移动数据"), ETHERNET("以太网"), OTHER("其他"), UNKNOWN("未知")
    }

    fun share(ctx: Context, physical: Network?) {
        val manager = ctx.getSystemService(ConnectivityManager::class.java)
        val caps = manager.getNetworkCapabilities(physical ?: manager.activeNetwork)
        val link = when {
            caps == null -> Link.UNKNOWN
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Link.WIFI
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Link.CELLULAR
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> Link.ETHERNET
            else -> Link.OTHER
        }
        val text = format(Build.VERSION.RELEASE, Build.VERSION.SDK_INT, Build.MODEL, link, history(ctx).all)
        val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
        ctx.startActivity(Intent.createChooser(send, "分享诊断信息"))
    }

    // Custom ROMs can put arbitrary text in MODEL. Export recognizable retail model formats only.
    private val retailModel = Regex(
        """(?:SM-[A-Z][0-9]{3,4}[A-Z0-9]{0,2}(?:/DS)?|Pixel (?:[1-9][0-9]?a?(?: Pro)?(?: XL| Fold)?|Fold|Tablet)|""" +
        """(?:CPH|RMX|XT|ASUS_AI|ONEPLUS A)[0-9]{4}(?:-[0-9]{1,2})?|[A-Z]{3}-[A-Z]{2,3}[0-9]{2}[A-Z]?|[0-9]{5}[0-9A-Z]{3,5}[CGI])"""
    )

    internal fun format(androidRelease: String, api: Int, model: String, link: Link, events: Map<String, *>): String {
        fun date(key: String) = (events[key] as? Long)?.takeIf { it > 0 }
            ?.let { Instant.ofEpochMilli(it).toString() } ?: "无记录"
        val code = (events["error"] as? Int)?.takeIf { it in 1..7 }?.toString() ?: "无记录"
        val os = androidRelease.takeIf { it.matches(Regex("""[1-9][0-9]?(?:\.[0-9]){0,2}""")) }?.plus(" ") ?: ""
        return listOf(
            "Nexus ${BuildConfig.VERSION_NAME}", "Android ${os}(API $api)",
            "机型 ${model.takeIf(retailModel::matches) ?: "未识别"}", "网络 ${link.label}",
            "最近 H3 → H2 ${date("fallback")}", "最近 H2 → H3 ${date("recovery")}", "最近 V2 错误码 $code"
        ).joinToString("\n")
    }
}
