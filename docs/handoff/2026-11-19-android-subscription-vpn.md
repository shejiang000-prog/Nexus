# Android 订阅 VPN 和节点迁移交接

## 当前进度

工作分支 `android/subscription-vpn-acceptance`，基线 `50db933`。验收对象为雷电 14、API 34、x86_64 的 Debug 包。提交门槛是七种协议的主节点和备用节点均有应用流量证据、断网恢复及主动断开通过。远端基线的三个作业为绿色，新分支的提交和 CI 状态单独记录在本页。

| 进度 | 改动入口 | 证据与边界 |
|---|---|---|
| 订阅与连接 | `SubscriptionScreen.kt`、`CompatEngine.kt`、`ShoesEngine.kt`、`TunnelService.kt` | 系统文件选择器已导入 Nexus 节点集合与标准 Base64 链接。Xray 或 shoes 接入真实 TUN。自动切换只使用获准来源，保留当前 VPN 路由 |
| 智能选择与测速 | `NodeQuality.kt`、`SubscriptionScreen.kt` | 首页共用节点列表；候选参考网络类型、最近成功率、延迟、抖动、额度和主动测速。固定私人出口仍优先 |
| 私人节点迁移 | `NodeManager.kt`、`ServerScreen.kt`、`Profile.kt`、`tools/install_node.py`、`tools/node_admin.py` | 雷电用 ECDSA 主机指纹从干净 Debian 12 部署并迁入干净 Ubuntu 24.04。核对源端两项服务停止、目标两项服务活动、CA、来宾额度和撤销状态。两个临时 WSL 系统已清理 |
| 迁移文件及出口保护 | `CatalogCheck.kt` 的 `migration-crypto`、`migration-ssh` 模式 | 密文往返、口令错误及密文篡改均实测。用不同的历史 IP 触发真实公网 IP 查询，确认 VPN 在地址变化后暂停转发。真实双出口变化待外部环境 |
| 自研协议恢复 | `crates/nexus-transport/src/lib.rs`、`tools/test_tunnel.py`、`BENCHMARKS.md` | 同一 UDP 丢弃场景，第二笔经认证的 512 KiB 请求从 8.09 秒降到 0.09 秒。14 项 Linux 传输检查通过。Windows 上的 Rust fmt、Clippy、工作区单测通过 |
| 构建 | `apps/android`、`.github/workflows/build.yml` | Debug、AndroidTest、独立应用和未签名 Release APK 均能构建。包内两种 Linux 节点 ELF 在本次构建中要求的最高 glibc 版本均为 2.34 |

## 本轮本地验收结果

七种协议的首选和停机切换均在雷电 14 上取得匹配节点的浏览器回执。七协议批测覆盖六组完整主备，VLESS 首选在最终 APK 上单独重测通过。各次试验与中途失败保留在下方。最后一轮 `--links --protocol vless --ld ...` 退出 0，收尾清理成功。

| 场景 | 实测结果 |
|---|---|
| 最终 APK 首选及切换 | 首选回执 `vless-a947506a990804f5`，停机后 `trojan-b49b3c574647192c`，两阶段均跑通浏览器、独立 UID 双站 HTTPS、DNS 和三种大小的 UDP |
| 两种物理网络一起关闭 | 应用进入重试，指定 wlan0 查询到私人节点的内核路由不可达，IPv4、IPv6 仅剩 VPN 和系统 dummy 默认路由 |
| 恢复 Wi-Fi 与数据 | 自动恢复，浏览器取得新的 `trojan-0fa64def34f523fe`，独立应用重复数据检查通过 |
| 真实测速、主动断开 | 恢复后各传 1 MiB，本次下载 3.50 Mbps、上传 5.20 Mbps。主动断开后 TUN 消失，独立应用经普通网络从两个 HTTPS 站点读回正文 |
| 界面及清理 | 已查看深浅色首页、节点列表和真实浏览器截图；本轮未见 Nexus 崩溃。恢复原网络及浅色设置，清理当次 QA 订阅和节点，原私人配置保留 |

工作分支交付包含本页和交接索引。下一位接手者可先检查下方工作分支 CI，随后推进真机与公网验收。

## 七种协议的实际流量

每次验证使用独立 QA 来源。浏览器访问带随机路径的网页，页面回执须匹配对应 Linux 节点。独立 UID 检查系统 VPN、指定 DNS、example.com 和 example.org 的 HTTPS 200 及正文，并核对 32、1200、1472 字节随机 UDP 回显。停止主节点进程后再次做同样检查，要求回执来自另一个节点。

| 主协议 | 已通过的停机切换 | 备注 |
|---|---|---|
| VLESS | VLESS → Trojan | 主、备均有浏览器及独立 UID 流量 |
| VMess | VMess → Shadowsocks | 主、备均有浏览器及独立 UID 流量 |
| Trojan | Trojan → Hysteria2；另一次为 TUIC | 主、备均有浏览器及独立 UID 流量 |
| Shadowsocks | Shadowsocks → TUIC；另一次为 AnyTLS | 主、备均有浏览器及独立 UID 流量 |
| Hysteria2 | Hysteria2 → VMess | 标准 Base64 订阅实测。早期冷启动曾先切到 VLESS；保留该记录，继续观察 |
| TUIC | TUIC → Shadowsocks；另一次为 AnyTLS | 实验对端使用 h3 和精确叶证书指纹 |
| AnyTLS | AnyTLS → TUIC | 截图可见正确回执时，旧雷电 WebView 偶尔没有公布正文的无障碍节点 |

以上行来自多次试验。02:05 接手点：Hysteria2 使用标准 Base64 来源作为首选节点，浏览器和独立应用通过，停机后 VMess 的回执和收发也通过。第一次扩展断网检查超时，雷电保留了 Wi-Fi NetworkAgent，Wi-Fi 关闭还会中断 adb。已用雷电原生控制通道运行长时间测试，增加物理接口和 VPN 路由检查；两种网络已恢复，残留测试来源已清理。新版统一复验进行中。主动测速、提交和当前提交 CI 仍待记录。

02:10 统一复验已确认 Hysteria2 → VLESS。下一项单独指定 VLESS 时启用了 VMess 备用，未取得该项首选回执；需复查后再决定是否交付。Android Debug、AndroidTest、探针及未签名 Release 最终构建成功。

02:14 同一份标准订阅继续通过 VMess → Shadowsocks、Trojan → TUIC，主备均核对了节点回执及独立应用流量。

随后通过 Shadowsocks → AnyTLS 和 TUIC → VLESS，AnyTLS 作为首选也已通过两应用的数据检查。

02:21 AnyTLS → VMess 的主备双应用均通过。关闭两种网络后，雷电保留 wlan0 IPv4 地址，内核实际查询到 QA 对端的物理路由返回不可达，TUN 的两族默认路由仍存在。旧脚本误要求地址消失，恢复 Wi-Fi 时短暂复用了旧 adb 通道；已改为核对两族物理默认路由、对端的指定接口路由和恢复后连续可用的 adb，旧来源已清理。最终 APK 已覆盖安装，开始 VLESS 首选、断网恢复、测速和断开复验。

## 复验与接手

1. 保留应用数据覆盖安装 `:app:assembleDebug`、`:app:assembleDebugAndroidTest`、`:vpnprobe:assembleDebug` 产物。查看 `adb devices -l`，只操作选定的雷电模拟器。Android 必须已经授予该应用 VPN 权限。
2. 在仓库根目录使用下面的 PowerShell 命令。Linux 侧使用 Xray 26.11.9（linux/amd64，676eaf6）和 shoes 0.3.8 CLI，本机使用 adb、Python 3、Node.js 22。替换路径时保留节点版本和应用签名。应使用隔离的 QA 节点和合成订阅。
3. 主机脚本逐个停、启独立服务端，自动操作雷电内置浏览器。首选无障碍树核对正文，必要时读取当前同一测试 URL 的可见 WebView 正文。截图目录可指向工作区以外。不要把原始订阅、私钥或迁移文件提交到 Git。
4. 出现失败时保留协议、主备回执、独立应用失败阶段和当次结果。不应仅凭工具退出码、设备 VPN 图标或先前分支 CI 判定通过。

```powershell
$labHost = ((wsl -d NexusLab -- hostname -I).Trim() -split '\s+')[0]
python -u tools/verify_subscription_vpn.py --serial emulator-5554 --distro NexusLab --host $labHost `
  --xray /tmp/nexus-fixture-xray --shoes /tmp/nexus-shoes-fixture-1813/shoes `
  --temp-dir E:/temp/nexus-bootstrap-tools --links --ld D:/leidian/LDPlayer14/ld.exe `
  --screens E:/temp/nexus-bootstrap-tools/subscription-acceptance
```

在新机器上，可用对应版本的 Linux 可执行文件替换两个 `/tmp` 路径；从仓库根目录启动，让 WSL 在同一检出中运行 `tools/compat_lab.py`。工作分支的新 CI 可用 `gh run list --branch android/subscription-vpn-acceptance` 查看，核对 run 的 headSha 与待交付提交一致。[工作分支的 GitHub Actions](https://github.com/shejiang000-prog/Nexus/actions/workflows/build.yml?query=branch%3Aandroid%2Fsubscription-vpn-acceptance)

本机 QA 记录在 `E:/temp/nexus-bootstrap-tools/subscription-acceptance`。针对单一协议可增加 `--protocol hysteria2` 等参数。迁移测试需两个全新的授权测试系统，并通过测试器生成的一次性公钥配置 SSH；`migration-ssh` 会部署和归档服务，只适用于可丢弃环境。正常用户流程见 [Android 使用说明](../ANDROID.md)。

## 外部验收与工具限制

- 真实手机验证相机实扫、熄屏长连、电耗、系统回收及蜂窝和 Wi-Fi 切换；公网 VPS 验证运营商 UDP 限制、真实双栈 IPv6、双公网出口变化、跨地区持续吞吐和长期性能对比。
- 此次迁移使用 x86_64 系统，arm64 服务端已交叉构建。仅配置 Ed25519 主机密钥的测试 SSH 端点协商失败，默认 OpenSSH 主机密钥配置使用 ECDSA 验收通过。
- Codex Security 差异扫描预检返回工作树 HEAD 无法解析，未生成正式扫描报告。一次修改启动探测时序的自动审批返回 `blocked by policy`，该修改未应用。
- 每次完成一个功能、发现阻塞或验收取得新结果，更新此页的对应行和下一步。推送时把交接文件与对应代码放在同一个提交中。
