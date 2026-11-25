package dev.nexus.localclient

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class InstallerStderrTest {
    @Test fun exposesOnlyTheOccupiedPortFromAFailedInstaller() {
        for ((port, protocol) in listOf(443 to "tcp", 8443 to "udp")) {
            val stderr = InstallerStderr()
            stderr.write(("x".repeat(131072) + "\nport occ").toByteArray())
            stderr.write("upied: $port/$protocol\r\nprivate detail\n".toByteArray())
            assertEquals("服务器的 $port 端口已被其他程序（如 nginx）占用，请停用它或换一台干净的服务器后重试。",
                friendlyError(requireNotNull(stderr.failure(1))))
            assertNull(stderr.failure(0))
            assertNull(stderr.failure(-1))
        }
        val invalid = InstallerStderr()
        invalid.write("port occupied: 0/tcp\nport occupied: 65536/udp\nport occupied: 443/tcp private detail\n".toByteArray())
        assertNull(invalid.failure(1))
    }
}
