package dev.nexus.localclient

import com.jcraft.jsch.JSchException
import java.net.ConnectException
import java.net.SocketTimeoutException
import org.junit.Assert.assertEquals
import org.junit.Test

class SshTimeoutMessageTest {
    @Test fun onlyATcpDialTimeoutSuggestsChangingIp() {
        val generic = friendlyError(IllegalStateException())
        val timeout = SocketTimeoutException()
        assertEquals("无法连接到服务器。这个 IP 可能在国内无法访问，可以在服务商处更换 IP 后重试。",
            friendlyError(JSchException("timeout: socket is not established", timeout)))
        assertEquals(generic, friendlyError(JSchException("Session.connect: read timed out", timeout)))
        assertEquals(generic, friendlyError(JSchException("channel timeout")))
        val refused = "连不上服务器的 SSH。如果服务商给的 SSH 端口不是 22，请填写 IP:端口。"
        assertEquals(refused, friendlyError(JSchException("Connection refused", ConnectException())))
        assertEquals(refused, friendlyError(JSchException("socket connect failed", ConnectException("ECONNREFUSED"))))
        assertEquals(generic, friendlyError(JSchException("socket connect failed", ConnectException("Network is unreachable"))))
        assertEquals("登录失败，请检查服务器密码或本机的管理授权。", friendlyError(JSchException("Auth fail")))
        assertEquals("服务器身份已变化，操作已停止。请先向服务器提供商核实。", friendlyError(ChangedServer()))
    }
}
