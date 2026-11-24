package dev.nexus.localclient

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DiagnosticExportTest {
    @Test fun shareContainsOnlyPublicFieldsAndTypedEvents() {
        val privateFields = mapOf(
            "node" to "private-node-name", "ipv4" to "192.0.2.27", "ipv6" to "2001:db8::7",
            "domain" to "node.example.invalid", "key" to "syntheticPrivateKey",
            "invite" to "nexus://invite/v2#privateTestToken", "link" to "vless://share@example.invalid",
            "subscription" to "https://feed.example.invalid/path", "account" to "privateAccountID",
            "device" to "a1234567890b47f9593cbd1fbb002551"
        )
        val events = privateFields + mapOf<String, Any>("fallback" to 1000L, "recovery" to 2000L, "error" to 3)
        val report = DiagnosticExport.format("16", 36, "SM-S911B", DiagnosticExport.Link.CELLULAR, events)
        for (expected in listOf("Nexus ${BuildConfig.VERSION_NAME}", "Android 16 (API 36)", "SM-S911B", "移动数据",
            "H3 → H2 1970-01-01T00:00:01Z", "H2 → H3 1970-01-01T00:00:02Z", "错误码 3")) {
            assertTrue(expected, report.contains(expected))
        }
        for (secret in privateFields.values) {
            assertFalse(report.contains(secret))
            // Even malformed ROM properties or values under an allowed event key cannot escape as text.
            val poisoned = DiagnosticExport.format(secret, 36, secret, DiagnosticExport.Link.OTHER,
                events + mapOf("fallback" to secret, "recovery" to secret, "error" to secret))
            assertFalse(poisoned.contains(secret))
        }
    }
}
