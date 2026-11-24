package dev.nexus.localclient

import android.app.Instrumentation
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.Bundle
import android.os.Debug
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.net.Socket
import java.security.SecureRandom

// Read local process counters only. Sampling idle must not send traffic through the VPN.
internal fun Instrumentation.soak(result: Bundle, traffic: () -> Unit, stop: () -> Unit) {
    val clock = SystemClock::elapsedRealtime
    val mobile = Class.forName("mobile.Mobile")
    val goroutines = mobile.getMethod("labGoroutines")
    val samples = mutableListOf<String>()
    val start = clock()
    fun sample(label: String): Pair<Int, Int> {
        val rss = File("/proc/self/status").useLines { lines ->
            lines.first { it.startsWith("VmRSS:") }.substringAfter(":").trim().substringBefore(" ").toInt()
        }
        val pss = Debug.MemoryInfo().also { Debug.getMemoryInfo(it) }.totalPss
        val fds = requireNotNull(File("/proc/self/fd").list()).size
        val go = (goroutines.invoke(null) as Number).toInt()
        val row = "$label,${clock()-start},$pss,$rss,$go,$fds"
        samples.add(row)
        result.putString("app-samples-ms-pssKiB-rssKiB-go-fd",samples.joinToString(" | "))
        sendStatus(1,Bundle().apply {putString("sample",row)})
        return go to fds
    }
    var cycles=0
    val initial = sample("start")
    var idle = initial
    for (minute in 5..45 step 5) {
        val end = start + minute * 60000L
        while (clock() < end) {
            if (minute<=30) { traffic(); cycles++ }
            val left = end-clock()
            if (left>0) SystemClock.sleep(if(minute<=30) minOf(15000,left) else left)
        }
        idle = sample(if(minute<=30) "mixed-$minute" else "idle-${minute-30}")
    }
    result.putInt("mixed-cycles",cycles)
    stop()
    SystemClock.sleep(10000)
    val stopped = sample("disconnected")
    val udp = mobile.getMethod("labUDPWorkers").invoke(null) as Int
    val tcp = mobile.getMethod("labTCPWorkers").invoke(null) as Int
    result.putString("stopped-workers","udp=$udp tcp=$tcp")
    check(udp == 0 && tcp == 0) { "workers after stop: udp=$udp tcp=$tcp" }
    check(kotlin.math.abs(idle.first-initial.first) <= 10) { "goroutines: $initial -> $idle" }
    check(idle.second <= initial.second && stopped.second <= initial.second) { "fd: $initial -> $idle -> $stopped" }
}

internal fun Instrumentation.networkChanges(kind:String, result:Bundle, connect:()->Socket, echo:(Socket)->Unit) {
    val cm=targetContext.getSystemService(ConnectivityManager::class.java)
    val clock=SystemClock::elapsedRealtime
    fun shell(command:String) = ParcelFileDescriptor.AutoCloseInputStream(uiAutomation.executeShellCommand(command))
        .bufferedReader().use { it.readText().trim() }
    fun network(transport:Int) = cm.allNetworks.firstOrNull {
        cm.getNetworkCapabilities(it)?.let { caps ->
            caps.hasTransport(transport) && !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) &&
                    caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
        }==true
    }
    fun until(limit:Long, test:()->Boolean):Boolean {
        val end=clock()+limit
        do { if(test())return true; SystemClock.sleep(100) } while(clock()<end)
        return false
    }
    fun available() = runCatching { connect().use(echo) }.isSuccess
    fun deliveries(id:Long):Int = connect().use { s ->
        DataOutputStream(s.getOutputStream()).apply { writeByte(81);writeLong(id);flush() }
        DataInputStream(s.getInputStream()).readInt()
    }
    val wifi=NetworkCapabilities.TRANSPORT_WIFI
    val mobile=NetworkCapabilities.TRANSPORT_CELLULAR
    val originalWifi=shell("settings get global wifi_on")
    val originalData=shell("settings get global mobile_data")
    val originalAirplane=shell("settings get global airplane_mode_on")
    check(originalWifi=="1" && originalData=="1" && originalAirplane=="0") { "start with Wi-Fi and data enabled, airplane mode off" }
    val trials=mutableListOf<String>()
    val random=SecureRandom()
    try {
        repeat(10) { n ->
            check(until(30000){available()})
            val id=random.nextLong()
            connect().use { old ->
                // Delivered operation receives no ACK on this socket. Read its independent ledger.
                DataOutputStream(old.getOutputStream()).apply { writeByte(78);writeLong(id);flush() }
                check(until(3500){runCatching{deliveries(id)==1}.getOrDefault(false)})
                val forward=clock()
                if(kind=="airplane") shell("cmd connectivity airplane-mode enable") else {
                    if(kind=="offline") shell("svc data disable")
                    shell("svc wifi disable")
                }
                val first=if(kind=="handover") {
                    if(until(10000){network(mobile)!=null && !shell("ip link show wlan0").contains("state UP") && available()})
                        clock()-forward else -1L
                } else {
                    check(until(15000){!available()}) { "physical disconnection not observed" }
                    if(kind=="offline") SystemClock.sleep(30000)
                    clock()-forward
                }
                val restored=clock()
                if(kind=="airplane") shell("cmd connectivity airplane-mode disable") else {
                    if(kind=="offline") shell("svc data enable")
                    shell("svc wifi enable")
                }
                val back=if(until(40000){network(wifi)!=null && available()}) clock()-restored else -1L
                val count=runCatching{deliveries(id)}.getOrDefault(-1)
                val row="${n+1},$first,$back,$count"
                trials.add(row)
                result.putString("$kind-trials",trials.joinToString(" | "))
                sendStatus(1,Bundle().apply{putString("$kind-trial",row)})
                check(back>=0 && count==1) { "automatic Wi-Fi recovery or delivery count failed" }
            }
        }
        if(kind=="handover") check(trials.all{it.split(",")[1]!="-1"}) { "no working cellular Network observed" }
    } finally {
        shell("cmd connectivity airplane-mode disable")
        shell("svc data enable")
        shell("svc wifi enable")
    }
}
