package dev.nexus.localclient

import androidx.compose.animation.*
import androidx.compose.animation.core.*
import androidx.compose.foundation.*
import androidx.compose.foundation.interaction.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.*
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.*
import kotlinx.coroutines.delay

internal val signalCard get() = Shapes.card

// Wall time also preserves the short accessibility fade when Android's duration scale is zero.
internal suspend fun frameProgress(ms:Int, update:(Float)->Unit) {
    val start=withFrameNanos { it }
    do {
        val f=withFrameNanos { ((it-start)/(ms*1_000_000f)).coerceIn(0f,1f) }
        update(f)
    } while(f<1f)
}

/** Shrinks slightly while pressed so a tap answers before the action finishes. */
@Composable
internal fun Modifier.pressScale(interaction:InteractionSource,pressed:Float=.97f):Modifier {
    val down by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(if(down && LocalMotion.current) pressed else 1f,motionSpring(Spring.StiffnessMedium),label="按压缩放")
    return graphicsLayer { scaleX=scale;scaleY=scale }
}

/** A card face with a hairline border; clickable cards also get press scale and ripple. */
@Composable
internal fun NexusCard(modifier:Modifier=Modifier,onClick:(()->Unit)?=null,onClickLabel:String?=null,
                       color:Color=MaterialTheme.colorScheme.surface,content:@Composable ColumnScope.()->Unit) {
    val interaction=remember { MutableInteractionSource() }
    Surface(modifier.then(if(onClick!=null) Modifier.pressScale(interaction,.985f) else Modifier),shape=Shapes.card,color=color,
        border=BorderStroke(Sizes.hairline,palette.line)) {
        Column(if(onClick==null) Modifier else Modifier.clickable(interaction,LocalIndication.current,role=Role.Button,
            onClickLabel=onClickLabel,onClick=onClick),content=content)
    }
}

@Composable
internal fun PrimaryAction(onClick:()->Unit,modifier:Modifier=Modifier,enabled:Boolean=true,content:@Composable RowScope.()->Unit) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val interaction=remember { MutableInteractionSource() }
    val glow=p.signal.copy(alpha=if(p.dark) .22f else .26f)
    val fill=Brush.horizontalGradient(listOf(p.signal,lerp(p.signal,p.signalDeep,.55f)))
    Button(onClick,modifier.heightIn(min=Sizes.buttonHeight).pressScale(interaction)
        .then(if(enabled) Modifier.shadow(8.dp,Shapes.control,ambientColor=glow,spotColor=glow).background(fill,Shapes.control) else Modifier),
        enabled=enabled,shape=Shapes.control,interactionSource=interaction,
        colors=ButtonDefaults.buttonColors(containerColor=Color.Transparent,contentColor=p.onSignal,
            disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant),content=content)
}

@Composable
internal fun SecondaryAction(onClick:()->Unit,modifier:Modifier=Modifier,enabled:Boolean=true,content:@Composable RowScope.()->Unit) {
    val c=MaterialTheme.colorScheme
    val interaction=remember { MutableInteractionSource() }
    OutlinedButton(onClick,modifier.heightIn(min=Sizes.touch).pressScale(interaction),enabled=enabled,shape=Shapes.control,
        border=BorderStroke(1.5.dp,palette.line),interactionSource=interaction,
        colors=ButtonDefaults.outlinedButtonColors(containerColor=c.surface,contentColor=c.onSurface,
            disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant),content=content)
}

/** A small pill action for page headers, where a full-width primary button would dominate. */
@Composable
internal fun CompactAction(onClick:()->Unit,modifier:Modifier=Modifier,enabled:Boolean=true,filled:Boolean=true,content:@Composable RowScope.()->Unit) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val interaction=remember { MutableInteractionSource() }
    val colors=if(filled) ButtonDefaults.buttonColors(containerColor=p.signal,contentColor=p.onSignal,
        disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant)
        else ButtonDefaults.buttonColors(containerColor=c.surfaceVariant,contentColor=c.onSurface,
        disabledContainerColor=c.surfaceVariant,disabledContentColor=c.onSurfaceVariant)
    Button(onClick,modifier.heightIn(min=Sizes.compactHeight).pressScale(interaction,.95f),enabled=enabled,shape=Shapes.pill,colors=colors,
        interactionSource=interaction,contentPadding=PaddingValues(start=14.dp,end=Space.lg)) {
        GlyphIcon(Glyph.Plus,size=14.dp)
        Spacer(Modifier.width(6.dp))
        ProvideTextStyle(MaterialTheme.typography.labelLarge.copy(fontSize=15.sp)) { content() }
    }
}

@Composable
internal fun Mark(modifier:Modifier,ink:Color=LocalContentColor.current) {
    Canvas(modifier) {
        val w=size.minDimension
        drawPath(Path().apply { moveTo(0f,w*.85f);lineTo(w*.30f,w*.13f);lineTo(w*.53f,w*.13f)
            lineTo(w*.71f,w*.51f);lineTo(w*.87f,w*.13f);lineTo(w,w*.13f);lineTo(w*.70f,w*.87f)
            lineTo(w*.51f,w*.87f);lineTo(w*.32f,w*.47f);lineTo(w*.17f,w*.85f);close() },ink)
    }
}

@Composable
internal fun Segment(labels:List<String>,selected:Int,select:(Int)->Unit) {
    val c=MaterialTheme.colorScheme
    val p=palette
    BoxWithConstraints(Modifier.fillMaxWidth().background(c.surfaceVariant,Shapes.control).padding(3.dp)) {
        val width=maxWidth/labels.size
        val position by animateDpAsState(width*selected,motionSpring(Spring.StiffnessMediumLow,.82f),label="选中位置")
        val shade=Color.Black.copy(alpha=.12f)
        Box(Modifier.offset(x=position).width(width).height(Sizes.segment)
            .shadow(if(p.dark) 0.dp else 3.dp,Shapes.chip,ambientColor=shade,spotColor=shade)
            .background(if(p.dark) lerp(p.well,p.ink,.14f) else p.surface,Shapes.chip))
        Row { labels.forEachIndexed { i,s ->
            val ink by animateColorAsState(if(i==selected) c.onSurface else c.onSurfaceVariant,motionTween(Motion.standard),label="分段文字")
            Box(Modifier.weight(1f).height(Sizes.segment).selectable(i==selected,role=Role.Tab){select(i)},
                contentAlignment=Alignment.Center){Text(s,color=ink,style=MaterialTheme.typography.labelLarge.copy(fontSize=15.sp))} } }
    }
}

@Composable
internal fun Notice(message:String,error:Boolean=false) {
    val c=MaterialTheme.colorScheme
    Surface(shape=Shapes.control,color=if(error) palette.dangerSoft else c.surfaceVariant) {
        Text(message,Modifier.fillMaxWidth().padding(horizontal=Space.md,vertical=10.dp),color=if(error)c.error else c.onSurfaceVariant,
            style=MaterialTheme.typography.bodyMedium)
    }
}

@Composable
internal fun ActionRow(title:String,detail:String,index:Int,click:(()->Unit)?) {
    val c=MaterialTheme.colorScheme
    NexusCard(onClick=click) {
        Row(Modifier.fillMaxWidth().padding(Space.md),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.md)) {
            Box(Modifier.size(40.dp).background(palette.signalSoft,Shapes.chip),contentAlignment=Alignment.Center){
                CompositionLocalProvider(LocalContentColor provides c.primary){SectionIcon(index)} }
            Column(Modifier.weight(1f)) { Text(title,style=MaterialTheme.typography.titleSmall.copy(fontSize=16.sp))
                if(detail.isNotEmpty())Text(detail,color=c.onSurfaceVariant,style=MaterialTheme.typography.bodyMedium) }
            if(click!=null) GlyphIcon(Glyph.Chevron,size=18.dp,tint=c.outline)
        }
    }
}

private fun regionBadge(region:String):String {
    val codes=region.codePoints().toArray()
    if(codes.size==2 && codes.all {it in 0x1F1E6..0x1F1FF})return codes.map { ('A'.code+it-0x1F1E6).toChar() }.joinToString("")
    return if(region.isBlank() || region=="未提供") "·" else String(codes,0,minOf(2,codes.size)).uppercase(java.util.Locale.ROOT)
}

/** Where a row sits in a grouped list, so a run of rows reads as one card. */
internal enum class GroupPlace { Single, First, Middle, Last }
internal fun groupPlace(index:Int,last:Int,startsGroup:Boolean,endsGroup:Boolean)=when {
    (index==0 || startsGroup) && (index==last || endsGroup) -> GroupPlace.Single
    index==0 || startsGroup -> GroupPlace.First
    index==last || endsGroup -> GroupPlace.Last
    else -> GroupPlace.Middle
}
internal fun GroupPlace.shape():RoundedCornerShape { val r=Radius.lg; return when(this) {
    GroupPlace.Single->RoundedCornerShape(r); GroupPlace.First->RoundedCornerShape(topStart=r,topEnd=r)
    GroupPlace.Last->RoundedCornerShape(bottomStart=r,bottomEnd=r); GroupPlace.Middle->RoundedCornerShape(0.dp) } }

@Composable
internal fun RegionBadge(region:String,private:Boolean=false) {
    val c=MaterialTheme.colorScheme
    val text=regionBadge(region)
    Box(Modifier.size(Sizes.badge).background(if(private)signalSoft else c.surfaceVariant,Shapes.chip),contentAlignment=Alignment.Center){
        // A node without a known region shows the brand mark rather than a lone dot.
        if(text=="·") Mark(Modifier.size(18.dp),c.outline)
        else Text(text,style=numberStyle.copy(fontSize=14.sp),fontWeight=FontWeight.Bold,color=if(private)c.primary else c.onSurface) }
}

/** Latency colour bands shared by the meter and the node details. */
@Composable
internal fun latencyTone(latency:Long?):Color { val c=MaterialTheme.colorScheme; return when {
    latency==null->c.onSurfaceVariant; latency<350->c.primary; latency<800->signalAmber; else->c.onSurfaceVariant } }

@Composable
internal fun LatencyMeter(latency:Long?,testing:Boolean=false) {
    val c=MaterialTheme.colorScheme
    val good=signal
    val fair=signalAmber
    // -1 is "testing", -2 is "untested"; each state keeps its own value while it fades out.
    val motion=LocalMotion.current
    AnimatedContent(if(testing) -1L else latency ?: -2L,transitionSpec={ fadeThrough(motion) },contentAlignment=Alignment.CenterEnd,label="延迟") { shown ->
        val latency=shown.takeIf { it>=0 }
        val bars=when { latency==null->0;latency<150->4;latency<350->3;latency<800->2;else->1 }
        val tint=when { latency==null->c.outlineVariant;latency<350->good;latency<800->fair;else->c.onSurfaceVariant }
        if(shown==-1L) Text("测试中",Modifier.widthIn(min=56.dp).shimmer(Shapes.pill).padding(horizontal=Space.sm,vertical=Space.xxs),
            style=MaterialTheme.typography.labelMedium,color=c.onSurfaceVariant,textAlign=TextAlign.Center)
        else Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
            // Untested nodes show only the label, leaving the name more room.
            if(latency!=null) Canvas(Modifier.size(16.dp,14.dp)){ repeat(4){ i ->
                val w=size.width/4;val h=size.height*(i+1)/4
                drawRoundRect(if(i<bars)tint else c.outlineVariant,Offset(i*w+w*.15f,size.height-h),androidx.compose.ui.geometry.Size(w*.7f,h),
                    androidx.compose.ui.geometry.CornerRadius(1.dp.toPx()))
            } }
            Text(if(latency==null)"未测" else "$latency ms",Modifier.widthIn(min=if(latency==null) 0.dp else 52.dp),style=numberStyle.copy(fontSize=14.sp),
                fontWeight=FontWeight.SemiBold,color=latencyTone(latency),textAlign=TextAlign.End)
        }
    }
}

@Composable
internal fun SignalNode(region:String,name:String,detail:String,latency:Long?,selected:Boolean,info:String,
                        onDetails:()->Unit,onSelect:(()->Unit)?,place:GroupPlace=GroupPlace.Single,testing:Boolean=false) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val tint by animateColorAsState(if(selected) p.signalSoft.copy(alpha=p.signalSoft.alpha*.55f) else Color.Transparent,motionTween(Motion.standard),label="选中底色")
    Column(Modifier.fillMaxWidth().clip(place.shape()).background(c.surface).background(tint)) {
        if(place==GroupPlace.Middle || place==GroupPlace.Last) HorizontalDivider(Modifier.padding(start=60.dp),color=c.outlineVariant)
        Row(Modifier.fillMaxWidth().clickable(onClick=onDetails).padding(start=Space.md,end=Space.xs).heightIn(min=56.dp),
            verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.md)) {
            RegionBadge(region)
            Column(Modifier.weight(1f).padding(vertical=7.dp)) {
                Text(name,style=MaterialTheme.typography.titleSmall.copy(fontSize=16.sp),maxLines=1,overflow=TextOverflow.Ellipsis)
                Text(detail,color=c.onSurfaceVariant,style=MaterialTheme.typography.detail,maxLines=1)
                if(info.isNotBlank()) Text(info,color=signalAmber,style=MaterialTheme.typography.bodySmall)
            }
            LatencyMeter(latency,testing)
            if(onSelect!=null || selected) Box(Modifier.size(44.dp).then(if(onSelect!=null)Modifier.clickable(role=Role.Button,onClick=onSelect)
                .semantics {contentDescription="使用此节点"} else Modifier),contentAlignment=Alignment.Center){
                SelectionDot(selected)
            }
        }
    }
}

/** The radio mark on node rows: an outlined ring that springs into a filled check when chosen. */
@Composable
internal fun SelectionDot(selected:Boolean) {
    val c=MaterialTheme.colorScheme
    val p=palette
    val fill by animateFloatAsState(if(selected) 1f else 0f,motionSpring(Spring.StiffnessMedium),label="选中")
    Canvas(Modifier.size(24.dp)) {
        val r=size.minDimension/2
        val shown=fill.coerceIn(0f,1f)
        if(shown<1f) drawCircle(c.outline.copy(alpha=1f-shown),r-1.dp.toPx(),style=Stroke(1.5.dp.toPx()))
        if(fill>0f) {
            drawCircle(p.signal,r*fill.coerceIn(0f,1.1f))
            val w=size.width*.5f; val o=Offset(size.width*.25f,size.height*.25f)
            val path=Path().apply{moveTo(o.x+w*.05f,o.y+w*.52f);lineTo(o.x+w*.4f,o.y+w*.85f);lineTo(o.x+w*.95f,o.y+w*.18f)}
            drawPath(path,p.onSignal.copy(alpha=shown),style=Stroke(2.4.dp.toPx(),cap=StrokeCap.Round,join=StrokeJoin.Round))
        }
    }
}

/** Small grey heading above a grouped list. */
@Composable
internal fun SectionLabel(text:String,modifier:Modifier=Modifier) {
    Text(text,modifier.padding(start=Space.xs,top=Space.sm,bottom=Space.xs),color=MaterialTheme.colorScheme.onSurfaceVariant,
        style=MaterialTheme.typography.labelMedium.copy(fontWeight=FontWeight.SemiBold))
}

/**
 * One row of a grouped settings-style list. [trailing] replaces the default chevron (a switch,
 * a status word); rows without [onClick] are not clickable.
 */
@Composable
internal fun ListRow(title:String,detail:String="",place:GroupPlace=GroupPlace.Single,enabled:Boolean=true,
                     titleColor:Color=Color.Unspecified,trailing:(@Composable ()->Unit)?=null,onClick:(()->Unit)?=null) {
    val c=MaterialTheme.colorScheme
    Column(Modifier.fillMaxWidth().clip(place.shape()).background(c.surface)) {
        if(place==GroupPlace.Middle || place==GroupPlace.Last) HorizontalDivider(Modifier.padding(start=Space.lg),color=c.outlineVariant)
        Row(Modifier.fillMaxWidth().then(if(onClick!=null)Modifier.clickable(enabled=enabled,role=Role.Button,onClick=onClick) else Modifier)
            .heightIn(min=52.dp).padding(start=Space.lg,end=Space.md,top=Space.sm,bottom=Space.sm),
            verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.md)) {
            Column(Modifier.weight(1f).graphicsLayer{alpha=if(enabled)1f else .5f}) {
                Text(title,style=MaterialTheme.typography.titleSmall.copy(fontSize=16.sp),color=if(titleColor==Color.Unspecified)c.onSurface else titleColor)
                if(detail.isNotEmpty()) Text(detail,color=c.onSurfaceVariant,style=MaterialTheme.typography.bodySmall)
            }
            if(trailing!=null) trailing() else if(onClick!=null) GlyphIcon(Glyph.Chevron,size=18.dp,tint=c.outline)
        }
    }
}

/** Renders the small Markdown subset used by PRIVACY.md and notices: headings, bullets, paragraphs. */
@Composable
internal fun SimpleDocument(lines:List<String>) {
    val c=MaterialTheme.colorScheme
    val body=MaterialTheme.typography.bodyMedium
    Column(verticalArrangement=Arrangement.spacedBy(6.dp)) {
        lines.forEach { raw -> val line=raw.trimEnd(); when {
            line.isBlank() -> Spacer(Modifier.height(Space.xs))
            line.startsWith("# ") -> Text(line.removePrefix("# "),style=MaterialTheme.typography.titleLarge)
            line.startsWith("## ") || line.startsWith("### ") -> Text(line.trimStart('#',' '),Modifier.padding(top=Space.sm),
                style=MaterialTheme.typography.titleMedium)
            line.trimStart().startsWith("- ") || line.trimStart().startsWith("* ") -> Row(horizontalArrangement=Arrangement.spacedBy(Space.sm)) {
                Text("•",color=c.primary); Text(line.trimStart().drop(2).replace("**",""),color=c.onSurface,style=body) }
            else -> Text(line.replace("**",""),color=c.onSurface,style=body)
        } }
    }
}

/** The connection page's current-node card: the whole card opens the node list. */
@Composable
internal fun CurrentNode(region:String,name:String,detail:String,latency:Long?,private:Boolean,open:()->Unit) {
    val c=MaterialTheme.colorScheme
    NexusCard(onClick=open,onClickLabel="切换节点") {
        Row(Modifier.fillMaxWidth().padding(horizontal=Space.md,vertical=10.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(Space.md)) {
            RegionBadge(region,private)
            Column(Modifier.weight(1f)) {
                Text(name,style=MaterialTheme.typography.titleMedium,maxLines=1,overflow=TextOverflow.Ellipsis)
                if(detail.isNotEmpty()) Text(detail,color=c.onSurfaceVariant,style=MaterialTheme.typography.detail,maxLines=1)
            }
            LatencyMeter(latency)
            GlyphIcon(Glyph.Chevron,size=18.dp,tint=c.outline)
        }
    }
}

@Composable
internal fun ListEntrance(index:Int,content:@Composable ()->Unit) {
    val motion=LocalMotion.current
    var fraction by remember { mutableFloatStateOf(if(index<8) 0f else 1f) }
    LaunchedEffect(Unit){ if(index<8){if(motion)delay(index*30L);frameProgress(if(motion)Motion.emphasized else Motion.reduced){fraction=enterCurve.transform(it)}} }
    Box(Modifier.graphicsLayer { alpha=fraction;translationY=if(motion)(1-fraction)*10.dp.toPx() else 0f }) {content()}
}

@Composable
internal fun AutoSelection(checked:Boolean,enabled:Boolean,onChange:(Boolean)->Unit){
    val c=MaterialTheme.colorScheme
    val well by animateColorAsState(if(checked) palette.signalSoft else c.surfaceVariant,motionTween(Motion.standard),label="自动选择图标")
    NexusCard {
        Row(Modifier.fillMaxWidth().padding(horizontal=Space.md,vertical=Space.sm),verticalAlignment=Alignment.CenterVertically,
            horizontalArrangement=Arrangement.spacedBy(Space.md)){
            Box(Modifier.size(32.dp).background(well,Shapes.chip),contentAlignment=Alignment.Center){
                GlyphIcon(Glyph.Bolt,size=18.dp,tint=if(checked) c.primary else c.onSurfaceVariant) }
            Column(Modifier.weight(1f)){Text("自动选最快",style=MaterialTheme.typography.titleSmall.copy(fontSize=16.sp))
                val motion=LocalMotion.current
                AnimatedContent(checked,transitionSpec={ fadeThrough(motion) },label="自动选择说明"){ on ->
                    Text(if(on)"按当前网络的实测结果选择节点" else "正在使用手动选择的节点",color=c.onSurfaceVariant,
                        style=MaterialTheme.typography.detail) }}
            Switch(checked,onChange,enabled=enabled,colors=switchColors())
        }
    }
}

/**
 * Loading placeholder: a well-coloured block with a soft highlight sweeping across. The sweep
 * stops when motion is reduced or the app is in the background, and only the draw phase reads it.
 */
@Composable
internal fun Modifier.shimmer(shape:Shape=Shapes.chip):Modifier {
    val p=palette
    val motion=LocalMotion.current
    val active=LocalForeground.current
    val phase=remember { mutableFloatStateOf(0f) }
    LaunchedEffect(motion,active) {
        if(motion && active) { val start=withFrameNanos { it }; while(true) withFrameNanos { phase.floatValue=((it-start)%1_400_000_000L)/1.4e9f } }
    }
    val highlight=if(p.dark) Color.White.copy(alpha=.06f) else Color.White.copy(alpha=.75f)
    return clip(shape).drawBehind {
        drawRect(p.well)
        if(motion) {
            val x=(phase.floatValue*1.8f-.4f)*size.width
            drawRect(Brush.linearGradient(listOf(Color.Transparent,highlight,Color.Transparent),start=Offset(x-size.width*.3f,0f),end=Offset(x+size.width*.3f,size.height)))
        }
    }
}

@Composable
internal fun SkeletonBlock(width:Dp?,height:Dp,modifier:Modifier=Modifier,shape:Shape=Shapes.chip) {
    Box(modifier.then(if(width==null) Modifier.fillMaxWidth() else Modifier.width(width)).height(height).shimmer(shape))
}
