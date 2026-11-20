# Android 发布准备第二轮

本地分支 `release/prep-2` 始于 `release/prep-1@90fb8eb`。本轮的发布依据包括源码审查、Windows / NexusLab 测试和一台三星真机的短时检查。雷电实验室、长时间耗电及全流程域名抓包仍缺实测证据。

## 变更

- [安全审查逐项记录](../security/2026-11-review.md)列出位置、触发条件、影响、严重度、修复与测试边界。Android 变更在 `Subscriptions.kt`、`CompatEngine.kt`、`NodeFormats.kt`、`ShoesEngine.kt` 和新增的 `NodeAddress.kt`。订阅不能将引擎指向本机或本地网段；HTTPS 拉取有整次预算；保留不影响引擎输出的机场展示参数。
- `NodeManager.kt`、`OwnerAccess.kt`、`SimpleServer.kt`、`QuickAccess.kt` 分别处理邀请显示名、管理私钥缓冲区、剪贴板和锁屏快捷开关。`ReleaseInfo.kt` 在创建 ContentProvider 前为 WorkManager 2.10.1 设置日志门槛，沿用原 Manifest 初始化流程。隐私文案同步到 APK 既有的资产生成流程。
- `tools/node_admin.py` 的邀请接收连接有 15 秒总网络时限。经用户明确授权，仅在本地 WSL 验证，安装脚本保持原样，另用临时目录验证其输入拒绝路径。`tools/test_node_registry.py` 加入超时及取消检查。`crates/nexus-protocol/src/lib.rs`、`crates/nexus-android/src/split.rs`、`forwarding.rs` 增加边界检查和确定性测试，保持协议字节格式。
- `.github/workflows/release.yml` 把构建端清单及说明的对照摘要通过 job output 交给发布端。工作树中未添加依赖、发布密钥或设备私有配置。
- `CatalogCheck.kt` 增加 security 检查；`SplitCheck.kt` 支持多机工具选用设备自己的 Xray 来源，合成检查结束后恢复上次连接记录。`SimpleCheck.kt` 通过隔离首选项渲染实际节点和设置页面，导出已裁去系统栏的截图。`proguard-test-api.pro` 更新独立测试 APK 实际调用的接口，保留 release 的 R8。`tools/verify_compat_interop.py` 将明确指定的单个实验室节点地址传给 instrumentation。
- [多机型检查工具](../../tools/device_check.py)记录系统版本、API、厂商、ROM、ABI、分辨率和字体缩放，输出 Markdown 以及 PNG。运行需要已解锁的 adb 设备和同签名 APK。仅在指定设备覆盖安装；安装或检查失败会记录原因并返回非零。主题和字号测试结束后从系统读回确认恢复。

```sh
python tools/device_check.py --serial <序列号> --apk <release.apk> --test-apk <release-androidTest.apk> --font-scale 1.3 --output <报告目录>
```

已有可用订阅、已授予 Nexus VPN 权限且 VPN 已断开时，可加 `--subscription` 检查首个有支持的 Xray 节点的来源。只有出口比较符合分流语义才将 split-public 记作通过；出口相同或对照缺失会给出原因。默认检查不要求订阅。每次运行都会新建目录；脚本不采集订阅地址。签名不一致需要提供与已安装版本一致的 APK。

## 验证记录

开测前运行 `route print` 核对默认路由，没有 singbox_tun 默认路由。Android 使用 `NEXUS_SHOES_TARGET=E:/temp/nexus-shoes-target`。Linux 目标目录位于 NexusLab 的 Linux 文件系统。

| 测试 | 实际结果 |
|---|---|
| Windows 与 Linux `cargo fmt --check`、workspace clippy（全部目标、警告为错） | 两套环境均退出 0。 |
| Windows 与 Linux `cargo test --workspace` | 两边各组均为 5、3、7、2 项通过，共 17 项，0 failed。覆盖协议输入、DNS 应答及指令、转发 DNS 长度，每项固定种子十万轮。 |
| 两端 `tools/test_tunnel.py`，指定 a14ec74 旧二进制 | Windows、Linux 均输出 `18 integration checks passed`，含旧客户端/服务端互通、多关联和撤销。 |
| Linux 注册表回归 | `PASS: request deadline and cancellation`；`PASS: two devices, idempotent claim, redemption limit, byte quota, live revoke, owner continuity, audit`。 |
| WSL 安装脚本输入检查 | 通过临时目录和只读系统命令替身，输出 `PASS: five invalid configurations and existing installation rejected before mutation`；没有安装、停用或覆盖服务。 |
| 工作流本地校验 | `release workflow YAML and manifest dependency OK`；`bundle.apk: OK`、`PASS: notes mismatch rejected`、`PASS: APK mismatch rejected`。未创建在线 Release。 |
| Android 构建 | 最终临时签名 release 和 release androidTest 为 `BUILD SUCCESSFUL in 1m 58s`；debug、debug androidTest 和 vpnprobe 为 `BUILD SUCCESSFUL in 19s`。 |
| 真机 debug | security、formats、默认 CatalogCheck 各为 `INSTRUMENTATION_CODE: -1`；formats 返回 `25 Clash, sing-box and link cases converted or refused as expected`。 |
| 真机 release | security、formats、refresh 均返回 `INSTRUMENTATION_CODE: -1`。refresh 输出 `failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed`。 |
| 真机 release 的 probe-vpn | 初测按已替换的按钮文案查找失败。改用当前文字「测试真实连通」，重跑输出 `active VPN: real-test button disabled, disconnect hint visible` 和 `INSTRUMENTATION_CODE: -1`。 |
| 多机工具真机示范 | `SM-S9480`、Android 16 / API 36、arm64-v8a、1440×3120。simple、simple-overview、simple-motion、release-update、默认 CatalogCheck、split-public Xray 和四组 simple-shots 全部通过，工具退出 0。原始报告与 32 张图在 `E:/temp/nexus-prep2/signoff/R5GL14PWDNA-20261120-214404-446361/`。 |
| 真机 Xray 与 1 MiB 测速 | 一次验收输出 `INSTRUMENTATION_CODE: -1`。绕过大陆的国内出口与物理网络一致、与全局不同，境外出口与全局一致，全局模式国内外出口一致。绕过大陆下载/上传 2.47/11.97 Mbps，全局 4.20/15.45 Mbps。本组单次读数仅说明流程完成。 |
| 多机工具错误处理 | 传入不存在的 APK 后报告记录「设备准备失败：找不到指定的 APK」。 |
| 真机隐私流程与 logcat | 实际走过 Xray 连接、两模式测速、手动检查更新，在关于页确认并打开 GitHub 反馈草稿。发布前最初一轮应用 UID 日志筛查中，URI 0、IPv4 样式 5、凭据或 UUID 样式 40；WorkManager 的网络状态和任务 UUID 命中。固定 logger 后同一路径采到 4,607 行，URI 0、密钥/口令/token/UUID 样式 0。IPv4 样式剩 2 条，标签为 AdrenoVK-0，均匹配驱动版本/构建提示；WorkManager 网络状态命中 0。按时间窗在手机端筛选，只记录数量和日志标签，未导出原始行。未提交反馈。 |
| 真机完整域名抓包 | **未验证**。手机没有可用的 tcpdump，用户决定本轮保留当前网络。源码域名清单、真实 UI 流程和本地日志不能覆盖浏览器、系统 DNS 或第三方节点的全部出站。 |

## 截图与设备范围

README 替换为连接、节点、我的服务器、设置四个页面的真机合成演示画面，文件位于 `docs/screenshots/{light,dark}-{connect,nodes,server,settings}.png`。已连接页使用演示状态；完整设备检查另有实际 VPN 测试。32 张设备截图包含每种主题和字号的首次打开、已连接、节点、服务器、设置、邀请、领取以及连接中。1.3 倍字号逐页查看，文字按行折叠，底部较长的内容可滚动，未观察到按钮文字截断或重叠。本轮没有据此修改业务页布局。收尾重新打开公开版，到系统读回字号 1.0、夜间模式 yes，确认 tun0 已消失、Nexus 已断开。

| 项目 | 尚缺的证据 |
|---|---|
| 雷电及 Android 实验室 | 用户要求雷电保持关闭。未在 emulator-5554 跑设备工具示范、debug/release 私人节点部署、绑定、邀请、领取、撤销、迁移、三引擎 split-lab、实验室 probe-vpn、vpnprobe 断网恢复或 verify_subscription_vpn.py。现有 split-lab 规则文件入口仅对 debuggable 生效，verify_subscription_vpn.py 的 GUI 流程使用局域网实验节点；当前未验证这些夹具可直接在收缩版重放。WSL 注册表通过不能替代 Android 全流程。 |
| 真机耗电 | 用户暂缓熄屏两小时和亮屏十分钟测试。没有重置 batterystats，没有本轮可报告的耗电、唤醒次数、锁时长、网络流量、CPU/GPU 时间，也未据此调整动画。 |
| 其他 ROM | 小米、华为、OPPO、vivo 及云真机未运行。报告只代表所连接的三星设备。 |
| 服务端与发布 | 未操作用户 VPS。部署脚本没有进行 root 安装复演。GitHub Actions 未在线运行，托管安全扫描未通过本机工作目录权限预检。 |

## Claude 评审

- **节点地址过滤放宽。** 原实现拒绝全部私网、CGNAT 和 198.18.0.0/15，会让 OpenClash fake-ip 路由器后面的用户和家用局域网节点连不上。改为只拒绝只能指向手机自身的地址（回环、未指定、链路本地、组播、240/4、本机接口地址）。去掉 `instrumentationPeer` 测试钩子及 `lab-host` 参数，security 检查改为正反两组地址。
- **小整理。** `QuickAccess.kt` 中订阅链接的注释回到 `subscriptionLink` 上方。README 八张截图右缘的三星侧屏面板把手已抹去。
- **复测。** Windows `cargo fmt --check`、clippy（警告为错）、`cargo test --workspace` 通过；临时签名 release 与 release androidTest 构建成功；真机 R5GL14PWDNA release 包 security、formats、refresh 均 `INSTRUMENTATION_CODE: -1`。`tools/device_check.py --font-scale 1.3 --subscription` 锁屏时在准备阶段按预期停止并提示解锁；解锁后整套重跑全部通过（simple、simple-overview、simple-motion、release-update、CatalogCheck、split-public Xray、两种字号×两种主题截图），字号 1.0 与夜间模式已读回恢复，VPN 已断开。
- **大字号修复。** 1.3 倍字号下「我的服务器」累计流量「375.00 MB」折成两行、两格高度不一；改为单行自动缩小字号（13–22sp），重拍确认。其余页面只有个别行尾单字换行，未改。
- **仍未完成。** 雷电实验室回归、耗电测试、完整域名抓包、其他 ROM 设备。

