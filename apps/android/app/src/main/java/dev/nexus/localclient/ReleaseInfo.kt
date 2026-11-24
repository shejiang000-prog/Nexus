package dev.nexus.localclient

import android.app.Application
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import java.io.File
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject

internal const val REPOSITORY = "https://github.com/shejiang000-prog/Nexus"
internal data class ReleaseInfo(val tag:String,val summary:String) {
    val version get()=tag.removePrefix("v")
    val page get()="$REPOSITORY/releases/tag/$tag"
}

internal object ReleaseUpdates {
    private val lock=Mutex()
    val available=MutableStateFlow<ReleaseInfo?>(null)
    private val stable=Regex("v?(0|[1-9][0-9]{0,8})[.](0|[1-9][0-9]{0,8})[.](0|[1-9][0-9]{0,8})")
    private fun parts(tag:String)=requireNotNull(stable.matchEntire(tag)).groupValues.drop(1).map(String::toInt)
    fun parse(raw:String,current:String=BuildConfig.VERSION_NAME):ReleaseInfo? {
        val json=JSONObject(raw)
        val tag=json.getString("tag_name")
        val difference=parts(tag).zip(parts(current)).firstOrNull {it.first!=it.second}
        return if(difference!=null && difference.first>difference.second)
            ReleaseInfo(tag,json.optString("body").take(600).trim()) else null
    }
    private fun fetch(ctx:Context):String {
        val cm=ctx.getSystemService(ConnectivityManager::class.java)
        val active=cm.activeNetwork ?: error("offline")
        fun isVpn(n:android.net.Network)=cm.getNetworkCapabilities(n)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true
        val net=if(isVpn(active)) active else cm.allNetworks.firstOrNull(::isVpn) ?: active.takeUnless { TunnelService.state.value.vpn } ?: error("offline")
        val c=net.openConnection(URL("https://api.github.com/repos/shejiang000-prog/Nexus/releases/latest")) as HttpsURLConnection
        return try {
            c.connectTimeout=6000;c.readTimeout=6000;c.useCaches=false;c.instanceFollowRedirects=false
            c.setRequestProperty("User-Agent","Nexus/"+BuildConfig.VERSION_NAME)
            check(c.responseCode==200)
            c.inputStream.use {it.bounded(256*1024).toString(Charsets.UTF_8)}
        } finally { c.disconnect() }
    }
    suspend fun check(ctx:Context,manual:Boolean=false,request:(Context)->String=::fetch):Boolean=withContext(Dispatchers.IO){
        lock.withLock {
            val p=ctx.getSharedPreferences("release-check",0)
            fun cached()=p.getString("response",null)?.let {runCatching {parse(it)}.getOrNull()}
            available.value=cached()
            if(!manual && !p.getBoolean("automatic",true))return@withLock false
            val now=System.currentTimeMillis()
            // Automatic checks run at most daily; a tap on 「检查更新」 only waits out a minute.
            val wait=if(manual) 60_000L else 86_400_000L
            if(now-p.getLong("attempt",0) in 0 until wait)return@withLock p.getBoolean("ok",false)
            if(!p.edit().putLong("attempt",now).putBoolean("ok",false).commit())return@withLock false
            val info=try {parse(request(ctx))}catch(_:Exception){return@withLock false}
            available.value=info
            p.edit().putBoolean("ok",true).putString("response",info?.let{JSONObject().put("tag_name",it.tag).put("body",it.summary).toString()}).apply()
            true
        }
    }
}

class BootReceiver:BroadcastReceiver(){
    override fun onReceive(context:Context,intent:Intent){
        if(intent.action!=Intent.ACTION_BOOT_COMPLETED)return
        val p=context.getSharedPreferences("local",0)
        if(!p.getBoolean("boot-connect",false) || !p.contains(TunnelService.RESUME))return
        if(VpnService.prepare(context)!=null){
            p.edit().putBoolean("boot-needs-consent",true).apply()
            return
        }
        // Resolve the saved request again inside the service in case consent or settings changed.
        try { context.startForegroundService(Intent(context,TunnelService::class.java).setAction("resume-boot")) }
        catch(_:IllegalStateException){p.edit().putBoolean("boot-restricted",true).apply()}
        catch(_:SecurityException){p.edit().putBoolean("boot-needs-consent",true).apply()}
    }
}

class NexusApp:Application(){
    @android.annotation.SuppressLint("RestrictedApi")
    override fun attachBaseContext(base: Context) {
        super.attachBaseContext(base)
        // WorkManager 2.10.1 keeps the first logger, including one created before its configuration.
        androidx.work.Logger.setLogger(androidx.work.Logger.LogcatLogger(android.util.Log.ERROR))
    }
    override fun onCreate(){
        super.onCreate()
        clearLegacyData(this)
        val next=Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler {thread,problem->
            try {CrashReports.write(this,problem)}catch(_:Throwable){}finally {next?.uncaughtException(thread,problem)}
        }
    }
}

internal object CrashReports {
    private fun directory(ctx:Context)=File(ctx.noBackupFilesDir,"crashes")
    fun files(ctx:Context)=directory(ctx).listFiles()?.filter {it.name.matches(Regex("[0-9]+[.]txt"))}?.sortedByDescending {it.name}.orEmpty()
    // Only real compiled frame symbols are admitted. Messages, filenames, thread names and
    // exception text may hold remote input or secrets and are never serialized.
    fun text(problem:Throwable):String=buildString {
        appendLine("Nexus "+BuildConfig.VERSION_NAME+" · Android API "+Build.VERSION.SDK_INT)
        var cause:Throwable?=problem
        repeat(6){
            val seen=cause ?: return@buildString
            if(it>0)appendLine("Caused by")
            for(f in seen.stackTrace.take(128)){
                val name=f.className
                val method=f.methodName
                if(!name.matches(Regex("[A-Za-z_$][A-Za-z0-9_.$]{0,159}")) || !method.matches(Regex("[A-Za-z_$<>][A-Za-z0-9_$<>-]{0,99}")))continue
                val cls=runCatching {Class.forName(name,false,CrashReports::class.java.classLoader)}.getOrNull() ?: continue
                if(method!="<init>" && method!="<clinit>" && runCatching {cls.declaredMethods.none {it.name==method}}.getOrDefault(true))continue
                append("at ").append(name).append('.').append(method).append(':').appendLine(f.lineNumber)
                if(length>65000)return@buildString
            }
            cause=seen.cause
        }
    }.take(65536)
    @Synchronized fun write(ctx:Context,problem:Throwable){
        val dir=directory(ctx);dir.mkdirs()
        val file=File(dir,System.currentTimeMillis().toString()+".txt")
        val pending=File(dir,"pending")
        pending.writeBytes(text(problem).toByteArray().let {if(it.size<=65536)it else it.copyOf(65536)})
        if(pending.renameTo(file))files(ctx).drop(5).forEach {it.delete()}
    }
}
