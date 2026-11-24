# Android Debug 使用与验证

Android 可使用 V2 私人节点或第三方订阅连接 VPN。V2 设备私钥由 Android Keystore 保存，第三方节点沿用各自的认证方式。

## V2 私人节点邀请

在「节点 → 添加节点」粘贴或扫描 `nexus://invite/v2#…`；也可从首次启动页进入「收到了邀请」。核对弹窗中的节点地址和端口，点「确认兑换」为本机注册。卡片显示账户到期日和实际承载；老节点省略账户到期信息时显示「到期时间未知」。点卡片进入节点列表，再点设备记录，可查看设备 ID 简写或移除本机密钥。移除后需向邀请人索取新的邀请。节点管理员仍可在账本中撤销设备。

分享 V2 节点时，输入 1–3650 天或打开「永久」，再点邀请或设备行的「续期」。账号可在永久和按天之间切换。按天续期从原到期日和服务器当前时间的较晚者开始，永久账号改为按天时从服务器当前时间开始。App 生成的邀请链接最多保留 7 天供领取。

在「我的服务器」填写服务器 IP。域名可留空，安装器自动部署 V2 和 Reality。有域名时 V2 由 Caddy 申请和续期公有证书，也可勾选一同部署的 Reality 或 Hysteria2。Hysteria2 需要域名。使用 IP 时节点保存一把 P-256 私钥，证书续期与升级沿用该密钥。普通卸载保留密钥，彻底清除（`uninstall --purge`）后重装需重新领取邀请。

管理员通过已安装节点生成一次性链接，`$EXPIRES_AT` 为 RFC3339 格式的领取截止时间。CLI 从部署配置读取入口和端口，IP 模式自动附带 SPKI 固定值。若服务器上已有 `qrencode`，追加 `--qr` 会在终端绘制二维码；否则仍输出可复制的链接。二维码与链接是同一份凭据，请私下分享。

```sh
runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db invite --link "$USER_ID" "$EXPIRES_AT"
```

Android Keystore 优先使用 StrongBox。域名邀请校验证书链和域名，带固定值时也校验固定值。IP 邀请使用固定值验证身份。兑换和每次 H2/H3 握手均执行验证，失败返回证书错误并停止。认证失败、撤销或到期会停止自动重试；网络中断按退避重连。绕过大陆、全局代理、DNS 和「设置 → 按应用代理与出口诊断」沿用现有 VPN 路径。H2 恢复到 H3 时已有 H2 流可继续读写。

升级时重新生成节点配置，或移除旧版站点中的公开 `/health.bin` 路由和同名静态文件后重载 Caddy。V2 handler 只向出示有效设备证明的 H2/H3 请求提供健康对象与账户到期时间。新客户端在旧节点返回公开 HEAD 200 时尝试一次 CONNECT-UDP 认证，目标为 `1.1.1.1:53`。需要兼容这条旧路径的节点须允许该目标。更新节点后才能做已认证的健康比较。

## 构建

安装 JDK 21、Android SDK 36、NDK 28.2.13676358、Python 3、Rust 1.100.0。将 cargo 与 python 加入 PATH，或设置 NEXUS_PYTHON 指向实际 Python。执行：

```sh
rustup target add aarch64-linux-android x86_64-linux-android
cargo install cargo-ndk --version 4.1.2 --locked
cd apps/android
./gradlew :app:assembleDebug
```

标准 Debug 与 Release 使用 `tools/build_v2_aar.py` 从固定源码合编 libXray 与 V2，Gradle 自动执行。准备 Go 1.27.4、来自 `golang.org/x/mobile@v0.0.0-20261011034528-197770444867` 的 gomobile 和 gobind，将它们放在 PATH。将含提交 `5a0f7d8d1c28b34afb5727b04b26a10f6dbac7e3` 的 libXray 仓库放在 `target/libXray`，也可设置 `NEXUS_LIBXRAY_SOURCE`。脚本读取该 Git 对象，核对 go.sum、应用仓库内补丁，并逐 ABI 验证 Android 架构及传输依赖。预备 Go 模块、Gradle、Cargo、shoes 源码与分流数据缓存后，设置 `CARGO_NET_OFFLINE=true` 并给 gradlew 加 `--offline` 可离线构建。离线模式禁用 Go 模块代理及远程校验服务，仍按固定源码中的 go.sum 验算。Go build info 会记录本地模块替换的绝对路径。逐字节复现时使用相同版本的 Go、NDK、JDK、相同的工作目录绝对路径（可用 `NEXUS_AAR_WORKDIR` 指定一个尚不存在的仓库外目录）。并行构建使用各自的工作目录。Windows 使用 gradlew.bat。APK 位于 app/build/outputs/apk/debug/app-debug.apk，另外两个验收目标是 :app:assembleDebugAndroidTest 和 :vpnprobe:assembleDebug。

Windows 的 PowerShell 构建示例沿用实验室已有的 Go 工具及离线模块缓存。换机器时将路径改成同版本安装的实际位置。

```powershell
$goBin = 'E:\Tools\gopath\pkg\mod\golang.org\toolchain@v0.0.1-go1.27.4.windows-amd64\bin'
$mobileBin = 'E:\temp\nexus-round5\bin'
$env:Path = "$goBin;$mobileBin;$env:Path"
$env:GOTOOLCHAIN = 'local'
$env:GOPATH = 'E:\Tools\gopath'
$env:GOMODCACHE = 'E:\temp\toolchains\go-mod-r2'
$env:NEXUS_LIBXRAY_SOURCE = 'E:\temp\nexus-round5\libXray'
go version
go env GOMODCACHE
```

Debug 将两个架构的 V2 Linux 节点放入 APK，可通过 SSH 离线部署。Release 包对每个节点程序只内置 SHA-256。目标服务器需要 curl 或 GNU Wget、系统自带的 timeout 与 sha256sum，以及到 GitHub Releases 的 HTTPS 连接。App 根据本机版本和远端架构选择同版本标签的程序。优先使用 curl，缺少 curl 时使用 wget。初始请求和每次重定向都须使用 HTTPS。连接超时 15 秒，整次下载限时 300 秒。程序下载到私有临时目录，校验通过后才执行。切回上次已安装的旧版程序时使用服务器上的副本。

Release 准备脚本从 Gradle 的 `app/build/nodeBinaries/linux` 取同一次构建的节点文件，核对两个 APK 中的摘要，作为 `nexus-node-linux-amd64` 和 `nexus-node-linux-arm64` 随对应 `vX.Y.Z` 的 APK 发布。正式发布须使用新的版本标签。在该标签具有匹配资产前，Release 一键部署会报告失败。

## 部署、分享与订阅

Android 的 V2 部署和节点更新支持 Debian 12、13，以及 Ubuntu 22.04、24.04、26.04、26.10，适用于 x86_64 和 aarch64。已绑定的管理员可在「我的服务器」点击「更新节点程序」。更新期间服务会重启，设备授权和证书保留。

V2 管理页可签发邀请、调整有效期、撤销设备和导出已启用的兼容协议链接。只有 V2 提供按设备撤销；兼容客户端依靠可复制的凭据接入。

订阅的「管理」可设置置顶、排序和是否参与自动选择。勾选「验证网页并尝试备用节点」后，先验证选中节点可到达 example.com，失败时从已参与自动选择的来源尝试最多七个候选。连接期间每 20 秒再次检查，连续两次失败后切换。首页的「按当前网络质量选择节点」会考虑来源置顶、剩余额度、有效期、最近一天同类网络上的网页成功率、耗时、抖动及已有测速结果。点击当前节点的测速按钮会经 VPN 访问 speed.cloudflare.com，下载和上传各 1 MiB。

首页的「订阅与节点」接受 HTTPS 订阅、节点文本、Base64、Clash/mihomo proxies 和 sing-box outbounds。各来源分别加密保存在手机中。具体协议的实测进度见交接记录。配置导出会包含节点连接凭据。

可用的协议为 VLESS（含 REALITY 与 Vision）、VMess、Trojan、Shadowsocks（含 2022 系列）、Hysteria2（含 salamander 混淆与端口跳跃）、TUIC v5 和 AnyTLS。传输方式支持 TCP、TCP HTTP 伪装、WebSocket（含早期数据）、HTTPUpgrade、gRPC 和 XHTTP。Clash 的 `fingerprint` 按叶证书 SHA-256 固定校验。WebSocket 节点未写 SNI 时使用 Host 头，与 mihomo、v2rayN 一致。

无法使用的节点仍会导入，列表中以红字写明原因，不参与自动选择。以下情况会被拒绝：要求跳过证书验证（`skip-cert-verify`、`insecure=1`）、自定义 CA、ECH、Shadowsocks 插件（obfs、v2ray-plugin 等）、HTTP/2 与 QUIC 传输（Xray 已移除）、VMess alterId 不为 0、旧式流加密（如 aes-256-cfb）、TUIC v4 和链式代理。下载订阅时 User-Agent 含 `v2rayN`，面板通常据此返回 Base64 分享链接。
