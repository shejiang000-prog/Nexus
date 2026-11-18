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

七种兼容协议均已有来自 Android 原生引擎的测试网页回执。分协议状态和全设备验收范围见 [交接记录](docs/handoff/INDEX.md)。`tools/compat_lab.py` 可启动限定目标的对端。Android 仪器测试入口为 `CatalogCheck` 的 interop 模式。独立测试 APK 的 `Probe` 使用另一个 Android UID，可以核对 VPN 或直连默认网络、DNS、两站 HTTPS 和不同大小的 UDP 回包。恢复脚本验证模拟器两种底层网络关闭和重新开启过程中的 TUN 路由。

## 雷电订阅协议端到端验收

安装同一构建的 app Debug、app AndroidTest 和 vpnprobe Debug。主机需要 adb、Python 3 和 Node.js 22，隔离的 WSL 节点需要测试用的 Xray 和 shoes Linux 可执行文件。运行 `python tools/verify_subscription_vpn.py --help` 查看参数。可用 `--links` 测普通 Base64 链接订阅，`--ld` 追加断网恢复与主动测速，`--screens` 保留当前测试页截图。

每个主节点都要通过系统文件选择器导入的来源建立 Android VPN。雷电浏览器需要显示与当次请求匹配的节点回执和公网网页，独立 UID 需要校验系统 VPN、DNS、两站 HTTPS 正文及三种 UDP 包长。关闭当前服务端进程后再执行同样检查，并核对新回执来自不同协议。测试生成的节点、凭据和来源在运行结束后清理。
