package dev.nexus.localclient

import android.net.Network
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.UnknownHostException

/**
 * Whether an imported node may point at [address]. Only destinations that can mean this phone
 * itself are refused (loopback, unspecified, link-local, multicast, reserved IPv4). LAN, CGNAT,
 * ULA and the 198.18.0.0/15 fake-ip range stay allowed: home servers and router-side proxies
 * (OpenClash fake-ip answers, for example) legitimately use them.
 */
internal fun allowedNodeAddress(address: InetAddress): Boolean {
    if (address.isAnyLocalAddress || address.isLoopbackAddress || address.isLinkLocalAddress || address.isMulticastAddress) return false
    val bytes = address.address
    return bytes.size != 4 || (bytes[0].toInt() and 255).let { it != 0 && it < 240 }
}

internal fun nodeIP(host: String): InetAddress? =
    android.system.Os.inet_pton(if (':' in host) android.system.OsConstants.AF_INET6 else android.system.OsConstants.AF_INET, host)

internal fun nodeAddress(network: Network, host: String): InetAddress {
    fun denied(): Nothing = throw UnknownHostException("节点地址不可用")
    if (host.length !in 1..253 || host.any { !it.isLetterOrDigit() && it !in ".:-" }) denied()
    val own = runCatching {
        NetworkInterface.getNetworkInterfaces()?.toList().orEmpty().flatMap { it.inetAddresses.toList() }.toSet()
    }.getOrDefault(emptySet())
    return network.getAllByName(host).firstOrNull { allowedNodeAddress(it) && it !in own } ?: denied()
}
