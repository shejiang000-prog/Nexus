package dev.nexus.localclient

import org.junit.Assert.assertEquals
import org.junit.Test

class V2InstallProtocolsTest {
    @Test fun deploymentUsesTheDefaultsAndHonorsDomainOptOuts() {
        assertEquals("v2,reality", v2InstallProtocols(""))
        assertEquals("v2,reality", v2InstallProtocols("", emptySet()))
        assertEquals("v2,reality,hysteria2", v2InstallProtocols("node.example"))
        assertEquals("v2", v2InstallProtocols("node.example", emptySet()))
        assertEquals("v2,hysteria2", v2InstallProtocols("node.example", setOf("hysteria2")))
    }
}
