package dev.nexus.localclient

import java.time.Instant

internal class V2ShareValidity(val days: Int?) {
    init { require(days == null || days in 1..3650) }

    companion object {
        fun parse(text: String, permanent: Boolean): V2ShareValidity? {
            if (permanent) return V2ShareValidity(null)
            if (!text.all { it in '0'..'9' }) return null
            return text.toIntOrNull()?.takeIf { it in 1..3650 }?.let { V2ShareValidity(it) }
        }
    }

    private fun end(start: Long, count: Int) = Instant.ofEpochSecond(start).plusSeconds(count * 86400L).toString()

    fun createUser(now: Long) = days?.let { "create-user " + end(now,it) } ?: "create-user"

    fun invite(account: String, now: Long) = "invite --link $account " + end(now,minOf(days ?: 7,7))

    fun renew(account: String, now: Long, expiresAt: Long?) =
        "renew $account " + (days?.let { end(maxOf(now,expiresAt ?: now),it) } ?: "never")
}
