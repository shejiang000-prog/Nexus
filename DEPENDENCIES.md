# 工具链与依赖

| 部分 | 选择及检查依据 |
|---|---|
| Rust 核心 | Rust 1.100.0，workspace Cargo.lock。Tokio、Quinn、rustls 和 tokio-rustls。关闭默认 TLS 提供器，仅启用 ring 与 TLS 1.3 |
| Android | JDK 21、Java/Kotlin 字节码 17、Gradle 8.14.3（wrapper 校验 SHA-256）、AGP 8.13.0、Kotlin/Compose 编译器 2.2.20、Compose BOM 2025.12.01、Material3 1.4.0、Activity Compose 1.10.1、WorkManager work-runtime-ktx 2.10.1（订阅周期更新），minSdk 26、target/compileSdk 36 |
| Android 数字字体 | Space Grotesk（google/fonts），OFL-1.1；随 APK 分发，中文使用系统字体 |
| Android native | NDK 28.2.13676358、Rust arm64-v8a 与 x86_64、jni 0.21.1。Gradle 调用 tools/build_android.py 同步构建本次源码 |
| Windows 工程 | Tauri 2、系统 WebView2、MSVC 与 Windows SDK，自有桌面 Cargo.lock。桌面 Debug 构建已验证 |
| Linux | Debian 13 x86_64 WSL2，普通用户启动服务，Rust 原生二进制。其他发行版/架构须另验 |
| 测试 | Python 3、OpenSSL 3 CLI，短期测试身份。无中央服务器、订阅 SDK、商业代理内核或预置服务器 |

Android TUN 使用 netstack-smoltcp 0.2.5（MIT 或 Apache-2.0），smoltcp 负责 TCP/IP，futures-util 驱动有界包通道。直接和间接 Rust 包见 THIRD_PARTY_LICENSES.md。Android 构建可执行 `gradlew :app:dependencies --configuration debugRuntimeClasspath` 复核解析图。AndroidX 使用 Apache-2.0。Android SDK/NDK 与 WebView2 各自适用平台提供方的条款，正式分发需保留适用 notices。

上游资料：[Quinn](https://docs.rs/quinn/)、[rustls](https://docs.rs/rustls/)、[Android Keystore](https://developer.android.com/privacy-and-security/keystore)、[AndroidX](https://source.android.com/docs/setup/about/licenses)、[Tauri](https://v2.tauri.app/start/prerequisites/)。

## Windows TUN（PC-M2）

| 组件 | 固定版本 | 摘要 |
| --- | --- | --- |
| 官方 Wintun ZIP | 0.14.1 | SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51` |
| ZIP 内 `wintun/bin/amd64/wintun.dll` | 0.14.1，原样使用 | SHA-256 `e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce` |
| `golang.zx2c4.com/wintun` | `v0.0.0-20230126152724-0fa3db229ce2` | `h1:B82qJJgjvYKsXS9jeunTOisW56dUokqW/FOteYJJ/yg=` |
| `golang.zx2c4.com/wireguard/windows/tunnel/winipcfg` | 模块 `golang.zx2c4.com/wireguard/windows v1.1.1` | `h1:8/H97U1v1PNDNcBsMZgU3KFuND9MQdTsU2NOwmCXArE=` |

本次 Windows x64 分发的三项核验结果如下。

1. 官方 0.14.1 ZIP 的 SHA-256 与上表及官网公布值相同。[官方发行与摘要](https://www.wintun.net/)
2. ZIP 内 amd64 DLL 的 Authenticode 状态为 `Valid`，签名者为 WireGuard LLC，签名证书指纹为 `DF98E075A012ED8C86FBCF14854B8F9555CB3D45`。Windows SDK `signtool verify /pa` 通过。
3. 内嵌内核驱动通过微软签名验证。本机 DriverStore 中的对应 CAT、SYS、INF 各自完整匹配 DLL 内的字节。CAT 有效签名者为 Microsoft Windows Hardware Compatibility Publisher，`signtool verify /kp /c wintun.cat wintun.sys` 通过。

构建复用 libXray 锁定的两个 Go 模块。`--wintun-zip` 对每次打包重查上表 ZIP 与 DLL 摘要，逐字节复制上述已验签的 DLL 并附完整许可。
