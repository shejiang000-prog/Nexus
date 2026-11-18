# Nexus 工程边界

本地优先的私人网络客户端。此次优先交付 Android 私人节点端口转发。服务端和授权材料归用户所有。安装、签名、构建缓存、测试身份和发行各自独立。

| 模块 | 职责 |
|---|---|
| nexus-protocol | 检查版本和定长 OPEN，解析状态、IPv4/IPv6 和有界 UDP 帧 |
| nexus-transport | Quinn QUIC、rustls 双向 TLS、证书链与设备叶证书校验，向协议层交付双向字节流 |
| nexus-client | 发送 OPEN，只有目标获准且服务端成功建立目标 socket 才返回成功。CLI 提供 TCP 或 UDP 测试入口 |
| nexus-server | 监听同号 UDP/TCP；仅一个经所有者授权的设备，逐目标核对精确 IP 与端口 |
| nexus-android | JNI 桥接、Keystore 签名回调、回环监听、TUN IP 包和 TCP 流转换、取消及活动连接生命周期 |
| apps/android | Kotlin / Compose、文件选择器、信任确认、设备身份及前台服务 |
| apps/desktop | 独立 Tauri 2 / WebView2 初始化工程。保留首页、订阅、服务器、朋友分享的导航和空状态 |

Android 用系统文件选择器导出公钥，拥有者在线下签发设备证书。导入文件仅含公开证书、服务器和目标。导入时检查本机公钥、有效期和签名，并向用户展示 CA 指纹。启动时由设备 Keystore 完成 P-256 签名。前台服务负责 Rust 会话；断开通过关闭 Unix socket pair 取消所有子任务。显示的端口只属于本次会话。

Windows 与 Linux CLI 的开发身份采用短期文件凭据。Android 的两种私人入口共用设备身份、rustls 认证与服务端 OPEN。VpnService 为版本 1 建立单条 IPv4 主机路由，为版本 2 建立 IPv4/IPv6 默认路由并指定 DNS，仅保护底层传输套接字。服务保留 TUN 描述符，网络变化后取消旧流并重新认证。Rust 转发 TCP 流和有界的双向 UDP 关联。

订阅使用 libXray 和 shoes 的 Android 接口。单个出站参数来自订阅，TUN、路由和底层套接字由客户端控制。私人节点管理经指纹校验的 SSH 部署两项普通用户 systemd 服务。nexus-server 按服务器本地授权库检查设备并扣减配额，node_admin.py 负责签发及邀请领取。两项服务独立于订阅引擎及官方账户。
