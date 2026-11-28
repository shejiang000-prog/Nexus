package dev.nexus.localclient

import android.os.Build
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** Sends what the user typed, with the app and Android versions, only when they press send. */
internal object Feedback {
    private const val ENDPOINT = "https://liuguang.club/api/feedback"
    const val MAX_BODY = 2000
    const val MAX_CONTACT = 100

    enum class Result { SENT, BUSY, FAILED }

    private fun system(): String {
        val model = Build.MODEL.orEmpty().filter { !it.isISOControl() }.trim()
        return ("Android " + Build.VERSION.RELEASE + if (model.isEmpty()) "" else " · $model").take(80)
    }

    internal fun payload(body: String, contact: String, version: String, system: String): String =
        JSONObject()
            .put("app", "android")
            .put("version", version)
            .put("system", system)
            .put("contact", contact.trim().filter { !it.isISOControl() }.take(MAX_CONTACT))
            .put("body", body.trim().take(MAX_BODY))
            .toString()

    fun send(body: String, contact: String): Result {
        val connection = URL(ENDPOINT).openConnection() as HttpURLConnection
        return try {
            connection.requestMethod = "POST"
            connection.connectTimeout = 10_000
            connection.readTimeout = 15_000
            connection.instanceFollowRedirects = false
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            val data = payload(body, contact, BuildConfig.VERSION_NAME, system()).toByteArray()
            connection.setFixedLengthStreamingMode(data.size)
            connection.outputStream.use { it.write(data) }
            when (connection.responseCode) {
                200 -> Result.SENT
                429 -> Result.BUSY
                else -> Result.FAILED
            }
        } catch (_: java.io.IOException) {
            Result.FAILED
        } finally {
            connection.disconnect()
        }
    }
}
