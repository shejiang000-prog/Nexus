package dev.nexus.localclient

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Network
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import java.util.concurrent.atomic.AtomicLong
import mobile.Mobile

internal class V2Session(
    private val node: V2Node, private val service: VpnService, network: Network,
    bearer: (String) -> Unit
) : AutoCloseable {
    companion object {
        val foregroundEvents = AtomicLong()
    }

    private var foreground = foregroundEvents.get()
    private var lastExpiry = -1L
    private val power = service.getSystemService(PowerManager::class.java)
    private val protector = mobile.Protector { fd ->
        runCatching {
            check(service.protect(fd))
            ParcelFileDescriptor.fromFd(fd).use { network.bindSocket(it.fileDescriptor) }
            true
        }.getOrDefault(false)
    }
    val client = try {
        Mobile.start(node.authority, node.endpoint, "auto", node.ca, node.deviceIdBytes, null,
            node.signer(), protector, mobile.Resolver { host ->
                network.getAllByName(host).joinToString(",") { it.hostAddress!! }
            }, mobile.Observer { bearer(it) })
    } catch (e: Exception) { throw V2Failure(Mobile.errorCode(e)) }

    private fun updatePower() = client.setRecoveryAllowed(power.isInteractive && !power.isDeviceIdleMode)

    private val powerChanges = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) { updatePower() }
    }

    private fun confirm() {
        try { client.confirm() } catch (e: Exception) { throw V2Failure(Mobile.errorCode(e)) }
    }

    private fun refreshExpiry() {
        val expires = client.accountExpiry()
        if (expires >= 0 && expires != lastExpiry) {
            V2Devices.updateExpiry(service,node.key,expires)
            lastExpiry = expires
        }
    }

    init {
        try {
            service.registerReceiver(powerChanges,IntentFilter(Intent.ACTION_SCREEN_OFF).apply {
                addAction(Intent.ACTION_SCREEN_ON)
                addAction(PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED)
            })
        } catch (e: Exception) {
            client.stop()
            throw e
        }
        try {
            updatePower()
            confirm()
            refreshExpiry()
        } catch (e: Exception) {
            close()
            throw e
        }
    }

    fun check(flowError: Int) {
        if (flowError != 0 && flowError != 3) throw V2Failure(flowError)
        val event = foregroundEvents.get()
        if (flowError == 3 || event != foreground) {
            foreground = event
            confirm()
        }
        refreshExpiry()
    }

    override fun close() {
        try { service.unregisterReceiver(powerChanges) } finally { client.stop() }
    }
}
