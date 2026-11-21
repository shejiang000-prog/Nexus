package dev.nexus.localclient

import android.app.Instrumentation
import android.os.Bundle
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.io.File
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import mobile.Mobile
import mobile.Signer

object M0Check {
    @JvmStatic
    fun run(instrumentation: Instrumentation, args: Bundle, result: Bundle) {
        val targetContext = instrumentation.targetContext
        val prepareKey = args.getString("mode") == "m0-key"
        check(targetContext.packageName == "dev.nexus.lab.m0")
        val dir = targetContext.noBackupFilesDir
        val alias = "nexus-m0-test"
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        if (prepareKey) {
            if (!store.containsAlias(alias)) {
                KeyPairGenerator.getInstance("EC", "AndroidKeyStore").apply {
                    initialize(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                        .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                        .setDigests(KeyProperties.DIGEST_SHA256).build())
                }.generateKeyPair()
            }
            val point = (store.getCertificate(alias).publicKey as ECPublicKey).w
            val key = (if (point.affineY.testBit(0)) "03" else "02") + point.affineX.toString(16).padStart(64, '0')
            File(dir, "m0-public").writeText(key)
            result.putString("key", "device public key ready")
        } else {
            val key = store.getKey(alias, null) as java.security.PrivateKey
            var signs = 0
            val signer = Signer { message ->
                signs++
                Signature.getInstance("SHA256withECDSA").run { initSign(key); update(message); sign() }
            }
            val config = org.json.JSONObject(File(dir, "m0-lab.json").readText())
            var stage = "start"
            try {
                val client = Mobile.start(config.getString("authority"), config.getString("endpoint"), "h2",
                    config.getString("ca").toByteArray(), ByteArray(16), null, signer, null)
                try {
                    stage = "association"
                    val assoc = client.openUDP(config.getString("echo"))
                    try {
                        for (size in listOf(4, 0, 65507)) {
                            stage = "echo $size"
                            val packet = ByteArray(size) { (it % 251).toByte() }
                            assoc.writePacket(packet)
                            check(assoc.readPacket().orEmptyBytes().contentEquals(packet))
                        }
                    } finally { assoc.close() }
                    check(signs == 1 && client.state() == "h2")
                    result.putString("h2", "4, 0, 65507 byte echoes verified; one Keystore signature")
                } finally { client.stop() }
            } catch (e: Exception) {
                val msg = e.message.orEmpty()
                val cause = listOf("extended connect", "connection refused", "timeout", "certificate", "invalid", "EOF", "export", "broken pipe", "unknown authority", "unreachable", "closed", "signature").firstOrNull { msg.contains(it, true) } ?: e.javaClass.simpleName
                result.putString("stage", "$stage / $cause")
                throw e
            }
        }
    }
    private fun ByteArray?.orEmptyBytes() = this ?: byteArrayOf()
}
