package dev.nexus.localclient

import android.content.Context
import android.content.Intent

internal fun v2Request(context: Context, key: String): Intent =
    Intent(context, TunnelService::class.java).setAction("v2").putExtra("v2-key", key)

internal class V2Failure(val code: Int) : Exception()

internal object V2Connection {
    fun error(code: Int) = when (code) {
        1 -> "V2 节点配置无效，请检查连接参数"
        2 -> "节点证书验证失败，请检查域名和系统时间"
        3 -> "设备认证失败，授权可能已撤销或到期，请联系节点管理员"
        4 -> "节点资源暂时不足，请稍后重试"
        5 -> "节点无法访问目标地址，请检查目标或节点策略"
        6 -> "V2 网络连接中断，正在等待重试"
        7 -> "请求可能已经送达，请先确认结果"
        else -> "V2 连接失败"
    }

    fun retry(code: Int) = code == 4 || code == 6
}
