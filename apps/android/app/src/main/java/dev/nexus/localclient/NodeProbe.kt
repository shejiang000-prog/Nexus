package dev.nexus.localclient

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import com.shoesproxy.ShoesNative
import kotlinx.coroutines.*
import org.json.JSONArray
import org.json.JSONObject
import java.io.DataInputStream
import java.io.IOException
import java.net.*
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger
import javax.net.ssl.*

internal data class ProbeResult(val ms: Long, val reason: String, val detail: String, val at: Long = System.currentTimeMillis()) {
    val ok get() = reason.isEmpty()
    val label get() = if (ok) "真连 $ms ms" else "真连失败：$reason"
    fun recent(window: Long, now: Long = System.currentTimeMillis()) = now-at in 0 until window
}

internal data class ProbeSite(val host: String, val path: String, val expected: Int)

internal object NodeProbe {
    private val sites = listOf(ProbeSite("cp.cloudflare.com","/generate_204",204), ProbeSite("example.com","/",200))
    private val loopback = InetAddress.getByName("127.0.0.1")

    fun physical(cm: ConnectivityManager) = cm.activeNetwork?.takeIf {
        cm.getNetworkCapabilities(it)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true
    }

    // Run on one IO thread while holding the native engines' lock. Cancellation of the parent
    // also cancels the workers, closes their sockets, and releases both engines before unlocking.
    suspend fun run(context: Context, rows: List<NodeRow>, targets: List<ProbeSite> = sites,
                    factory: SSLSocketFactory = SSLSocketFactory.getDefault() as SSLSocketFactory,
                    ready: (List<Int>) -> Unit = {}, onResult: (NodeRow, ProbeResult) -> Unit): Int = withContext(Dispatchers.IO) {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        val network = checkNotNull(physical(cm))
        val quality = NodeQuality(context, network)
        check(!TunnelService.state.value.active && CompatEngine.engineLock.tryLock())
        try {
            val job = currentCoroutineContext().job
            runBlocking(job) {
                val batch = this
                val monitor = launch {
                    while (isActive) {
                        if (TunnelService.state.value.active || physical(cm) != network) batch.cancel("网络已切换")
                        delay(150)
                    }
                }
                try {
                    val worker = async(Dispatchers.IO) {
                        batch(rows, network, targets, factory, ready) { row, value ->
                            quality.record(row.favorite,value.ok,value.ms,value)
                            onResult(row,value)
                        }
                    }
                    worker.await()
                } finally { monitor.cancel() }
            }
        } finally { CompatEngine.engineLock.unlock() }
    }

    private suspend fun batch(rows: List<NodeRow>, network: android.net.Network, targets: List<ProbeSite>, factory: SSLSocketFactory,
                              ready: (List<Int>) -> Unit, result: (NodeRow,ProbeResult) -> Unit): Int {
        val ins = JSONArray()
        val outs = JSONArray()
        val rules = JSONArray()
        val shoes = JSONArray()
        val ports = linkedMapOf<Int,NodeRow>()
        val token = UUID.randomUUID().toString()
        for (row in rows) {
            currentCoroutineContext().ensureActive()
            if (CompatEngine.problem(row.node) != null) continue
            val out = try {
                CompatEngine.resolved(CompatEngine.outbound(row.node),network)
            } catch (_: UnknownHostException) {
                result(row, ProbeResult(-1,"节点拒绝","物理网络无法解析节点域名"))
                continue
            }
            val port = (1..32).firstNotNullOfOrNull {
                ServerSocket(0,1,loopback).use { it.localPort }.takeUnless(ports::containsKey)
            } ?: error("本地监听端口不足")
            if (out.has("shoesClient")) {
                val client = try { ShoesEngine.resolvedClient(out,network) } catch (_: UnknownHostException) {
                    result(row, ProbeResult(-1,"节点拒绝","物理网络无法解析节点域名"))
                    continue
                }
                val rule = JSONObject().put("masks",JSONArray(targets.map { it.host + ":443" }.distinct())).put("action","allow").put("client_chain",client)
                shoes.put(JSONObject().put("address","127.0.0.1:$port").put("protocol",JSONObject()
                    .put("type","mixed").put("username","probe").put("password",token)).put("rules",JSONArray().put(rule)))
            } else {
                val accounts = JSONArray().put(JSONObject().put("user","probe").put("pass",token))
                ins.put(JSONObject().put("listen","127.0.0.1").put("port",port).put("tag","in$port").put("protocol","socks")
                    .put("settings",JSONObject().put("auth","password").put("accounts",accounts)))
                outs.put(out.put("tag","out$port"))
                rules.put(JSONObject().put("type","field").put("inboundTag",JSONArray().put("in$port")).put("outboundTag","out$port"))
            }
            ports[port] = row
        }
        currentCoroutineContext().ensureActive()
        val bind: (Int) -> Boolean = { fd -> runCatching {
            ParcelFileDescriptor.fromFd(fd).use { network.bindSocket(it.fileDescriptor) }; true
        }.getOrDefault(false) }
        var xray = false
        var handle = 0L
        var pair: Array<ParcelFileDescriptor>? = null
        try {
            if (ins.length() > 0) {
                CompatEngine.probeSockets(bind)
                val config = JSONObject().put("log",JSONObject().put("loglevel","none")).put("inbounds",ins)
                    .put("outbounds",outs).put("routing",JSONObject().put("rules",rules))
                xray = true
                CompatEngine.invoke("runXray",JSONObject().put("xrayJson",config.toString()))
            }
            if (shoes.length() > 0) {
                pair = ParcelFileDescriptor.createSocketPair()
                shoes.put(JSONObject().put("device_fd",pair[0].fd))
                check(ShoesNative.init("off") == 0)
                handle = ShoesNative.start(shoes.toString()) { fd -> bind(fd) }
                check(handle > 0)
                delay(300)
                check(ShoesNative.isRunning())
            }
            val entries = ports.entries.toList()
            ready(ports.keys.toList())
            val index = AtomicInteger()
            val active = AtomicInteger()
            val peak = AtomicInteger()
            coroutineScope {
                repeat(minOf(8, entries.size)) {
                    launch {
                        while (isActive) {
                            val entry = entries.getOrNull(index.getAndIncrement()) ?: break
                            peak.accumulateAndGet(active.incrementAndGet(), ::maxOf)
                            try {
                                val value = probe(entry.key,token,targets,factory)
                                ensureActive()
                                result(entry.value,value)
                            } finally { active.decrementAndGet() }
                        }
                    }
                }
            }
            return peak.get()
        } finally {
            try { if (handle > 0) ShoesNative.stop(handle) } finally {
                pair?.forEach { it.close() }
                try { if (xray) CompatEngine.invoke("stopXray") } finally { CompatEngine.probeSockets(null) }
            }
        }
    }

    private suspend fun probe(port: Int, token: String, targets: List<ProbeSite>, factory: SSLSocketFactory): ProbeResult {
        val details = mutableListOf<String>()
        var reason = "测试地址不可达"
        for (site in targets) {
            val start = SystemClock.elapsedRealtime()
            try {
                val status = coroutineScope {
                    Socket().use { socket ->
                        // A blocking TLS or SOCKS read is cancelled by closing its transport.
                        val io = async(Dispatchers.IO) { runCatching { request(socket,port,token,site,factory) } }
                        withTimeoutOrNull(8000) { io.await().getOrThrow() } ?: throw SocketTimeoutException()
                    }
                }
                val ms = SystemClock.elapsedRealtime()-start
                details += "${site.host} · HTTP $status · $ms ms"
                if (status == site.expected) return ProbeResult(ms,"",details.joinToString("\n"))
                reason = "测试地址不可达"
            } catch (e: IOException) {
                reason = when (e) {
                    is SocketTimeoutException -> "超时"
                    is SSLException -> "握手失败"
                    else -> "节点拒绝"
                }
                details += "${site.host} · $reason"
            }
        }
        return ProbeResult(-1,reason,details.joinToString("\n"))
    }

    // SOCKS5 with per-batch credentials. Send the destination name to the node for remote DNS.
    private fun request(socket: Socket, port: Int, token: String, site: ProbeSite, factory: SSLSocketFactory): Int {
        socket.connect(InetSocketAddress(loopback,port),8000)
        socket.soTimeout = 8000
        val input = DataInputStream(socket.getInputStream())
        val output = socket.getOutputStream()
        fun expect(value: Int) { if (input.read() != value) throw ConnectException() }
        output.write(byteArrayOf(5,1,2))
        expect(5); expect(2)
        val user = "probe".toByteArray()
        val pass = token.toByteArray()
        output.write(byteArrayOf(1,user.size.toByte()) + user + byteArrayOf(pass.size.toByte()) + pass)
        expect(1); expect(0)
        val host = site.host.toByteArray()
        output.write(byteArrayOf(5,1,0,3,host.size.toByte()) + host + byteArrayOf(1,0xbb.toByte()))
        expect(5); expect(0); expect(0)
        val length = when (input.read()) { 1 -> 4; 3 -> input.readUnsignedByte(); 4 -> 16; else -> throw ConnectException() }
        input.readFully(ByteArray(length+2))
        (factory.createSocket(socket,site.host,443,true) as SSLSocket).use { tls ->
            tls.sslParameters = tls.sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS" }
            tls.startHandshake()
            tls.outputStream.write("GET ${site.path} HTTP/1.1\r\nHost: ${site.host}\r\nConnection: close\r\n\r\n".toByteArray())
            val response = tls.inputStream.buffered()
            val headers = java.io.ByteArrayOutputStream()
            var tail = 0
            while (headers.size() < 32768) {
                val b = response.read()
                if (b < 0) break
                headers.write(b)
                tail = (tail shl 8) or b
                if (tail == 0x0d0a0d0a) return headers.toString("ISO-8859-1").substringBefore('\n')
                    .split(' ').getOrNull(1)?.toIntOrNull() ?: -1
            }
            throw IOException("响应头无效")
        }
    }
}
