package dev.nexus.localclient

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private fun browse(ctx:Context,url:String) {
    try { ctx.startActivity(Intent(Intent.ACTION_VIEW,Uri.parse(url))) }
    catch(_:android.content.ActivityNotFoundException){android.widget.Toast.makeText(ctx,"请先安装浏览器",android.widget.Toast.LENGTH_SHORT).show()}
}

@Composable
internal fun BootPreference(){
    val p=LocalContext.current.getSharedPreferences("local",0)
    var enabled by remember {mutableStateOf(p.getBoolean("boot-connect",false))}
    Row(verticalAlignment=Alignment.CenterVertically){Text("开机后自动连接",Modifier.weight(1f))
        Switch(enabled,{enabled=it;p.edit().putBoolean("boot-connect",it).apply()})}
    Text("手机重启并首次解锁后，恢复关机前仍在使用的连接。手动断开后不会自动连接。三星等部分手机会拦截开机启动，更可靠的做法是开启下面的「始终开启的 VPN」，它在重启后同样会自动连接。",color=MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
internal fun ReleaseStartup(){
    val ctx=LocalContext.current
    val prefs=remember {ctx.getSharedPreferences("local",0)}
    var restricted by remember {mutableStateOf(prefs.getBoolean("boot-restricted",false))}
    var consent by remember {mutableStateOf(prefs.getBoolean("boot-needs-consent",false))}
    var dismissed by androidx.compose.runtime.saveable.rememberSaveable {mutableStateOf("")}
    val latest by ReleaseUpdates.available.collectAsState()
    LaunchedEffect(Unit){ReleaseUpdates.check(ctx)}
    if(consent){
        val close={consent=false;prefs.edit().remove("boot-needs-consent").apply();Unit}
        AlertDialog(onDismissRequest=close,title={Text("开机连接需要授权")},text={Text("请在连接页点按圆环，重新允许 Nexus 使用 VPN。")},
            confirmButton={PrimaryAction(close){Text("知道了")}})
    }else if(restricted){
        val close={restricted=false;prefs.edit().remove("boot-restricted").apply();Unit}
        AlertDialog(onDismissRequest=close,title={Text("系统限制了开机连接")},text={Text("请在连接页手动连接，或在系统 VPN 设置中开启始终连接。")},confirmButton={PrimaryAction(close){Text("知道了")}})
    }else latest?.takeIf {it.tag!=dismissed}?.let {release->
        AlertDialog(onDismissRequest={dismissed=release.tag},title={Text("有新版本 "+release.version)},
            text={Text(release.summary.ifEmpty {"可在 GitHub 查看此版本。"})},
            confirmButton={PrimaryAction({dismissed=release.tag;browse(ctx,release.page)}){Text("去下载")}},
            dismissButton={SecondaryAction({dismissed=release.tag}){Text("稍后")}})
    }
}

@Composable
internal fun UpdatePreference(){
    val ctx=LocalContext.current
    val prefs=remember {ctx.getSharedPreferences("release-check",0)}
    var auto by remember {mutableStateOf(prefs.getBoolean("automatic",true))}
    var checked by remember {mutableStateOf(false)}
    var busy by remember {mutableStateOf(false)}
    val scope=rememberCoroutineScope()
    val latest by ReleaseUpdates.available.collectAsState()
    Column(verticalArrangement=Arrangement.spacedBy(6.dp)){
        Row(verticalAlignment=Alignment.CenterVertically){Text("自动检查更新",Modifier.weight(1f))
            Switch(auto,{auto=it;prefs.edit().putBoolean("automatic",it).apply()})}
        Text("自动检查每天最多联系 GitHub 一次。",color=MaterialTheme.colorScheme.onSurfaceVariant)
        SecondaryAction({busy=true;scope.launch {try{checked=ReleaseUpdates.check(ctx,true)}finally{busy=false}}},enabled=!busy){Text(if(busy)"正在检查…" else "检查更新")}
        val release=latest
        if(release!=null){Text("有新版本 "+release.version);if(release.summary.isNotBlank())Text(release.summary)
            PrimaryAction({browse(ctx,release.page)}){Text("去下载")}}
        else if(checked)Text("当前已是最新版本")
    }
}

@Composable
internal fun AboutScreen(){
    val ctx=LocalContext.current
    var document by androidx.compose.runtime.saveable.rememberSaveable {mutableStateOf("")}
    var feedback by remember {mutableStateOf(false)}
    var logs by remember {mutableStateOf(false)}
    var report by remember {mutableStateOf("")}
    val assets=ctx.assets
    val licenses by produceState(emptyList<String>()){
        value=withContext(Dispatchers.IO){
            fun files(path:String):List<String> {
                val children=assets.list(path).orEmpty()
                return if(children.isEmpty())listOf(path) else children.flatMap{files("$path/$it")}
            }
            files("licenses").sorted()
        }
    }
    androidx.activity.compose.BackHandler(document.isNotEmpty()||logs){document="";logs=false;report=""}
    val lines by produceState(emptyList<String>(),document){
        value=if(document.isEmpty()||document=="licenses") emptyList() else withContext(Dispatchers.IO){
            assets.open(document).bufferedReader().useLines {it.chunked(16).map {part->part.joinToString("\n")}.toList()}
        }
    }
    LazyColumn(Modifier.fillMaxSize().padding(horizontal=20.dp),contentPadding=PaddingValues(vertical=16.dp),verticalArrangement=Arrangement.spacedBy(12.dp)){
        if(logs){
            item{Text("本机崩溃记录",style=MaterialTheme.typography.headlineMedium)}
            val files=CrashReports.files(ctx)
            if(files.isEmpty())item{Text("尚无崩溃记录")}
            items(files){file->SecondaryAction({report=file.readText()}){Text(java.text.DateFormat.getDateTimeInstance().format(java.util.Date(file.lastModified())))}}
            if(report.isNotBlank()){item{Text(report)};item{SecondaryAction({
                ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Nexus 崩溃堆栈",report))
            }){Text("复制这份堆栈")}}}
        }else if(document=="licenses"){
            item{Text("开源许可",style=MaterialTheme.typography.headlineMedium)}
            items(licenses){path->SecondaryAction({document=path}){Text(path.removePrefix("licenses/"))}}
        }else if(document.isNotEmpty()){
            item{Text(if(document=="PRIVACY.md")"隐私说明" else document.substringAfterLast('/'),style=MaterialTheme.typography.headlineMedium)}
            items(lines){Text(it)}
        }else{
            item{Text("关于 Nexus",style=MaterialTheme.typography.headlineMedium)}
            item{Text("版本 "+BuildConfig.VERSION_NAME+" · "+BuildConfig.VERSION_CODE);Text("构建时间 "+BuildConfig.BUILT_AT)}
            item{Text("Nexus 以 GPL-3.0 发布")}
            item{SecondaryAction({browse(ctx,REPOSITORY)}){Text("查看 GitHub 源码")}}
            item{SecondaryAction({document="PRIVACY.md"}){Text("隐私说明")}}
            item{SecondaryAction({document="licenses"}){Text("开源许可")}}
            item{SecondaryAction({logs=true}){Text("查看本机崩溃记录")}}
            item{SecondaryAction({feedback=true}){Text("反馈问题")}}
        }
    }
    if(feedback){
        val details="App "+BuildConfig.VERSION_NAME+"\nAndroid "+Build.VERSION.RELEASE+"\n机型 "+Build.MODEL
        AlertDialog(onDismissRequest={feedback=false},title={Text("在 GitHub 反馈问题？")},
            text={Text("浏览器会预填以下信息，由你检查并提交。崩溃记录可在上一页查看和复制。\n\n"+details)},
            confirmButton={PrimaryAction({feedback=false;browse(ctx,Uri.parse("$REPOSITORY/issues/new").buildUpon()
                .appendQueryParameter("title","Android 问题反馈").appendQueryParameter("body",details+"\n\n遇到的问题：").build().toString())}){Text("打开 GitHub")}},
            dismissButton={SecondaryAction({feedback=false}){Text("取消")}})
    }
}
