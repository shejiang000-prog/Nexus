package dev.nexus.localclient

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class V2ShareValidityTest {
    @Test fun daysMustBeInRangeUnlessPermanentIsSelected() {
        for (text in listOf("", "0", "-1", "1.5", "3651", "99999999999")) {
            assertNull(V2ShareValidity.parse(text, false))
        }
        assertEquals(1, V2ShareValidity.parse("1", false)?.days)
        assertEquals(3650, V2ShareValidity.parse("3650", false)?.days)
        assertNull(requireNotNull(V2ShareValidity.parse("42", true)).days)
    }

    @Test fun oneDayUsesTheLaterOfServerTimeAndCurrentExpiry() {
        val day = V2ShareValidity(1)
        val now = 86400L
        val fromNow = "renew user 1970-01-03T00:00:00Z"
        assertEquals(fromNow, day.renew("user", now, null))
        assertEquals(fromNow, day.renew("user", now, now - 60))
        assertEquals("renew user 1970-01-04T00:00:00Z", day.renew("user", now, 2 * now))
        assertEquals("invite --link user 1970-01-03T00:00:00Z", day.invite("user", now))
    }
}
