package dev.nexus.localclient

import android.app.Instrumentation
import android.os.Bundle
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
    fun globalWakeups() = runCatching { File("/sys/power/wakeup_count").readText().trim().toLong() }.getOrNull()
    fun threadWakeups() = File("/proc/self/task").listFiles().orEmpty().mapNotNull { dir ->
        val wakeups=runCatching {
            File(dir,"sched").useLines { lines ->
                lines.firstOrNull { it.substringBefore(':').trim() in setOf("se.statistics.nr_wakeups","se.nr_wakeups") }
                    ?.substringAfter(':')?.trim()?.toLongOrNull()
            }
        }.getOrNull()
        wakeups?.let {dir.name to it}
    }.toMap()
    val startKernel=globalWakeups()
    val startTasks=threadWakeups()
    var changes=0
    val watch=launch(start=CoroutineStart.UNDISPATCHED) {
        TunnelService.state.collect { if(!it.vpn || !it.bearer.equals(bearer,true)) changes++ }
    }
    val start=SystemClock.elapsedRealtime()
    try { delay(600000) } finally { watch.cancelAndJoin() }
    result.putLong("idle-ms",SystemClock.elapsedRealtime()-start)
    result.putInt("idle-unexpected-states",changes)
    val endKernel=globalWakeups()
    if(startKernel!=null && endKernel!=null) result.putLong("system-wakeup-count-delta",endKernel-startKernel)
    val endTasks=threadWakeups()
    val common=startTasks.keys.intersect(endTasks.keys)
    if(common.isNotEmpty()) {
        result.putInt("sched-wakeup-surviving-threads",common.size)
        result.putLong("sched-wakeup-delta",common.sumOf { endTasks.getValue(it)-startTasks.getValue(it) })
    }
    check(changes==0)
}
