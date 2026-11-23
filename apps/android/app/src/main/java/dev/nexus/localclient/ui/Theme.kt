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
    // White and blue in every system theme (see LightPalette); there is no dark variant.
    val dark=false
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
    val tokens=LightPalette
    val colors=remember(dark) {
        val p=tokens
        val base=if(dark) darkColorScheme() else lightColorScheme()
        base.copy(background=p.canvas,onBackground=p.ink,surface=p.surface,onSurface=p.ink,primary=p.signalText,onPrimary=if(dark) p.onSignal else Color.White,
            secondary=p.inkSoft,onSecondary=p.canvas,primaryContainer=p.signalSoft,onPrimaryContainer=p.ink,secondaryContainer=p.well,onSecondaryContainer=p.ink,
            tertiary=p.inkSoft,onTertiary=p.canvas,tertiaryContainer=p.well,onTertiaryContainer=p.ink,
            surfaceVariant=p.well,onSurfaceVariant=p.inkSoft,outline=p.inkMuted,outlineVariant=p.line,surfaceTint=p.surface,
            surfaceContainerLowest=p.canvas,surfaceContainerLow=p.surface,surfaceContainer=p.surface,surfaceContainerHigh=p.raised,surfaceContainerHighest=p.well,
            inverseSurface=p.ink,inverseOnSurface=p.canvas,error=p.danger,errorContainer=p.dangerSoft,onErrorContainer=p.danger)
    }
    val shapes=remember { androidx.compose.material3.Shapes(extraSmall=androidx.compose.foundation.shape.RoundedCornerShape(Radius.xs),
        small=androidx.compose.foundation.shape.RoundedCornerShape(Radius.sm),medium=androidx.compose.foundation.shape.RoundedCornerShape(Radius.md),
        large=androidx.compose.foundation.shape.RoundedCornerShape(Radius.lg),extraLarge=androidx.compose.foundation.shape.RoundedCornerShape(Radius.xl)) }
    val type=remember { nexusTypography() }
    CompositionLocalProvider(LocalPalette provides tokens,LocalMotion provides animate,LocalForeground provides active) { MaterialTheme(colors,shapes=shapes,typography=type,content=content) }
}

/**
 * Type scale. Body text stays at 16 sp for reading; secondary lines step down to 15 and 13 sp,
 * titles step up, and the connection timer uses the bundled display face.
 */
internal fun nexusTypography():Typography {
    val t=Typography()
    fun style(size:Int,line:Int,weight:FontWeight=FontWeight.Normal)=t.bodyLarge.copy(fontSize=size.sp,lineHeight=line.sp,fontWeight=weight,letterSpacing=0.sp)
    return t.copy(
        displaySmall=style(34,40,FontWeight.Bold).copy(fontFamily=numberFont,fontFeatureSettings="tnum"),
        headlineLarge=style(28,36,FontWeight.Bold),
        headlineMedium=style(24,32,FontWeight.Bold),
        headlineSmall=style(22,28,FontWeight.Bold),
        titleLarge=style(20,28,FontWeight.Bold),
        titleMedium=style(17,24,FontWeight.SemiBold),
        titleSmall=style(15,22,FontWeight.SemiBold),
        bodyLarge=style(16,22),
        bodyMedium=style(15,21),
        bodySmall=style(13,18),
        labelLarge=style(16,22,FontWeight.SemiBold),
        labelMedium=style(14,20,FontWeight.Medium),
        labelSmall=style(12,16,FontWeight.Medium))
}

/** Secondary line under a row title (node type, card detail): between bodyMedium and bodySmall. */
internal val Typography.detail get() = bodySmall.copy(fontSize=14.sp,lineHeight=20.sp)
