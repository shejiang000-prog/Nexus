package dev.nexus.localclient

import android.app.Instrumentation
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import java.io.File
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking

/** Observe local state events without sending a health request or polling the device from adb. */
internal fun Instrumentation.idleBearer(bearer:String, result:Bundle) = runBlocking {
    fun alarms():Long {
        val info=ParcelFileDescriptor.AutoCloseInputStream(uiAutomation.executeShellCommand("dumpsys alarm")).bufferedReader().use { it.readText() }
        val stats=info.substringAfter("Alarm Stats:","")
        check(stats.isNotEmpty()){"AlarmManager wakeup counters unavailable"}
        val line=Regex("""(?m)^\s*[^ :]+:${Regex.escape(targetContext.packageName)}\s+.*? (\d+) wakeups?:\s*$""")
        return line.findAll(stats).sumOf { it.groupValues[1].toLong() }
    }
    // schedstat's third counter is CPU dispatches (not radio wakeups). Observe common surviving threads.
    fun dispatches() = File("/proc/self/task").listFiles().orEmpty().mapNotNull { dir ->
        runCatching { dir.name to File(dir,"schedstat").readText().trim().split(Regex("\\s+"))[2].toLong() }.getOrNull()
    }.toMap()
    val startAlarms=alarms()
    val startTasks=dispatches()
    var changes=0
    val watch=launch(start=CoroutineStart.UNDISPATCHED) {
        TunnelService.state.collect { if(!it.vpn || !it.bearer.equals(bearer,true)) changes++ }
    }
    val start=SystemClock.elapsedRealtime()
    try { delay(600000) } finally { watch.cancelAndJoin() }
    result.putLong("idle-ms",SystemClock.elapsedRealtime()-start)
    result.putInt("idle-unexpected-states",changes)
    result.putLong("uid-wakeup-alarms",alarms()-startAlarms)
    val endTasks=dispatches()
    val common=startTasks.keys.intersect(endTasks.keys)
    if(common.isNotEmpty()) {
        result.putInt("sched-surviving-threads",common.size)
        result.putLong("cpu-dispatch-delta",common.sumOf { endTasks.getValue(it)-startTasks.getValue(it) })
    }
    check(changes==0)
}
