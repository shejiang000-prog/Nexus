package dev.nexus.localclient

import android.content.Context
import android.util.Base64
import com.jcraft.jsch.*
import org.json.JSONObject
import java.security.MessageDigest

private fun sha(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes)
private fun hex(bytes: ByteArray) = bytes.joinToString("") { "%02x".format(it) }

internal val v2CompatProtocols = mapOf("reality" to "Reality", "hysteria2" to "Hysteria2")

internal fun v2InstallProtocols(domain: String, selected: Set<String> = v2CompatProtocols.keys): String {
    require(v2CompatProtocols.keys.containsAll(selected))
    val extras = if (domain.isBlank()) listOf("reality") else v2CompatProtocols.keys.filter { it in selected }
    return (listOf("v2") + extras).joinToString(",")
}

internal class CompatLinkFailure(message: String) : IllegalStateException(message)

class NodeManager(private val context: Context, val host: String, val port: Int, val user: String,
                  private val password: String, private val fingerprint: String, private val sshKey: ByteArray? = null,
                  private val loadKey: (() -> ByteArray)? = null) {
    private fun <T> session(action: (Session) -> T): T {
        require(user.matches(Regex("[A-Za-z_][a-zA-Z0-9_-]{0,31}")) && port in 1..65535)
        require(fingerprint.matches(Regex("SHA256:[a-zA-Z0-9+/]{43}")))
        if (OwnerAccess(context).knownPin(SshEndpoint(host, port).authority)?.let { it!=fingerprint }==true) throw ChangedServer()
        val jsch = JSch()
        val identity = loadKey?.invoke() ?: sshKey?.copyOf()
        identity?.let { try { jsch.addIdentity("Nexus SSH", it, null, password.takeIf(String::isNotEmpty)?.toByteArray()) } finally { it.fill(0) } }
        val repository = CheckedHost { it == fingerprint }
        jsch.hostKeyRepository = repository
        val ssh = jsch.getSession(user, host, port)
        ssh.setConfig("StrictHostKeyChecking","yes")
        ssh.setConfig("PreferredAuthentications",if (identity == null) "password" else "publickey")
        ssh.timeout = 30000
        if (identity == null) ssh.setPassword(password.toByteArray())
        try { ssh.connect(10000); return action(ssh) } catch (e: JSchException) { if (repository.changed) throw ChangedServer(); throw e } finally { ssh.disconnect(); jsch.removeAllIdentity() }
    }
    private class CheckedHost(val accept: (String) -> Boolean) : HostKeyRepository {
            var changed = false
            override fun check(host: String?, key: ByteArray): Int {
                val actual = "SHA256:" + Base64.encodeToString(sha(key), Base64.NO_WRAP or Base64.NO_PADDING)
                changed = !accept(actual)
                return if (!changed) HostKeyRepository.OK else HostKeyRepository.CHANGED
            }
            override fun getHostKey() = emptyArray<HostKey>()
            override fun getHostKey(host: String?, type: String?) = emptyArray<HostKey>()
            override fun getKnownHostsRepositoryID() = "Pinned server key"
            override fun remove(host: String?, type: String?) = Unit
            override fun remove(host: String?, type: String?, key: ByteArray?) = Unit
            override fun add(hostkey: HostKey?, ui: UserInfo?) = Unit
        }

    companion object {
        internal fun discover(endpoint: SshEndpoint): String {
            var found: String? = null
            val jsch = JSch()
            jsch.hostKeyRepository = CheckedHost { pin -> found = pin; false }
            val session = jsch.getSession("root",endpoint.host,endpoint.port)
            session.setConfig("StrictHostKeyChecking","yes")
            try { session.connect(8000) } catch (e: JSchException) { if (found == null) throw e }
            finally { session.disconnect() }
            return requireNotNull(found)
        }
    }

    fun authorize(publicKey: String, deploy: Boolean) = session { ssh ->
        require(publicKey.matches(Regex("(ssh-ed25519|ecdsa-sha2-nistp256) [A-Za-z0-9+/=]+ Nexus")))
        val installed = installedV2(ssh)
        check(installed != deploy) { if (installed) "这台服务器已经部署，请选择绑定" else "尚未找到部署，请选择部署" }
        val key = "restrict $publicKey"
        run(ssh, "umask 077 && mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && " +
            "(grep -qxF '$key' ~/.ssh/authorized_keys || printf '\\n%s\\n' '$key' >> ~/.ssh/authorized_keys)")
        Unit
    }
    private fun installedV2(ssh: Session) = run(ssh, "if test -x /opt/nexus-v2/bin/nexus-node; then echo yes; fi") == "yes"


    private fun run(ssh: Session, cmd: String, data: String = "", limit: Int = 131072): String {
        val channel = ssh.openChannel("exec") as ChannelExec
        try {
            channel.setCommand(cmd)
            channel.setInputStream(data.byteInputStream())
            val output = channel.inputStream
            channel.connect(8000)
            val result = output.bounded(limit).toString(Charsets.UTF_8)
            val deadline = android.os.SystemClock.elapsedRealtime() + 3000
            while (!channel.isClosed && android.os.SystemClock.elapsedRealtime() < deadline) Thread.sleep(20)
            check(channel.exitStatus == 0)
            return result.trim()
        } finally { channel.disconnect() }
    }
    private fun upload(sftp: ChannelSftp, asset: String, destination: String): String {
        val digest = MessageDigest.getInstance("SHA-256")
        context.assets.open(asset).use { input ->
            java.security.DigestInputStream(input, digest).use { sftp.put(it, destination) }
        }
        return hex(digest.digest())
    }

    private fun stageNode(ssh: Session, name: String, arch: String, destination: String): String {
        val asset = "linux/$arch/$name"
        ssh.setServerAliveInterval(15000)
        val digest = if (BuildConfig.DEBUG) {
            val sftp = ssh.openChannel("sftp") as ChannelSftp
            try {
                sftp.connect(8000)
                upload(sftp, asset, destination)
            } finally { sftp.disconnect() }
        } else {
            val pin = context.assets.open("$asset.sha256").bufferedReader().use { it.readText().trim() }
            check(pin.matches(Regex("[0-9a-f]{64}"))) { "安装包中的节点校验值无效" }
            val cpu = if (arch == "x86_64") "amd64" else "arm64"
            val url = "https://github.com/shejiang000-prog/Nexus/releases/download/v${BuildConfig.VERSION_NAME}/$name-linux-$cpu"
            try {
                val fetch = context.assets.open("download_node.sh").bufferedReader().use { it.readText().replace("\r\n", "\n") }
                run(ssh, "timeout 300 sh -s -- '$url' '$destination'", fetch)
            } catch (e: Exception) {
                throw IllegalStateException("服务器下载节点程序失败，请检查服务器网络及对应版本的发布文件", e)
            }
            pin
        }
        check(run(ssh, "sha256sum '$destination'").substringBefore(' ') == digest) { "节点程序 SHA-256 校验失败" }
        return digest
    }

    internal fun installV2(domain: String, extraProtocols: Set<String> = v2CompatProtocols.keys) {
        require(nodeIP(host) != null) { "请填写有效的服务器 IP" }
        val protocols = v2InstallProtocols(domain, extraProtocols)
        val address = if (domain.isBlank()) "--public-host $host" else {
            require(domain.length <= 253 && '.' in domain && nodeIP(domain) == null &&
                domain.split('.').all { it.matches(Regex("[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?")) }) { "请填写有效的域名" }
            "--domain $domain"
        }
        runV2Installer("install --non-interactive $address --protocols $protocols")
    }

    internal fun upgradeV2() = runV2Installer("upgrade")

    private fun runV2Installer(arguments: String) = session { ssh ->
        val arch = run(ssh, "uname -m")
        require(arch in setOf("x86_64", "aarch64"))
        val dir = run(ssh, "umask 077 && mktemp -d /tmp/nexus-upload.XXXXXXXXXX")
        require(dir.matches(Regex("/tmp/nexus-upload\\.[A-Za-z0-9]{10}")))
        val staged = "$dir/nexus-node"
        try {
            stageNode(ssh, "nexus-node", arch, staged)
            val sudo = if (user == "root") "" else "sudo -n "
            run(ssh, "chmod 0700 $staged && ${sudo}$staged $arguments")
        } finally { runCatching { run(ssh, "rm -f -- $staged && rmdir -- $dir") } }
        Unit
    }

    private val v2Ledger: String get() =
        (if (user == "root") "" else "sudo -n ") +
            "runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db"

    internal fun v2Invite(days: Int? = null): String = session { ssh ->
        val validity = V2ShareValidity(days)
        val now = run(ssh, "date -u +%s").toLong()
        val account = run(ssh, "$v2Ledger ${validity.createUser(now)}")
        require(account.matches(Regex("[0-9a-f]{32}")))
        run(ssh, "$v2Ledger ${validity.invite(account,now)}").also { V2Invite.parse(it) }
    }

    internal fun v2Renew(account: String, validity: V2ShareValidity) {
        require(account.matches(Regex("[0-9a-f]{32}")))
        session { ssh ->
            val expires = if (validity.days == null) null else {
                val row = run(ssh, "$v2Ledger users").lineSequence().filter(String::isNotBlank)
                    .map { JSONObject(it) }.first { it.getString("id") == account }
                check(row.has("expires_at"))
                if (row.isNull("expires_at")) null else row.getLong("expires_at")
            }
            val now = run(ssh, "date -u +%s").toLong()
            run(ssh, "$v2Ledger ${validity.renew(account,now,expires)}")
        }
    }

    internal fun v2CompatLink(account: String, protocol: String): String {
        require(account.matches(Regex("[0-9a-f]{32}")))
        val label = requireNotNull(v2CompatProtocols[protocol])
        val prefix = if (protocol == "reality") "vless://" else "hysteria2://"
        return session { ssh ->
            if (run(ssh, "if test -f /etc/systemd/system/nexus-v2-$protocol.service; then echo installed; fi") != "installed")
                throw CompatLinkFailure("这台服务器未部署 $label。")
            // Emit only a fixed marker on failure; remote diagnostics can contain credentials.
            val links = run(ssh, "$v2Ledger compat enable $account $protocol && $v2Ledger compat export $account links || echo ISSUE_FAILED")
            if (links.lineSequence().last() == "ISSUE_FAILED")
                throw CompatLinkFailure("$label 链接未签发，请检查账号有效期、节点版本和服务状态。")
            links.lineSequence().singleOrNull { it.startsWith(prefix) }
                ?: throw CompatLinkFailure("节点未返回此协议的有效链接。")
        }
    }

    internal fun v2Devices() = session { ssh ->
        org.json.JSONArray().apply {
            run(ssh, "$v2Ledger devices").lineSequence().filter(String::isNotBlank).forEach { put(JSONObject(it)) }
        }
    }

    internal fun v2Revoke(id: String) {
        require(id.matches(Regex("[0-9a-f]{32}")))
        session { run(it, "$v2Ledger revoke $id") }
    }

}
