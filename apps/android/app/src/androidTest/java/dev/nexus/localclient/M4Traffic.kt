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
        val defaultUrl = if (phase == "reference") "https://example.com/" else cfg.optString("probeUrl","https://example.com/")
        val alternate = if (defaultUrl == "https://example.com/") when (phase) {
            "cold" -> "https://example.org/"
            "recover" -> "https://example.net/"
            else -> defaultUrl
        } else defaultUrl
        val uri = Uri.parse(cfg.optString(phase + "Url", alternate)).buildUpon()
            .appendQueryParameter("m4", java.util.UUID.randomUUID().toString()).build()
        require(uri.scheme == "https" && uri.host != null && uri.userInfo == null)
        val target = uri.toString()
        val defaultTitle = if (phase == "reference") "Example Domain" else cfg.optString("probeTitle","Example Domain")
        val title = cfg.optString(phase+"Title", defaultTitle)
        val text = cfg.optString(phase+"Text", if (phase == "reference") "Example Domain" else cfg.optString("probeText",title))
        require(text.isNotBlank())
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
                        let expectedTitle=${JSONObject.quote(title)}, expectedText=${JSONObject.quote(text)};
                        let body=document.body && document.body.innerText.trim();
                        return {titleOK:!expectedTitle || document.title===expectedTitle,
                          urlOK:document.URL===${JSONObject.quote(target)},
                          bodyOK:!!body && (expectedTitle ? body.includes(expectedText) : body===expectedText),
                          complete:document.readyState==='complete',age:performance.now(),
                          dnsStart:n&&n.domainLookupStart,dnsEnd:n&&n.domainLookupEnd,
                          tcpStart:n&&n.connectStart,tlsStart:n&&n.secureConnectionStart,tcpEnd:n&&n.connectEnd,
                          request:n&&n.requestStart,response:n&&n.responseStart,responseEnd:n&&n.responseEnd,
                          status:n&&n.responseStatus,bytes:n&&n.decodedBodySize}})()""") { raw ->
                        if (done.count == 0L) return@evaluateJavascript
                        runCatching {
                            val nav = JSONObject(raw)
                            for (key in listOf("titleOK","urlOK","bodyOK","complete"))
                                evidence.put(key,nav.optBoolean(key))
                            for (key in listOf("dnsStart","dnsEnd","tcpStart","tlsStart","tcpEnd","request","response","responseEnd"))
                                if (!nav.isNull(key)) evidence.put(key + "SinceNavMs",nav.getDouble(key))
                            val status = nav.optInt("status")
                            if (status > 0) evidence.put("httpStatus",status)
                            if (!nav.isNull("bytes")) evidence.put("mainDecodedBodyBytes",nav.getLong("bytes"))
                            val age = nav.getDouble("age")
                            val upper = now()-age
                            val end = nav.getDouble("responseEnd")
                            val first = nav.getDouble("response")
                            evidence.put("navigationOriginBootMsLower",finished-age)
                                .put("navigationOriginBootMsUpper",upper)
                                .put("pageFinishedBootMs",finished)
                            // Older engines omit responseStatus. A successful main-frame callback plus the
                            // expected page is usable evidence; never invent a status code for it.
                            if (listOf("titleOK","urlOK","bodyOK","complete").all { nav.optBoolean(it) } &&
                                (status == 0 || status in 200..299) && first > 0 && end >= first && end <= age) {
                                evidence.put("mainResponseEndBootMsLower",finished-age+end)
                                    .put("mainResponseEndBootMsUpper",upper+end)
                                    .put("httpEvidence",if (status > 0) "navigation-status-and-document" else "verified-document-no-main-frame-error")
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
        fun attempt(data: JSONObject, retry: Boolean, action: () -> Unit) {
            var tries = 0
            while (now() < deadline && available(kind)) {
                tries++
                val start = now()
                data.put("tries", tries).put("attemptBootMs",start)
                try {
                    val network = vpn()
                    action()
                    val received = if (data.has("mainResponseEndBootMsUpper"))
                        kotlin.math.ceil(data.getDouble("mainResponseEndBootMsUpper")).toLong() else now()
                    if (vpn() == network && available(kind) && received >= origin && start >= origin && received <= deadline) {
                        data.put("receivedBootMs",received).put("elapsedMs",received-origin)
                        return
                    }
                    break
                } catch (e: Exception) { data.put("lastError",e.javaClass.simpleName) }
                if (!retry) break
                SystemClock.sleep(25)
            }
        }
        val worker = if (phase == "reference") null else kotlin.concurrent.thread(name = "m4-echo") {
            attempt(echoData,true) { echoSocket().use(::ping) }
        }
        try { attempt(pageData,false) { page(phase,pageData) } }
        finally { worker?.join() }
        if (worker != null) out.put("tcpEcho",echoData)
        return out.put("webView",pageData)
    }

    override fun close() {
        inst.runOnMainSync { home.setContentView(FrameLayout(home)); view.destroy() }
    }
}
