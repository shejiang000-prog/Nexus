# Nexus · Android 优先

## 2026-11-23 协议 V2 M1 第 2 部分

[账本故障处理、管理命令与冻结版验收](2026-11-23-protocol-v2-m1-2.md)


## 2026-11-23 协议 V2 M0 第三部分

[传输接口冻结、解析回调和固定样本门槛](2026-11-23-protocol-v2-m0-3.md)

## 2026-11-22 协议 V2 M0 第二部分

[普通 CONNECT、跨端接口及正式代码门槛复测](2026-11-22-protocol-v2-m0-2.md)

## 2026-11-22 协议 V2 M0 第一部分

[正式传输接口、H2 跨端验收与 Caddy 可见差异](2026-11-22-protocol-v2-m0-1.md)

## 2026-11-21 Android 1.0.1

[出口与网页诊断、IPv6 常见问题](2026-11-21-release-1.0.1.md)

## 2026-11-26 Android 发布准备第二轮

[安全审查、设备回归与大字体截图](2026-11-26-release-prep-2.md)

## 2026-11-25 Android GitHub 发布准备

[公开版签名、检查更新、开机连接与设备验收](2026-11-25-release-prep-1.md)

## 2026-11-24 Android 简易连接与服务器管理

[Nexus Signal 六屏、连接动效与实机验收](2026-11-24-simple-mode.md)。含明暗主题、真机帧耗时、订阅总览、免密绑定和邀请流程。

## 2026-11-23 Android 分流 · 绕过大陆

[三种引擎的实际路径、规则快照、雷电验收与测速](2026-11-23-split-routing.md)。私人节点和 Xray 支持国内直连，TUIC / AnyTLS 在界面提示全局代理。

## 2026-11-22 自研协议 UDP 关联复用

[协议改动、兼容矩阵、基准与雷电验收](2026-11-22-udp-mux.md)。Windows 和 Linux 均验证了 64 个关联共用一条 QUIC 连接，以及基线二进制双向互通。

## 2026-11-21 Android 节点真实连通测试

[批量 HTTPS 真连、节点质量排序与雷电验收](2026-11-21-node-real-test.md)。Airport-Test 支持 41/41，实际通过 37 个。


## 2026-11-20 Android 订阅刷新与总览

[自动更新、VPN 来源保护、首页流量总览与雷电验证](2026-11-20-subscription-refresh.md)。

## 2026-11-19 订阅兼容性

[问题清单、改动与复验命令](2026-11-19-subscription-compat.md)。Clash/mihomo 与 sing-box 的 VLESS REALITY、HTTPUpgrade、XHTTP、Hysteria2 混淆与端口跳跃、SS-2022 可用；不可用的节点在列表里写明原因并排除出自动选择。雷电上 25 项格式检查、16 个节点的实连回执，以及 VLESS、TUIC 全设备 VPN 验收通过。

## 2026-11-19 自研协议性能与容量

[问题清单、改动与复验命令](2026-11-19-protocol-performance.md)。QUIC 改用 BBR 并放大流控窗口，额度计费改为预留租约，提高服务端与 Android 并发上限，DNS 复用已认证连接，订阅下载跟随 HTTPS 重定向。Windows、Linux、Android 构建与测试及雷电真实节点验收通过；180 ms、3% 丢包下 512 KiB 往返中位耗时由 6.3 秒降到 0.85 秒。

## 2026-11-19 安卓订阅全设备验收

[逐项进度、复验命令和接手事项](2026-11-19-android-subscription-vpn.md)

本轮雷电七种协议均完成首选连接与停机切换，包含一次 VLESS 定向重测。双网关闭、自动恢复、真实上下行测速、断开后双站直连通过。

分支 `android/subscription-vpn-acceptance`。雷电 14（API 34，x86_64）使用保留数据的覆盖安装。首页直接展示订阅和节点，按网络类型、最近验证成功率、耗时、抖动及实测速率排序。自动选择尊重来源的置顶顺序、自动选择开关、到期时间和流量额度。手动启动的 Cloudflare 测速经选中的 VPN 发送和接收各 1 MiB。

私人节点管理已在两个新建的 x86_64 WSL 系统上由同一台安卓客户端实测。手机经固定主机指纹的 SSH 将节点部署在 Debian 12，签发并撤销一个限额 123456 字节的来宾，导出加密迁移文件，然后在 Ubuntu 24.04 上迁入。旧系统的两个服务已停用，新系统两项服务均活动，CA、撤销状态、配额和服务版本核对通过。Android 实测正确口令、错误口令和篡改密文。源和目标均使用 OpenSSH 默认主机密钥集合，安卓校验 ECDSA 主机指纹；单独只提供 Ed25519 主机密钥的实验配置未能协商。

出口变化保护使用迁移后的节点访问真实 IP 查询服务，预置历史地址不同后，客户端进入持有 VPN 并暂停流量的状态。实际双公网出口切换仍需独立 VPS。两台临时系统已移除。Rust 工作区格式检查、Clippy、单测、14 项 Linux 传输验证及授权库迁移验证通过。Android Debug、AndroidTest、独立探测 APK 和未签名 Release APK 构建通过。

## 2026-11-19 全设备与节点管理开发分支

分支 `android/full-device-vpn` 基于 `06ede8b`。系统测试使用 API 35 模拟器和雷电 14（API 34 x86_64），节点位于独立 Debian 13 WSL 环境。

| 功能 | 实测证据与范围 |
|---|---|
| 私人节点全设备 VPN | 主模拟器保留设备身份覆盖安装候选 APK 后，确认 IPv4 / IPv6 默认 TUN 路由和节点 DNS。独立 UID 访问 example.com 与 example.org，分别取得 HTTPS 200 并核对正文，32、1200、1472 字节随机 UDP 原样返回 |
| 网络恢复与主动断开 | 同时关闭 Wi-Fi 和移动数据后进入等待，双栈默认 TUN 路由保留。恢复网络后自动认证，同一独立应用再次通过两站 HTTPS 和三种 UDP 包长。主动断开后 TUN 与前台服务消失，普通网络上的独立前台应用从两站读回正确正文 |
| 订阅来源 | 模拟器导入三个测试来源，更新一个来源后其余数据保持一致，持久层没有测试凭据明文 |
| 协议互通 | VLESS、VMess、Trojan、Shadowsocks、Hysteria2、TUIC 和 AnyTLS 均从 Android 测试入口获得独立 Debian HTTP 回执。TUIC 明确协商 h3，AnyTLS 与 TUIC 的实验对端使用固定测试证书。该用例使用回环 SOCKS 入口，与系统 VPN 测试分开 |
| 服务器与邀请 | 二号模拟器通过带指纹校验的 SSH 与 SFTP 部署 Debian 13 x86_64，两个普通用户服务运行，Android 的另一用户使用独立 Keystore 领取邀请。Linux 测试验证额度、活动连接撤销和拥有者继续传输。维护测试依次更新、回滚、重新更新，两个服务均启动，CA 和授权记录保留 |
| 回归 | 14 项进程级传输测试通过，包含强制 QUIC、TCP 回退及认证拒绝。工作区 Clippy 和单测通过。Android 安装包包含 x86_64 和 arm64-v8a |

雷电内置浏览器实测发现，节点无公网 IPv6 路由时，手机提前确认 TCP 握手会导致浏览器显示 ERR_CONNECTION_CLOSED。转发器现在等节点实际连接目标后才确认 SYN，拒绝时返回 TCP RST。修复后浏览器成功加载 example.com 和 www.debian.org 的页面正文。独立应用核对 IPv4、IPv6 被拒目标均在握手前失败。

雷电上的公钥由本机生成，Debian 为它签发独立配置，导入时 CA 指纹与节点一致。断网时双栈 TUN 路由保留，恢复后独立应用再次完成两站 HTTPS 和三种 UDP 回显。浏览器新请求 `ab1902ea` 的页面回执 `8399aa6219e836560d7f1482` 与节点日志一致。主动断开后没有 TUN 或前台服务，普通网络下两个 HTTPS 正文正确。本地转发另行完成 524288 字节回显，断开后旧端口拒绝连接。真机、独立公网 VPS 和实际 IPv6 网站尚待验证。

第二台模拟器实际运行了拥有者和来宾的 Android 私人节点客户端。来宾自行签名、重复领取同一证书并取得 HTTPS 200。随后服务器撤销该设备，来宾新连接在打开手机端口前被拒，拥有者仍取得 HTTPS 200。设备叶证书签发时间留出两分钟的时钟偏差余量，客户端仍检查有效期。Linux 维护用例已经过更新、回滚、再次更新。Debug 包通过安装，检查了 API 35 首页与订阅页实屏。

| 协议 | SUPPORTED | CODED | TESTED | WORKING |
|---|---|---|---|---|
| VLESS | 是 | 是 | Android → Debian | 测试链接 TCP 网页 |
| VMess | 是 | 是 | Android → Debian | 测试链接 TCP 网页 |
| Trojan | 是 | 是 | Android → Debian | TLS 网页回执 |
| Shadowsocks | 是 | 是 | Android → Debian | AES-128-GCM 网页回执 |
| Hysteria2 | 是 | 是 | Android → Debian | QUIC 上的网页回执 |
| TUIC | 是 | 是 | Android → Debian | TUIC v5、h3 网页回执 |
| AnyTLS | 是 | 是 | Android → Debian | TLS 网页回执 |

这些结果只覆盖表中的链路和测试参数。

候选 APK 的 Rust 包转发路径又通过独立的 DNS 事务测试。向受控描述符写入真实 IPv4/UDP 包，节点解析器返回正确事务号和回答记录。这个用例覆盖最后版本的原生代码，系统 TUN 仍以单独的模拟器检查为准。弱网实验完成 5 种载体、12 组 RTT/丢包条件、720 次应用层往返。[基准与可复验数据](../../BENCHMARKS.md)

最后一份 Debug 构建重新安装至 emulator-5570。七种协议回执、独立 DNS 事务、拥有者 HTTPS、来宾撤销与本机端口关闭全部通过。来宾轮换密钥后，先前配置仍能用原 Keystore 别名签名。通过实际界面连续导入三个独立来源，首页同时展示三个来源及各自的节点数。Android 通过 VIEW Intent 打开测试邀请后停在用户核对页面。已修复 SQLite WAL 快照在仅移动主文件时恢复失败的问题，授权备份和恢复回归通过。

在 emulator-5560 验证断开后的普通网络时，Android 曾将没有前台界面的测试 UID 标为 APP_BACKGROUND。独立探针现在使用前台 Activity，在系统默认网络上进行 DNS 和 HTTPS 核验，全程使用未绑定的普通套接字。Codex Security 的 diff 模式未能识别工作树，Git 实测该目录为具有有效 HEAD 的非裸工作树。

API 35 实测固定出口时，不可用节点连续三次失败仍保留 VPN，解除固定后，客户端自动与可用备用私人节点完成认证。雷电选定仅浏览器使用 VPN 时，Android 路由包含浏览器 UID 10033，浏览器回执与节点一致，未选中的独立应用通过普通网络打开两个 HTTPS 网站。另在系统设置启用始终开启和屏蔽直连，系统拉起 VPN 后独立应用通过 DNS/TCP/UDP 验证；强行停止 Nexus 后 Wi-Fi 仍开启、TUN 消失，浏览器的新请求报 ERR_NETWORK_ACCESS_DENIED。两项系统开关及全设备路由范围均恢复。相机实扫、订阅智能选择及实际公网 IP 变化保护仍需独立设备验收。

已在隔离 Debian 节点验证可恢复的移除部署，两个服务均停止，启动单元和程序从活动路径移除。恢复归档后 CA 保持一致，拥有者重新取得 HTTPS 200。当前备份恢复对象是服务器授权库，恢复校验当前服务器配置与 CA。跨机器迁移、物理设备长连接电耗、独立公网 VPS 与 VLESS + REALITY 横向基准尚未验证。

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
