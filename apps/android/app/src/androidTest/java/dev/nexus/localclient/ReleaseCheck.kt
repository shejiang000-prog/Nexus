package dev.nexus.localclient

import android.app.Instrumentation
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.util.Base64
import androidx.activity.compose.setContent
import kotlinx.coroutines.runBlocking
import org.json.JSONObject

internal fun Instrumentation.releaseCheck(args:Bundle,result:Bundle){
    val ctx=targetContext
    val p=ctx.getSharedPreferences("local",0)
    when(args.getString("mode")){
        "release-icons"->{
            check(android.os.Build.VERSION.SDK_INT>=33)
            val icon=ctx.packageManager.getApplicationIcon(ctx.applicationInfo) as android.graphics.drawable.AdaptiveIconDrawable
            val mono=requireNotNull(icon.monochrome).mutate()
            val dark=ctx.resources.configuration.uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK==android.content.res.Configuration.UI_MODE_NIGHT_YES
            mono.setTint(ctx.getColor(if(dark)android.R.color.system_accent1_100 else android.R.color.system_accent1_800))
            val bg=android.graphics.drawable.ColorDrawable(ctx.getColor(if(dark)android.R.color.system_neutral1_800 else android.R.color.system_accent1_100))
            val themed=android.graphics.drawable.AdaptiveIconDrawable(bg,mono)
            val bitmap=android.graphics.Bitmap.createBitmap(192,192,android.graphics.Bitmap.Config.ARGB_8888)
            try{
                themed.setBounds(0,0,192,192)
                themed.draw(android.graphics.Canvas(bitmap))
                val bytes=java.io.ByteArrayOutputStream()
                check(bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG,100,bytes))
                result.putString("icon",Base64.encodeToString(bytes.toByteArray(),Base64.NO_WRAP))
            }finally{bitmap.recycle()}
        }
        "release-import"->{
            check(ctx.packageName=="dev.nexus.localclient")
            val source=Subscriptions(ctx).all().singleOrNull {it.label=="Airport-Test" && it.url.startsWith("https://")}
                ?: error("source unavailable")
            // The operator opens the public build's manual import dialog first. Keep the secret
            // inside this device and only write it to the intended package's masked input.
            val root=requireNotNull(uiAutomation.rootInActiveWindow)
            check(root.packageName=="dev.nexus.android")
            fun fields(n:android.view.accessibility.AccessibilityNodeInfo):List<android.view.accessibility.AccessibilityNodeInfo> =
                (if(n.isEditable && n.packageName=="dev.nexus.android")listOf(n) else emptyList())+
                    (0 until n.childCount).flatMap {n.getChild(it)?.let(::fields).orEmpty()}
            val inputs=fields(root)
            check(inputs.size==2)
            fun fill(password:Boolean,value:String){
                val field=inputs.single {it.isPassword==password}
                check(field.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_SET_TEXT,Bundle().apply{
                    putCharSequence(android.view.accessibility.AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,value)
                }))
            }
            fill(false,source.label)
            fill(true,source.url)
            result.putString("input","filled local masked import form")
        }
        "release-key"->result.putString("public",Base64.encodeToString(Identity.publicKey(),Base64.NO_WRAP))
        "release-update"->{
            val prefs=ctx.getSharedPreferences("release-check",0)
            val original=prefs.all.toMap()
            try{
                fun checkResponse(raw:String?,ok:Boolean,new:Boolean){
                    prefs.edit().clear().commit()
                    val success=runBlocking {ReleaseUpdates.check(ctx,true){raw ?: throw java.io.IOException()}}
                    check(success==ok && (ReleaseUpdates.available.value!=null)==new)
                    var repeated=false
                    runBlocking {ReleaseUpdates.check(ctx,true){repeated=true;raw.orEmpty()}}
                    check(!repeated)
                }
                checkResponse("""{"tag_name":"v2.0.0","body":"更新测试"}""",true,true)
                prefs.edit().putBoolean("automatic",false).commit()
                val home=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
                try{splitLabel("有新版本 2.0.0",false);splitLabel("更新测试",false);splitLabel("去下载",false)}
                finally{runOnMainSync{home.finish()}}
                checkResponse("""{"tag_name":"v1.0.0"}""",true,false)
                checkResponse(null,false,false)
                checkResponse("""{"tag_name":"broken"}""",false,false)
                val secret="https://private.test/secret"
                val crash=IllegalStateException(secret).apply{stackTrace=arrayOf(StackTraceElement(secret,secret,secret,3))}
                check(!CrashReports.text(crash).contains("private.test"))
                result.putString("updates","new release banner + summary + browser action; same version, offline and malformed silent; daily limit; crash message excluded")
            }finally{
                val edit=prefs.edit().clear()
                original.forEach{(key,v)->when(v){is String->edit.putString(key,v);is Boolean->edit.putBoolean(key,v);is Long->edit.putLong(key,v)}}
                edit.commit();ReleaseUpdates.available.value=null
            }
        }
        "release-private"->{
            check(VpnService.prepare(ctx)==null)
            val json=JSONObject(String(Base64.decode(args.getString("profile64"),Base64.DEFAULT)))
            check(json.getString("tls_name")=="nexus.test" && json.getString("server").substringBefore(':')=="127.0.0.1")
            val saved=Profile.load(ctx)
            check(saved==null || saved.tlsName=="nexus.test")
            val original=listOf("servers","profile","simple-node").associateWith {p.getString(it,null)}
            Profile.parse(json.toString()).save(ctx)
            p.edit().putString("simple-node","private").commit()
            // Allow the activity foreground-start exemption to apply on retail devices.
            val home=startActivitySync(Intent(ctx,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            try{
                ctx.startForegroundService(Intent(ctx,TunnelService::class.java).setAction("vpn"))
                val end=android.os.SystemClock.elapsedRealtime()+45000
                while(TunnelService.state.value.connectedAt==0L && android.os.SystemClock.elapsedRealtime()<end)Thread.sleep(250)
                check(TunnelService.state.value.connectedAt>0 && p.contains(TunnelService.RESUME))
                val cm=ctx.getSystemService(android.net.ConnectivityManager::class.java)
                val vpn=cm.allNetworks.first {cm.getNetworkCapabilities(it)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN)==true}
                val profile=Profile.parse(json.toString())
                check(profile.dns==null)
                vpn.socketFactory.createSocket().use {socket->
                    socket.soTimeout=16000
                    socket.connect(java.net.InetSocketAddress(profile.vpnHost,json.getString("target").substringAfterLast(':').toInt()),16000)
                    socket.getOutputStream().write("GET /vpn/aced1234 HTTP/1.0\r\nHost: nexus.test\r\n\r\n".toByteArray())
                    val page=socket.getInputStream().bounded(8192).decodeToString()
                    check(page.contains(" 200 ") && page.contains("Debian receipt: "))
                }
                result.putString("private","private VPN authenticated; HTTP 200 + temporary Debian receipt; resume request saved")
            }finally{
                ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
                val end=android.os.SystemClock.elapsedRealtime()+6000
                while(TunnelService.state.value.active && android.os.SystemClock.elapsedRealtime()<end)Thread.sleep(100)
                val restore=p.edit()
                original.forEach { (k,v)->restore.putString(k,v) }
                restore.commit()
                runOnMainSync{home.finish()}
            }
        }
        "release-boot"->{check(p.contains(TunnelService.RESUME));p.edit().putBoolean("boot-connect",true).commit();result.putString("boot","armed")}
        "release-state"->{val s=TunnelService.state.value;result.putString("state","connected="+(s.connectedAt>0)+"; boot="+p.getBoolean("boot-connect",false)+"; saved="+p.contains(TunnelService.RESUME))}
        "release-stop"->{
            check(p.edit().putBoolean("boot-connect",false).commit())
            ctx.startService(Intent(ctx,TunnelService::class.java).setAction("stop"))
            val deadline=android.os.SystemClock.elapsedRealtime()+6000
            while((p.contains(TunnelService.RESUME) || TunnelService.state.value.active) && android.os.SystemClock.elapsedRealtime()<deadline)Thread.sleep(100)
            check(!p.contains(TunnelService.RESUME) && !TunnelService.state.value.active)
            result.putString("boot","disabled; saved request cleared")
        }
    }
}
