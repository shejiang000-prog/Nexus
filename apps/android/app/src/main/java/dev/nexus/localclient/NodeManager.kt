package dev.nexus.localclient

import android.content.Context
import android.util.Base64
import com.jcraft.jsch.*
import org.json.JSONObject
import java.net.URI
import java.net.URL
import java.security.MessageDigest
import java.security.cert.X509Certificate
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

private fun sha(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes)
private fun hex(bytes: ByteArray) = bytes.joinToString("") { "%02x".format(it) }
private fun base64(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.NO_WRAP)

class NodeManager(private val context: Context, val host: String, val port: Int, val user: String,
                  private val password: String, private val fingerprint: String, private val sshKey: ByteArray? = null,
                  private val loadKey: (() -> ByteArray)? = null) {
    private fun <T> session(action: (Session) -> T): T {
        require(user.matches(Regex("[A-Za-z_][a-zA-Z0-9_-]{0,31}")) && port in 1..65535)
        require(fingerprint.matches(Regex("SHA256:[a-zA-Z0-9+/]{43}")))
        if (port==22 && OwnerAccess(context).knownPin(host)?.let { it!=fingerprint }==true) throw ChangedServer()
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
        fun discover(host: String): String {
            var found: String? = null
            val jsch = JSch()
            jsch.hostKeyRepository = CheckedHost { pin -> found = pin; false }
            val session = jsch.getSession("root",host,22)
            session.setConfig("StrictHostKeyChecking","yes")
            try { session.connect(8000) } catch (e: JSchException) { if (found == null) throw e }
            finally { session.disconnect() }
            return requireNotNull(found)
        }
    }

    fun authorize(publicKey: String, deploy: Boolean, v2: Boolean = false) = session { ssh ->
        require(publicKey.matches(Regex("(ssh-ed25519|ecdsa-sha2-nistp256) [A-Za-z0-9+/=]+ Nexus")))
        val kind = installedKind(ssh)
        val installed = if (v2) kind != 0 else kind == 1
        check(installed != deploy) { if (installed) "这台服务器已经部署，请选择绑定" else "尚未找到部署，请选择部署" }
        val key = "restrict $publicKey"
        run(ssh, "umask 077 && mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && " +
            "(grep -qxF '$key' ~/.ssh/authorized_keys || printf '\\n%s\\n' '$key' >> ~/.ssh/authorized_keys)")
        Unit
    }
    private fun installedKind(ssh: Session): Int = run(ssh,
        "if test -x /opt/nexus-v2/bin/nexus-node; then echo 2; elif test -f /opt/nexus-node/node_admin.py; then echo 1; else echo 0; fi"
    ).toInt()

    fun isV2(): Boolean = session { installedKind(it) == 2 }

    private fun run(ssh: Session, cmd: String, data: JSONObject? = null, limit: Int = 131072): String {
        val channel = ssh.openChannel("exec") as ChannelExec
        try {
            channel.setCommand(cmd)
            channel.setInputStream((data?.toString() ?: "").byteInputStream())
            val output = channel.inputStream
            channel.connect(8000)
            val result = output.bounded(limit).toString(Charsets.UTF_8)
            val deadline = android.os.SystemClock.elapsedRealtime() + 3000
            while (!channel.isClosed && android.os.SystemClock.elapsedRealtime() < deadline) Thread.sleep(20)
            check(channel.exitStatus == 0)
            return result.trim()
        } finally { channel.disconnect() }
    }
    fun deploy(name: String, dns: String, nodePort: Int, claimPort: Int, migration: JSONObject? = null): Profile {
        require(name.length in 1..64 && nodePort in 1024..65535 && claimPort in 1024..65535 && nodePort != claimPort)
        val config = JSONObject().put("name", name).put("host", host).put("dns", dns)
            .put("port", nodePort).put("claim_port", claimPort).put("pub", base64(Identity.publicKey()))
        migration?.let { config.put("migration",it) }
        return Profile.parse(install(config))
    }
    private fun upload(sftp: ChannelSftp, asset: String, destination: String): String {
        val digest = MessageDigest.getInstance("SHA-256")
        context.assets.open(asset).use { input ->
            java.security.DigestInputStream(input, digest).use { sftp.put(it, destination) }
        }
        return hex(digest.digest())
    }

    internal fun installV2(domain: String, extraProtocols: Set<String> = emptySet()) = session { ssh ->
        require(nodeIP(host) != null) { "请填写有效的服务器 IP" }
        require(extraProtocols.all { it == "reality" || it == "hysteria2" })
        val protocols = if (domain.isBlank() || extraProtocols.isEmpty()) ""
            else " --protocols " + (listOf("v2") + extraProtocols.sorted()).joinToString(",")
        val address = if (domain.isBlank()) "--public-host $host" else {
            require(domain.length <= 253 && '.' in domain && nodeIP(domain) == null &&
                domain.split('.').all { it.matches(Regex("[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?")) }) { "请填写有效的域名" }
            "--domain $domain"
        }
        val arch = run(ssh, "uname -m")
        require(arch in setOf("x86_64", "aarch64"))
        val dir = run(ssh, "umask 077 && mktemp -d /tmp/nexus-upload.XXXXXXXXXX")
        require(dir.matches(Regex("/tmp/nexus-upload\\.[A-Za-z0-9]{10}")))
        val staged = "$dir/nexus-node"
        try {
            val sftp = ssh.openChannel("sftp") as ChannelSftp
            val digest = try {
                sftp.connect(8000)
                upload(sftp, "linux/$arch/nexus-node", staged)
            } finally { sftp.disconnect() }
            check(run(ssh, "sha256sum $staged").substringBefore(' ') == digest)
            ssh.setServerAliveInterval(15000)
            val sudo = if (user == "root") "" else "sudo -n "
            run(ssh, "chmod 0700 $staged && ${sudo}$staged install --non-interactive $address$protocols")
        } finally { runCatching { run(ssh, "rm -f -- $staged && rmdir -- $dir") } }
        Unit
    }

    private val v2Ledger: String get() =
        (if (user == "root") "" else "sudo -n ") +
            "runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db"

    internal fun v2Invite(days: Int? = null): String = session { ssh ->
        require(days == null || days in 1..365)
        fun until(n: Int) = run(ssh, "date -u -d '+$n days' +%Y-%m-%dT%H:%M:%SZ").also {
            java.time.Instant.parse(it)
            require(it.matches(Regex("[0-9TZ:-]+")))
        }
        val deadline = until(minOf(days ?: 7, 7))
        val accountEnd = days?.let { " " + until(it) }.orEmpty()
        val account = run(ssh, "$v2Ledger create-user$accountEnd")
        require(account.matches(Regex("[0-9a-f]{32}")))
        run(ssh, "$v2Ledger invite --link $account $deadline").also { V2Invite.parse(it) }
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

    fun exportMigration(pass: CharArray, output: java.io.OutputStream) {
        writeMigration(pass,output) { install(JSONObject().put("action","migration-export")).toByteArray() }
    }
    fun maintain(action: String) {
        require(action in setOf("upgrade","rollback","archive","reactivate"))
        check(JSONObject(install(JSONObject().put("action",action))).getBoolean("active")== (action!="archive"))
    }
    private fun install(config: JSONObject): String = session { ssh ->
        val arch = run(ssh, "uname -m")
        require(arch in setOf("x86_64", "aarch64"))
        val dir = run(ssh, "umask 077 && mktemp -d /tmp/nexus-upload.XXXXXXXXXX")
        require(dir.matches(Regex("/tmp/nexus-upload\\.[A-Za-z0-9]{10}")))
        try {
            val hashes = JSONObject()
            val sftp = ssh.openChannel("sftp") as ChannelSftp
            try {
                sftp.connect(8000)
                for ((file, asset) in listOf("nexus-server" to "linux/$arch/nexus-server", "node_admin.py" to "node_admin.py", "install_node.py" to "install_node.py")) {
                    hashes.put(file, upload(sftp, asset, "$dir/$file"))
                }
            } finally { sftp.disconnect() }
            check(run(ssh,"sha256sum $dir/install_node.py").substringBefore(' ')==hashes.getString("install_node.py"))
            config.put("arch",arch).put("sha256",hashes)
            val sudo = if (user == "root") "" else "sudo -n "
            run(ssh, "${sudo}python3 -I $dir/install_node.py", config, if (config.optString("action")=="migration-export") 4*1024*1024+1 else 131072)
        } finally { runCatching { run(ssh, "rm -f -- $dir/install_node.py $dir/node_admin.py $dir/nexus-server && rmdir -- $dir") } }
    }
    fun service(restart: Boolean): String = session { ssh ->
        val sudo = if (user=="root") "" else "sudo -n "
        if (restart) run(ssh,"${sudo}systemctl restart nexus-node.service nexus-claim.service")
        val report = run(ssh, """
            LC_ALL=C
            export LC_ALL
            /opt/nexus-node/nexus-server --version 2>/dev/null || echo 'nexus-server (旧版本)'
            systemctl show nexus-node.service nexus-claim.service -p Id -p ActiveState -p Result -p NRestarts --no-pager
            awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2} END {printf "内存  %.0f / %.0f MiB\n",(t-a)/1024,t/1024}' /proc/meminfo
            awk '{printf "运行时间  %.1f 小时\n",$1/3600}' /proc/uptime
            awk '{printf "系统负载（1/5/15 分钟）  %s / %s / %s\n",$1,$2,$3}' /proc/loadavg
            { head -1 /proc/stat; sleep 0.25; head -1 /proc/stat; } | awk '{t=0; for(i=2;i<=9;i++) t+=${'$'}i; idle=$5+$6} NR==1 {t0=t;i0=idle} END {if(t>t0) printf "CPU 使用率  %.1f%%\n",100*(1-(idle-i0)/(t-t0))}'
            awk -F: '/:/ && $1 !~ /^[[:space:]]*lo$/ {split($2,n," "); rx+=n[1]; tx+=n[9]} END {printf "网卡累计  ↓ %.1f MiB  ↑ %.1f MiB\n",rx/1048576,tx/1048576}' /proc/net/dev
            journalctl -q -u nexus-node.service -u nexus-claim.service -p err -n1 -o short-unix 2>/dev/null | awk 'NR==1 && $1 ~ /^[0-9]+[.]/ {print "最近错误记录（Unix 秒）  " $1}'
        """.trimIndent())
        report.lineSequence().joinToString("\n") { it.replace("Id=nexus-node.service","隧道服务")
            .replace("Id=nexus-claim.service","邀请服务").replace("ActiveState=","状态  ")
            .replace("NRestarts=","自动重启  ").replace("Result=","最近运行结果  ") }
    }
    fun owner(action: String, args: JSONObject = JSONObject()): String {
        require(action in setOf("invite","devices","revoke","edit","audit","owner","backup","restore"))
        val sudo = if (user == "root") "" else "sudo -n "
        return session { run(it, "${sudo}runuser -u nexus-node -- python3 -I /opt/nexus-node/node_admin.py /var/lib/nexus-node/identity $action", args) }
    }
}

internal fun writeMigration(pass: CharArray, output: java.io.OutputStream, snapshot: () -> ByteArray) {
    val random = java.security.SecureRandom()
    val salt = ByteArray(16).also(random::nextBytes)
    val iv = ByteArray(12).also(random::nextBytes)
    val cipher = migrationCipher(javax.crypto.Cipher.ENCRYPT_MODE,pass,salt,iv)
    val data = snapshot()
    try {
        output.write("NXM1".toByteArray())
        output.write(salt)
        output.write(iv)
        output.write(cipher.doFinal(data))
        output.flush()
    } finally { data.fill(0) }
}

private fun migrationCipher(mode: Int, pass: CharArray, salt: ByteArray, iv: ByteArray): javax.crypto.Cipher {
    require(pass.size in 12..256)
    val spec = javax.crypto.spec.PBEKeySpec(pass,salt,600000,256)
    val secret = try { javax.crypto.SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded }
        finally { spec.clearPassword(); pass.fill('\u0000') }
    return try {
        javax.crypto.Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(mode,javax.crypto.spec.SecretKeySpec(secret,"AES"),javax.crypto.spec.GCMParameterSpec(128,iv))
        }
    } finally { secret.fill(0) }
}

internal fun readMigration(input: java.io.InputStream, pass: CharArray): JSONObject {
    val data = input.bounded(4*1024*1024+64)
    require(data.size > 48 && data.copyOfRange(0,4).contentEquals("NXM1".toByteArray()))
    val cipher = migrationCipher(javax.crypto.Cipher.DECRYPT_MODE,pass,data.copyOfRange(4,20),data.copyOfRange(20,32))
    val plain = cipher.doFinal(data,32,data.size-32)
    return try { JSONObject(plain.toString(Charsets.UTF_8)).also { require(it.getString("format")=="nexus-migration/1") } }
        finally { plain.fill(0) }
}

data class Invitation(val name: String, val url: URL, val pin: String, private val token: String) {
    companion object {
        fun parse(link: String): Invitation {
            val prefix = "nexus://invite/"
            require(link.startsWith(prefix) && link.length in 32..4096 && link.substring(prefix.length).matches(Regex("[A-Za-z0-9_-]+={0,2}")))
            val value = checkedObject(Base64.decode(link.substring(prefix.length), Base64.URL_SAFE).toString(Charsets.UTF_8))
            val uri = URI(value.getString("url"))
            require(value.getInt("v") == 1 && uri.scheme == "https" && !uri.host.isNullOrBlank() && uri.userInfo == null &&
                uri.path == "/claim" && uri.rawQuery == null && uri.fragment == null)
            val pin = value.getString("pin")
            val token = value.getString("token")
            require(pin.matches(Regex("[a-f0-9]{64}")) && token.matches(Regex("[A-Za-z0-9_-]{43}")))
            val name=value.getString("name")
            require(name.isNotBlank() && name.length<=64 && name.none {it.isISOControl() || Character.getType(it)==Character.FORMAT.toInt()})
            return Invitation(name, uri.toURL(), pin, token)
        }
    }
    fun claim(deviceName: String): Profile {
        require(deviceName.isNotBlank() && deviceName.length <= 64)
        val trust = object : X509TrustManager {
            override fun getAcceptedIssuers() = emptyArray<X509Certificate>()
            override fun checkClientTrusted(chain: Array<X509Certificate>?, auth: String?) =
                throw java.security.cert.CertificateException("client authentication unsupported")
            override fun checkServerTrusted(chain: Array<X509Certificate>, auth: String?) {
                val leaf = chain.first()
                leaf.checkValidity()
                if (hex(sha(leaf.encoded)) != pin) throw java.security.cert.CertificateException("invitation pin mismatch")
            }
        }
        val tls = SSLContext.getInstance("TLS").apply { init(null, arrayOf<TrustManager>(trust), null) }
        val connection = url.openConnection() as HttpsURLConnection
        try {
            connection.sslSocketFactory = tls.socketFactory
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 8000; connection.readTimeout = 10000
            connection.requestMethod = "POST"; connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            val pub = Identity.publicKey()
            val proof = Identity.sign("Nexus claim v1\u0000".toByteArray() + token.toByteArray() + pub,requireNotNull(Identity.owner(pub)))
            val data = JSONObject().put("token", token).put("name", deviceName).put("pub", base64(pub))
                .put("proof",base64(proof)).toString().toByteArray()
            connection.setFixedLengthStreamingMode(data.size)
            connection.outputStream.use { it.write(data) }
            check(connection.responseCode == 200)
            return Profile.parse(connection.inputStream.bounded(32768).toString(Charsets.UTF_8))
        } finally { connection.disconnect() }
    }
}
