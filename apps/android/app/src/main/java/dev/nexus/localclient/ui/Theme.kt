package dev.nexus.localclient

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.database.ContentObserver
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.*
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver

internal val signalDark @Composable get() = isSystemInDarkTheme()
internal val signal @Composable get() = if(signalDark) Color(0xFF3EE6A3) else Color(0xFF2BD394)
internal val signalSoft @Composable get() = signal.copy(alpha=if(signalDark) .14f else .18f)
internal val signalAmber @Composable get() = if(signalDark) Color(0xFFFFB547) else Color(0xFF9A5B00)
// The bundled file is the variable font (default instance Light). Register each weight we use so
// Android selects a real axis value instead of synthesizing bold from Light.
@OptIn(androidx.compose.ui.text.ExperimentalTextApi::class)
internal val numberFont = FontFamily(listOf(FontWeight.Normal, FontWeight.Medium, FontWeight.SemiBold, FontWeight.Bold).map {
    Font(R.font.space_grotesk, it, variationSettings = FontVariation.Settings(FontVariation.weight(it.weight)))
})
internal val numberStyle @Composable get() = MaterialTheme.typography.bodyLarge.copy(fontFamily=numberFont,fontFeatureSettings="tnum")
internal val LocalMotion = compositionLocalOf { true }
internal val LocalForeground = compositionLocalOf { true }

@Composable
internal fun NexusTheme(content:@Composable ()->Unit) {
    val dark=signalDark
    val ctx=LocalContext.current
    val lifecycle=(ctx as? ComponentActivity)?.lifecycle
    val power=remember(ctx){ctx.getSystemService(PowerManager::class.java)}
    fun foreground() = power.isInteractive && lifecycle?.currentState?.isAtLeast(Lifecycle.State.RESUMED)!=false
    fun motion() = Settings.Global.getFloat(ctx.contentResolver,Settings.Global.ANIMATOR_DURATION_SCALE,1f)>0f
    var active by remember { mutableStateOf(foreground()) }
    var animate by remember { mutableStateOf(motion()) }
    DisposableEffect(ctx,lifecycle) {
        val observer=LifecycleEventObserver { _,_ -> active=foreground() }
        lifecycle?.addObserver(observer)
        val screen=object:BroadcastReceiver(){override fun onReceive(c:Context,i:Intent){active=foreground()}}
        ctx.registerReceiver(screen,IntentFilter(Intent.ACTION_SCREEN_OFF).apply { addAction(Intent.ACTION_SCREEN_ON) })
        val scale=object:ContentObserver(Handler(Looper.getMainLooper())){override fun onChange(selfChange:Boolean){animate=motion()}}
        ctx.contentResolver.registerContentObserver(Settings.Global.getUriFor(Settings.Global.ANIMATOR_DURATION_SCALE),false,scale)
        onDispose { lifecycle?.removeObserver(observer);ctx.unregisterReceiver(screen);ctx.contentResolver.unregisterContentObserver(scale) }
    }
    val colors=remember(dark) {
        fun tone(light:Long,night:Long)=Color(if(dark) night else light)
        val bg=tone(0xFFF3F5F8,0xFF0A0D11)
        val face=tone(0xFFFFFFFF,0xFF12171D)
        val well=tone(0xFFE9EDF1,0xFF1A2129)
        val line=tone(0xFFD8DFE6,0xFF263039)
        val ink=tone(0xFF0A0D11,0xFFEEF2F6)
        val sub=tone(0xFF47525E,0xFFA9B4BF)
        val muted=tone(0xFF5F6B78,0xFF8592A0)
        val green=tone(0xFF0A7F55,0xFF3EE6A3)
        val base=if(dark) darkColorScheme() else lightColorScheme()
        base.copy(background=bg,onBackground=ink,surface=face,onSurface=ink,primary=green,onPrimary=tone(0xFFFFFFFF,0xFF03140C),
            secondary=sub,onSecondary=bg,primaryContainer=well,onPrimaryContainer=ink,secondaryContainer=well,onSecondaryContainer=ink,
            tertiary=sub,onTertiary=bg,tertiaryContainer=well,onTertiaryContainer=ink,
            surfaceVariant=well,onSurfaceVariant=sub,outline=muted,outlineVariant=line,surfaceTint=face,
            surfaceContainerLowest=bg,surfaceContainerLow=face,surfaceContainer=face,surfaceContainerHigh=well,surfaceContainerHighest=well,
            inverseSurface=ink,inverseOnSurface=bg,error=tone(0xFFC42B2B,0xFFFF7A7A))
    }
    val type=remember {
        val t=Typography()
        val title=t.headlineMedium.copy(fontSize=30.sp,lineHeight=38.sp,fontWeight=FontWeight.Bold)
        t.copy(headlineMedium=title,headlineSmall=title,bodySmall=t.bodyLarge,bodyMedium=t.bodyLarge,
            labelLarge=t.bodyLarge.copy(fontWeight=FontWeight.SemiBold),labelMedium=t.bodyLarge,labelSmall=t.bodyLarge)
    }
    CompositionLocalProvider(LocalMotion provides animate,LocalForeground provides active) { MaterialTheme(colors,typography=type,content=content) }
}
