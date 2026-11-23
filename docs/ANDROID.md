# Android Debug 使用与验证

Android 可用 Nexus 私人节点连接全设备 VPN，或按配置开放本机 TCP 转发。私人节点验证服务器名称、CA 和 Android Keystore 设备身份。订阅节点另外使用对应协议的认证流程。

## 全设备连接

版本 2 私人配置在版本 1 的基础上增加数值 IPv4 字段 `dns`。核对并导入配置后点击「连接 VPN」，首次使用需在 Android 系统弹窗确认。IPv4、IPv6 默认路由进入 TUN，系统 DNS 和各应用的 TCP、UDP 经私人节点转发。DNS 服务器必须从节点可达。只给底层隧道套接字调用 protect。物理网络断开时保留 VPN 路由，新网络可用后重建会话。主动断开后关闭 TUN，恢复系统选路。系统撤销或杀死 VPN 进程时的保护依赖系统策略。

在「出口与诊断」可锁定私人节点或开启备用节点切换。固定出口优先。协议握手成功和网页访问成功是两个独立检查。可用「检测端口、DNS 和网页」分别验证。ipinfo.io 支持单次查询，用户还可为具体节点启用每五分钟一次的出口查询，并选择地址变化时暂停转发。

雷电模拟器的 ADB 会随 Wi-Fi 暂时离线。在已连接的 Nexus 首页，使用 `python tools/verify_android_vpn_recovery.py --ld-shell <雷电安装目录>/ld.exe --ld-index 0` 从模拟器控制台执行断网恢复验收。脚本会恢复测试前开启的 Wi-Fi 和移动数据。

测试机可给下方 android_lab.py 命令附加 `--dns <节点可达的 IPv4 解析器>` 生成版本 2 配置。该模式允许转发至公网地址。服务器默认保留目标白名单，开启公网访问需要显式指定 `--internet`，私人网络地址仍需单独授权。只向受信任设备签发证书。

## 旧版单目标配置

导入配置后选择「连接 VPN」，核对界面列出的浏览器目标和服务端目标，首次使用需确认 Android 系统的连接请求。浏览器直接访问该地址，无需设置 HTTP 代理。普通 IPv4 配置使用原目标 IP；服务端回环及 IPv6 目标使用手机侧 IPv4 别名 192.0.2.1。端口保持一致。HTTPS 站点仍须通过浏览器自身的证书验证。

VPN 仅接管指定 IPv4 的 /32 路由，并在 Rust 转发层检查许可端口。其他地址使用原网络。网络切换会结束旧 TCP 流；系统网络恢复后重新验证私人节点，浏览器可发起新请求。点击「断开」或从 Android 设置撤销授权会撤回该路由。配置内的服务端回环地址具有独立于手机的含义。

实验网页使用 `http://<界面地址及端口>/vpn/<8至32位小写十六进制请求号>`。每次 GET 返回服务端生成的一次性回执并禁止缓存。用不同请求号验证接通、断网恢复和断开；比对浏览器回执与 Linux 控制台记录。

连接后的 Nexus 首页可运行 `python tools/verify_android_vpn_recovery.py --serial emulator-5560`。该测试要求模拟器的 Wi-Fi 和移动数据起初均已开启，短暂关闭两者并检查仍保留的主机路由，最后恢复原设置。

## 构建

安装 JDK 21、Android SDK 36、NDK 28.2.13676358、Python 3、Rust 1.100.0。将 cargo 与 python 加入 PATH，或设置 NEXUS_PYTHON 指向实际 Python。执行：

```sh
rustup target add aarch64-linux-android x86_64-linux-android
cargo install cargo-ndk --version 4.1.2 --locked
cd apps/android
./gradlew :app:assembleDebug
```

构建前，在支持的 Linux 环境依照仓库 CI 的 Android 作业交叉编译两个 nexus-server，将它们放在 app/build/linuxNodes/{x86_64,aarch64}/nexus-server。也可用 NEXUS_LINUX_BINARIES 指定具有相同布局的目录。标准 Debug 与 Release 使用 `tools/build_v2_aar.py` 从固定源码合编 libXray 与 V2，Gradle 自动执行。准备 Go 1.27.4、来自 `golang.org/x/mobile@v0.0.0-20261011034528-197770444867` 的 gomobile 和 gobind，将它们放在 PATH。将含提交 `5a0f7d8d1c28b34afb5727b04b26a10f6dbac7e3` 的 libXray 仓库放在 `target/libXray`，也可设置 `NEXUS_LIBXRAY_SOURCE`。脚本读取该 Git 对象，核对 go.sum、应用仓库内补丁，并逐 ABI 验证 Android 架构及传输依赖。预备 Go 模块、Gradle、Cargo、shoes 源码与分流数据缓存后，设置 `GOPROXY=off`、`CARGO_NET_OFFLINE=true` 并给 gradlew 加 `--offline` 可离线构建。Go build info 会记录本地模块替换的绝对路径。逐字节复现时使用相同版本的 Go、NDK、JDK、相同的工作目录绝对路径（可用 `NEXUS_AAR_WORKDIR` 指定一个尚不存在的仓库外目录）。并行构建使用各自的工作目录。Windows 使用 gradlew.bat。APK 位于 app/build/outputs/apk/debug/app-debug.apk，另外两个验收目标是 :app:assembleDebugAndroidTest 和 :vpnprobe:assembleDebug。

## 部署、分享与订阅

服务器维护页可以检测或重启服务，从受信任 APK 更新程序，或回滚到上一次安装的程序。上述操作会中断现有连接。拥有者可在 VPS 上备份、恢复授权库；恢复操作将设备、邀请和额度带回快照时点，需要单独确认。在「出口与诊断」可为私人节点选择业务应用。空列表覆盖全设备，非空列表仅覆盖指定应用。使用始终开启或系统级阻止直连前，请在 Android 系统设置确认期望的网络行为。

「移除部署」会停止服务、取消自动启动并在服务器侧归档程序与 systemd 单元，保留设备和签发身份。「恢复已移除部署」还原原来的安装并启动两项服务。

需要搬迁时，在源服务器输入至少 12 字符的迁移口令，选择「迁出服务器」并保存加密文件。旧服务会停止。填好空白目标服务器的 SSH 指纹、端口和 DNS，用相同口令选择「迁入服务器」。成员可在「出口与诊断」更新地址，保留自己的设备证书。旧邀请指向原服务器，需要重新分享。

订阅的「管理」可设置置顶、排序和是否参与自动选择。勾选「验证网页并尝试备用节点」后，先验证选中节点可到达 example.com，失败时从已参与自动选择的来源尝试最多七个候选。连接期间每 20 秒再次检查，连续两次失败后切换。首页的「按当前网络质量选择节点」会考虑来源置顶、剩余额度、有效期、最近一天同类网络上的网页成功率、耗时、抖动及已有测速结果。点击当前节点的测速按钮会经 VPN 访问 speed.cloudflare.com，下载和上传各 1 MiB。

在「部署与分享」输入本人服务器的 SSH 地址，并通过 VPS 控制台独立确认 SHA-256 主机指纹。安装器接受 Debian 12、Debian 13 和 Ubuntu 24.04，APK 提供 x86_64 与 aarch64 节点程序，需要 root 或免密 sudo。确认页面显示端口。首次安装创建普通服务账户和两个 systemd 服务，已有部署会退出。部署完可给其他设备创建限额、限时邀请。接收者的设备自己生成私钥，并在领取时证明持有对应私钥。

首页的「订阅与节点」接受 HTTPS 订阅、节点文本、Base64、Clash/mihomo proxies 和 sing-box outbounds。各来源分别加密保存在手机中。具体协议的实测进度见交接记录。配置导出会包含节点连接凭据。

可用的协议为 VLESS（含 REALITY 与 Vision）、VMess、Trojan、Shadowsocks（含 2022 系列）、Hysteria2（含 salamander 混淆与端口跳跃）、TUIC v5 和 AnyTLS。传输方式支持 TCP、TCP HTTP 伪装、WebSocket（含早期数据）、HTTPUpgrade、gRPC 和 XHTTP。Clash 的 `fingerprint` 按叶证书 SHA-256 固定校验。WebSocket 节点未写 SNI 时使用 Host 头，与 mihomo、v2rayN 一致。

无法使用的节点仍会导入，列表中以红字写明原因，不参与自动选择。以下情况会被拒绝：要求跳过证书验证（`skip-cert-verify`、`insecure=1`）、自定义 CA、ECH、Shadowsocks 插件（obfs、v2ray-plugin 等）、HTTP/2 与 QUIC 传输（Xray 已移除）、VMess alterId 不为 0、旧式流加密（如 aes-256-cfb）、TUIC v4 和链式代理。下载订阅时 User-Agent 含 `v2rayN`，面板通常据此返回 Base64 分享链接。

## 使用自己的测试环境

1. 在手机点击「导出公钥」，将生成的 PEM 公钥交给服务器拥有者。设备私钥始终保留在原手机。
2. 在已获授权的 Linux 测试机安装 Python 3 和 OpenSSL 3，运行 `cargo build --locked -p nexus-server`。将公钥复制到测试机，独立核对公钥来源。
3. 在仓库根目录执行下述命令，选择手机能访问的 Linux IP，输出路径须为空。此工具创建临时 CA、只授权本机公钥，并启动真实回显目标。终端保持打开，Enter 会结束服务并移除临时身份。此工具仅用于验收。
4. 将生成的公开 JSON 配置传回原手机，点击「导入配置」，核对服务端控制台的 CA 指纹，选择「确认导入」。再次打开客户端可看到已保存的配置。
5. 测试原有本地端口模式时，点击「本地转发」，确认同机应用可用范围。显示端口表示服务端已完成身份认证和首次目标 OPEN。应用或通知可随时断开。被转发的应用需连接界面显示的回环 TCP 端口。
6. 调试机可使用 `adb -s <serial> forward tcp:0 tcp:<界面端口>` 建立临时映射，对映射的主机端口发 HTTP POST。验收服务会原样返回请求体；断开后应用端口应拒绝连接。完成后移除本次 adb 映射。

模拟器保持解锁并显示已连接首页时，可运行 `python tools/verify_android_forward.py --serial <serial>` 自动核对 512 KiB 随机回包、点击断开，并在模拟器内验证旧端口拒绝连接。

```sh
python3 tools/android_lab.py --pubkey ~/nexus-device.pub --profile ~/nexus-lab.json \
  --host <Linux-测试机-IP> --server-bin target/debug/nexus-server
```

手工部署时，配置字段为 version=1、name、server（IP:端口）、tls_name、target（IP:端口）、ca_der、device_der。末两项为 DER 公共证书的 Base64；本批仅接受直接由该 CA 签发且属于本机的设备证书。此格式没有私钥、订阅令牌或 SSH 凭据。一个配置对应一个固定目标。
