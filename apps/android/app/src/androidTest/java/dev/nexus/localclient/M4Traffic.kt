package dev.nexus.localclient

import android.app.Activity
import android.app.Instrumentation
import android.net.Network
import android.net.Uri
import android.os.SystemClock
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import org.json.JSONObject
import java.io.DataInputStream
import java.net.InetSocketAddress
import java.security.SecureRandom
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/** Android boot milliseconds throughout. Only timings and event names leave the device. */
internal class M4Traffic(
    private val inst: Instrumentation,
    private val home: Activity,
    private val cfg: JSONObject,
    private val vpn: () -> Network,
    private val available: (String) -> Boolean,
) : AutoCloseable {
    private fun now() = SystemClock.elapsedRealtime()
    private val random = SecureRandom()
    private val address = cfg.getString("echoHost").let { value ->
        require(android.util.Patterns.IP_ADDRESS.matcher(value).matches())
        InetSocketAddress(java.net.InetAddress.getByName(value), cfg.getInt("echoPort"))
    }
    private lateinit var view: WebView

    init {
        require(address.port > 0)
        inst.runOnMainSync {
            view = WebView(home)
            view.settings.apply {
                cacheMode = WebSettings.LOAD_NO_CACHE
                javaScriptEnabled = true
                allowFileAccess = false
                allowContentAccess = false
            }
            home.setContentView(view)
        }
    }

    fun echoSocket() = vpn().socketFactory.createSocket().apply {
        try {
            connect(address,3000)
            soTimeout = 3000
            tcpNoDelay = true
        } catch (e: Exception) { close(); throw e }
    }

    fun ping(socket: java.net.Socket) {
        val nonce = ByteArray(32).also(random::nextBytes)
        socket.getOutputStream().write(nonce)
        val reply = ByteArray(nonce.size)
        DataInputStream(socket.getInputStream()).readFully(reply)
        check(nonce.contentEquals(reply))
    }

    private fun page(phase: String, evidence: JSONObject) {
        val defaultUrl = cfg.optString("probeUrl","https://example.com/")
        val alternate = if (defaultUrl == "https://example.com/") when (phase) {
            "cold" -> "https://example.org/"
            "recover" -> "https://example.net/"
            else -> defaultUrl
        } else defaultUrl
        val uri = Uri.parse(cfg.optString(phase + "Url", alternate)).buildUpon()
            .appendQueryParameter("m4", java.util.UUID.randomUUID().toString()).build()
        require(uri.scheme == "https" && uri.host != null && uri.userInfo == null)
        val target = uri.toString()
        val title = cfg.optString("probeTitle", "Example Domain")
        val done = CountDownLatch(1)
        val valid = AtomicBoolean()
        inst.runOnMainSync {
            view.webViewClient = object : WebViewClient() {
                override fun onReceivedError(v: WebView, req: WebResourceRequest, err: WebResourceError) {
                    if (req.isForMainFrame && req.url == uri) {
                        evidence.put("webError",err.errorCode)
                        done.countDown()
                    }
                }
                override fun onReceivedHttpError(v: WebView, req: WebResourceRequest, res: WebResourceResponse) {
                    if (req.isForMainFrame && req.url == uri) {
                        evidence.put("httpError",res.statusCode)
                        done.countDown()
                    }
                }
                override fun onPageFinished(v: WebView, url: String) {
                    if (url != target || done.count == 0L) return
                    val finished = now()
                    // Read the DOM title after the main document has loaded. WebView.title can lag this callback.
                    v.evaluateJavascript("""(() => {let n=performance.getEntriesByType('navigation')[0];
                        return {title:document.title,age:performance.now(),
                          dnsStart:n&&n.domainLookupStart,dnsEnd:n&&n.domainLookupEnd,
                          tcpStart:n&&n.connectStart,tlsStart:n&&n.secureConnectionStart,tcpEnd:n&&n.connectEnd,
                          response:n&&n.responseStart}})()""") { raw ->
                        if (done.count == 0L) return@evaluateJavascript
                        runCatching {
                            val nav = JSONObject(raw)
                            if (nav.optString("title") == title) {
                                for (key in listOf("dnsStart","dnsEnd","tcpStart","tlsStart","tcpEnd","response"))
                                    if (!nav.isNull(key)) evidence.put(key + "SinceNavMs",nav.getDouble(key))
                                val age = nav.getDouble("age")
                                evidence.put("navigationOriginBootMsLower",finished-age)
                                    .put("navigationOriginBootMsUpper",now()-age)
                                    .put("pageFinishedBootMs",finished)
                                valid.set(true)
                            }
                        }
                        done.countDown()
                    }
                }
            }
            evidence.put("loadUrlBootMs",now())
            view.loadUrl(target)
        }
        try { check(done.await(12,TimeUnit.SECONDS) && valid.get()) }
        finally { inst.runOnMainSync { view.stopLoading(); view.webViewClient = WebViewClient() } }
    }

    fun measure(phase: String, kind: String, origin: Long, deadline: Long): JSONObject {
        val out = JSONObject().put("originBootMs",origin)
        // Do not enqueue sockets on a known-dead bearer before the new bearer is selected.
        // Both application probes then start independently, so slow DNS does not delay TCP echo.
        while (!available(kind) && now() < deadline) SystemClock.sleep(10)
        if (!available(kind)) return out.put("state","bearer-timeout")
        out.put("eligibleBootMs",now())
        val echoData = JSONObject()
        val pageData = JSONObject()
        fun attempt(data: JSONObject, action: () -> Unit) {
            var tries = 0
            while (now() < deadline && available(kind)) {
                tries++
                val start = now()
                data.put("tries", tries).put("attemptBootMs",start)
                try {
                    val network = vpn()
                    action()
                    val received = data.optLong("pageFinishedBootMs",now())
                    if (vpn() == network && available(kind) && received >= origin && start >= origin && received <= deadline) {
                        data.put("receivedBootMs",received).put("elapsedMs",received-origin)
                        return
                    }
                    break
                } catch (e: Exception) { data.put("lastError",e.javaClass.simpleName) }
                SystemClock.sleep(25)
            }
        }
        val worker = kotlin.concurrent.thread(name = "m4-echo") { attempt(echoData) { echoSocket().use(::ping) } }
        try { attempt(pageData) { page(phase,pageData) } }
        finally { worker.join() }
        return out.put("tcpEcho",echoData).put("webView",pageData)
    }

    override fun close() {
        inst.runOnMainSync { home.setContentView(FrameLayout(home)); view.destroy() }
    }
}
