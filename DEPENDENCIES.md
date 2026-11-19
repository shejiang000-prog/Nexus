# 工具链与依赖

| 部分 | 选择及检查依据 |
|---|---|
| Rust 核心 | Rust 1.100.0，workspace Cargo.lock。Tokio、Quinn、rustls 和 tokio-rustls。关闭默认 TLS 提供器，仅启用 ring 与 TLS 1.3 |
| Android | JDK 21、Java/Kotlin 字节码 17、Gradle 8.14.3（wrapper 校验 SHA-256）、AGP 8.13.0、Kotlin/Compose 编译器 2.2.20、Compose BOM 2025.12.01、Material3 1.4.0、Activity Compose 1.10.1、WorkManager work-runtime-ktx 2.10.1（订阅周期更新），minSdk 26、target/compileSdk 36 |
| Android native | NDK 28.2.13676358、Rust arm64-v8a 与 x86_64、jni 0.21.1。Gradle 调用 tools/build_android.py 同步构建本次源码 |
| Windows 工程 | Tauri 2、系统 WebView2、MSVC 与 Windows SDK，自有桌面 Cargo.lock。桌面 Debug 构建已验证 |
| Linux | Debian 13 x86_64 WSL2，普通用户启动服务，Rust 原生二进制。其他发行版/架构须另验 |
| 测试 | Python 3、OpenSSL 3 CLI，短期测试身份。无中央服务器、订阅 SDK、商业代理内核或预置服务器 |

Android TUN 使用 netstack-smoltcp 0.2.5（MIT 或 Apache-2.0），smoltcp 负责 TCP/IP，futures-util 驱动有界包通道。直接和间接 Rust 包见 THIRD_PARTY_LICENSES.md。Android 构建可执行 `gradlew :app:dependencies --configuration debugRuntimeClasspath` 复核解析图。AndroidX 使用 Apache-2.0。Android SDK/NDK 与 WebView2 各自适用平台提供方的条款，正式分发需保留适用 notices。

上游资料：[Quinn](https://docs.rs/quinn/)、[rustls](https://docs.rs/rustls/)、[Android Keystore](https://developer.android.com/privacy-and-security/keystore)、[AndroidX](https://source.android.com/docs/setup/about/licenses)、[Tauri](https://v2.tauri.app/start/prerequisites/)。
