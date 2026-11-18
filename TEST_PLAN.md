# 验收矩阵

| 场景 | 现有测试或待验收内容 |
|---|---|
| V1 帧 | cargo test 校验 IPv4/IPv6、未知标记、截断和超限 |
| 转发与认证 | tools/test_tunnel.py 启动真实二进制、HTTP 和 UDP 目标，验证完整字节、两种传输、未授权设备、错名证书、越权目的地、重启、替换证书、UDP 丢弃后的回退 |
| Windows 客户端 → Linux | tools/test_windows_linux.py 使用独立 WSL 网络；可选 --public 以系统信任链校验 example.com 的 HTTPS 内容 |
| Android | 安装 ABI 匹配的 APK，导出真实设备公钥、导入配置并核对 CA，连接、验证目标回包、断开及关闭端口，检查页面/日志 |
| Android VPN | 使用浏览器访问 Linux 回环网页并核对独立回执。tools/verify_android_vpn_recovery.py 在模拟器关闭两种网络，检查等待状态和保留的路由，再恢复网络并等待重新认证。用新请求核对恢复后的回执。断开后核对 TUN、主机路由、前台服务及原网络 |
| 多订阅 A | 待接入三家独立订阅，确认独立更新和筛选，缺失配额显示未提供 |
| 部署 B | 待验证干净 Ubuntu 24.04 / Debian 12、13 及两种架构、安装预览/回滚、重启持久化 |
| 分享 C | 待验证两台设备领取、到期、配额与即时撤销；当前仅手动设备授权 |
| 专属协议 D | 开放的自定义协议 + 私钥拥有证明；公网 VPS 独立验收、原生 QUIC Datagram 待办 |
| 稳定性 E | CLI 重启和回退已纳入自动化；移动网络切换、业务固定出口防泄漏和长稳待办 |
| 隐私 F | 无中央 API。需在产品功能齐全后补充离线、订阅本地加密与全链路流量观察 |

VLESS、VMess、Trojan、Shadowsocks、Hysteria2、TUIC、AnyTLS：各项 SUPPORTED / CODED / TESTED / WORKING 均为否。本批无兼容订阅引擎。
