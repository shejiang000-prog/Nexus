package dev.nexus.localclient

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ImportLinkTest {
    @Test fun acceptsWebsiteImportLinksOnly() {
        assertEquals("https://panel.example.com/s/abc123" to "流光",
            importLink("nexus://install-config?url=https%3A%2F%2Fpanel.example.com%2Fs%2Fabc123&name=%E6%B5%81%E5%85%89"))
        assertEquals("https://a.example/s/x" to "", importLink("NEXUS://install-config/?url=https://a.example/s/x"))
        assertEquals(40, importLink("nexus://install-config?url=https://a.example/s&name=" + "名".repeat(60))!!.second.length)
        for (bad in listOf(
            "nexus://install-config?url=http%3A%2F%2Fa.example%2Fs",
            "nexus://install-config?url=https%3A%2F%2F",
            "nexus://install-config?url=https%3A%2F%2Fa.example%2Fs%20x",
            "nexus://install-config?url=https%3A%2F%2Fa.example%2F%0A",
            "nexus://install-config?name=x",
            "nexus://invite/v2#abc",
            "clash://install-config?url=https%3A%2F%2Fa.example%2Fs",
            "nexus://install-config?url=%ZZ")) {
            assertNull(bad, importLink(bad))
        }
    }
}
