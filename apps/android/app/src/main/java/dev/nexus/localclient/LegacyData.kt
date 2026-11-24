package dev.nexus.localclient

import android.content.Context
import android.content.Intent
import java.security.KeyStore

/** Discard the retired protocol's local records before any entry point can resume an old VPN. */
internal fun clearLegacyData(context: Context) {
    val local = context.getSharedPreferences("local", Context.MODE_PRIVATE)
    if (local.getBoolean("v1-cleared", false)) return
    val owners = context.getSharedPreferences("owner-access", Context.MODE_PRIVATE)
    val saved = owners.all
    val retained = saved.keys.filter { it.startsWith("v2/") && saved[it] == true }
        .map { it.removePrefix("v2/") }.toSet()
    val ownerEdit = owners.edit()
    for (key in saved.keys) {
        if (key.substringBefore('/') in setOf("key","ready","v2") && key.substringAfter('/') !in retained) ownerEdit.remove(key)
    }
    if (saved["current"] !in retained) ownerEdit.remove("current")
    if (!ownerEdit.commit()) return

    val edit = local.edit().remove("profile").remove("servers").remove("pinned").remove("failover")
    if (local.getString("simple-node", null) == "private") edit.remove("simple-node")
    val oldEndpoint = Regex("(?:apps|monitor|hold|exit)/[^/]+/(?:[0-9A-F]{2}:){31}[0-9A-F]{2}")
    local.all.keys.filter(oldEndpoint::matches).forEach(edit::remove)
    for (key in listOf(TunnelService.RESUME, TunnelService.LAST)) {
        val request = local.getString(key,null) ?: continue
        if (runCatching { Intent.parseUri(request,0).action }.getOrNull() !in setOf("subscription","v2")) edit.remove(key)
    }
    // Persist the removals before deleting the keys. A failed cleanup retries on next start.
    if (!edit.commit()) return
    val cleaned = runCatching {
        val keys = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        for (alias in keys.aliases().toList()) {
            if (alias == "nexus-device-v1" || alias.startsWith("nexus-device-v1-") ||
                (alias == "nexus-owner-ssh-v1" && retained.isEmpty())) keys.deleteEntry(alias)
        }
    }
    if (cleaned.isSuccess) local.edit().putBoolean("v1-cleared",true).apply()
}
