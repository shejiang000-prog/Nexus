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
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private fun browse(ctx:Context,url:String) {
    try { ctx.startActivity(Intent(Intent.ACTION_VIEW,Uri.parse(url))) }
    catch(_:android.content.ActivityNotFoundException){android.widget.Toast.makeText(ctx,"请先安装浏览器",android.widget.Toast.LENGTH_SHORT).show()}
}

/** The two ways Nexus comes back on its own: always-on VPN (reliable) and the boot receiver. */
@Composable
internal fun RecoverySettings(alwaysOn:Boolean,openVpnSettings:()->Unit){
    val c=MaterialTheme.colorScheme
    val p=LocalContext.current.getSharedPreferences("local",0)
    var enabled by remember {mutableStateOf(p.getBoolean("boot-connect",false))}
    Column {
        ListRow("断线自动恢复",if(alwaysOn) "已设为始终开启的 VPN，被系统关闭或重启后自动重连"
            else "系统清理后台时可能关闭 Nexus。设为「始终开启的 VPN」后会自动重连",GroupPlace.First,
            trailing={ if(alwaysOn) Text("已开启",color=c.primary,style=MaterialTheme.typography.labelLarge) else LinkLabel("去开启") },
            onClick=if(alwaysOn) null else openVpnSettings)
        ListRow("开机后自动连接","重启解锁后恢复上次的连接。三星等手机可能拦截，以上一项为准",GroupPlace.Last,
            trailing={ Switch(enabled,{enabled=it;p.edit().putBoolean("boot-connect",it).apply()},colors=switchColors()) })
    }
}

/** A trailing call to action on a list row: accent text with a chevron. */
@Composable
internal fun LinkLabel(text:String) {
    val c=MaterialTheme.colorScheme
    Row(verticalAlignment=Alignment.CenterVertically) {
        Text(text,color=c.primary,style=MaterialTheme.typography.labelLarge)
        GlyphIcon(Glyph.Chevron,Modifier.padding(start=Space.xxs),size=14.dp,tint=c.primary)
    }
}

@Composable
internal fun switchColors()=SwitchDefaults.colors(checkedTrackColor=signal,checkedThumbColor=palette.onSignal,
    uncheckedTrackColor=MaterialTheme.colorScheme.surfaceVariant,uncheckedThumbColor=MaterialTheme.colorScheme.onSurfaceVariant)

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
internal fun UpdateSettings(){
    val ctx=LocalContext.current
    val c=MaterialTheme.colorScheme
    val prefs=remember {ctx.getSharedPreferences("release-check",0)}
    var auto by remember {mutableStateOf(prefs.getBoolean("automatic",true))}
    var checked by remember {mutableStateOf(false)}
    var busy by remember {mutableStateOf(false)}
    val scope=rememberCoroutineScope()
    val latest by ReleaseUpdates.available.collectAsState()
    Column {
        ListRow("自动检查更新","每天最多联系 GitHub 一次，不发送设备信息",GroupPlace.First,
            trailing={ Switch(auto,{auto=it;prefs.edit().putBoolean("automatic",it).apply()},colors=switchColors()) })
        val release=latest
        if(release!=null) ListRow("有新版本 "+release.version,release.summary.lines().firstOrNull { it.isNotBlank() }.orEmpty().take(80),GroupPlace.Last,
            trailing={ LinkLabel("去下载") }) { browse(ctx,release.page) }
        else ListRow("检查更新",when { busy->"正在检查…"; checked->"当前 "+BuildConfig.VERSION_NAME+" 已是最新"; else->"当前版本 "+BuildConfig.VERSION_NAME },
            GroupPlace.Last,enabled=!busy) { busy=true;scope.launch {try{checked=ReleaseUpdates.check(ctx,true)}finally{busy=false}} }
    }
}

private fun licenseTitle(path:String):Pair<String,String> {
    val parts=path.removePrefix("licenses/").split('/')
    val file=parts.last().substringBeforeLast('.')
    val owner=parts.first()
    val generic=file.equals("LICENSE",true) || file.equals("SOURCES",true)
    val title=if(parts.size==1 || !generic) file else owner
    return title to (if(parts.size==1 || title==owner) "许可证全文" else owner)
}

@Composable
internal fun AboutScreen(){
    val ctx=LocalContext.current
    val c=MaterialTheme.colorScheme
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
    androidx.activity.compose.BackHandler(document.isNotEmpty()||logs){ if(report.isNotEmpty()) report="" else {document="";logs=false} }
    val lines by produceState(emptyList<String>(),document){
        value=if(document.isEmpty()||document=="licenses") emptyList() else withContext(Dispatchers.IO){
            assets.open(document).bufferedReader().readLines()
        }
    }
    val built=remember { runCatching { java.text.SimpleDateFormat("yyyy-MM-dd HH:mm",java.util.Locale.ROOT)
        .format(java.util.Date(java.time.Instant.parse(BuildConfig.BUILT_AT).toEpochMilli())) }.getOrDefault(BuildConfig.BUILT_AT) }
    val crashes=remember(logs) { CrashReports.files(ctx) }
    LazyColumn(Modifier.fillMaxSize().padding(horizontal=20.dp),contentPadding=PaddingValues(vertical=16.dp)){
        if(logs){
            item{Text("本机崩溃记录",style=MaterialTheme.typography.headlineMedium)}
            item{Text("只含 App 内部的类名和方法名，不含链接、地址或密钥。",Modifier.padding(top=6.dp,bottom=10.dp),color=c.onSurfaceVariant,fontSize=14.sp)}
            if(crashes.isEmpty())item{Text("尚无崩溃记录",color=c.onSurfaceVariant)}
            if(report.isBlank()) items(crashes.size){i->
                val file=crashes[i]
                ListRow(java.text.DateFormat.getDateTimeInstance().format(java.util.Date(file.lastModified())),"",groupPlace(i,crashes.lastIndex,false,false)){report=file.readText()}
            } else {
                item{Surface(shape=signalCard,color=c.surface){Text(report,Modifier.padding(14.dp),fontSize=12.sp,lineHeight=16.sp,fontFamily=FontFamily.Monospace)}}
                item{Spacer(Modifier.height(12.dp));PrimaryAction({
                    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Nexus 崩溃堆栈",report))
                },Modifier.fillMaxWidth()){Text("复制这份堆栈")}}
            }
        }else if(document=="licenses"){
            item{Text("开源许可",style=MaterialTheme.typography.headlineMedium)}
            item{Text("Nexus 使用的第三方组件及其许可证全文。",Modifier.padding(top=6.dp,bottom=10.dp),color=c.onSurfaceVariant,fontSize=14.sp)}
            items(licenses.size){i->
                val (title,owner)=licenseTitle(licenses[i])
                ListRow(title,owner,groupPlace(i,licenses.lastIndex,false,false)){document=licenses[i]}
            }
        }else if(document.isNotEmpty()){
            if(document!="PRIVACY.md") item{Text(licenseTitle(document).first,Modifier.padding(bottom=12.dp),style=MaterialTheme.typography.headlineMedium)}
            item{
                if(document.endsWith(".md")) SimpleDocument(lines)
                else Text(lines.joinToString("\n"),fontSize=13.sp,lineHeight=19.sp,fontFamily=FontFamily.Monospace)
            }
        }else{
            item{
                Column(Modifier.fillMaxWidth().padding(top=12.dp,bottom=4.dp),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(6.dp)){
                    Box(Modifier.size(72.dp).background(signal,RoundedCornerShape(22.dp)),contentAlignment=Alignment.Center){ Mark(Modifier.size(34.dp),palette.onSignal) }
                    Text("Nexus",style=MaterialTheme.typography.headlineMedium.copy(fontFamily=numberFont))
                    Text("版本 "+BuildConfig.VERSION_NAME+" · 构建于 "+built,color=c.onSurfaceVariant,fontSize=14.sp)
                    Text("以 GPL-3.0 开源发布",color=c.onSurfaceVariant,fontSize=14.sp)
                }
            }
            item{SectionLabel("说明")}
            item{ListRow("GitHub 源码","github.com/shejiang000-prog/Nexus",GroupPlace.First){browse(ctx,REPOSITORY)}}
            item{ListRow("隐私说明","App 不收集、不上传任何数据",GroupPlace.Middle){document="PRIVACY.md"}}
            item{ListRow("开源许可","${licenses.size} 份第三方许可证",GroupPlace.Last){document="licenses"}}
            item{SectionLabel("遇到问题")}
            item{ListRow("本机崩溃记录",if(crashes.isEmpty()) "暂无" else "最近 ${crashes.size} 次",GroupPlace.First){logs=true}}
            item{ListRow("反馈问题","在 GitHub 提交，由你确认后发送",GroupPlace.Last){feedback=true}}
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
