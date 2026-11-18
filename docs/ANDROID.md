# Android Debug 使用与验证

Android 提供私人节点 VPN 和本地 TCP 转发。两种模式都验证服务器名称、CA 和本机设备证书，优先使用 QUIC。每份配置指定一个 TCP 目标。

## 浏览器使用 VPN

导入配置后选择「连接 VPN」，核对界面列出的浏览器目标和服务端目标，首次使用需确认 Android 系统的连接请求。浏览器直接访问该地址，无需设置 HTTP 代理。普通 IPv4 配置使用原目标 IP；服务端回环及 IPv6 目标使用手机侧 IPv4 别名 192.0.2.1。端口保持一致。HTTPS 站点仍须通过浏览器自身的证书验证。

VPN 仅接管指定 IPv4 的 /32 路由，并在 Rust 转发层检查许可端口。其他地址使用原网络。网络切换会结束旧 TCP 流；系统网络恢复后重新验证私人节点，浏览器可发起新请求。点击「断开」或从 Android 设置撤销授权会撤回该路由。配置内的服务端回环地址具有独立于手机的含义。

实验网页使用 `http://<界面地址及端口>/vpn/<8至32位小写十六进制请求号>`。每次 GET 返回服务端生成的一次性回执并禁止缓存。用不同请求号验证接通、断网恢复和断开；比对浏览器回执与 Linux 控制台记录。

连接后的 Nexus 首页可运行 `python tools/verify_android_vpn_recovery.py --serial emulator-5560`。该测试要求模拟器的 Wi-Fi 和移动数据起初均已开启，短暂关闭两者并检查仍保留的主机路由，最后恢复原设置。

## 构建

安装 JDK 21、Android SDK 36、NDK 28.2.13676358、Python 3、Rust 1.100.0。将 cargo 与 python 加入 PATH，或设置 NEXUS_PYTHON 指向实际 Python。执行：

```sh
rustup target add aarch64-linux-android x86_64-linux-android
cd apps/android
./gradlew :app:assembleDebug
```

Windows 使用 gradlew.bat。APK 位于 app/build/outputs/apk/debug/app-debug.apk。首次运行需要用户自己的测试服务器；安装包无预置可用节点。

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
