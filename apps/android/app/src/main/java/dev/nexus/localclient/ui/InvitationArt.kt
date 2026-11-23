package dev.nexus.localclient

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.geometry.*
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.*
import com.google.zxing.*
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import kotlin.math.*

internal fun invitationMatrix(link:String):Pair<com.google.zxing.common.BitMatrix,Boolean>? {
    for(level in listOf(ErrorCorrectionLevel.H,ErrorCorrectionLevel.L)){
        try {
            val matrix=QRCodeWriter().encode(link,BarcodeFormat.QR_CODE,1,1,mapOf(
                EncodeHintType.ERROR_CORRECTION to level,EncodeHintType.MARGIN to 4))
            return matrix to (level==ErrorCorrectionLevel.H)
        }catch(_:WriterException){ /* Longer messages can exceed one QR symbol's capacity. */ }
    }
    return null
}

@Composable
internal fun InvitationCode(link:String) {
    val code=remember(link){invitationMatrix(link)}
    if(code==null){Notice("邀请内容较长，请用下方按钮发送或复制");return}
    val (matrix,branded)=code
    val n=matrix.width
    val motion=LocalMotion.current
    val extent=remember(n){FloatArray(n*n){i->hypot(i%n-(n-1)/2f,i/n-(n-1)/2f)}}
    val total=(extent.max()*22+240).toInt()
    var elapsed by remember(link){mutableFloatStateOf(0f)}
    LaunchedEffect(link,motion){frameProgress(if(motion)total else 150){elapsed=it}}
    Box(Modifier.size(224.dp).background(Color.White,RoundedCornerShape(12.dp)).semantics{contentDescription="邀请二维码"},contentAlignment=Alignment.Center){
        Canvas(Modifier.fillMaxSize()){
            val unit=floor(size.minDimension/n).coerceAtLeast(1f)
            val pad=floor((size.minDimension-unit*n)/2)
            for(y in 0 until n)for(x in 0 until n) if(matrix[x,y]){
                val p=if(motion)((elapsed*total-extent[y*n+x]*22)/240).coerceIn(0f,1f) else elapsed
                if(p>0f){
                    val inset=if(motion)(1-enterCurve.transform(p))*unit/2 else 0f
                    drawRect(Color.Black.copy(alpha=if(motion)1f else p),Offset(pad+x*unit+inset,pad+y*unit+inset),
                        Size(unit-2*inset,unit-2*inset))
                }
            }
        }
        if(branded)Box(Modifier.size(28.dp).background(Color.White,RoundedCornerShape(5.dp)),contentAlignment=Alignment.Center){Mark(Modifier.size(20.dp),Color.Black)}
    }
}

@Composable
internal fun ClaimArt(done:Boolean) {
    val c=MaterialTheme.colorScheme
    val motion=LocalMotion.current
    val active=LocalForeground.current
    val selected=signal
    var phase by remember{mutableFloatStateOf(0f)}
    LaunchedEffect(done,motion,active){
        if(!active)return@LaunchedEffect
        if(done)frameProgress(if(motion)450 else 150){phase=it}
        else if(motion) {val start=withFrameNanos{it};while(true)withFrameNanos{phase=((it-start)%1_200_000_000L)/1.2e9f}}
    }
    Box(Modifier.fillMaxWidth().height(100.dp),contentAlignment=Alignment.Center){
        Canvas(Modifier.size(240.dp,88.dp)){
            val mid=size.height/2
            val offset=24.dp.toPx()
            drawLine(if(done)selected else c.outline,Offset(offset,mid),Offset(size.width-offset,mid),2.dp.toPx(),
                pathEffect=if(done)null else PathEffect.dashPathEffect(floatArrayOf(5.dp.toPx(),5.dp.toPx()),if(motion)-phase*10.dp.toPx() else 0f))
        }
        Row(Modifier.width(240.dp),horizontalArrangement=Arrangement.SpaceBetween){listOf(2,3).forEach{ i->
            Box(Modifier.size(56.dp).background(c.surface,RoundedCornerShape(16.dp)),contentAlignment=Alignment.Center){SectionIcon(i,28.dp)}
        }}
        if(done)Box(Modifier.size(36.dp).graphicsLayer{if(motion){scaleX=enterCurve.transform(phase);scaleY=scaleX};alpha=phase}
            .background(selected,CircleShape),contentAlignment=Alignment.Center){GlyphIcon(Glyph.Check,size=18.dp,tint=palette.onSignal)}
    }
}

@Composable
internal fun ClaimButton(done:Boolean,busy:Boolean,onClick:()->Unit) {
    val motion=LocalMotion.current
    val green=signal
    var p by remember{mutableFloatStateOf(0f)}
    LaunchedEffect(done){if(done)frameProgress(if(motion)650 else 150){p=it}}
    Box(contentAlignment=Alignment.Center){
        PrimaryAction(onClick,Modifier.fillMaxWidth(),enabled=!busy && !done){Text(if(done)"已接受邀请" else if(busy)"正在加入…" else "接受并连接")}
        if(done && motion)Canvas(Modifier.matchParentSize()){
            drawRoundRect(green.copy(alpha=1-p),cornerRadius=CornerRadius(17.dp.toPx()),style=Stroke((1+4*p).dp.toPx()))
            repeat(10){i->val angle=2*PI*i/10;drawCircle(green.copy(alpha=1-p),2.dp.toPx(),Offset(center.x+(size.width*.45f+p*6.dp.toPx())*cos(angle).toFloat(),
                center.y+(size.height*.42f+p*12.dp.toPx())*sin(angle).toFloat()))}
        }
    }
}
