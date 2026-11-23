package dev.nexus.localclient

import android.net.Network
import android.net.VpnService
import android.os.ParcelFileDescriptor
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import mobile.Mobile

internal class V2Session(
    private val node: V2Node, service: VpnService, network: Network, dns: String,
    bearer: (String) -> Unit
) : AutoCloseable {
    private val failure = AtomicInteger()
    private val authTarget = "$dns:53"
    private val protector = mobile.Protector { fd ->
        runCatching {
            check(service.protect(fd))
            ParcelFileDescriptor.fromFd(fd).use { network.bindSocket(it.fileDescriptor) }
            true
        }.getOrDefault(false)
    }
    val client = try {
        if (expired()) throw V2Failure(3)
        Mobile.start(node.authority, node.endpoint, "auto", node.ca, node.deviceIdBytes, null,
            node.signer(), protector, mobile.Resolver { host ->
                network.getAllByName(host).joinToString(",") { it.hostAddress!! }
            }, mobile.Observer { bearer(it) })
    } catch (e: V2Failure) { throw e }
      catch (e: Exception) { throw V2Failure(Mobile.errorCode(e)) }

    private fun expired() = node.expiresAt?.let { System.currentTimeMillis() / 1000 >= it } == true

    // A TLS handshake alone does not authenticate the device. No DNS packet is sent.
    private fun authenticate() {
        try {
            client.openUDP(authTarget).close()
        } catch (e: Exception) {
            val code = Mobile.errorCode(e)
            // A denied DNS destination is a valid, authenticated response from this node.
            if (code != 5) throw V2Failure(code)
        }
    }

    private val monitor = try {
        authenticate()
        thread(name = "Nexus V2 enrollment status") {
            try {
                var interval = 10_000L
                while (true) {
                    Thread.sleep(interval)
                    if (expired()) throw V2Failure(3)
                    try {
                        authenticate()
                        interval = 10_000L
                    } catch (e: V2Failure) {
                        if (!V2Connection.retry(e.code)) throw e
                        // A probe on a retired lane can fail while new VPN traffic is healthy.
                        interval = (interval * 2).coerceAtMost(30_000L)
                    }
                }
            } catch (e: InterruptedException) {
                Thread.currentThread().interrupt()
            } catch (e: V2Failure) { failure.set(e.code) }
        }
    } catch (e: Exception) {
        client.stop()
        throw e
    }

    fun check(flowError: Int) {
        val code = if (expired()) 3 else if (flowError != 0) flowError else failure.get()
        if (code != 0) throw V2Failure(code)
    }

    override fun close() {
        monitor.interrupt()
        client.stop()
    }
}

