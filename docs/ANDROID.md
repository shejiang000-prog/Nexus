# Android Debug 使用与验证

本批支持用户主动开启的本地 TCP 端口转发。按域名与 CA 验证对端，通过 Android Keystore 签名，由用户 VPS 上的设备证书许可决定是否接受。端口仅监听 127.0.0.1。传输优先 QUIC；UDP 超时后可使用同一服务器的 TLS/TCP。

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
5. 点击「开始转发」，确认同机应用可用范围。显示端口表示服务端已完成身份认证和首次目标 OPEN。应用或通知可随时断开。被转发的应用需连接界面显示的回环 TCP 端口。
6. 调试机可使用 `adb -s <serial> forward tcp:0 tcp:<界面端口>` 建立临时映射，对映射的主机端口发 HTTP POST。验收服务会原样返回请求体；断开后应用端口应拒绝连接。完成后移除本次 adb 映射。

模拟器保持解锁并显示已连接首页时，可运行 `python tools/verify_android_forward.py --serial <serial>` 自动核对 512 KiB 随机回包、点击断开，并在模拟器内验证旧端口拒绝连接。

```sh
python3 tools/android_lab.py --pubkey ~/nexus-device.pub --profile ~/nexus-lab.json \
  --host <Linux-测试机-IP> --server-bin target/debug/nexus-server
```

手工部署时，配置字段为 version=1、name、server（IP:端口）、tls_name、target（IP:端口）、ca_der、device_der。末两项为 DER 公共证书的 Base64；本批仅接受直接由该 CA 签发且属于本机的设备证书。此格式没有私钥、订阅令牌或 SSH 凭据。一个配置对应一个固定目标。
