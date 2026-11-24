package dev.nexus.localclient

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class SshEndpointTest {
    @Test fun parsesSshAddressesAndPreservesDefaultPortBindings() {
        val ipv4 = "192.0.2.7"
        val ipv6 = "2001:DB8::7"
        assertEquals(SshEndpoint(ipv4, 22), SshEndpoint.parse(ipv4))
        assertEquals(ipv4, SshEndpoint.parse("$ipv4:22").authority)
        assertEquals(SshEndpoint(ipv4, 25358), SshEndpoint.parse("$ipv4:25358"))
        assertEquals("$ipv4:25358", SshEndpoint.parse("$ipv4:25358").authority)
        assertEquals(SshEndpoint(ipv6, 22), SshEndpoint.parse(ipv6))
        assertEquals(ipv6, SshEndpoint.parse("[$ipv6]:22").authority)
        assertEquals(SshEndpoint(ipv6, 65535), SshEndpoint.parse("[$ipv6]:65535"))
        assertEquals("[$ipv6]:65535", SshEndpoint.parse("[$ipv6]:65535").authority)
        for (invalid in listOf("$ipv4:", "$ipv4:0", "$ipv4:-1", "[$ipv6]:65536", "[$ipv6]:ssh",
                "256.0.2.1", "127.1", "[2001:::7]:22", "[192.0.2.1]:22", "example.com:22")) {
            assertThrows(invalid, IllegalArgumentException::class.java) { SshEndpoint.parse(invalid) }
        }
    }
}
