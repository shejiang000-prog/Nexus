# Nexus · Android 优先

## 2026-11-18 单目标 VpnService 与浏览器验收

环境为 API 35 x86_64 可见模拟器和同一台 Debian 13 WSL2 私人节点。Android 使用系统 VPN 授权、单条 IPv4 主机路由和非 VPN 底层网络；TCP 和 QUIC 隧道套接字绑定到所选网络。服务器只允许访问它自己的回环网页目标。用户在模拟器 Chrome 中手动导航，验收同时检查屏幕正文和 Linux 服务端 GET 日志。

| 检查 | 结果 |
|---|---|
| 浏览器通过私人节点 | Chrome 返回 Debian 页面，首次请求 b7a52c94 的回执为 04ea3962e6c2a4e6f22c89b1，与服务端一致 |
| 网络中断与恢复 | 关闭 Wi-Fi 和移动数据后显示「等待网络」，保留私人主机的 TUN 路由。恢复两种网络后自动重新认证；新请求 63ad914f 的回执为 e4076c35415bb8c657e3fafa，与服务端一致 |
| 用户主动断开 | 最终构建再次通过断网恢复。点击断开后 UI 回到「未连接」，tun0、/32 路由和前台服务均已移除；默认网络恢复，模拟器直连 Debian 收到 ICMP 回应 |
| 原本地端口模式 | 最终构建收发 524288 字节逐字节一致，HTTP 200，断开后本地端口拒绝连接 |

验收中修复两处问题：默认网络回调会返回 VPN 自身，现仅监听物理网络；系统绑定会延长服务生命周期，现显式关闭 TUN 和活动流再停止服务。Windows 核心回归通过 2 项单测与 14 项进程集成检查，Android x86_64 原生目标通过 Clippy。最终本地 Debug APK 的 SHA-256 为 `ff0a60d9d6d7255dc015f7a58a86ec5f6d800bac51b04089ddeff3afa783ae97`。

上次 CI 的 Android 作业在 SDK 安装处报告 sdkmanager 不在 PATH，现从 ANDROID_HOME 直接调用；同批 core 和 desktop-bootstrap 已通过。Codex Security 差异扫描未能识别当前 Git 工作树。本轮覆盖单目标 TCP、模拟器断网恢复及断开清理。全设备 VPN、UDP/DNS 和真机弱网仍需独立验收。

## 2026-11-18 首次设备授权与转发验收

Android Debug 0.1.0（versionCode 1），API 35 x86_64 模拟器，使用 Android Keystore 生成的 P-256 设备密钥。服务端运行于独立 WSL2 Debian 13 x86_64，以普通用户身份启动。验收配置只包含公钥证书与地址。

| 步骤 | 实际结果 |
|---|---|
| 导出、签发 | 在 Android 系统文件选择器保存公钥。Linux 用短期 CA 为该公钥签发 clientAuth 证书，并将设备叶证书加入服务器授权 |
| 导入 | 从 Downloads 选择配置；屏幕上的完整 SHA-256 指纹与签发端一致，随后确认导入 |
| 连接 | Android 前台服务通过 Rust 客户端完成 mTLS 和目标 OPEN，回环端口 37675 进入 LISTEN |
| 实际数据 | 主机测试程序 → adb 映射 → Android 本地端口 → Nexus 隧道 → Debian 上独立的 HTTP 服务。POST 返回 200；发送和收到的随机二进制各 524288 字节，逐字节一致 |
| 断开 | 点击「断开」，界面恢复「未连接」。手机侧监听消失，手机内直接 TCP 重连返回 connection refused；前台服务列表为空，测试映射已移除 |

回显内容 SHA-256 为 `81c363b536bf69377fabb74568ec67108b6e982cb273611500ac8f808886a17b`。本地已验 APK 的 SHA-256 为 `74a3456ac1c1e2576dd4f3c1bda1aa2796ca73ab84b8eaf951c36f8a5a4f87ae`。临时签发器与回显服务已结束；重验时重新签发配置。自动模式本次未采集具体选中的载体，传输类型单独由 CLI 的强制 QUIC/TLS 测试覆盖。

按 [Android 操作说明](../ANDROID.md) 建立测试环境，界面显示端口后运行 `python tools/verify_android_forward.py --serial <模拟器序列号>` 可复验数据与关闭行为。脚本会点击断开。

Rust 核心此前在 Windows 和 Linux 各通过 2 项协议测试与 14 项进程集成检查。Windows 到 Debian 的强制 QUIC 和 TLS 两种路径均已验证 HTTP、UDP 回显及 example.com HTTPS。Windows 外壳保留在初始化阶段。Android ARM64 已打包，真机、手机 UDP、系统 VpnService、多订阅及公网 VPS 验收待办。下一步优先 Android。
